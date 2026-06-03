package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cups-web/internal/auth"
	"cups-web/internal/ipp"
	"cups-web/internal/store"
)

func TestListPrintersHandler_FiltersPrintersForUserACL(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice", "pass")

	cups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
			<a href="/printers/allow">Allow Printer</a>
			<a href="/printers/deny">Deny Printer</a>
			<a href="/printers/open">Open Printer</a>
		</body></html>`))
	}))
	defer cups.Close()

	host := mustURLHost(t, cups.URL)
	allowURI := "http://" + host + "/printers/allow"
	denyURI := "http://" + host + "/printers/deny"
	openURI := "http://" + host + "/printers/open"

	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:    allowURI,
		SubjectType:   store.PrinterACLSubjectUser,
		SubjectUserID: user.ID,
		Effect:        store.PrinterACLEffectAllow,
	})
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  denyURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})

	t.Setenv("CUPS_HOST", cups.URL)

	rec := performSessionRequest(t, http.MethodGet, "/api/printers", nil, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var printers []ipp.Printer
	if err := json.NewDecoder(rec.Body).Decode(&printers); err != nil {
		t.Fatalf("decode printers response: %v", err)
	}
	if len(printers) != 2 {
		t.Fatalf("len(printers) = %d, want 2", len(printers))
	}
	assertPrinterURIs(t, printers, allowURI, openURI)
}

func TestListPrintersHandler_FiltersPrintersForUserGroupACL(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice-group", "pass")

	cups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
			<a href="/printers/group-allow">Group Allow Printer</a>
			<a href="/printers/group-miss">Group Miss Printer</a>
			<a href="/printers/open">Open Printer</a>
		</body></html>`))
	}))
	defer cups.Close()

	host := mustURLHost(t, cups.URL)
	groupAllowURI := "http://" + host + "/printers/group-allow"
	groupMissURI := "http://" + host + "/printers/group-miss"
	openURI := "http://" + host + "/printers/open"

	groupID := seedPrinterGroupWithMembers(t, ctx, s, "Finance", user.ID)
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:     groupAllowURI,
		SubjectType:    store.PrinterACLSubjectGroup,
		SubjectGroupID: groupID,
		Effect:         store.PrinterACLEffectAllow,
	})
	otherGroupID := seedPrinterGroupWithMembers(t, ctx, s, "Marketing")
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:     groupMissURI,
		SubjectType:    store.PrinterACLSubjectGroup,
		SubjectGroupID: otherGroupID,
		Effect:         store.PrinterACLEffectAllow,
	})

	t.Setenv("CUPS_HOST", cups.URL)

	rec := performSessionRequest(t, http.MethodGet, "/api/printers", nil, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var printers []ipp.Printer
	if err := json.NewDecoder(rec.Body).Decode(&printers); err != nil {
		t.Fatalf("decode printers response: %v", err)
	}
	if len(printers) != 2 {
		t.Fatalf("len(printers) = %d, want 2", len(printers))
	}
	assertPrinterURIs(t, printers, groupAllowURI, openURI)
}

func TestPrinterInfoHandler_ReturnsForbiddenForUnauthorizedPrinter(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice", "pass")

	cups := newPrinterListServer(t, "deny")
	defer cups.Close()
	printerURI := "http://" + mustURLHost(t, cups.URL) + "/printers/deny"
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  printerURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})
	t.Setenv("CUPS_HOST", cups.URL)

	rec := performSessionRequest(t, http.MethodGet, "/api/printer-info?uri="+url.QueryEscape(printerURI), nil, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	assertJSONError(t, rec, http.StatusForbidden, "forbidden printer")
}

func TestPrinterInfoHandler_ReturnsForbiddenWhenUserMissingAllowedGroup(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "group-miss", "pass")

	cups := newPrinterListServer(t, "group-only")
	defer cups.Close()
	printerURI := "http://" + mustURLHost(t, cups.URL) + "/printers/group-only"

	groupID := seedPrinterGroupWithMembers(t, ctx, s, "Allowed Group")
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:     printerURI,
		SubjectType:    store.PrinterACLSubjectGroup,
		SubjectGroupID: groupID,
		Effect:         store.PrinterACLEffectAllow,
	})
	t.Setenv("CUPS_HOST", cups.URL)

	rec := performSessionRequest(t, http.MethodGet, "/api/printer-info?uri="+url.QueryEscape(printerURI), nil, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	assertJSONError(t, rec, http.StatusForbidden, "forbidden printer")
}

func TestPrintHandler_ReturnsForbiddenWithoutSavingOrRecording(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice", "pass")

	cups := newPrinterListServer(t, "deny")
	defer cups.Close()
	printerURI := "http://" + mustURLHost(t, cups.URL) + "/printers/deny"
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  printerURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})
	t.Setenv("CUPS_HOST", cups.URL)

	oldUploadDir := uploadDir
	uploadDir = t.TempDir()
	defer func() { uploadDir = oldUploadDir }()

	rec := performMultipartPrintRequest(t, printerURI, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	}, "blocked.pdf", []byte("%PDF-1.4\nnot-a-real-pdf\n"))

	assertJSONError(t, rec, http.StatusForbidden, "forbidden printer")
	assertNoPrintRecords(t, ctx, s)
	assertDirEmpty(t, uploadDir)
}

func TestPrintHandler_UserDenyOverridesGroupAllowWithoutSavingOrRecording(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice-group-deny", "pass")

	cups := newPrinterListServer(t, "group-allowed-user-denied")
	defer cups.Close()
	printerURI := "http://" + mustURLHost(t, cups.URL) + "/printers/group-allowed-user-denied"

	groupID := seedPrinterGroupWithMembers(t, ctx, s, "Color Access", user.ID)
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:     printerURI,
		SubjectType:    store.PrinterACLSubjectGroup,
		SubjectGroupID: groupID,
		Effect:         store.PrinterACLEffectAllow,
	})
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:    printerURI,
		SubjectType:   store.PrinterACLSubjectUser,
		SubjectUserID: user.ID,
		Effect:        store.PrinterACLEffectDeny,
	})
	t.Setenv("CUPS_HOST", cups.URL)

	oldUploadDir := uploadDir
	uploadDir = t.TempDir()
	defer func() { uploadDir = oldUploadDir }()

	rec := performMultipartPrintRequest(t, printerURI, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	}, "blocked.pdf", []byte("%PDF-1.4\nnot-a-real-pdf\n"))

	assertJSONError(t, rec, http.StatusForbidden, "forbidden printer")
	assertNoPrintRecords(t, ctx, s)
	assertDirEmpty(t, uploadDir)
}

func TestPrintHandler_RejectsUnlistedEquivalentPrinterURIWithoutSavingOrRecording(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice-equivalent-uri", "pass")

	cups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><a href="/printers/deny">Deny Printer</a></body></html>`))
	}))
	defer cups.Close()

	canonicalURI := "http://" + mustURLHost(t, cups.URL) + "/printers/deny"
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  canonicalURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})
	t.Setenv("CUPS_HOST", cups.URL)

	oldUploadDir := uploadDir
	uploadDir = t.TempDir()
	defer func() { uploadDir = oldUploadDir }()

	requestedURI := strings.Replace(canonicalURI, "127.0.0.1", "localhost", 1)
	rec := performMultipartPrintRequest(t, requestedURI, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	}, "blocked.pdf", []byte("%PDF-1.4\nnot-a-real-pdf\n"))

	assertJSONError(t, rec, http.StatusBadRequest, "invalid printer")
	assertNoPrintRecords(t, ctx, s)
	assertDirEmpty(t, uploadDir)
}

func TestPrinterInfoHandler_RejectsURIOutsideConfiguredCUPSPrinters(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	user := seedLocalUser(t, ctx, s, "alice-ssrf", "pass")

	cups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><a href="/printers/safe">Safe Printer</a></body></html>`))
	}))
	defer cups.Close()
	t.Setenv("CUPS_HOST", cups.URL)

	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	requestedURI := target.URL + "/printers/evil"
	rec := performSessionRequest(t, http.MethodGet, "/api/printer-info?uri="+url.QueryEscape(requestedURI), nil, auth.Session{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	assertJSONError(t, rec, http.StatusBadRequest, "invalid printer")
	if got := atomic.LoadInt32(&targetHits); got != 0 {
		t.Fatalf("external printer-info target was contacted %d times; want 0", got)
	}
}

func TestListPrintersHandler_AdminBypassesACL(t *testing.T) {
	ctx := context.Background()
	s := setupAuthHandlerTest(t, ctx)
	admin := seedAdminUser(t, ctx, s, "admin-runtime")

	cups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
			<a href="/printers/deny">Deny Printer</a>
			<a href="/printers/open">Open Printer</a>
		</body></html>`))
	}))
	defer cups.Close()

	denyURI := "http://" + mustURLHost(t, cups.URL) + "/printers/deny"
	seedPrinterACLRule(t, ctx, s, store.CreatePrinterACLRuleInput{
		PrinterURI:  denyURI,
		SubjectType: store.PrinterACLSubjectRole,
		SubjectRole: store.RoleUser,
		Effect:      store.PrinterACLEffectDeny,
	})

	t.Setenv("CUPS_HOST", cups.URL)

	rec := performSessionRequest(t, http.MethodGet, "/api/printers", nil, auth.Session{
		UserID:   admin.ID,
		Username: admin.Username,
		Role:     admin.Role,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var printers []ipp.Printer
	if err := json.NewDecoder(rec.Body).Decode(&printers); err != nil {
		t.Fatalf("decode printers response: %v", err)
	}
	if len(printers) != 2 {
		t.Fatalf("len(printers) = %d, want 2", len(printers))
	}
}

func performSessionRequest(t *testing.T, method string, target string, body []byte, sess auth.Session) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	if err := auth.SetSession(rec, sess); err != nil {
		t.Fatalf("auth.SetSession() err = %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}

	rec = httptest.NewRecorder()
	switch target {
	case "/api/printers":
		listPrintersHandler(rec, req)
	default:
		printerInfoHandler(rec, req)
	}
	return rec
}

func seedAdminUser(t *testing.T, ctx context.Context, s *store.Store, username string) store.User {
	t.Helper()

	var user store.User
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:     username,
			PasswordHash: "hash",
			Role:         store.RoleAdmin,
			AuthSource:   "local",
		})
		if err != nil {
			return err
		}
		user = created
		return nil
	}); err != nil {
		t.Fatalf("seed admin user: %v", err)
	}
	return user
}

func performMultipartPrintRequest(t *testing.T, printerURI string, sess auth.Session, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("printer", printerURI)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/print", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", "test-csrf-token")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "test-csrf-token"})

	rec := httptest.NewRecorder()
	if err := auth.SetSession(rec, sess); err != nil {
		t.Fatalf("auth.SetSession() err = %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}

	rec = httptest.NewRecorder()
	printHandler(rec, req)
	return rec
}

func seedPrinterACLRule(t *testing.T, ctx context.Context, s *store.Store, input store.CreatePrinterACLRuleInput) {
	t.Helper()

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := store.CreatePrinterACLRule(ctx, tx, input)
		return err
	}); err != nil {
		t.Fatalf("seed printer acl rule: %v", err)
	}
}

func seedPrinterGroupWithMembers(t *testing.T, ctx context.Context, s *store.Store, name string, userIDs ...int64) int64 {
	t.Helper()

	var groupID int64
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		group, err := store.CreatePrinterGroup(ctx, tx, store.CreatePrinterGroupInput{
			Name:        name,
			Description: "runtime acl test group",
		})
		if err != nil {
			return err
		}
		if err := store.ReplacePrinterGroupMembers(ctx, tx, group.ID, userIDs); err != nil {
			return err
		}
		groupID = group.ID
		return nil
	}); err != nil {
		t.Fatalf("seed printer group: %v", err)
	}
	return groupID
}

func assertPrinterURIs(t *testing.T, printers []ipp.Printer, want ...string) {
	t.Helper()

	got := make([]string, 0, len(printers))
	for _, printer := range printers {
		got = append(got, printer.URI)
	}
	if len(got) != len(want) {
		t.Fatalf("printer uri count = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("printer uris = %v, want %v", got, want)
		}
	}
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantError string) {
	t.Helper()

	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["error"] != wantError {
		t.Fatalf("error response = %q, want %q", body["error"], wantError)
	}
}

func assertNoPrintRecords(t *testing.T, ctx context.Context, s *store.Store) {
	t.Helper()

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		records, err := store.ListPrintRecords(ctx, tx, store.PrintFilter{})
		if err != nil {
			return err
		}
		if len(records) != 0 {
			t.Fatalf("len(records) = %d, want 0", len(records))
		}
		return nil
	}); err != nil {
		t.Fatalf("assert no print records: %v", err)
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if matched, _ := filepath.Match(".*", filepath.Base(path)); matched {
			return nil
		}
		t.Fatalf("unexpected file saved in upload dir: %s", path)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", dir, err)
	}
}

func mustURLHost(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	return parsed.Host
}

func newPrinterListServer(t *testing.T, printerNames ...string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printers" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var body strings.Builder
		body.WriteString("<html><body>")
		for _, name := range printerNames {
			body.WriteString("<a href=\"/printers/")
			body.WriteString(name)
			body.WriteString("\">")
			body.WriteString(name)
			body.WriteString("</a>")
		}
		body.WriteString("</body></html>")
		_, _ = w.Write([]byte(body.String()))
	}))
}
