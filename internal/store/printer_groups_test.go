package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestPrinterGroupCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var created PrinterGroup
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err = CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{
			Name:        "  Teachers  ",
			Description: "faculty printers",
		})
		if err != nil {
			return err
		}
		if created.Name != "Teachers" {
			t.Fatalf("CreatePrinterGroup().Name = %q, want %q", created.Name, "Teachers")
		}
		if created.Description != "faculty printers" {
			t.Fatalf("CreatePrinterGroup().Description = %q, want %q", created.Description, "faculty printers")
		}
		if created.CreatedAt == "" || created.UpdatedAt == "" {
			t.Fatalf("CreatePrinterGroup() timestamps should be populated")
		}

		updated, err := UpdatePrinterGroup(ctx, tx, created.ID, UpdatePrinterGroupInput{
			Name:        "Teachers West",
			Description: "west campus",
		})
		if err != nil {
			return err
		}
		if updated.Name != "Teachers West" {
			t.Fatalf("UpdatePrinterGroup().Name = %q, want %q", updated.Name, "Teachers West")
		}
		if updated.Description != "west campus" {
			t.Fatalf("UpdatePrinterGroup().Description = %q, want %q", updated.Description, "west campus")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("group CRUD tx err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		got, err := GetPrinterGroupByID(ctx, tx, created.ID)
		if err != nil {
			return err
		}
		if got.Name != "Teachers West" {
			t.Fatalf("GetPrinterGroupByID().Name = %q, want %q", got.Name, "Teachers West")
		}

		groups, err := ListPrinterGroups(ctx, tx)
		if err != nil {
			return err
		}
		if len(groups) != 1 {
			t.Fatalf("len(ListPrinterGroups()) = %d, want 1", len(groups))
		}
		if groups[0].ID != created.ID {
			t.Fatalf("ListPrinterGroups()[0].ID = %d, want %d", groups[0].ID, created.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify printer groups err = %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		return DeletePrinterGroup(ctx, tx, created.ID)
	})
	if err != nil {
		t.Fatalf("DeletePrinterGroup() err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		_, err := GetPrinterGroupByID(ctx, tx, created.ID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetPrinterGroupByID() err = %v, want sql.ErrNoRows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify delete err = %v", err)
	}
}

func TestCreatePrinterGroup_ValidationAndUniqueness(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{
			Name: "   ",
		})
		if !errors.Is(err, ErrPrinterGroupNameRequired) {
			t.Fatalf("CreatePrinterGroup() empty name err = %v, want ErrPrinterGroupNameRequired", err)
		}

		_, err = CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{Name: "Students"})
		if err != nil {
			return err
		}
		_, err = CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{Name: "Students"})
		if !errors.Is(err, ErrPrinterGroupNameExists) {
			t.Fatalf("CreatePrinterGroup() duplicate err = %v, want ErrPrinterGroupNameExists", err)
		}

		_, err = UpdatePrinterGroup(ctx, tx, 9999, UpdatePrinterGroupInput{Name: "Missing"})
		if !errors.Is(err, ErrPrinterGroupNotFound) {
			t.Fatalf("UpdatePrinterGroup() missing err = %v, want ErrPrinterGroupNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("validation tx err = %v", err)
	}
}

func TestReplacePrinterGroupMembers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var group PrinterGroup
	var user1 User
	var user2 User
	var user3 User
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		group, err = CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{Name: "Ops"})
		if err != nil {
			return err
		}
		user1, err = CreateUser(ctx, tx, CreateUserInput{Username: "ops-1", PasswordHash: "hash", Role: RoleUser})
		if err != nil {
			return err
		}
		user2, err = CreateUser(ctx, tx, CreateUserInput{Username: "ops-2", PasswordHash: "hash", Role: RoleUser})
		if err != nil {
			return err
		}
		user3, err = CreateUser(ctx, tx, CreateUserInput{Username: "ops-3", PasswordHash: "hash", Role: RoleUser})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed group members err = %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		return ReplacePrinterGroupMembers(ctx, tx, group.ID, []int64{user2.ID, user1.ID, user2.ID})
	})
	if err != nil {
		t.Fatalf("ReplacePrinterGroupMembers() err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		members, err := ListPrinterGroupMembers(ctx, tx, group.ID)
		if err != nil {
			return err
		}
		if len(members) != 2 {
			t.Fatalf("len(ListPrinterGroupMembers()) = %d, want 2", len(members))
		}
		if members[0].ID != user1.ID || members[1].ID != user2.ID {
			t.Fatalf("ListPrinterGroupMembers() IDs = [%d %d], want [%d %d]", members[0].ID, members[1].ID, user1.ID, user2.ID)
		}

		groupIDs, err := ListPrinterGroupIDsByUserID(ctx, tx, user2.ID)
		if err != nil {
			return err
		}
		if len(groupIDs) != 1 || groupIDs[0] != group.ID {
			t.Fatalf("ListPrinterGroupIDsByUserID() = %v, want [%d]", groupIDs, group.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify replaced members err = %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		err := ReplacePrinterGroupMembers(ctx, tx, group.ID, []int64{user3.ID, 9999})
		if !errors.Is(err, ErrPrinterGroupMemberUserNotFound) {
			t.Fatalf("ReplacePrinterGroupMembers() missing user err = %v, want ErrPrinterGroupMemberUserNotFound", err)
		}
		return ReplacePrinterGroupMembers(ctx, tx, group.ID, nil)
	})
	if err != nil {
		t.Fatalf("replace members validation err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		members, err := ListPrinterGroupMembers(ctx, tx, group.ID)
		if err != nil {
			return err
		}
		if len(members) != 0 {
			t.Fatalf("len(ListPrinterGroupMembers()) after clear = %d, want 0", len(members))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify cleared members err = %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		return DeletePrinterGroup(ctx, tx, group.ID)
	})
	if err != nil {
		t.Fatalf("DeletePrinterGroup() err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		groupIDs, err := ListPrinterGroupIDsByUserID(ctx, tx, user1.ID)
		if err != nil {
			return err
		}
		if len(groupIDs) != 0 {
			t.Fatalf("ListPrinterGroupIDsByUserID() after delete = %v, want empty", groupIDs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify deleted group members err = %v", err)
	}
}

func TestDeletePrinterGroup_RemovesDependentMembershipsAndACLRules(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var group PrinterGroup
	var user User
	printerURI := "ipp://cups/printers/color"
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		group, err = CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{Name: "Color Access"})
		if err != nil {
			return err
		}
		user, err = CreateUser(ctx, tx, CreateUserInput{Username: "color-user", PasswordHash: "hash", Role: RoleUser})
		if err != nil {
			return err
		}
		if err := ReplacePrinterGroupMembers(ctx, tx, group.ID, []int64{user.ID}); err != nil {
			return err
		}
		_, err = CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:     printerURI,
			SubjectType:    PrinterACLSubjectGroup,
			SubjectGroupID: group.ID,
			Effect:         PrinterACLEffectAllow,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed group dependencies err = %v", err)
	}

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		return DeletePrinterGroup(ctx, tx, group.ID)
	})
	if err != nil {
		t.Fatalf("DeletePrinterGroup() err = %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		groupIDs, err := ListPrinterGroupIDsByUserID(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		if len(groupIDs) != 0 {
			t.Fatalf("ListPrinterGroupIDsByUserID() after group delete = %v, want empty", groupIDs)
		}

		rules, err := ListPrinterACLRulesByPrinter(ctx, tx, printerURI)
		if err != nil {
			return err
		}
		if len(rules) != 0 {
			t.Fatalf("ListPrinterACLRulesByPrinter() after group delete returned %d rules, want 0", len(rules))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify group dependency cleanup err = %v", err)
	}
}

func TestReplacePrinterGroupMembers_RequiresExistingGroup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		err := ReplacePrinterGroupMembers(ctx, tx, 9999, []int64{})
		if !errors.Is(err, ErrPrinterGroupNotFound) {
			t.Fatalf("ReplacePrinterGroupMembers() missing group err = %v, want ErrPrinterGroupNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("missing group tx err = %v", err)
	}
}

func TestOpen_HotUpgradeMigratesPrinterGroupsTables(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	seedLegacyStore(t, dbPath)

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		group, err := CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{Name: "Legacy Migrated"})
		if err != nil {
			return err
		}
		user, err := GetUserByUsername(ctx, tx, "legacy-user")
		if err != nil {
			return err
		}
		if err := ReplacePrinterGroupMembers(ctx, tx, group.ID, []int64{user.ID}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("exercise printer groups after migration err = %v", err)
	}
}
