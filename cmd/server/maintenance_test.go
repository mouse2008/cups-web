package main

import (
	"context"
	"database/sql"
	"errors"
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
