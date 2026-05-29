package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestOpen_MigratesLDAPUserColumns(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

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
			DefaultRole: RoleUser,
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
			DefaultRole: RoleUser,
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
			Username:    "bob",
			LDAPUID:     "bob-uid",
			LDAPDN:      "cn=bob,dc=example,dc=com",
			DefaultRole: RoleAdmin,
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
			Username:    "carol",
			LDAPUID:     "carol-uid",
			LDAPDN:      "cn=carol,dc=example,dc=com",
			DefaultRole: RoleUser,
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
			Username:    "present",
			LDAPUID:     "present-uid",
			LDAPDN:      "cn=present,dc=example,dc=com",
			DefaultRole: RoleUser,
		})
		if err != nil {
			return err
		}
		presentUserID = presentUser.ID

		missingUser, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username:    "missing",
			LDAPUID:     "missing-uid",
			LDAPDN:      "cn=missing,dc=example,dc=com",
			DefaultRole: RoleUser,
		})
		if err != nil {
			return err
		}
		missingUserID = missingUser.ID

		markedCount, err = MarkMissingLDAPUsers(ctx, tx, map[string]struct{}{
			"present-uid": {},
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
