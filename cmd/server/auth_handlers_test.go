package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cups-web/internal/auth"
	"cups-web/internal/ldap"
	"cups-web/internal/store"

	"golang.org/x/crypto/bcrypt"
)

type fakeLDAPClient struct {
	searchUserResult []ldap.DirectoryUser
	bindErr          error
	searchUserCalls  int
	bindUserCalls    int
	bindDN           string
	bindPassword     string
}

func (f *fakeLDAPClient) SearchUser(ctx context.Context, cfg ldap.Config, username string) ([]ldap.DirectoryUser, error) {
	f.searchUserCalls++
	return f.searchUserResult, nil
}

func (f *fakeLDAPClient) BindUser(ctx context.Context, cfg ldap.Config, dn string, password string) error {
	f.bindUserCalls++
	f.bindDN = dn
	f.bindPassword = password
	return f.bindErr
}

func (f *fakeLDAPClient) SearchAllUsers(ctx context.Context, cfg ldap.Config) ([]ldap.DirectoryUser, error) {
	return nil, nil
}

func TestLoginHandler_LocalUserLoginStillWorks(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	seedLocalUser(t, ctx, s, "alice", "local-pass")

	rec := postLogin(t, "alice", "local-pass")
	if rec.Code != http.StatusOK {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	var body map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("login response ok = false, want true")
	}
	if cookieByName(rec, "session") == nil {
		t.Fatalf("session cookie was not set")
	}
	if cookieByName(rec, "csrf_token") == nil {
		t.Fatalf("csrf_token cookie was not set")
	}

	assertUserByUsername(t, ctx, s, "alice", func(got store.User) {
		if got.LastLoginAt == "" {
			t.Fatalf("LastLoginAt = empty, want timestamp after local login")
		}
	})
}

func TestLoginHandler_LocalUserWrongPasswordDoesNotFallThroughToLDAP(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	seedLocalUser(t, ctx, s, "alice", "local-pass")
	enableLDAPConfig(t, ctx, s)
	client := &fakeLDAPClient{
		searchUserResult: []ldap.DirectoryUser{{
			Username: "alice",
			UID:      "alice-uid",
			DN:       "cn=alice,dc=example,dc=com",
		}},
	}
	ldapService = ldap.NewService(s, client)

	rec := postLogin(t, "alice", "wrong-pass")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusUnauthorized)
	}
	if client.searchUserCalls != 0 || client.bindUserCalls != 0 {
		t.Fatalf("LDAP was called for local wrong password: search=%d bind=%d, want 0/0", client.searchUserCalls, client.bindUserCalls)
	}

	assertUserByUsername(t, ctx, s, "alice", func(got store.User) {
		if got.AuthSource != "local" {
			t.Fatalf("AuthSource = %q, want local", got.AuthSource)
		}
		if got.LastLoginAt != "" {
			t.Fatalf("LastLoginAt = %q, want unchanged empty value after failed login", got.LastLoginAt)
		}
	})
}

func TestLoginHandler_ProvisionsLDAPUserOnFirstSuccessfulLogin(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	enableLDAPConfig(t, ctx, s)
	client := &fakeLDAPClient{
		searchUserResult: []ldap.DirectoryUser{{
			Username:    "alice",
			UID:         "alice-uid",
			DN:          "cn=alice,dc=example,dc=com",
			DisplayName: "Alice LDAP",
			Email:       "alice@example.com",
			Phone:       "123456",
		}},
	}
	ldapService = ldap.NewService(s, client)

	rec := postLogin(t, "alice", "ldap-pass")
	if rec.Code != http.StatusOK {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if client.searchUserCalls != 1 {
		t.Fatalf("SearchUser calls = %d, want 1", client.searchUserCalls)
	}
	if client.bindDN != "cn=alice,dc=example,dc=com" {
		t.Fatalf("BindUser dn = %q, want matched DN", client.bindDN)
	}
	if client.bindPassword != "ldap-pass" {
		t.Fatalf("BindUser password = %q, want submitted password", client.bindPassword)
	}

	assertUserByUsername(t, ctx, s, "alice", func(got store.User) {
		if got.AuthSource != "ldap" {
			t.Fatalf("AuthSource = %q, want ldap", got.AuthSource)
		}
		if got.Role != store.RoleUser {
			t.Fatalf("Role = %q, want %q", got.Role, store.RoleUser)
		}
		if got.LDAPUID != "alice-uid" {
			t.Fatalf("LDAPUID = %q, want alice-uid", got.LDAPUID)
		}
		if got.LDAPDN != "cn=alice,dc=example,dc=com" {
			t.Fatalf("LDAPDN = %q, want matched DN", got.LDAPDN)
		}
		if got.LastLoginAt == "" {
			t.Fatalf("LastLoginAt = empty, want timestamp after LDAP login")
		}
	})
}

func TestLoginHandler_ExistingLDAPBackedUserStillAuthenticatesViaLDAP(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	enableLDAPConfig(t, ctx, s)
	seedLDAPBackedUser(t, ctx, s, "alice", "alice-uid", "cn=alice,dc=example,dc=com")

	client := &fakeLDAPClient{
		searchUserResult: []ldap.DirectoryUser{{
			Username:    "alice",
			UID:         "alice-uid",
			DN:          "cn=alice,dc=example,dc=com",
			DisplayName: "Alice LDAP",
			Email:       "alice@example.com",
			Phone:       "123456",
		}},
	}
	ldapService = ldap.NewService(s, client)

	rec := postLogin(t, "alice", "ldap-pass")
	if rec.Code != http.StatusOK {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if client.searchUserCalls != 1 {
		t.Fatalf("SearchUser calls = %d, want 1", client.searchUserCalls)
	}
	if client.bindUserCalls != 1 {
		t.Fatalf("BindUser calls = %d, want 1", client.bindUserCalls)
	}

	assertUserByUsername(t, ctx, s, "alice", func(got store.User) {
		if got.AuthSource != "ldap" {
			t.Fatalf("AuthSource = %q, want ldap", got.AuthSource)
		}
		if got.LDAPUID != "alice-uid" {
			t.Fatalf("LDAPUID = %q, want alice-uid", got.LDAPUID)
		}
		if got.LastLoginAt == "" {
			t.Fatalf("LastLoginAt = empty, want timestamp after LDAP login")
		}
	})
}

func TestLoginHandler_RejectsLDAPUserWhenDirectoryMatchIsAmbiguous(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	enableLDAPConfig(t, ctx, s)
	client := &fakeLDAPClient{
		searchUserResult: []ldap.DirectoryUser{
			{Username: "alice", UID: "alice-uid-1", DN: "cn=alice1,dc=example,dc=com"},
			{Username: "alice", UID: "alice-uid-2", DN: "cn=alice2,dc=example,dc=com"},
		},
	}
	ldapService = ldap.NewService(s, client)

	rec := postLogin(t, "alice", "ldap-pass")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusUnauthorized)
	}
	if client.bindUserCalls != 0 {
		t.Fatalf("BindUser calls = %d, want 0 for ambiguous search", client.bindUserCalls)
	}

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		_, err := store.GetUserByUsername(ctx, tx, "alice")
		if err == nil {
			t.Fatalf("ambiguous LDAP login created user row, want no row")
		}
		if err != sql.ErrNoRows {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("verify no LDAP user row: %v", err)
	}
}

func TestLoginHandler_ReturnsServerErrorOnLDAPOperationalFailure(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)

	enableLDAPConfig(t, ctx, s)
	client := &fakeLDAPClient{
		searchUserResult: []ldap.DirectoryUser{{
			Username: "alice",
			UID:      "alice-uid",
			DN:       "cn=alice,dc=example,dc=com",
		}},
		bindErr: errors.New("ldap unavailable"),
	}
	ldapService = ldap.NewService(s, client)

	rec := postLogin(t, "alice", "ldap-pass")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("LoginHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusInternalServerError)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != "login failed" {
		t.Fatalf("error response = %q, want %q", body["error"], "login failed")
	}
}

func setupAuthHandlerTest(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()

	oldStore := appStore
	oldLDAPService := ldapService
	s, err := store.Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("store.Open() err = %v", err)
	}
	appStore = s
	ldapService = nil
	if err := auth.SetupSecureCookie(s.DB); err != nil {
		t.Fatalf("auth.SetupSecureCookie() err = %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		appStore = oldStore
		ldapService = oldLDAPService
	})
	return s
}

func seedLocalUser(t *testing.T, ctx context.Context, s *store.Store, username string, password string) store.User {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword() err = %v", err)
	}
	var user store.User
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:     username,
			PasswordHash: string(hash),
			Role:         store.RoleUser,
			AuthSource:   "local",
		})
		user = created
		return err
	}); err != nil {
		t.Fatalf("seed local user: %v", err)
	}
	return user
}

func seedLDAPBackedUser(t *testing.T, ctx context.Context, s *store.Store, username string, ldapUID string, ldapDN string) store.User {
	t.Helper()

	var user store.User
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:        username,
			PasswordHash:    "",
			Role:            store.RoleUser,
			AuthSource:      "ldap",
			LDAPUID:         ldapUID,
			LDAPDN:          ldapDN,
			LDAPSyncEnabled: true,
			LDAPPresent:     true,
		})
		user = created
		return err
	}); err != nil {
		t.Fatalf("seed LDAP-backed user: %v", err)
	}
	return user
}

func enableLDAPConfig(t *testing.T, ctx context.Context, s *store.Store) {
	t.Helper()

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingBool(ctx, tx, store.SettingLDAPEnabled, true); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPURL, "ldap://ldap.example.com:389"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBaseDN, "dc=example,dc=com"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPUserFilter, "(objectClass=person)"); err != nil {
			return err
		}
		return store.SetSettingString(ctx, tx, store.SettingLDAPLoginAttr, "uid")
	}); err != nil {
		t.Fatalf("enable LDAP config: %v", err)
	}
}

func postLogin(t *testing.T, username string, password string) *httptest.ResponseRecorder {
	t.Helper()

	payload := []byte(`{"username":` + mustJSON(t, username) + `,"password":` + mustJSON(t, password) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	LoginHandler(rec, req)
	return rec
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()

	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() err = %v", err)
	}
	return string(b)
}

func cookieByName(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func assertUserByUsername(t *testing.T, ctx context.Context, s *store.Store, username string, check func(store.User)) {
	t.Helper()

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		user, err := store.GetUserByUsername(ctx, tx, username)
		if err != nil {
			return err
		}
		check(user)
		return nil
	}); err != nil {
		t.Fatalf("assert user %q: %v", username, err)
	}
}
