package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	SettingRetentionDays          = "retention_days"
	SettingLDAPEnabled            = "ldap_enabled"
	SettingLDAPURL                = "ldap_url"
	SettingLDAPBindDN             = "ldap_bind_dn"
	SettingLDAPBindPassword       = "ldap_bind_password"
	SettingLDAPBaseDN             = "ldap_base_dn"
	SettingLDAPUserFilter         = "ldap_user_filter"
	SettingLDAPLoginAttr          = "ldap_login_attr"
	SettingLDAPDisplayNameAttr    = "ldap_display_name_attr"
	SettingLDAPEmailAttr          = "ldap_email_attr"
	SettingLDAPPhoneAttr          = "ldap_phone_attr"
	SettingLDAPSyncIntervalMins   = "ldap_sync_interval_minutes"
	SettingLDAPLastSyncStartedAt  = "ldap_last_sync_started_at"
	SettingLDAPLastSyncFinishedAt = "ldap_last_sync_finished_at"
	SettingLDAPLastSyncStatus     = "ldap_last_sync_status"
	SettingLDAPLastSyncMessage    = "ldap_last_sync_message"
	SettingLDAPLastSyncCount      = "ldap_last_sync_count"
)

type Store struct {
	DB *sql.DB
}

const printerACLRulesTableDDL = `CREATE TABLE IF NOT EXISTS printer_acl_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	printer_uri TEXT NOT NULL,
	subject_type TEXT NOT NULL,
	subject_role TEXT,
	subject_user_id INTEGER,
	subject_group_id INTEGER,
	effect TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(subject_user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY(subject_group_id) REFERENCES printer_groups(id) ON DELETE CASCADE,
	CHECK (subject_type IN ('role', 'user', 'group')),
	CHECK (effect IN ('allow', 'deny')),
	CHECK (
		(subject_type = 'role' AND subject_role IS NOT NULL AND subject_role <> '' AND subject_user_id IS NULL AND subject_group_id IS NULL) OR
		(subject_type = 'user' AND subject_user_id IS NOT NULL AND subject_role IS NULL AND subject_group_id IS NULL) OR
		(subject_type = 'group' AND subject_group_id IS NOT NULL AND subject_role IS NULL AND subject_user_id IS NULL)
	)
)`

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set foreign_keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	s := &Store{DB: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) WithTx(ctx context.Context, readOnly bool, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			protected INTEGER NOT NULL DEFAULT 0,
			contact_name TEXT,
			phone TEXT,
			email TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS print_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			printer_uri TEXT NOT NULL,
			filename TEXT NOT NULL,
			stored_path TEXT NOT NULL,
			pages INTEGER NOT NULL,
			job_id TEXT,
			status TEXT NOT NULL,
			is_duplex INTEGER NOT NULL DEFAULT 0,
			is_color INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		printerACLRulesTableDDL,
		`CREATE TABLE IF NOT EXISTS printer_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS printer_group_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(group_id, user_id),
			FOREIGN KEY(group_id) REFERENCES printer_groups(id) ON DELETE CASCADE,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "protected INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "auth_source TEXT NOT NULL DEFAULT 'local'"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_dn TEXT"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_uid TEXT"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_sync_enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_present INTEGER NOT NULL DEFAULT 1"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "last_ldap_sync_at TEXT"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "users", "last_login_at TEXT"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := s.ensurePrinterACLRulesSchema(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	indexStmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ldap_uid_unique
			ON users(ldap_uid)
			WHERE ldap_uid IS NOT NULL AND ldap_uid <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ldap_dn_unique
			ON users(ldap_dn)
			WHERE ldap_dn IS NOT NULL AND ldap_dn <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_printer_acl_rules_printer_uri
			ON printer_acl_rules(printer_uri)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_printer_acl_rules_role_unique
			ON printer_acl_rules(printer_uri, subject_type, subject_role, effect)
			WHERE subject_type = 'role' AND subject_role IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_printer_acl_rules_user_unique
			ON printer_acl_rules(printer_uri, subject_type, subject_user_id, effect)
			WHERE subject_type = 'user' AND subject_user_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_printer_acl_rules_group_unique
			ON printer_acl_rules(printer_uri, subject_type, subject_group_id, effect)
			WHERE subject_type = 'group' AND subject_group_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_printer_group_members_group_id
			ON printer_group_members(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_printer_group_members_user_id
			ON printer_group_members(user_id)`,
	}
	for _, stmt := range indexStmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := addColumnIfMissing(ctx, s.DB, "print_jobs", "is_duplex INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := addColumnIfMissing(ctx, s.DB, "print_jobs", "is_color INTEGER NOT NULL DEFAULT 1"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES (?, ?)`,
		SettingRetentionDays, "0",
	); err != nil {
		return fmt.Errorf("seed settings: %w", err)
	}

	return nil
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func addColumnIfMissing(ctx context.Context, db *sql.DB, table string, columnDef string) error {
	_, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, columnDef))
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

func (s *Store) ensurePrinterACLRulesSchema(ctx context.Context) error {
	tableSQL, err := tableSQL(ctx, s.DB, "printer_acl_rules")
	if err != nil {
		return err
	}
	if tableSQL == "" {
		return nil
	}
	if strings.Contains(tableSQL, "subject_group_id") && strings.Contains(tableSQL, "'group'") {
		return nil
	}

	for _, indexName := range []string{
		"idx_printer_acl_rules_printer_uri",
		"idx_printer_acl_rules_role_unique",
		"idx_printer_acl_rules_user_unique",
		"idx_printer_acl_rules_group_unique",
	} {
		if _, err := s.DB.ExecContext(ctx, `DROP INDEX IF EXISTS `+indexName); err != nil {
			return err
		}
	}

	if _, err := s.DB.ExecContext(ctx, `ALTER TABLE printer_acl_rules RENAME TO printer_acl_rules_legacy`); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, printerACLRulesTableDDL); err != nil {
		return err
	}

	subjectGroupExpr := `NULL`
	hasGroupColumn, err := tableHasColumn(ctx, s.DB, "printer_acl_rules_legacy", "subject_group_id")
	if err != nil {
		return err
	}
	if hasGroupColumn {
		subjectGroupExpr = `subject_group_id`
	}

	if _, err := s.DB.ExecContext(ctx, `INSERT INTO printer_acl_rules (
		id, printer_uri, subject_type, subject_role, subject_user_id, subject_group_id, effect, created_at, updated_at
	)
	SELECT
		id, printer_uri, subject_type, subject_role, subject_user_id, `+subjectGroupExpr+`, effect, created_at, updated_at
	FROM printer_acl_rules_legacy`); err != nil {
		return err
	}

	if _, err := s.DB.ExecContext(ctx, `DROP TABLE printer_acl_rules_legacy`); err != nil {
		return err
	}
	return nil
}

func tableSQL(ctx context.Context, db *sql.DB, table string) (string, error) {
	var sqlText sql.NullString
	err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sqlText)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sqlText.String, nil
}

func tableHasColumn(ctx context.Context, db *sql.DB, table string, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+strconv.Quote(table)+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
