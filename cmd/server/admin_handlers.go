package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"

	"cups-web/internal/auth"
	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"
)

var (
	errDeleteDefaultAdmin = errors.New("default admin cannot be deleted")
	errDeleteLDAPUser     = errors.New("ldap users cannot be deleted")
	errProtectedRole      = errors.New("protected admin role cannot change")
	errAdminRename        = errors.New("admin username cannot change")
	errLDAPPasswordChange = errors.New("ldap user password cannot be changed in app")
	errLDAPUsernameChange = errors.New("ldap username cannot be changed in app")
)

type adminUserPayload struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	ContactName string `json:"contactName"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
}

type adminUserResponse struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	Role            string `json:"role"`
	Protected       bool   `json:"protected"`
	ContactName     string `json:"contactName"`
	Phone           string `json:"phone"`
	Email           string `json:"email"`
	AuthSource      string `json:"authSource"`
	LDAPSyncEnabled bool   `json:"ldapSyncEnabled"`
	LDAPPresent     bool   `json:"ldapPresent"`
	LastLDAPSyncAt  string `json:"lastLdapSyncAt"`
	LastLoginAt     string `json:"lastLoginAt"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type settingsPayload struct {
	RetentionDays *int64               `json:"retentionDays"`
	LDAP          *ldapSettingsPayload `json:"ldap"`
}

type ldapSettingsPayload struct {
	Enabled             *bool   `json:"enabled"`
	URL                 *string `json:"url"`
	BindDN              *string `json:"bindDn"`
	BindPassword        *string `json:"bindPassword"`
	BaseDN              *string `json:"baseDn"`
	UserFilter          *string `json:"userFilter"`
	LoginAttr           *string `json:"loginAttr"`
	DisplayNameAttr     *string `json:"displayNameAttr"`
	EmailAttr           *string `json:"emailAttr"`
	PhoneAttr           *string `json:"phoneAttr"`
	SyncIntervalMinutes *int64  `json:"syncIntervalMinutes"`
}

type adminSettingsResponse struct {
	RetentionDays  int64                     `json:"retentionDays"`
	LDAP           adminLDAPSettingsResponse `json:"ldap"`
	LDAPSyncStatus adminLDAPSyncStatus       `json:"ldapSyncStatus"`
}

type adminLDAPSettingsResponse struct {
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
}

type adminLDAPSyncStatus struct {
	LastStartedAt  string `json:"lastStartedAt"`
	LastFinishedAt string `json:"lastFinishedAt"`
	LastStatus     string `json:"lastStatus"`
	LastMessage    string `json:"lastMessage"`
	LastCount      int64  `json:"lastCount"`
}

type ldapSyncService interface {
	SyncAll(ctx context.Context, cfg ldapauth.Config) (ldapauth.SyncReport, error)
}

func adminListUsersHandler(w http.ResponseWriter, r *http.Request) {
	var resp []adminUserResponse
	err := appStore.WithTx(r.Context(), true, func(tx *sql.Tx) error {
		users, err := store.ListUsers(r.Context(), tx)
		if err != nil {
			return err
		}
		resp = mapAdminUsers(users)
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, resp)
}

func adminCreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var payload adminUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Username == "" || payload.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "username and password required")
		return
	}
	role := normalizeRole(payload.Role)
	if role == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid role")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	var created store.User
	err = appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		user, err := store.CreateUser(r.Context(), tx, store.CreateUserInput{
			Username:     payload.Username,
			PasswordHash: string(hash),
			Role:         role,
			Protected:    false,
			ContactName:  payload.ContactName,
			Phone:        payload.Phone,
			Email:        payload.Email,
		})
		if err != nil {
			return err
		}
		created = user
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	writeJSON(w, mapAdminUser(created))
}

func adminUpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var payload adminUserPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Username == "" {
		writeJSONError(w, http.StatusBadRequest, "username required")
		return
	}
	role := normalizeRole(payload.Role)
	if role == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid role")
		return
	}

	var updated store.User
	err = appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		current, err := store.GetUserByID(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if current.AuthSource == "ldap" {
			if strings.TrimSpace(payload.Password) != "" {
				return errLDAPPasswordChange
			}
			if payload.Username != current.Username {
				return errLDAPUsernameChange
			}
		}
		if current.Username == "admin" && payload.Username != "admin" {
			return errAdminRename
		}
		if current.Username == "admin" && role != store.RoleAdmin {
			return errProtectedRole
		}
		if current.Username == "admin" {
			role = store.RoleAdmin
		}

		var pwdHash *string
		if strings.TrimSpace(payload.Password) != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			h := string(hash)
			pwdHash = &h
		}

		user, err := store.UpdateUser(r.Context(), tx, store.UpdateUserInput{
			ID:           id,
			Username:     payload.Username,
			PasswordHash: pwdHash,
			Role:         role,
			ContactName:  payload.ContactName,
			Phone:        payload.Phone,
			Email:        payload.Email,
		})
		if err != nil {
			return err
		}
		updated = user
		return nil
	})
	if err != nil {
		if errors.Is(err, errLDAPPasswordChange) {
			writeJSONError(w, http.StatusBadRequest, errLDAPPasswordChange.Error())
			return
		}
		if errors.Is(err, errLDAPUsernameChange) {
			writeJSONError(w, http.StatusBadRequest, errLDAPUsernameChange.Error())
			return
		}
		if errors.Is(err, errAdminRename) {
			writeJSONError(w, http.StatusBadRequest, errAdminRename.Error())
			return
		}
		if errors.Is(err, errProtectedRole) {
			writeJSONError(w, http.StatusBadRequest, "admin role cannot change")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "user not found")
		} else {
			if errors.Is(err, bcrypt.ErrPasswordTooLong) {
				writeJSONError(w, http.StatusBadRequest, "password too long")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "failed to update user")
		}
		return
	}
	writeJSON(w, mapAdminUser(updated))
}

func adminDeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	sess, _ := auth.GetSession(r)
	if sess.UserID == id {
		writeJSONError(w, http.StatusBadRequest, "cannot delete current user")
		return
	}
	err = appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		user, err := store.GetUserByID(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if user.AuthSource == "ldap" {
			return errDeleteLDAPUser
		}
		if user.Username == "admin" {
			return errDeleteDefaultAdmin
		}
		return store.DeleteUser(r.Context(), tx, id)
	})
	if err != nil {
		if errors.Is(err, errDeleteLDAPUser) {
			writeJSONError(w, http.StatusBadRequest, errDeleteLDAPUser.Error())
			return
		}
		if errors.Is(err, errDeleteDefaultAdmin) {
			writeJSONError(w, http.StatusBadRequest, "admin cannot be deleted")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "user not found")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "failed to delete user")
		}
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func adminGetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var resp adminSettingsResponse
	err := appStore.WithTx(r.Context(), true, func(tx *sql.Tx) error {
		val, err := store.GetSettingInt(r.Context(), tx, store.SettingRetentionDays, 0)
		if err != nil {
			return err
		}
		resp.RetentionDays = val

		cfg, err := loadLDAPSettingsResponse(r.Context(), tx)
		if err != nil {
			return err
		}
		resp.LDAP = cfg

		status, err := loadLDAPSyncStatus(r.Context(), tx)
		if err != nil {
			return err
		}
		resp.LDAPSyncStatus = status
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, resp)
}

func adminUpdateSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var payload settingsPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	err := appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		if payload.RetentionDays != nil {
			if *payload.RetentionDays < 0 {
				return errors.New("invalid retentionDays")
			}
			if err := store.SetSettingInt(r.Context(), tx, store.SettingRetentionDays, *payload.RetentionDays); err != nil {
				return err
			}
		}
		if payload.LDAP != nil {
			if err := updateLDAPSettings(r.Context(), tx, payload.LDAP); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func adminLDAPSyncHandler(w http.ResponseWriter, r *http.Request) {
	cfg, err := ldapauth.LoadConfig(r.Context(), appStore)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load ldap settings")
		return
	}

	syncer, ok := any(currentLDAPService()).(ldapSyncService)
	if !ok || syncer == nil {
		writeJSONError(w, http.StatusInternalServerError, "ldap service not initialized")
		return
	}

	report, err := syncer.SyncAll(r.Context(), cfg)
	if err != nil {
		if errors.Is(err, ldapauth.ErrLDAPDisabled) || errors.Is(err, ldapauth.ErrInvalidConfig) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "ldap sync failed")
		return
	}

	writeJSON(w, map[string]any{
		"ok":     true,
		"report": report,
	})
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "":
		return store.RoleUser
	case store.RoleUser:
		return store.RoleUser
	case store.RoleAdmin:
		return store.RoleAdmin
	default:
		return ""
	}
}

func parseIDParam(r *http.Request) (int64, error) {
	idStr := mux.Vars(r)["id"]
	return strconv.ParseInt(idStr, 10, 64)
}

func mapAdminUsers(users []store.User) []adminUserResponse {
	resp := make([]adminUserResponse, 0, len(users))
	for _, user := range users {
		resp = append(resp, mapAdminUser(user))
	}
	return resp
}

func mapAdminUser(user store.User) adminUserResponse {
	return adminUserResponse{
		ID:              user.ID,
		Username:        user.Username,
		Role:            user.Role,
		Protected:       user.Username == "admin" || user.Protected,
		ContactName:     user.ContactName,
		Phone:           user.Phone,
		Email:           user.Email,
		AuthSource:      user.AuthSource,
		LDAPSyncEnabled: user.LDAPSyncEnabled,
		LDAPPresent:     user.LDAPPresent,
		LastLDAPSyncAt:  user.LastLDAPSyncAt,
		LastLoginAt:     user.LastLoginAt,
		CreatedAt:       user.CreatedAt,
		UpdatedAt:       user.UpdatedAt,
	}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func updateLDAPSettings(ctx context.Context, tx *sql.Tx, payload *ldapSettingsPayload) error {
	if payload.Enabled != nil {
		if err := store.SetSettingBool(ctx, tx, store.SettingLDAPEnabled, *payload.Enabled); err != nil {
			return err
		}
	}
	if payload.URL != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPURL, strings.TrimSpace(*payload.URL)); err != nil {
			return err
		}
	}
	if payload.BindDN != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBindDN, strings.TrimSpace(*payload.BindDN)); err != nil {
			return err
		}
	}
	if payload.BindPassword != nil && strings.TrimSpace(*payload.BindPassword) != "" {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBindPassword, *payload.BindPassword); err != nil {
			return err
		}
	}
	if payload.BaseDN != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPBaseDN, strings.TrimSpace(*payload.BaseDN)); err != nil {
			return err
		}
	}
	if payload.UserFilter != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPUserFilter, strings.TrimSpace(*payload.UserFilter)); err != nil {
			return err
		}
	}
	if payload.LoginAttr != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPLoginAttr, strings.TrimSpace(*payload.LoginAttr)); err != nil {
			return err
		}
	}
	if payload.DisplayNameAttr != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPDisplayNameAttr, strings.TrimSpace(*payload.DisplayNameAttr)); err != nil {
			return err
		}
	}
	if payload.EmailAttr != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPEmailAttr, strings.TrimSpace(*payload.EmailAttr)); err != nil {
			return err
		}
	}
	if payload.PhoneAttr != nil {
		if err := store.SetSettingString(ctx, tx, store.SettingLDAPPhoneAttr, strings.TrimSpace(*payload.PhoneAttr)); err != nil {
			return err
		}
	}
	if payload.SyncIntervalMinutes != nil {
		if *payload.SyncIntervalMinutes <= 0 {
			return errors.New("invalid syncIntervalMinutes")
		}
		if err := store.SetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, *payload.SyncIntervalMinutes); err != nil {
			return err
		}
	}
	return nil
}

func loadLDAPSettingsResponse(ctx context.Context, tx *sql.Tx) (adminLDAPSettingsResponse, error) {
	var resp adminLDAPSettingsResponse

	enabled, err := store.GetSettingBool(ctx, tx, store.SettingLDAPEnabled, false)
	if err != nil {
		return resp, err
	}
	url, err := store.GetSettingString(ctx, tx, store.SettingLDAPURL, "")
	if err != nil {
		return resp, err
	}
	bindDN, err := store.GetSettingString(ctx, tx, store.SettingLDAPBindDN, "")
	if err != nil {
		return resp, err
	}
	bindPassword, err := store.GetSettingString(ctx, tx, store.SettingLDAPBindPassword, "")
	if err != nil {
		return resp, err
	}
	baseDN, err := store.GetSettingString(ctx, tx, store.SettingLDAPBaseDN, "")
	if err != nil {
		return resp, err
	}
	userFilter, err := store.GetSettingString(ctx, tx, store.SettingLDAPUserFilter, "(objectClass=person)")
	if err != nil {
		return resp, err
	}
	loginAttr, err := store.GetSettingString(ctx, tx, store.SettingLDAPLoginAttr, "uid")
	if err != nil {
		return resp, err
	}
	displayNameAttr, err := store.GetSettingString(ctx, tx, store.SettingLDAPDisplayNameAttr, "cn")
	if err != nil {
		return resp, err
	}
	emailAttr, err := store.GetSettingString(ctx, tx, store.SettingLDAPEmailAttr, "mail")
	if err != nil {
		return resp, err
	}
	phoneAttr, err := store.GetSettingString(ctx, tx, store.SettingLDAPPhoneAttr, "telephoneNumber")
	if err != nil {
		return resp, err
	}
	syncIntervalMinutes, err := store.GetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, 60)
	if err != nil {
		return resp, err
	}

	resp = adminLDAPSettingsResponse{
		Enabled:             enabled,
		URL:                 url,
		BindDN:              bindDN,
		BindPassword:        "",
		HasBindPassword:     strings.TrimSpace(bindPassword) != "",
		BaseDN:              baseDN,
		UserFilter:          userFilter,
		LoginAttr:           loginAttr,
		DisplayNameAttr:     displayNameAttr,
		EmailAttr:           emailAttr,
		PhoneAttr:           phoneAttr,
		SyncIntervalMinutes: syncIntervalMinutes,
	}
	return resp, nil
}

func loadLDAPSyncStatus(ctx context.Context, tx *sql.Tx) (adminLDAPSyncStatus, error) {
	var resp adminLDAPSyncStatus
	var err error

	resp.LastStartedAt, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncStartedAt, "")
	if err != nil {
		return resp, err
	}
	resp.LastFinishedAt, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncFinishedAt, "")
	if err != nil {
		return resp, err
	}
	resp.LastStatus, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncStatus, "")
	if err != nil {
		return resp, err
	}
	resp.LastMessage, err = store.GetSettingString(ctx, tx, store.SettingLDAPLastSyncMessage, "")
	if err != nil {
		return resp, err
	}
	resp.LastCount, err = store.GetSettingInt(ctx, tx, store.SettingLDAPLastSyncCount, 0)
	if err != nil {
		return resp, err
	}
	return resp, nil
}
