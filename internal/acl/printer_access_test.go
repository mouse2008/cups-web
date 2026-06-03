package acl

import (
	"context"
	"database/sql"
	"testing"

	"cups-web/internal/ipp"
	"cups-web/internal/store"
)

func TestCanUsePrinter(t *testing.T) {
	tests := []struct {
		name      string
		userRole  string
		printer   string
		seedRules func(context.Context, *testing.T, *sql.Tx, store.User)
		want      bool
	}{
		{
			name:     "no rules allows regular user",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/open",
			want:     true,
		},
		{
			name:     "role allow grants access",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/role-allow",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, _ store.User) {
				t.Helper()
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:  "ipp://cups/printers/role-allow",
					SubjectType: store.PrinterACLSubjectRole,
					SubjectRole: store.RoleUser,
					Effect:      store.PrinterACLEffectAllow,
				})
			},
			want: true,
		},
		{
			name:     "role deny blocks access",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/role-deny",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, _ store.User) {
				t.Helper()
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:  "ipp://cups/printers/role-deny",
					SubjectType: store.PrinterACLSubjectRole,
					SubjectRole: store.RoleUser,
					Effect:      store.PrinterACLEffectDeny,
				})
			},
			want: false,
		},
		{
			name:     "user allow overrides role deny",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/user-allow",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:  "ipp://cups/printers/user-allow",
					SubjectType: store.PrinterACLSubjectRole,
					SubjectRole: store.RoleUser,
					Effect:      store.PrinterACLEffectDeny,
				})
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:    "ipp://cups/printers/user-allow",
					SubjectType:   store.PrinterACLSubjectUser,
					SubjectUserID: user.ID,
					Effect:        store.PrinterACLEffectAllow,
				})
			},
			want: true,
		},
		{
			name:     "group allow grants access",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/group-allow",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				groupID := mustCreateGroupWithMember(ctx, t, tx, "Group Allow", user.ID)
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:     "ipp://cups/printers/group-allow",
					SubjectType:    store.PrinterACLSubjectGroup,
					SubjectGroupID: groupID,
					Effect:         store.PrinterACLEffectAllow,
				})
			},
			want: true,
		},
		{
			name:     "group deny blocks access",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/group-deny",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				groupID := mustCreateGroupWithMember(ctx, t, tx, "Group Deny", user.ID)
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:     "ipp://cups/printers/group-deny",
					SubjectType:    store.PrinterACLSubjectGroup,
					SubjectGroupID: groupID,
					Effect:         store.PrinterACLEffectDeny,
				})
			},
			want: false,
		},
		{
			name:     "user deny overrides role allow",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/user-deny",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:  "ipp://cups/printers/user-deny",
					SubjectType: store.PrinterACLSubjectRole,
					SubjectRole: store.RoleUser,
					Effect:      store.PrinterACLEffectAllow,
				})
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:    "ipp://cups/printers/user-deny",
					SubjectType:   store.PrinterACLSubjectUser,
					SubjectUserID: user.ID,
					Effect:        store.PrinterACLEffectDeny,
				})
			},
			want: false,
		},
		{
			name:     "user allow overrides group deny",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/user-allow-group-deny",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				groupID := mustCreateGroupWithMember(ctx, t, tx, "User Override Allow", user.ID)
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:     "ipp://cups/printers/user-allow-group-deny",
					SubjectType:    store.PrinterACLSubjectGroup,
					SubjectGroupID: groupID,
					Effect:         store.PrinterACLEffectDeny,
				})
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:    "ipp://cups/printers/user-allow-group-deny",
					SubjectType:   store.PrinterACLSubjectUser,
					SubjectUserID: user.ID,
					Effect:        store.PrinterACLEffectAllow,
				})
			},
			want: true,
		},
		{
			name:     "user deny overrides group allow",
			userRole: store.RoleUser,
			printer:  "ipp://cups/printers/user-deny-group-allow",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				groupID := mustCreateGroupWithMember(ctx, t, tx, "User Override Deny", user.ID)
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:     "ipp://cups/printers/user-deny-group-allow",
					SubjectType:    store.PrinterACLSubjectGroup,
					SubjectGroupID: groupID,
					Effect:         store.PrinterACLEffectAllow,
				})
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:    "ipp://cups/printers/user-deny-group-allow",
					SubjectType:   store.PrinterACLSubjectUser,
					SubjectUserID: user.ID,
					Effect:        store.PrinterACLEffectDeny,
				})
			},
			want: false,
		},
		{
			name:     "admin remains fully allowed",
			userRole: store.RoleAdmin,
			printer:  "ipp://cups/printers/admin-bypass",
			seedRules: func(ctx context.Context, t *testing.T, tx *sql.Tx, user store.User) {
				t.Helper()
				mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
					PrinterURI:    "ipp://cups/printers/admin-bypass",
					SubjectType:   store.PrinterACLSubjectUser,
					SubjectUserID: user.ID,
					Effect:        store.PrinterACLEffectDeny,
				})
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := openTestStore(t, ctx)
			defer s.Close()

			var user store.User
			err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
				var err error
				user, err = store.CreateUser(ctx, tx, store.CreateUserInput{
					Username:     "test-user",
					PasswordHash: "hash",
					Role:         tt.userRole,
				})
				if err != nil {
					return err
				}
				if tt.seedRules != nil {
					tt.seedRules(ctx, t, tx, user)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("seed authorization test data: %v", err)
			}

			authorizer := NewPrinterAuthorizer()
			err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
				allowed, err := authorizer.CanUsePrinter(ctx, tx, user, tt.printer)
				if err != nil {
					return err
				}
				if allowed != tt.want {
					t.Fatalf("CanUsePrinter() = %v, want %v", allowed, tt.want)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("CanUsePrinter() err = %v", err)
			}
		})
	}
}

func TestFilterPrinters(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, ctx)
	defer s.Close()

	var user store.User
	err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		user, err = store.CreateUser(ctx, tx, store.CreateUserInput{
			Username:     "visible-user",
			PasswordHash: "hash",
			Role:         store.RoleUser,
		})
		if err != nil {
			return err
		}

		rules := []store.CreatePrinterACLRuleInput{
			{
				PrinterURI:  "ipp://cups/printers/role-allowed",
				SubjectType: store.PrinterACLSubjectRole,
				SubjectRole: store.RoleUser,
				Effect:      store.PrinterACLEffectAllow,
			},
			{
				PrinterURI:  "ipp://cups/printers/whitelist-miss",
				SubjectType: store.PrinterACLSubjectRole,
				SubjectRole: store.RoleAdmin,
				Effect:      store.PrinterACLEffectAllow,
			},
			{
				PrinterURI:    "ipp://cups/printers/user-denied",
				SubjectType:   store.PrinterACLSubjectUser,
				SubjectUserID: user.ID,
				Effect:        store.PrinterACLEffectDeny,
			},
		}
		for _, rule := range rules {
			mustCreateRule(ctx, t, tx, rule)
		}
		groupID := mustCreateGroupWithMember(ctx, t, tx, "Visible Group", user.ID)
		mustCreateRule(ctx, t, tx, store.CreatePrinterACLRuleInput{
			PrinterURI:     "ipp://cups/printers/group-allowed",
			SubjectType:    store.PrinterACLSubjectGroup,
			SubjectGroupID: groupID,
			Effect:         store.PrinterACLEffectAllow,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed filter test data: %v", err)
	}

	authorizer := NewPrinterAuthorizer()
	printers := []ipp.Printer{
		{Name: "Open", URI: "ipp://cups/printers/open"},
		{Name: "Role Allowed", URI: "ipp://cups/printers/role-allowed"},
		{Name: "Group Allowed", URI: "ipp://cups/printers/group-allowed"},
		{Name: "Whitelist Miss", URI: "ipp://cups/printers/whitelist-miss"},
		{Name: "User Denied", URI: "ipp://cups/printers/user-denied"},
	}

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		visible, err := authorizer.FilterPrinters(ctx, tx, user, printers)
		if err != nil {
			return err
		}
		if len(visible) != 3 {
			t.Fatalf("len(visible) = %d, want 3", len(visible))
		}
		if visible[0].URI != "ipp://cups/printers/open" {
			t.Fatalf("visible[0].URI = %q, want open printer", visible[0].URI)
		}
		if visible[1].URI != "ipp://cups/printers/role-allowed" {
			t.Fatalf("visible[1].URI = %q, want role-allowed printer", visible[1].URI)
		}
		if visible[2].URI != "ipp://cups/printers/group-allowed" {
			t.Fatalf("visible[2].URI = %q, want group-allowed printer", visible[2].URI)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("FilterPrinters() err = %v", err)
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

func mustCreateRule(ctx context.Context, t *testing.T, tx *sql.Tx, input store.CreatePrinterACLRuleInput) {
	t.Helper()

	if _, err := store.CreatePrinterACLRule(ctx, tx, input); err != nil {
		t.Fatalf("CreatePrinterACLRule() err = %v", err)
	}
}

func mustCreateGroupWithMember(ctx context.Context, t *testing.T, tx *sql.Tx, name string, userID int64) int64 {
	t.Helper()

	group, err := store.CreatePrinterGroup(ctx, tx, store.CreatePrinterGroupInput{
		Name:        name,
		Description: "acl test group",
	})
	if err != nil {
		t.Fatalf("CreatePrinterGroup() err = %v", err)
	}
	if err := store.ReplacePrinterGroupMembers(ctx, tx, group.ID, []int64{userID}); err != nil {
		t.Fatalf("ReplacePrinterGroupMembers() err = %v", err)
	}
	return group.ID
}
