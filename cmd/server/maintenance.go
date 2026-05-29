package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"
)

const (
	cleanupInterval  = time.Hour
	ldapPollInterval = time.Minute
)

var errLDAPSyncServiceUnavailable = errors.New("ldap service not initialized")

func startMaintenance(s *store.Store, uploads string) {
	go func() {
		if err := cleanupOldPrints(context.Background(), s, uploads, time.Now()); err != nil {
			log.Println("cleanup failed:", err)
		}
		if err := runLDAPSyncOnce(context.Background(), s, currentLDAPSyncService(), time.Now()); err != nil {
			log.Println("scheduled ldap sync failed:", err)
		}

		cleanupTicker := time.NewTicker(cleanupInterval)
		ldapTicker := time.NewTicker(ldapPollInterval)
		defer cleanupTicker.Stop()
		defer ldapTicker.Stop()

		for {
			select {
			case now := <-cleanupTicker.C:
				if err := cleanupOldPrints(context.Background(), s, uploads, now); err != nil {
					log.Println("cleanup failed:", err)
				}
			case now := <-ldapTicker.C:
				if err := runLDAPSyncOnce(context.Background(), s, currentLDAPSyncService(), now); err != nil {
					log.Println("scheduled ldap sync failed:", err)
				}
			}
		}
	}()
}

func currentLDAPSyncService() ldapSyncService {
	syncer, _ := any(currentLDAPService()).(ldapSyncService)
	return syncer
}

func runLDAPSyncOnce(ctx context.Context, s *store.Store, syncer ldapSyncService, now time.Time) error {
	cfg, err := ldapauth.LoadConfig(ctx, s)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.SyncIntervalMinutes <= 0 {
		return nil
	}

	lastStartedAt, err := loadLDAPLastSyncStartedAt(ctx, s)
	if err != nil {
		return err
	}
	if !shouldRunLDAPSync(lastStartedAt, cfg.SyncIntervalMinutes, now) {
		return nil
	}
	if syncer == nil {
		return errLDAPSyncServiceUnavailable
	}

	startedAt := now.UTC().Format(time.RFC3339)
	if err := persistLDAPSyncStatusForStore(ctx, s, startedAt, "", "running", "scheduled sync in progress", 0); err != nil {
		return err
	}

	report, err := syncer.SyncAll(ctx, cfg)
	finishedAt := nowRFC3339()
	if err != nil {
		if persistErr := persistLDAPSyncStatusForStore(ctx, s, startedAt, finishedAt, "error", err.Error(), 0); persistErr != nil {
			return persistErr
		}
		return err
	}

	return persistLDAPSyncStatusForStore(ctx, s, startedAt, finishedAt, "success", formatScheduledLDAPSyncSuccessMessage(report), int64(report.Upserted))
}

func shouldRunLDAPSync(lastStartedAt string, intervalMinutes int64, now time.Time) bool {
	if intervalMinutes <= 0 {
		return false
	}

	lastStartedAt = strings.TrimSpace(lastStartedAt)
	if lastStartedAt == "" {
		return true
	}

	lastRun, err := time.Parse(time.RFC3339, lastStartedAt)
	if err != nil {
		return true
	}
	if now.Before(lastRun) {
		return false
	}
	return now.Sub(lastRun) >= time.Duration(intervalMinutes)*time.Minute
}

func loadLDAPLastSyncStartedAt(ctx context.Context, s *store.Store) (string, error) {
	var lastStartedAt string
	err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		val, err := store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncStartedAt, "")
		if err != nil {
			return err
		}
		lastStartedAt = val
		return nil
	})
	return lastStartedAt, err
}

func persistLDAPSyncStatusForStore(ctx context.Context, s *store.Store, startedAt string, finishedAt string, status string, message string, count int64) error {
	return s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncStartedAt, startedAt); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncFinishedAt, finishedAt); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncStatus, status); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncMessage, message); err != nil {
			return err
		}
		return store.SetSettingInt(ctx, tx, store.SettingLDAPLastSyncCount, count)
	})
}

func formatScheduledLDAPSyncSuccessMessage(report ldapauth.SyncReport) string {
	return "scheduled sync completed: upserted=" + strconv.Itoa(report.Upserted) +
		" skipped=" + strconv.Itoa(report.Skipped) +
		" missingMarked=" + strconv.Itoa(report.MissingMarked)
}

func cleanupOldPrints(ctx context.Context, s *store.Store, uploads string, now time.Time) error {
	var retentionDays int64
	err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		val, err := store.GetSettingInt(ctx, tx, store.SettingRetentionDays, 0)
		if err != nil {
			return err
		}
		retentionDays = val
		return nil
	})
	if err != nil {
		return err
	}
	if retentionDays <= 0 {
		return nil
	}

	cutoff := now.AddDate(0, 0, -int(retentionDays)).UTC().Format(time.RFC3339)
	var paths []string
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT stored_path FROM print_jobs WHERE created_at < ?", cutoff)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			paths = append(paths, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		_, err = tx.ExecContext(ctx, "DELETE FROM print_jobs WHERE created_at < ?", cutoff)
		return err
	})
	if err != nil {
		return err
	}

	for _, rel := range paths {
		abs := filepath.Join(uploads, filepath.FromSlash(rel))
		_ = os.Remove(abs)
		convertedRel := convertedRelPath(rel)
		if convertedRel != "" {
			convertedAbs := filepath.Join(uploads, filepath.FromSlash(convertedRel))
			_ = os.Remove(convertedAbs)
		}
	}

	if len(paths) > 0 {
		if _, err := s.DB.ExecContext(ctx, "VACUUM"); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
	}
	return nil
}
