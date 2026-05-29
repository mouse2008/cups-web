package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"cups-web/internal/auth"
	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"

	"github.com/gorilla/mux"
)

type fakeAdminLDAPService struct {
	syncCalled bool
	syncCfg    ldapauth.Config
	syncReport ldapauth.SyncReport
	syncErr    error
}

func (f *fakeAdminLDAPService) AuthenticateOrProvision(ctx context.Context, cfg ldapauth.Config, username string, password string) (store.User, error) {
	return store.User{}, nil
}

func (f *fakeAdminLDAPService) SyncAll(ctx context.Context, cfg ldapauth.Config) (ldapauth.SyncReport, error) {
	f.syncCalled = true
	f.syncCfg = cfg
	return f.syncReport, f.syncErr
}

func TestAdminUpdateUser_RejectsLDAPPasswordChange(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user := seedLDAPBackedUser(t, ctx, s, "alice", "alice-uid", "cn=alice,dc=example,dc=com")

	rec := performAdminRequest(t, adminUpdateUserHandler, http.MethodPut, "/api/admin/users/1", map[string]any{
		"username":    "alice",
		"password":    "new-secret",
		"role":        "admin",
		"contactName": "Alice LDAP",
		"phone":       "123456",
		"email":       "alice@example.com",
	}, userAdminSession(), map[string]string{"id": strconv.FormatInt(user.ID, 10)})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("adminUpdateUserHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != "ldap user password cannot be changed in app" {
		t.Fatalf("error = %q, want ldap password rejection", body["error"])
	}

	assertUserByID(t, ctx, s, user.ID, func(got store.User) {
		if got.Role != store.RoleUser {
			t.Fatalf("Role = %q, want unchanged %q", got.Role, store.RoleUser)
		}
		if got.PasswordHash != "" {
			t.Fatalf("PasswordHash = %q, want unchanged empty hash", got.PasswordHash)
		}
	})
}

func TestAdminDeleteUser_RejectsLDAPUser(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user := seedLDAPBackedUser(t, ctx, s, "alice", "alice-uid", "cn=alice,dc=example,dc=com")

	rec := performAdminRequest(t, adminDeleteUserHandler, http.MethodDelete, "/api/admin/users/1", nil, userAdminSession(), map[string]string{"id": strconv.FormatInt(user.ID, 10)})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("adminDeleteUserHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != "ldap users cannot be deleted" {
		t.Fatalf("error = %q, want LDAP delete rejection", body["error"])
	}

	assertUserByID(t, ctx, s, user.ID, func(got store.User) {
		if got.Username != "alice" {
			t.Fatalf("Username = %q, want original user to remain", got.Username)
		}
	})
}

func TestAdminLDAPSync_TriggersServiceSync(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	enableFullLDAPConfig(t, ctx, s)

	fake := &fakeAdminLDAPService{
		syncReport: ldapauth.SyncReport{Upserted: 3, Skipped: 1, MissingMarked: 2},
	}
	oldLDAPService := ldapService
	ldapService = fake
	t.Cleanup(func() {
		ldapService = oldLDAPService
	})

	rec := performAdminRequest(t, adminLDAPSyncHandler, http.MethodPost, "/api/admin/ldap/sync", map[string]any{}, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminLDAPSyncHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if !fake.syncCalled {
		t.Fatalf("SyncAll was not called")
	}
	if fake.syncCfg.URL != "ldap://ldap.example.com:389" {
		t.Fatalf("SyncAll cfg.URL = %q, want configured LDAP URL", fake.syncCfg.URL)
	}

	var body struct {
		OK     bool                `json:"ok"`
		Report ldapauth.SyncReport `json:"report"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if !body.OK {
		t.Fatalf("ok = false, want true")
	}
	if body.Report != fake.syncReport {
		t.Fatalf("report = %+v, want %+v", body.Report, fake.syncReport)
	}
}

func TestAdminSettingsLDAP_RoundTripMasksStoredPassword(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncStartedAt, "2026-05-29T10:00:00Z"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncFinishedAt, "2026-05-29T10:01:00Z"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncStatus, "ok"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLastSyncMessage, "synced"); err != nil {
			return err
		}
		return store.SetSettingInt(ctx, tx, store.SettingLDAPLastSyncCount, 7)
	}); err != nil {
		t.Fatalf("seed LDAP sync status: %v", err)
	}

	putRec := performAdminRequest(t, adminUpdateSettingsHandler, http.MethodPut, "/api/admin/settings", map[string]any{
		"retentionDays": 14,
		"ldap": map[string]any{
			"enabled":             true,
			"url":                 "ldap://ldap.example.com:389",
			"bindDn":              "cn=service,dc=example,dc=com",
			"bindPassword":        "super-secret",
			"baseDn":              "dc=example,dc=com",
			"userFilter":          "(objectClass=person)",
			"loginAttr":           "uid",
			"displayNameAttr":     "cn",
			"emailAttr":           "mail",
			"phoneAttr":           "telephoneNumber",
			"syncIntervalMinutes": 45,
		},
	}, userAdminSession())
	if putRec.Code != http.StatusOK {
		t.Fatalf("adminUpdateSettingsHandler PUT status = %d, body = %s, want %d", putRec.Code, putRec.Body.String(), http.StatusOK)
	}

	getRec := performAdminRequest(t, adminGetSettingsHandler, http.MethodGet, "/api/admin/settings", nil, userAdminSession())
	if getRec.Code != http.StatusOK {
		t.Fatalf("adminGetSettingsHandler GET status = %d, body = %s, want %d", getRec.Code, getRec.Body.String(), http.StatusOK)
	}

	var body struct {
		RetentionDays int64 `json:"retentionDays"`
		LDAP          struct {
			Enabled             bool   `json:"enabled"`
			URL                 string `json:"url"`
			BindDN              string `json:"bindDn"`
			BindPassword        string `json:"bindPassword"`
			HasBindPassword     bool   `json:"hasBindPassword"`
			BaseDN              string `json:"baseDn"`
			UserFilter          string `json:"userFilter"`
			LoginAttr           string `json:"loginAttr"`
			DisplayNameAttr     string `json:"displayNameAttr"`
			EmailAttr           string `json:"emailAttr"`
			PhoneAttr           string `json:"phoneAttr"`
			SyncIntervalMinutes int64  `json:"syncIntervalMinutes"`
		} `json:"ldap"`
		LDAPSyncStatus struct {
			LastStartedAt  string `json:"lastStartedAt"`
			LastFinishedAt string `json:"lastFinishedAt"`
			LastStatus     string `json:"lastStatus"`
			LastMessage    string `json:"lastMessage"`
			LastCount      int64  `json:"lastCount"`
		} `json:"ldapSyncStatus"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if body.RetentionDays != 14 {
		t.Fatalf("retentionDays = %d, want 14", body.RetentionDays)
	}
	if !body.LDAP.Enabled {
		t.Fatalf("ldap.enabled = false, want true")
	}
	if body.LDAP.BindDN != "cn=service,dc=example,dc=com" {
		t.Fatalf("ldap.bindDn = %q, want configured DN", body.LDAP.BindDN)
	}
	if body.LDAP.BindPassword != "" {
		t.Fatalf("ldap.bindPassword = %q, want masked empty response", body.LDAP.BindPassword)
	}
	if !body.LDAP.HasBindPassword {
		t.Fatalf("ldap.hasBindPassword = false, want true")
	}
	if body.LDAP.SyncIntervalMinutes != 45 {
		t.Fatalf("ldap.syncIntervalMinutes = %d, want 45", body.LDAP.SyncIntervalMinutes)
	}
	if body.LDAPSyncStatus.LastStartedAt != "2026-05-29T10:00:00Z" {
		t.Fatalf("ldapSyncStatus.lastStartedAt = %q, want seeded value", body.LDAPSyncStatus.LastStartedAt)
	}
	if body.LDAPSyncStatus.LastFinishedAt != "2026-05-29T10:01:00Z" {
		t.Fatalf("ldapSyncStatus.lastFinishedAt = %q, want seeded value", body.LDAPSyncStatus.LastFinishedAt)
	}
	if body.LDAPSyncStatus.LastStatus != "ok" {
		t.Fatalf("ldapSyncStatus.lastStatus = %q, want ok", body.LDAPSyncStatus.LastStatus)
	}
	if body.LDAPSyncStatus.LastMessage != "synced" {
		t.Fatalf("ldapSyncStatus.lastMessage = %q, want synced", body.LDAPSyncStatus.LastMessage)
	}
	if body.LDAPSyncStatus.LastCount != 7 {
		t.Fatalf("ldapSyncStatus.lastCount = %d, want 7", body.LDAPSyncStatus.LastCount)
	}

	assertSettingString(t, ctx, s, store.SettingLDAPBindPassword, "super-secret")

	putRec2 := performAdminRequest(t, adminUpdateSettingsHandler, http.MethodPut, "/api/admin/settings", map[string]any{
		"ldap": map[string]any{
			"enabled":             true,
			"url":                 "ldap://ldap.example.com:389",
			"bindDn":              "cn=service,dc=example,dc=com",
			"bindPassword":        "",
			"baseDn":              "dc=example,dc=com",
			"userFilter":          "(objectClass=person)",
			"loginAttr":           "uid",
			"displayNameAttr":     "cn",
			"emailAttr":           "mail",
			"phoneAttr":           "telephoneNumber",
			"syncIntervalMinutes": 45,
		},
	}, userAdminSession())
	if putRec2.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body = %s, want %d", putRec2.Code, putRec2.Body.String(), http.StatusOK)
	}

	assertSettingString(t, ctx, s, store.SettingLDAPBindPassword, "super-secret")
}

func setupAdminHandlerTest(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()

	return setupAuthHandlerTest(t, ctx)
}

func performAdminRequest(t *testing.T, handler http.HandlerFunc, method string, target string, payload any, sess auth.Session, routeVars ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("encode request payload: %v", err)
		}
	}

	req := httptest.NewRequest(method, target, &body)
	if len(routeVars) > 0 && routeVars[0] != nil {
		req = mux.SetURLVars(req, routeVars[0])
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "test-csrf-token")
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "test-csrf-token"})
	}

	rec := httptest.NewRecorder()
	if err := auth.SetSession(rec, sess); err != nil {
		t.Fatalf("auth.SetSession() err = %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}

	rec = httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func userAdminSession() auth.Session {
	return auth.Session{
		UserID:   999,
		Username: "admin",
		Role:     store.RoleAdmin,
		Expires:  time.Now().Add(24 * time.Hour),
	}
}

func assertUserByID(t *testing.T, ctx context.Context, s *store.Store, id int64, check func(store.User)) {
	t.Helper()

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		user, err := store.GetUserByID(ctx, tx, id)
		if err != nil {
			return err
		}
		check(user)
		return nil
	}); err != nil {
		t.Fatalf("assert user %d: %v", id, err)
	}
}

func assertSettingString(t *testing.T, ctx context.Context, s *store.Store, key string, want string) {
	t.Helper()

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		got, err := store.GetSettingString(ctx, tx, key, "")
		if err != nil {
			return err
		}
		if got != want {
			t.Fatalf("setting %s = %q, want %q", key, got, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("assert setting %s: %v", key, err)
	}
}

func enableFullLDAPConfig(t *testing.T, ctx context.Context, s *store.Store) {
	t.Helper()

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := store.SetSettingBool(ctx, tx, store.SettingLDAPEnabled, true); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPURL, "ldap://ldap.example.com:389"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBindDN, "cn=service,dc=example,dc=com"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBindPassword, "super-secret"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBaseDN, "dc=example,dc=com"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPUserFilter, "(objectClass=person)"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLoginAttr, "uid"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPDisplayNameAttr, "cn"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPEmailAttr, "mail"); err != nil {
			return err
		}
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPPhoneAttr, "telephoneNumber"); err != nil {
			return err
		}
		return store.SetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, 45)
	}); err != nil {
		t.Fatalf("enable full LDAP config: %v", err)
	}
}
