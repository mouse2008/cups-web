package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"cups-web/internal/auth"
	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"

	"golang.org/x/crypto/bcrypt"
)

var errInvalidCredentials = errors.New("invalid credentials")

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]string{"error": msg})
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoginHandler handles POST /api/login
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "missing credentials")
		return
	}

	user, err := authenticateUser(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "login failed")
		return
	}

	sess := auth.Session{UserID: user.ID, Username: user.Username, Role: user.Role}
	if err := auth.SetSession(w, sess); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session error")
		return
	}
	// set csrf token cookie (readable by JS)
	token := randomToken()
	csrfCookie := &http.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	}
	http.SetCookie(w, csrfCookie)
	writeJSON(w, map[string]bool{"ok": true})
}

func authenticateUser(ctx context.Context, username string, password string) (store.User, error) {
	localUser, localErr := findLocalUser(ctx, username)
	if localErr != nil {
		return store.User{}, localErr
	}

	if localUser.ID != 0 && localUser.AuthSource == "local" {
		if bcrypt.CompareHashAndPassword([]byte(localUser.PasswordHash), []byte(password)) != nil {
			return store.User{}, errInvalidCredentials
		}
		if err := appStore.WithTx(ctx, false, func(tx *sql.Tx) error {
			return store.TouchLastLogin(ctx, tx, localUser.ID)
		}); err != nil {
			return store.User{}, err
		}
		localUser, err := findLocalUser(ctx, username)
		if err != nil {
			return store.User{}, err
		}
		return localUser, nil
	}

	cfg, err := ldapauth.LoadConfig(ctx, appStore)
	if err != nil {
		return store.User{}, errInvalidCredentials
	}
	if !cfg.Enabled {
		return store.User{}, errInvalidCredentials
	}
	service := currentLDAPService()
	if service == nil {
		return store.User{}, errInvalidCredentials
	}
	user, err := service.AuthenticateOrProvision(ctx, cfg, username, password)
	if err != nil {
		return store.User{}, errInvalidCredentials
	}
	return user, nil
}

func findLocalUser(ctx context.Context, username string) (store.User, error) {
	var user store.User
	err := appStore.WithTx(ctx, true, func(tx *sql.Tx) error {
		found, err := store.GetUserByUsername(ctx, tx, username)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		user = found
		return nil
	})
	return user, err
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	auth.ClearSession(w)
	writeJSON(w, map[string]bool{"ok": true})
}

// SessionHandler handles GET /api/session and returns session info if present
func SessionHandler(w http.ResponseWriter, r *http.Request) {
	sess, err := auth.GetSession(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, sess)
}

func CSRFHandler(w http.ResponseWriter, r *http.Request) {
	// Not used: CSRF token is set on login; provide endpoint if needed
	token := randomToken()
	csrfCookie := &http.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	}
	http.SetCookie(w, csrfCookie)
	writeJSON(w, map[string]string{"csrfToken": token})
}
