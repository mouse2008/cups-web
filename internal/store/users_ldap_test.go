package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestOpen_HotUpgradeMigratesLDAPUserColumns(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	seedLegacyStore(t, dbPath)

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		user, err := GetUserByUsername(ctx, tx, "legacy-user")
		if err != nil {
			return err
		}
		if user.AuthSource != authSourceLocal {
			t.Fatalf("AuthSource = %q, want %q", user.AuthSource, authSourceLocal)
		}
		if user.LDAPUID != "" || user.LDAPDN != "" {
			t.Fatalf("legacy user unexpectedly has LDAP identity fields populated")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify legacy user after migration: %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users (
				username, password_hash, role, protected,
				auth_source, ldap_uid, ldap_dn, ldap_sync_enabled, ldap_present, last_ldap_sync_at, last_login_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"ldap-user", "", RoleUser, 0,
			"ldap", "u-1", "cn=u1,dc=example,dc=com", 1, 1, nowUTC(), nowUTC(),
			nowUTC(), nowUTC(),
		)
		return err
	})
	if err != nil {
		t.Fatalf("expected LDAP columns to exist, got err = %v", err)
	}
}

func TestOpen_FailsWhenDuplicateLDAPIdentitiesExist(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	seedStoreWithDuplicateLDAPIdentity(t, dbPath)

	s, err := Open(ctx, dbPath)
	if s != nil {
		defer s.Close()
	}
	if err == nil {
		t.Fatalf("Open() err = nil, want duplicate LDAP identity failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("Open() err = %v, want unique constraint failure", err)
	}
}

func TestUpsertLDAPUser_PreservesLocalRoleAndProfile(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var got User
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username:    "alice",
			LDAPUID:     "alice-uid",
			LDAPDN:      "cn=alice,dc=example,dc=com",
			ContactName: "Alice LDAP",
			Email:       "alice@example.com",
			Phone:       "123",
		})
		if err != nil {
			return err
		}

		_, err = UpdateUser(ctx, tx, UpdateUserInput{
			ID:          created.ID,
			Username:    created.Username,
			Role:        RoleAdmin,
			ContactName: "Local Name",
			Phone:       "999",
			Email:       "local@example.com",
		})
		if err != nil {
			return err
		}

		got, err = UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username:    "alice",
			LDAPUID:     "alice-uid",
			LDAPDN:      "cn=alice,dc=example,dc=com",
			ContactName: "Alice From LDAP",
			Email:       "alice-new@example.com",
			Phone:       "555",
		})
		return err
	})
	if err != nil {
		t.Fatalf("UpsertLDAPUser() err = %v", err)
	}

	if got.Role != RoleAdmin {
		t.Fatalf("Role = %q, want %q", got.Role, RoleAdmin)
	}
	if got.ContactName != "Local Name" {
		t.Fatalf("ContactName = %q, want local value", got.ContactName)
	}
	if got.Phone != "999" {
		t.Fatalf("Phone = %q, want local value", got.Phone)
	}
	if got.Email != "local@example.com" {
		t.Fatalf("Email = %q, want local value", got.Email)
	}
	if got.AuthSource != "ldap" {
		t.Fatalf("AuthSource = %q, want ldap", got.AuthSource)
	}
	if !got.LDAPSyncEnabled {
		t.Fatalf("LDAPSyncEnabled = false, want true")
	}
	if !got.LDAPPresent {
		t.Fatalf("LDAPPresent = false, want true")
	}
	if got.LastLDAPSyncAt == "" {
		t.Fatalf("LastLDAPSyncAt = empty, want timestamp")
	}
}

func TestUpsertLDAPUser_CreatesNewUsersWithUserRole(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var got User
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		got, err = UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username: "bob",
			LDAPUID:  "bob-uid",
			LDAPDN:   "cn=bob,dc=example,dc=com",
		})
		return err
	})
	if err != nil {
		t.Fatalf("UpsertLDAPUser() err = %v", err)
	}
	if got.Role != RoleUser {
		t.Fatalf("Role = %q, want %q", got.Role, RoleUser)
	}
}

func TestUpsertLDAPUser_DoesNotReuseLocalUserByUsername(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var localUserID int64
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		localUser, err := CreateUser(ctx, tx, CreateUserInput{
			Username:     "carol",
			PasswordHash: "hash",
			Role:         RoleAdmin,
			ContactName:  "Local Carol",
			Email:        "carol@local.test",
		})
		if err != nil {
			return err
		}
		localUserID = localUser.ID

		_, err = UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username: "carol",
			LDAPUID:  "carol-uid",
			LDAPDN:   "cn=carol,dc=example,dc=com",
		})
		if err == nil {
			t.Fatalf("UpsertLDAPUser() err = nil, want unique constraint or similar failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		localUser, err := GetUserByID(ctx, tx, localUserID)
		if err != nil {
			return err
		}
		if localUser.AuthSource != "local" {
			t.Fatalf("AuthSource = %q, want local", localUser.AuthSource)
		}
		if localUser.LDAPUID != "" || localUser.LDAPDN != "" {
			t.Fatalf("local user was unexpectedly linked to LDAP identity")
		}

		_, err = GetUserByLDAPUIDOrDN(ctx, tx, "carol-uid", "cn=carol,dc=example,dc=com")
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetUserByLDAPUIDOrDN() err = %v, want sql.ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify local user unchanged: %v", err)
	}
}

func TestMarkMissingLDAPUsers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var presentUserID int64
	var missingUserID int64
	var markedCount int

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		presentUser, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username: "present",
			LDAPUID:  "present-uid",
			LDAPDN:   "cn=present,dc=example,dc=com",
		})
		if err != nil {
			return err
		}
		presentUserID = presentUser.ID

		dnOnlyUser, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username: "dn-only",
			LDAPDN:   "cn=dn-only,dc=example,dc=com",
		})
		if err != nil {
			return err
		}
		if !dnOnlyUser.LDAPPresent {
			t.Fatalf("DN-only user should start present")
		}

		missingUser, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username: "missing",
			LDAPUID:  "missing-uid",
			LDAPDN:   "cn=missing,dc=example,dc=com",
		})
		if err != nil {
			return err
		}
		missingUserID = missingUser.ID

		markedCount, err = MarkMissingLDAPUsers(ctx, tx, map[string]struct{}{
			"present-uid":                  {},
			"cn=dn-only,dc=example,dc=com": {},
		})
		return err
	})
	if err != nil {
		t.Fatalf("MarkMissingLDAPUsers() err = %v", err)
	}

	if markedCount != 1 {
		t.Fatalf("markedCount = %d, want 1", markedCount)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		presentUser, err := GetUserByID(ctx, tx, presentUserID)
		if err != nil {
			return err
		}
		if !presentUser.LDAPPresent {
			t.Fatalf("present user marked missing unexpectedly")
		}

		dnOnlyUser, err := GetUserByUsername(ctx, tx, "dn-only")
		if err != nil {
			return err
		}
		if !dnOnlyUser.LDAPPresent {
			t.Fatalf("DN-only user marked missing unexpectedly")
		}

		missingUser, err := GetUserByID(ctx, tx, missingUserID)
		if err != nil {
			return err
		}
		if missingUser.LDAPPresent {
			t.Fatalf("missing user still present, want absent")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify marked users: %v", err)
	}
}

func seedLegacyStore(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE users (
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
		`INSERT INTO users (
			username, password_hash, role, protected, contact_name, phone, email, created_at, updated_at
		) VALUES ('legacy-user', 'hash', 'user', 0, 'Legacy User', '10086', 'legacy@example.com', '` + nowUTC() + `', '` + nowUTC() + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy store stmt %q err = %v", stmt, err)
		}
	}
}

func seedStoreWithDuplicateLDAPIdentity(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()

	now := nowUTC()
	stmts := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			protected INTEGER NOT NULL DEFAULT 0,
			contact_name TEXT,
			phone TEXT,
			email TEXT,
			auth_source TEXT NOT NULL DEFAULT 'local',
			ldap_dn TEXT,
			ldap_uid TEXT,
			ldap_sync_enabled INTEGER NOT NULL DEFAULT 0,
			ldap_present INTEGER NOT NULL DEFAULT 1,
			last_ldap_sync_at TEXT,
			last_login_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`INSERT INTO users (
			username, password_hash, role, protected, auth_source, ldap_uid, ldap_dn, ldap_sync_enabled, ldap_present, created_at, updated_at
		) VALUES ('dup-1', '', 'user', 0, 'ldap', 'dup-uid', 'cn=dup-1,dc=example,dc=com', 1, 1, '` + now + `', '` + now + `')`,
		`INSERT INTO users (
			username, password_hash, role, protected, auth_source, ldap_uid, ldap_dn, ldap_sync_enabled, ldap_present, created_at, updated_at
		) VALUES ('dup-2', '', 'user', 0, 'ldap', 'dup-uid', 'cn=dup-2,dc=example,dc=com', 1, 1, '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed duplicate LDAP identity stmt %q err = %v", stmt, err)
		}
	}
}
