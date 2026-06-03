package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"cups-web/internal/store"
)

func TestAdminPrinterGroups_ListReturnsMembers(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user1 := seedLocalUser(t, ctx, s, "alice", "pass")
	user2 := seedLocalUser(t, ctx, s, "bob", "pass")
	seedPrinterGroup(t, ctx, s, "Teachers", "faculty", []int64{user2.ID, user1.ID})

	rec := performAdminRequest(t, adminListPrinterGroupsHandler, http.MethodGet, "/api/admin/printer-groups", nil, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminListPrinterGroupsHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	var body []adminPrinterGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(body))
	}
	if body[0].Name != "Teachers" {
		t.Fatalf("name = %q, want Teachers", body[0].Name)
	}
	if len(body[0].MemberUserIDs) != 2 || body[0].MemberUserIDs[0] != user1.ID || body[0].MemberUserIDs[1] != user2.ID {
		t.Fatalf("memberUserIds = %v, want [%d %d]", body[0].MemberUserIDs, user1.ID, user2.ID)
	}
}

func TestAdminPrinterGroups_CreatePersistsMembers(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user1 := seedLocalUser(t, ctx, s, "alice", "pass")
	user2 := seedLocalUser(t, ctx, s, "bob", "pass")

	rec := performAdminRequest(t, adminCreatePrinterGroupHandler, http.MethodPost, "/api/admin/printer-groups", map[string]any{
		"name":          "Teachers",
		"description":   "faculty printers",
		"memberUserIds": []int64{user2.ID, user1.ID, user2.ID},
	}, userAdminSession())
	if rec.Code != http.StatusOK {
		t.Fatalf("adminCreatePrinterGroupHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	var body adminPrinterGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Name != "Teachers" || body.Description != "faculty printers" {
		t.Fatalf("unexpected group response: %+v", body)
	}
	if len(body.MemberUserIDs) != 2 || body.MemberUserIDs[0] != user1.ID || body.MemberUserIDs[1] != user2.ID {
		t.Fatalf("memberUserIds = %v, want [%d %d]", body.MemberUserIDs, user1.ID, user2.ID)
	}

	assertPrinterGroupMembers(t, ctx, s, body.ID, []int64{user1.ID, user2.ID})
}

func TestAdminPrinterGroups_UpdateReplacesMembers(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user1 := seedLocalUser(t, ctx, s, "alice", "pass")
	user2 := seedLocalUser(t, ctx, s, "bob", "pass")
	user3 := seedLocalUser(t, ctx, s, "carol", "pass")
	group := seedPrinterGroup(t, ctx, s, "Teachers", "faculty", []int64{user1.ID, user2.ID})

	rec := performAdminRequest(t, adminUpdatePrinterGroupHandler, http.MethodPut, "/api/admin/printer-groups/1", map[string]any{
		"name":          "Teachers West",
		"description":   "west campus",
		"memberUserIds": []int64{user3.ID},
	}, userAdminSession(), map[string]string{"id": strconv.FormatInt(group.ID, 10)})
	if rec.Code != http.StatusOK {
		t.Fatalf("adminUpdatePrinterGroupHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	var body adminPrinterGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Name != "Teachers West" || len(body.MemberUserIDs) != 1 || body.MemberUserIDs[0] != user3.ID {
		t.Fatalf("unexpected update response: %+v", body)
	}
	assertPrinterGroupMembers(t, ctx, s, group.ID, []int64{user3.ID})
}

func TestAdminPrinterGroups_DeleteRemovesGroup(t *testing.T) {
	ctx := context.Background()
	s := setupAdminHandlerTest(t, ctx)
	user1 := seedLocalUser(t, ctx, s, "alice", "pass")
	group := seedPrinterGroup(t, ctx, s, "Teachers", "faculty", []int64{user1.ID})

	rec := performAdminRequest(t, adminDeletePrinterGroupHandler, http.MethodDelete, "/api/admin/printer-groups/1", nil, userAdminSession(), map[string]string{"id": strconv.FormatInt(group.ID, 10)})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("adminDeletePrinterGroupHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusNoContent)
	}

	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		_, err := store.GetPrinterGroupByID(ctx, tx, group.ID)
		if err == nil {
			t.Fatalf("GetPrinterGroupByID() err = nil, want not found")
		}
		return nil
	}); err != nil {
		t.Fatalf("verify delete: %v", err)
	}
}

func TestAdminPrinterGroups_CreateRejectsUnknownUser(t *testing.T) {
	ctx := context.Background()
	setupAdminHandlerTest(t, ctx)

	rec := performAdminRequest(t, adminCreatePrinterGroupHandler, http.MethodPost, "/api/admin/printer-groups", map[string]any{
		"name":          "Teachers",
		"memberUserIds": []int64{9999},
	}, userAdminSession())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("adminCreatePrinterGroupHandler status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}
}

func seedPrinterGroup(t *testing.T, ctx context.Context, s *store.Store, name string, description string, memberUserIDs []int64) store.PrinterGroup {
	t.Helper()
	var group store.PrinterGroup
	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := store.CreatePrinterGroup(ctx, tx, store.CreatePrinterGroupInput{Name: name, Description: description})
		if err != nil {
			return err
		}
		if err := store.ReplacePrinterGroupMembers(ctx, tx, created.ID, memberUserIDs); err != nil {
			return err
		}
		group = created
		return nil
	}); err != nil {
		t.Fatalf("seed printer group: %v", err)
	}
	return group
}

func assertPrinterGroupMembers(t *testing.T, ctx context.Context, s *store.Store, groupID int64, want []int64) {
	t.Helper()
	if err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		members, err := store.ListPrinterGroupMembers(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if len(members) != len(want) {
			t.Fatalf("len(members) = %d, want %d", len(members), len(want))
		}
		for i, member := range members {
			if member.ID != want[i] {
				t.Fatalf("members[%d].ID = %d, want %d", i, member.ID, want[i])
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("assert group members: %v", err)
	}
}
