package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestOpen_HotUpgradeMigratesPrinterACLRulesTable(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	seedLegacyStore(t, dbPath)

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		user, err := CreateUser(ctx, tx, CreateUserInput{
			Username:     "acl-user",
			PasswordHash: "hash",
			Role:         RoleUser,
		})
		if err != nil {
			return err
		}
		group, err := CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{
			Name:        "Legacy Migrated Group",
			Description: "created after hot upgrade",
		})
		if err != nil {
			return err
		}

		if _, err := CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:  "ipp://cups/printers/alpha",
			SubjectType: PrinterACLSubjectRole,
			SubjectRole: RoleUser,
			Effect:      PrinterACLEffectAllow,
		}); err != nil {
			return err
		}
		if _, err := CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:    "ipp://cups/printers/alpha",
			SubjectType:   PrinterACLSubjectUser,
			SubjectUserID: user.ID,
			Effect:        PrinterACLEffectDeny,
		}); err != nil {
			return err
		}
		if _, err := CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:     "ipp://cups/printers/alpha",
			SubjectType:    PrinterACLSubjectGroup,
			SubjectGroupID: group.ID,
			Effect:         PrinterACLEffectAllow,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed printer ACL rules after migration: %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		rules, err := ListPrinterACLRulesByPrinter(ctx, tx, "ipp://cups/printers/alpha")
		if err != nil {
			return err
		}
		if len(rules) != 3 {
			t.Fatalf("len(rules) = %d, want 3", len(rules))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify migrated printer ACL rules: %v", err)
	}
}

func TestListPrinterACLRulesByPrinters(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var userID int64
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		user, err := CreateUser(ctx, tx, CreateUserInput{
			Username:     "printer-acl-user",
			PasswordHash: "hash",
			Role:         RoleUser,
		})
		if err != nil {
			return err
		}
		userID = user.ID

		inputs := []CreatePrinterACLRuleInput{
			{
				PrinterURI:  "ipp://cups/printers/alpha",
				SubjectType: PrinterACLSubjectRole,
				SubjectRole: RoleUser,
				Effect:      PrinterACLEffectAllow,
			},
			{
				PrinterURI:    "ipp://cups/printers/beta",
				SubjectType:   PrinterACLSubjectUser,
				SubjectUserID: userID,
				Effect:        PrinterACLEffectDeny,
			},
			{
				PrinterURI:  "ipp://cups/printers/gamma",
				SubjectType: PrinterACLSubjectRole,
				SubjectRole: RoleAdmin,
				Effect:      PrinterACLEffectAllow,
			},
		}
		for _, input := range inputs {
			if _, err := CreatePrinterACLRule(ctx, tx, input); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed printer ACL rules: %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		rules, err := ListPrinterACLRulesByPrinters(ctx, tx, []string{
			"ipp://cups/printers/beta",
			"ipp://cups/printers/gamma",
		})
		if err != nil {
			return err
		}
		if len(rules) != 2 {
			t.Fatalf("len(rules) = %d, want 2", len(rules))
		}
		if rules[0].PrinterURI != "ipp://cups/printers/beta" {
			t.Fatalf("rules[0].PrinterURI = %q, want beta", rules[0].PrinterURI)
		}
		if !rules[0].SubjectUserID.Valid || rules[0].SubjectUserID.Int64 != userID {
			t.Fatalf("rules[0].SubjectUserID = %+v, want %d", rules[0].SubjectUserID, userID)
		}
		if rules[1].PrinterURI != "ipp://cups/printers/gamma" {
			t.Fatalf("rules[1].PrinterURI = %q, want gamma", rules[1].PrinterURI)
		}
		if rules[1].SubjectRole != RoleAdmin {
			t.Fatalf("rules[1].SubjectRole = %q, want %q", rules[1].SubjectRole, RoleAdmin)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify printer ACL list helper: %v", err)
	}
}

func TestCreatePrinterACLRule_GroupSubject(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var groupID int64
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		group, err := CreatePrinterGroup(ctx, tx, CreatePrinterGroupInput{
			Name:        "Finance Printers",
			Description: "group-scoped printer access",
		})
		if err != nil {
			return err
		}
		groupID = group.ID

		rule, err := CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:     "ipp://cups/printers/color",
			SubjectType:    PrinterACLSubjectGroup,
			SubjectGroupID: group.ID,
			Effect:         PrinterACLEffectAllow,
		})
		if err != nil {
			return err
		}
		if rule.SubjectType != PrinterACLSubjectGroup {
			t.Fatalf("rule.SubjectType = %q, want %q", rule.SubjectType, PrinterACLSubjectGroup)
		}
		if !rule.SubjectGroupID.Valid || rule.SubjectGroupID.Int64 != group.ID {
			t.Fatalf("rule.SubjectGroupID = %+v, want %d", rule.SubjectGroupID, group.ID)
		}
		if rule.SubjectRole != "" {
			t.Fatalf("rule.SubjectRole = %q, want empty", rule.SubjectRole)
		}
		if rule.SubjectUserID.Valid {
			t.Fatalf("rule.SubjectUserID = %+v, want null", rule.SubjectUserID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create group printer ACL rule: %v", err)
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		rules, err := ListPrinterACLRulesByPrinter(ctx, tx, "ipp://cups/printers/color")
		if err != nil {
			return err
		}
		if len(rules) != 1 {
			t.Fatalf("len(rules) = %d, want 1", len(rules))
		}
		if !rules[0].SubjectGroupID.Valid || rules[0].SubjectGroupID.Int64 != groupID {
			t.Fatalf("rules[0].SubjectGroupID = %+v, want %d", rules[0].SubjectGroupID, groupID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify stored group printer ACL rule: %v", err)
	}
}

func TestCreatePrinterACLRule_GroupSubjectRequiresExistingGroup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := CreatePrinterACLRule(ctx, tx, CreatePrinterACLRuleInput{
			PrinterURI:     "ipp://cups/printers/color",
			SubjectType:    PrinterACLSubjectGroup,
			SubjectGroupID: 9999,
			Effect:         PrinterACLEffectAllow,
		})
		return err
	})
	if err == nil {
		t.Fatalf("CreatePrinterACLRule() err = nil, want group validation error")
	}
	if err != ErrPrinterGroupNotFound {
		t.Fatalf("CreatePrinterACLRule() err = %v, want %v", err, ErrPrinterGroupNotFound)
	}
}
