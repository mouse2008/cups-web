package acl

import (
	"context"
	"database/sql"

	"cups-web/internal/ipp"
	"cups-web/internal/store"
)

type PrinterAuthorizer struct{}

func NewPrinterAuthorizer() PrinterAuthorizer {
	return PrinterAuthorizer{}
}

func (PrinterAuthorizer) CanUsePrinter(ctx context.Context, tx *sql.Tx, user store.User, printerURI string) (bool, error) {
	if user.Role == store.RoleAdmin {
		return true, nil
	}

	rules, err := store.ListPrinterACLRulesByPrinter(ctx, tx, printerURI)
	if err != nil {
		return false, err
	}
	groupIDs, err := store.ListPrinterGroupIDsByUserID(ctx, tx, user.ID)
	if err != nil {
		return false, err
	}
	return evaluatePrinterAccess(user, groupIDSet(groupIDs), rules), nil
}

func (PrinterAuthorizer) FilterPrinters(ctx context.Context, tx *sql.Tx, user store.User, printers []ipp.Printer) ([]ipp.Printer, error) {
	if len(printers) == 0 {
		return nil, nil
	}
	if user.Role == store.RoleAdmin {
		visible := make([]ipp.Printer, len(printers))
		copy(visible, printers)
		return visible, nil
	}

	printerURIs := make([]string, 0, len(printers))
	for _, printer := range printers {
		printerURIs = append(printerURIs, printer.URI)
	}

	rules, err := store.ListPrinterACLRulesByPrinters(ctx, tx, printerURIs)
	if err != nil {
		return nil, err
	}
	groupIDs, err := store.ListPrinterGroupIDsByUserID(ctx, tx, user.ID)
	if err != nil {
		return nil, err
	}

	rulesByPrinter := make(map[string][]store.PrinterACLRule, len(printers))
	for _, rule := range rules {
		rulesByPrinter[rule.PrinterURI] = append(rulesByPrinter[rule.PrinterURI], rule)
	}
	userGroups := groupIDSet(groupIDs)

	visible := make([]ipp.Printer, 0, len(printers))
	for _, printer := range printers {
		if evaluatePrinterAccess(user, userGroups, rulesByPrinter[printer.URI]) {
			visible = append(visible, printer)
		}
	}
	return visible, nil
}

func evaluatePrinterAccess(user store.User, userGroups map[int64]struct{}, rules []store.PrinterACLRule) bool {
	if user.Role == store.RoleAdmin {
		return true
	}
	if len(rules) == 0 {
		return true
	}

	var matchedUserAllow bool
	var matchedGroupAllow bool
	var matchedRoleAllow bool

	for _, rule := range rules {
		if rule.SubjectType == store.PrinterACLSubjectUser && rule.SubjectUserID.Valid && rule.SubjectUserID.Int64 == user.ID {
			if rule.Effect == store.PrinterACLEffectDeny {
				return false
			}
			if rule.Effect == store.PrinterACLEffectAllow {
				matchedUserAllow = true
			}
		}
	}
	if matchedUserAllow {
		return true
	}

	for _, rule := range rules {
		if rule.SubjectType != store.PrinterACLSubjectGroup || !rule.SubjectGroupID.Valid {
			continue
		}
		if _, ok := userGroups[rule.SubjectGroupID.Int64]; !ok {
			continue
		}
		if rule.Effect == store.PrinterACLEffectDeny {
			return false
		}
		if rule.Effect == store.PrinterACLEffectAllow {
			matchedGroupAllow = true
		}
	}
	if matchedGroupAllow {
		return true
	}

	for _, rule := range rules {
		if rule.SubjectType == store.PrinterACLSubjectRole && rule.SubjectRole == user.Role {
			if rule.Effect == store.PrinterACLEffectDeny {
				return false
			}
			if rule.Effect == store.PrinterACLEffectAllow {
				matchedRoleAllow = true
			}
		}
	}
	if matchedRoleAllow {
		return true
	}

	return false
}

func groupIDSet(groupIDs []int64) map[int64]struct{} {
	if len(groupIDs) == 0 {
		return nil
	}
	groupSet := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		groupSet[groupID] = struct{}{}
	}
	return groupSet
}
