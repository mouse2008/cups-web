package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cups-web/internal/acl"
	"cups-web/internal/auth"
	"cups-web/internal/middleware"
	"cups-web/internal/store"

	"github.com/gorilla/mux"
)

type adminPrinterACLResponse struct {
	PrinterURI string                   `json:"printerUri"`
	Rules      []adminPrinterACLRuleDTO `json:"rules"`
}

type adminPrinterACLRuleDTO struct {
	ID            int64  `json:"id"`
	SubjectType   string `json:"subjectType"`
	SubjectRole   string `json:"subjectRole"`
	SubjectUserID *int64 `json:"subjectUserId"`
	Effect        string `json:"effect"`
}

func TestAdminPrinterACL_GetReturnsEmptyRules(t *testing.T) {
	ctx := context.Background()
	setupAdminHandlerTest(t, ctx)

	rec := performAdminRequest(t, adminGetPrinterACLHandler, http.MethodGet, "/api/admin/printer-acl?printer=ipp://cups/printers/alpha", nil, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminGetPrinterACLHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	var body adminPrinterACLResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode printer ACL response: %v", err)
	}
	if body.PrinterURI != "ipp://cups/printers/alpha" {
		t.Fatalf("printerUri = %q, want alpha printer", body.PrinterURI)
	}
	if len(body.Rules) != 0 {
		t.Fatalf("len(rules) = %d, want 0", len(body.Rules))
	}
}

func TestAdminPrinterACL_GetReturnsExistingRules(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice", "pass")
	printerURI := "ipp://cups/printers/alpha"

	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  printerURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectAllow,
	})
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:    printerURI,
		SubjectType:   store.PrinterACLSubjectUser,
		SubjectUserID: user.ID,
		Effect:        store.PrinterACLEffectDeny,
	})

	rec := performAdminRequest(t, adminGetPrinterACLHandler, http.MethodGet, "/api/admin/printer-acl?printer="+printerURI, nil, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminGetPrinterACLHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	body := decodeAdminPrinterACLResponse(t, rec)
	if len(body.Rules) != 2 {
		t.Fatalf("len(rules) = %d, want 2", len(body.Rules))
	}
	assertAdminPrinterACLRule(t, body.Rules[0], store.PrinterACLSubjectRole, store.RoleUser, nil, store.PrinterACLEffectAllow)
	assertAdminPrinterACLRule(t, body.Rules[1], store.PrinterACLSubjectUser, "", &user.ID, store.PrinterACLEffectDeny)
}

func TestAdminPrinterACL_PutOverwritesRulesAndReadBack(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	deniedUser := seedLocalUser(t, ctx, s, "alice", "pass")
	allowedUser := seedLocalUser(t, ctx, s, "bob", "pass")
	printerURI := "ipp://cups/printers/alpha"
	otherPrinterURI := "ipp://cups/printers/beta"

	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  printerURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleAdmin,
		Effect:      store.PrinterACLEffectAllow,
	})
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  otherPrinterURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})

	rec := performAdminRequest(t, adminUpdatePrinterACLHandler, http.MethodPut, "/api/admin/printer-acl?printer="+printerURI, map[string]any{
		"rules": []map[string]any{
			{
				"subjectType": "role",
				"subjectRole": "user",
				"effect":      "allow",
			},
			{
				"subjectType":   "user",
				"subjectUserId": deniedUser.ID,
				"effect":        "deny",
			},
		},
	}, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminUpdatePrinterACLHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	body := decodeAdminPrinterACLResponse(t, rec)
	if body.PrinterURI != printerURI {
		t.Fatalf("printerUri = %q, want %q", body.PrinterURI, printerURI)
	}
	if len(body.Rules) != 2 {
		t.Fatalf("len(rules) = %d, want 2", len(body.Rules))
	}
	assertAdminPrinterACLRule(t, body.Rules[0], store.PrinterACLSubjectRole, store.RoleUser, nil, store.PrinterACLEffectAllow)
	assertAdminPrinterACLRule(t, body.Rules[1], store.PrinterACLSubjectUser, "", &deniedUser.ID, store.PrinterACLEffectDeny)

	getRec := performAdminRequest(t, adminGetPrinterACLHandler, http.MethodGet, "/api/admin/printer-acl?printer="+printerURI, nil, userAdminSession())
	if getRec.Code != http.StatusOK {
		t.Fatalf("adminGetPrinterACLHandler status = %d, body = %s, want %d", getRec.Code, getRec.Body.String(), http.StatusOK)
	}
	getBody := decodeAdminPrinterACLResponse(t, getRec)
	if len(getBody.Rules) != 2 {
		t.Fatalf("len(get rules) = %d, want 2", len(getBody.Rules))
	}

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		rules, err := store.ListPrinterACLRulesByPrinter(ctx, tx, printerURI)
		if err != nil {
			return err
		}
		if len(rules) != 2 {
			t.Fatalf("stored len(rules) = %d, want 2", len(rules))
		}
		if rules[0].SubjectRole == store.RoleAdmin {
			t.Fatalf("stale rule for %q was not deleted", store.RoleAdmin)
		}

		otherRules, err := store.ListPrinterACLRulesByPrinter(ctx, tx, otherPrinterURI)
		if err != nil {
			return err
		}
		if len(otherRules) != 1 {
			t.Fatalf("len(otherRules) = %d, want 1", len(otherRules))
		}

		authorizer := acl.NewPrinterAuthorizer()
		allowed, err := authorizer.CanUsePrinter(ctx, tx, deniedUser, printerURI)
		if err != nil {
			return err
		}
		if allowed {
			t.Fatalf("denied user still allowed after overwrite")
		}

		allowed, err = authorizer.CanUsePrinter(ctx, tx, allowedUser, printerURI)
		if err != nil {
			return err
		}
		if !allowed {
			t.Fatalf("role allow did not take effect for ordinary user")
		}

		return nil
	}); err != nil {
		t.Fatalf("verify overwritten ACL rules: %v", err)
	}
}

func TestAdminPrinterACL_PutRejectsInvalidPayload(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice", "pass")

	tests := []struct {
		name      string
		target    string
		payload   any
		wantError string
	}{
		{
			name:      "missing printer query",
			target:    "/api/admin/printer-acl",
			payload:   map[string]any{"rules": []map[string]any{}},
			wantError: "printer is required",
		},
		{
			name:   "missing group id",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "group", "effect": "allow"},
			}},
			wantError: "group rule requires subjectGroupId",
		},
		{
			name:   "invalid subject type",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "weird", "effect": "allow"},
			}},
			wantError: "invalid subjectType",
		},
		{
			name:   "missing role value",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "role", "effect": "allow"},
			}},
			wantError: "role rule requires subjectRole",
		},
		{
			name:   "missing user id",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "user", "effect": "allow"},
			}},
			wantError: "user rule requires subjectUserId",
		},
		{
			name:   "unknown user id",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "user", "subjectUserId": user.ID + 1000, "effect": "allow"},
			}},
			wantError: "user not found",
		},
		{
			name:   "invalid effect",
			target: "/api/admin/printer-acl?printer=ipp://cups/printers/alpha",
			payload: map[string]any{"rules": []map[string]any{
				{"subjectType": "role", "subjectRole": "user", "effect": "maybe"},
			}},
			wantError: "invalid effect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := performAdminRequest(t, adminUpdatePrinterACLHandler, http.MethodPut, tt.target, tt.payload, userAdminSession())
			assertJSONError(t, rec, http.StatusBadRequest, tt.wantError)
		})
	}
}

func TestAdminPrinterACL_NonAdminCannotAccess(t *testing.T) {
	ctx := context.Background()
	setupAdminHandlerTest(t, ctx)

	userSession := auth.Session{
		UserID:   123,
		Username: "alice",
		Role:     store.RoleUser,
		Expires:  time.Now().Add(24 * time.Hour),
	}

	getRec := performAdminPrinterACLRouteRequest(t, http.MethodGet, "/api/admin/printer-acl?printer=ipp://cups/printers/alpha", nil, userSession)
	if getRec.Code != http.StatusForbidden {
		t.Fatalf("GET status = %d, body = %s, want %d", getRec.Code, getRec.Body.String(), http.StatusForbidden)
	}

	putRec := performAdminPrinterACLRouteRequest(t, http.MethodPut, "/api/admin/printer-acl?printer=ipp://cups/printers/alpha", map[string]any{
		"rules": []map[string]any{},
	}, userSession)
	if putRec.Code != http.StatusForbidden {
		t.Fatalf("PUT status = %d, body = %s, want %d", putRec.Code, putRec.Body.String(), http.StatusForbidden)
	}
}

func decodeAdminPrinterACLResponse(t *testing.T, rec *httptest.ResponseRecorder) adminPrinterACLResponse {
	t.Helper()

	var body adminPrinterACLResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode printer ACL response: %v", err)
	}
	return body
}

func assertAdminPrinterACLRule(t *testing.T, rule adminPrinterACLRuleDTO, wantSubjectType string, wantSubjectRole string, wantSubjectUserID *int64, wantEffect string) {
	t.Helper()

	if rule.SubjectType != wantSubjectType {
		t.Fatalf("subjectType = %q, want %q", rule.SubjectType, wantSubjectType)
	}
	if rule.SubjectRole != wantSubjectRole {
		t.Fatalf("subjectRole = %q, want %q", rule.SubjectRole, wantSubjectRole)
	}
	if wantSubjectUserID == nil {
		if rule.SubjectUserID != nil {
			t.Fatalf("subjectUserId = %v, want nil", *rule.SubjectUserID)
		}
	} else {
		if rule.SubjectUserID == nil || *rule.SubjectUserID != *wantSubjectUserID {
			t.Fatalf("subjectUserId = %v, want %d", rule.SubjectUserID, *wantSubjectUserID)
		}
	}
	if rule.Effect != wantEffect {
		t.Fatalf("effect = %q, want %q", rule.Effect, wantEffect)
	}
}

func performAdminPrinterACLRouteRequest(t *testing.T, method string, target string, payload any, sess auth.Session) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("encode request payload: %v", err)
		}
	}

	req := httptest.NewRequest(method, target, &body)
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

	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	admin := api.PathPrefix("/admin").Subrouter()
	admin.Use(middleware.RequireSession)
	admin.Use(middleware.RequireAdmin)
	admin.Use(middleware.ValidateCSRF)
	admin.HandleFunc("/printer-acl", adminGetPrinterACLHandler).Methods(http.MethodGet)
	admin.HandleFunc("/printer-acl", adminUpdatePrinterACLHandler).Methods(http.MethodPut)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
