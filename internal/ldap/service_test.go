package ldap

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"cups-web/internal/store"

	gldap "github.com/go-ldap/ldap/v3"
)

type fakeClient struct {
	searchUserResult []DirectoryUser
	searchUserErr    error
	searchAllResult  []DirectoryUser
	searchAllErr     error
	bindErr          error
	bindDN           string
	bindPassword     string
}

func (f *fakeClient) SearchUser(ctx context.Context, cfg Config, username string) ([]DirectoryUser, error) {
	return f.searchUserResult, f.searchUserErr
}

func (f *fakeClient) BindUser(ctx context.Context, cfg Config, dn string, password string) error {
	f.bindDN = dn
	f.bindPassword = password
	return f.bindErr
}

func (f *fakeClient) SearchAllUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error) {
	return f.searchAllResult, f.searchAllErr
}

func TestAuthenticateOrProvision_CreatesLDAPUser(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	client := &fakeClient{
		searchUserResult: []DirectoryUser{{
			Username:    "alice",
			UID:         "alice-uid",
			DN:          "cn=alice,dc=example,dc=com",
			DisplayName: "Alice LDAP",
			Email:       "alice@example.com",
			Phone:       "123456",
		}},
	}
	svc := NewService(s, client)

	user, err := svc.AuthenticateOrProvision(ctx, testConfig(), "alice", "secret")
	if err != nil {
		t.Fatalf("AuthenticateOrProvision() err = %v", err)
	}
	if client.bindDN != "cn=alice,dc=example,dc=com" {
		t.Fatalf("BindUser() dn = %q, want matched DN", client.bindDN)
	}
	if client.bindPassword != "secret" {
		t.Fatalf("BindUser() password = %q, want submitted password", client.bindPassword)
	}
	if user.AuthSource != "ldap" {
		t.Fatalf("AuthSource = %q, want ldap", user.AuthSource)
	}
	if user.Role != store.RoleUser {
		t.Fatalf("Role = %q, want %q", user.Role, store.RoleUser)
	}
	if user.LDAPUID != "alice-uid" {
		t.Fatalf("LDAPUID = %q, want alice-uid", user.LDAPUID)
	}
	if user.LDAPDN != "cn=alice,dc=example,dc=com" {
		t.Fatalf("LDAPDN = %q, want matched DN", user.LDAPDN)
	}
	if user.LastLoginAt == "" {
		t.Fatalf("LastLoginAt = empty, want timestamp")
	}

	assertUserState(t, ctx, s, user.ID, func(got store.User) {
		if got.Username != "alice" {
			t.Fatalf("Username = %q, want alice", got.Username)
		}
		if got.ContactName != "Alice LDAP" {
			t.Fatalf("ContactName = %q, want LDAP display name", got.ContactName)
		}
		if got.Email != "alice@example.com" {
			t.Fatalf("Email = %q, want LDAP email", got.Email)
		}
		if !got.LDAPPresent {
			t.Fatalf("LDAPPresent = false, want true")
		}
		if got.LastLoginAt == "" {
			t.Fatalf("stored LastLoginAt = empty, want timestamp")
		}
	})
}

func TestSyncAll_MarksMissingUsersAsNotPresent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	var missingUserID int64
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		user, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
			Username:    "missing",
			LDAPUID:     "missing-uid",
			LDAPDN:      "cn=missing,dc=example,dc=com",
			ContactName: "Missing User",
		})
		if err != nil {
			return err
		}
		missingUserID = user.ID
		return nil
	}); err != nil {
		t.Fatalf("seed LDAP user: %v", err)
	}

	client := &fakeClient{
		searchAllResult: []DirectoryUser{
			{
				Username:    "present",
				UID:         "present-uid",
				DN:          "cn=present,dc=example,dc=com",
				DisplayName: "Present User",
			},
			{
				UID: "missing-username",
				DN:  "cn=broken,dc=example,dc=com",
			},
		},
	}
	svc := NewService(s, client)

	report, err := svc.SyncAll(ctx, testConfig())
	if err != nil {
		t.Fatalf("SyncAll() err = %v", err)
	}
	if report.Upserted != 1 {
		t.Fatalf("Upserted = %d, want 1", report.Upserted)
	}
	if report.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1", report.Skipped)
	}
	if report.MissingMarked != 1 {
		t.Fatalf("MissingMarked = %d, want 1", report.MissingMarked)
	}

	assertUserState(t, ctx, s, missingUserID, func(got store.User) {
		if got.LDAPPresent {
			t.Fatalf("LDAPPresent = true, want false for missing LDAP user")
		}
	})
}

func TestAuthenticateOrProvision_RejectsAmbiguousMatches(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	client := &fakeClient{
		searchUserResult: []DirectoryUser{
			{Username: "alice", UID: "a1", DN: "cn=a1,dc=example,dc=com"},
			{Username: "alice", UID: "a2", DN: "cn=a2,dc=example,dc=com"},
		},
	}
	svc := NewService(s, client)

	_, err := svc.AuthenticateOrProvision(ctx, testConfig(), "alice", "secret")
	if !errors.Is(err, ErrAmbiguousSearchResult) {
		t.Fatalf("AuthenticateOrProvision() err = %v, want %v", err, ErrAmbiguousSearchResult)
	}
	if client.bindDN != "" {
		t.Fatalf("BindUser() was called with dn %q, want no bind attempt", client.bindDN)
	}
}

func TestAuthenticateOrProvision_ClassifiesInvalidBindCredentials(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	client := &fakeClient{
		searchUserResult: []DirectoryUser{{
			Username: "alice",
			UID:      "alice-uid",
			DN:       "cn=alice,dc=example,dc=com",
		}},
		bindErr: gldap.NewError(gldap.LDAPResultInvalidCredentials, errors.New("invalid credentials")),
	}
	svc := NewService(s, client)

	_, err := svc.AuthenticateOrProvision(ctx, testConfig(), "alice", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("AuthenticateOrProvision() err = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestAuthenticateOrProvision_PreservesOperationalBindErrors(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	bindErr := errors.New("ldap bind unavailable")
	client := &fakeClient{
		searchUserResult: []DirectoryUser{{
			Username: "alice",
			UID:      "alice-uid",
			DN:       "cn=alice,dc=example,dc=com",
		}},
		bindErr: bindErr,
	}
	svc := NewService(s, client)

	_, err := svc.AuthenticateOrProvision(ctx, testConfig(), "alice", "secret")
	if !errors.Is(err, bindErr) {
		t.Fatalf("AuthenticateOrProvision() err = %v, want wrapped bind error", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("AuthenticateOrProvision() err = %v, want operational error not invalid credentials", err)
	}
}

func TestAuthenticateOrProvision_ReportsProvisioningConflictForLocalUsernameCollision(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:     "alice",
			PasswordHash: "hash",
			Role:         store.RoleUser,
			AuthSource:   "local",
		})
		return err
	}); err != nil {
		t.Fatalf("seed local user: %v", err)
	}

	client := &fakeClient{
		searchUserResult: []DirectoryUser{{
			Username: "alice",
			UID:      "alice-uid",
			DN:       "cn=alice,dc=example,dc=com",
		}},
	}
	svc := NewService(s, client)

	_, err := svc.AuthenticateOrProvision(ctx, testConfig(), "alice", "secret")
	if !errors.Is(err, ErrProvisioningConflict) {
		t.Fatalf("AuthenticateOrProvision() err = %v, want %v", err, ErrProvisioningConflict)
	}
}

func TestIsUsernameConstraintError(t *testing.T) {
	err := errors.New("constraint failed: UNIQUE constraint failed: users.username (2067)")

	if !isUsernameConstraintError(err) {
		t.Fatalf("isUsernameConstraintError() = false, want true")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{
			name:    "disabled config may be empty",
			cfg:     Config{},
			wantErr: nil,
		},
		{
			name: "enabled config requires URL",
			cfg: Config{
				Enabled:    true,
				BaseDN:     "dc=example,dc=com",
				UserFilter: "(objectClass=person)",
				LoginAttr:  "uid",
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "login config allows zero sync interval",
			cfg: Config{
				Enabled:             true,
				URL:                 "ldap://ldap.example.com:389",
				BaseDN:              "dc=example,dc=com",
				UserFilter:          "(objectClass=person)",
				LoginAttr:           "uid",
				SyncIntervalMinutes: 0,
			},
			wantErr: nil,
		},
		{
			name:    "valid enabled config passes",
			cfg:     testConfig(),
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfigValidateForSync(t *testing.T) {
	cfg := testConfig()
	cfg.SyncIntervalMinutes = 0

	err := cfg.ValidateForSync()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ValidateForSync() err = %v, want %v", err, ErrInvalidConfig)
	}
}

func openTestStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()

	s, err := store.Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("store.Open() err = %v", err)
	}
	return s
}

func assertUserState(t *testing.T, ctx context.Context, s *store.Store, userID int64, check func(store.User)) {
	t.Helper()

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		user, err := store.GetUserByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		check(user)
		return nil
	}); err != nil {
		t.Fatalf("assertUserState() err = %v", err)
	}
}

func testConfig() Config {
	return Config{
		Enabled:             true,
		URL:                 "ldap://ldap.example.com:389",
		BaseDN:              "dc=example,dc=com",
		UserFilter:          "(objectClass=person)",
		LoginAttr:           "uid",
		DisplayNameAttr:     "cn",
		EmailAttr:           "mail",
		PhoneAttr:           "telephoneNumber",
		SyncIntervalMinutes: 60,
	}
}
