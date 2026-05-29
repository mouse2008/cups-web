package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"
)

type fakeMaintenanceLDAPService struct {
	syncCalled bool
	syncCfg    ldapauth.Config
	syncReport ldapauth.SyncReport
	syncErr    error
}

func (f *fakeMaintenanceLDAPService) SyncAll(ctx context.Context, cfg ldapauth.Config) (ldapauth.SyncReport, error) {
	f.syncCalled = true
	f.syncCfg = cfg
	return f.syncReport, f.syncErr
}

func TestRunPeriodicLDAPSync_DisabledLDAPSkipsScheduledSync(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingBool(ctx, tx, store.SettingLDAPEnabled, false); err != nil {
			return err
		}
		return store.SetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, 45)
	}); err != nil {
		t.Fatalf("seed disabled ldap config: %v", err)
	}

	fake := &fakeMaintenanceLDAPService{}
	if err := runLDAPSyncOnce(ctx, s, fake, time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runLDAPSyncOnce() err = %v, want nil for disabled LDAP", err)
	}
	if fake.syncCalled {
		t.Fatalf("SyncAll was called for disabled LDAP")
	}

	assertSettingString(t, ctx, s, store.SettingLDAPLastSyncStatus, "")
	assertSettingString(t, ctx, s, store.SettingLDAPLastSyncStartedAt, "")
}

func TestRunPeriodicLDAPSync_SyncFailureRecordsErrorStatus(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	enableFullLDAPConfig(t, ctx, s)

	syncErr := errors.New("directory unavailable")
	fake := &fakeMaintenanceLDAPService{syncErr: syncErr}
	startedAt := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)

	err := runLDAPSyncOnce(ctx, s, fake, startedAt)
	if !errors.Is(err, syncErr) {
		t.Fatalf("runLDAPSyncOnce() err = %v, want %v", err, syncErr)
	}
	if !fake.syncCalled {
		t.Fatalf("SyncAll was not called")
	}
	if fake.syncCfg.URL != "ldap://ldap.example.com:389" {
		t.Fatalf("SyncAll cfg.URL = %q, want configured LDAP URL", fake.syncCfg.URL)
	}

	status := readLDAPSyncStatus(t, ctx, s)
	if status.LastStartedAt != startedAt.Format(time.RFC3339) {
		t.Fatalf("LastStartedAt = %q, want %q", status.LastStartedAt, startedAt.Format(time.RFC3339))
	}
	if status.LastFinishedAt == "" {
		t.Fatalf("LastFinishedAt = empty, want persisted timestamp")
	}
	if status.LastStatus != "error" {
		t.Fatalf("LastStatus = %q, want error", status.LastStatus)
	}
	if status.LastMessage != syncErr.Error() {
		t.Fatalf("LastMessage = %q, want %q", status.LastMessage, syncErr.Error())
	}
	if status.LastCount != 0 {
		t.Fatalf("LastCount = %d, want 0", status.LastCount)
	}
}

func TestRunPeriodicLDAPSync_PersistsStatusUsingInjectedStore(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	enableFullLDAPConfig(t, ctx, s)

	oldAppStore := appStore
	appStore = nil
	t.Cleanup(func() {
		appStore = oldAppStore
	})

	fake := &fakeMaintenanceLDAPService{
		syncReport: ldapauth.SyncReport{Upserted: 2, Skipped: 1, MissingMarked: 0},
	}
	startedAt := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)

	if err := runLDAPSyncOnce(ctx, s, fake, startedAt); err != nil {
		t.Fatalf("runLDAPSyncOnce() err = %v, want nil", err)
	}

	status := readLDAPSyncStatus(t, ctx, s)
	if status.LastStatus != "success" {
		t.Fatalf("LastStatus = %q, want success", status.LastStatus)
	}
	if status.LastMessage != "scheduled sync completed: upserted=2 skipped=1 missingMarked=0" {
		t.Fatalf("LastMessage = %q, want scheduled success summary", status.LastMessage)
	}
	if status.LastCount != 2 {
		t.Fatalf("LastCount = %d, want 2", status.LastCount)
	}
}

func TestShouldRunLDAPSync_PreventsConstantReruns(t *testing.T) {
	now := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		lastStartedAt string
		intervalMins  int64
		want          bool
	}{
		{
			name:         "invalid interval skips",
			intervalMins: 0,
			want:         false,
		},
		{
			name:         "first run allowed",
			intervalMins: 45,
			want:         true,
		},
		{
			name:          "before interval blocks rerun",
			lastStartedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
			intervalMins:  45,
			want:          false,
		},
		{
			name:          "at interval allows rerun",
			lastStartedAt: now.Add(-45 * time.Minute).Format(time.RFC3339),
			intervalMins:  45,
			want:          true,
		},
		{
			name:          "invalid timestamp forces recovery run",
			lastStartedAt: "not-a-time",
			intervalMins:  45,
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunLDAPSync(tt.lastStartedAt, tt.intervalMins, now)
			if got != tt.want {
				t.Fatalf("shouldRunLDAPSync(%q, %d, %s) = %v, want %v", tt.lastStartedAt, tt.intervalMins, now.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

func TestCleanupOldPrints_RemovesExpiredJobsAndConvertedCopies(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	uploads := t.TempDir()
	now := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingInt(ctx, tx, store.SettingRetentionDays, 7); err != nil {
			return err
		}
		user, err := store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:     "cleanup-user",
			PasswordHash: "",
			Role:         store.RoleUser,
			AuthSource:   "local",
		})
		if err != nil {
			return err
		}

		oldRel := filepath.ToSlash(filepath.Join("20260520", "old.pdf"))
		newRel := filepath.ToSlash(filepath.Join("20260528", "new.pdf"))
		for _, rec := range []store.PrintRecord{
			{
				UserID:     user.ID,
				PrinterURI: "ipp://printer/1",
				Filename:   "old.pdf",
				StoredPath: oldRel,
				Pages:      1,
				Status:     "printed",
				CreatedAt:  now.AddDate(0, 0, -9).UTC().Format(time.RFC3339),
			},
			{
				UserID:     user.ID,
				PrinterURI: "ipp://printer/1",
				Filename:   "new.pdf",
				StoredPath: newRel,
				Pages:      1,
				Status:     "printed",
				CreatedAt:  now.AddDate(0, 0, -1).UTC().Format(time.RFC3339),
			},
		} {
			record := rec
			if _, err := store.InsertPrintRecord(ctx, tx, &record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed print jobs: %v", err)
	}

	oldPath := filepath.Join(uploads, "20260520", "old.pdf")
	oldConvertedPath := filepath.Join(uploads, "20260520", "old.pdf"+convertedSuffix)
	newPath := filepath.Join(uploads, "20260528", "new.pdf")
	newConvertedPath := filepath.Join(uploads, "20260528", "new.pdf"+convertedSuffix)
	for _, path := range []string{oldPath, oldConvertedPath, newPath, newConvertedPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := cleanupOldPrints(ctx, s, uploads, now); err != nil {
		t.Fatalf("cleanupOldPrints() err = %v", err)
	}

	assertPrintJobCount(t, ctx, s, 1)
	assertFileMissing(t, oldPath)
	assertFileMissing(t, oldConvertedPath)
	assertFileExists(t, newPath)
	assertFileExists(t, newConvertedPath)
}

func readLDAPSyncStatus(t *testing.T, ctx context.Context, s *store.Store) adminLDAPSyncStatus {
	t.Helper()

	var status adminLDAPSyncStatus
	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		var err error
		status.LastStartedAt, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncStartedAt, "")
		if err != nil {
			return err
		}
		status.LastFinishedAt, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncFinishedAt, "")
		if err != nil {
			return err
		}
		status.LastStatus, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncStatus, "")
		if err != nil {
			return err
		}
		status.LastMessage, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncMessage, "")
		if err != nil {
			return err
		}
		status.LastCount, err = store.GetSettingInt(ctx, tx, store.SettingLDAPLastSyncCount, 0)
		return err
	}); err != nil {
		t.Fatalf("read LDAP sync status: %v", err)
	}
	return status
}

func assertPrintJobCount(t *testing.T, ctx context.Context, s *store.Store, want int) {
	t.Helper()

	var got int
	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM print_jobs").Scan(&got)
	}); err != nil {
		t.Fatalf("count print jobs: %v", err)
	}
	if got != want {
		t.Fatalf("print_jobs count = %d, want %d", got, want)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file %s to be removed, stat err = %v", path, err)
	}
}
