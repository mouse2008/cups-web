package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	PrinterACLSubjectRole  = "role"
	PrinterACLSubjectGroup = "group"
	PrinterACLSubjectUser  = "user"
)

const (
	PrinterACLEffectAllow = "allow"
	PrinterACLEffectDeny  = "deny"
)

type PrinterACLRule struct {
	ID             int64
	PrinterURI     string
	SubjectType    string
	SubjectRole    string
	SubjectUserID  sql.NullInt64
	SubjectGroupID sql.NullInt64
	Effect         string
	CreatedAt      string
	UpdatedAt      string
}

type CreatePrinterACLRuleInput struct {
	PrinterURI     string
	SubjectType    string
	SubjectRole    string
	SubjectUserID  int64
	SubjectGroupID int64
	Effect         string
}

const printerACLRuleSelectColumns = `
	id, printer_uri, subject_type, subject_role, subject_user_id, subject_group_id, effect, created_at, updated_at`

func CreatePrinterACLRule(ctx context.Context, tx *sql.Tx, input CreatePrinterACLRuleInput) (PrinterACLRule, error) {
	input = normalizeCreatePrinterACLRuleInput(input)
	if err := validateCreatePrinterACLRuleInput(input); err != nil {
		return PrinterACLRule{}, err
	}
	if input.SubjectType == PrinterACLSubjectGroup {
		if err := ensurePrinterGroupExists(ctx, tx, input.SubjectGroupID); err != nil {
			return PrinterACLRule{}, err
		}
	}

	now := nowUTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO printer_acl_rules (
		printer_uri, subject_type, subject_role, subject_user_id, subject_group_id, effect, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		input.PrinterURI,
		input.SubjectType,
		nullableStringValue(input.SubjectRole),
		nullableInt64Value(input.SubjectUserID),
		nullableInt64Value(input.SubjectGroupID),
		input.Effect,
		now,
		now,
	)
	if err != nil {
		return PrinterACLRule{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return PrinterACLRule{}, err
	}
	return GetPrinterACLRuleByID(ctx, tx, id)
}

func GetPrinterACLRuleByID(ctx context.Context, tx *sql.Tx, id int64) (PrinterACLRule, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+printerACLRuleSelectColumns+` FROM printer_acl_rules WHERE id = ?`, id)
	return scanPrinterACLRule(row)
}

func ListPrinterACLRulesByPrinter(ctx context.Context, tx *sql.Tx, printerURI string) ([]PrinterACLRule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+printerACLRuleSelectColumns+`
		FROM printer_acl_rules
		WHERE printer_uri = ?
		ORDER BY printer_uri, subject_type, subject_role, subject_user_id, subject_group_id, effect, id`, printerURI)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPrinterACLRules(rows)
}

func ListPrinterACLRulesByPrinters(ctx context.Context, tx *sql.Tx, printerURIs []string) ([]PrinterACLRule, error) {
	if len(printerURIs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, 0, len(printerURIs))
	args := make([]any, 0, len(printerURIs))
	for _, printerURI := range printerURIs {
		placeholders = append(placeholders, "?")
		args = append(args, printerURI)
	}

	query := `SELECT ` + printerACLRuleSelectColumns + `
		FROM printer_acl_rules
		WHERE printer_uri IN (` + strings.Join(placeholders, ", ") + `)
		ORDER BY printer_uri, subject_type, subject_role, subject_user_id, subject_group_id, effect, id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPrinterACLRules(rows)
}

func DeletePrinterACLRulesByPrinter(ctx context.Context, tx *sql.Tx, printerURI string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM printer_acl_rules WHERE printer_uri = ?`, printerURI)
	return err
}

func scanPrinterACLRule(scanner interface{ Scan(...any) error }) (PrinterACLRule, error) {
	var rule PrinterACLRule
	var subjectRole sql.NullString
	err := scanner.Scan(
		&rule.ID,
		&rule.PrinterURI,
		&rule.SubjectType,
		&subjectRole,
		&rule.SubjectUserID,
		&rule.SubjectGroupID,
		&rule.Effect,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	rule.SubjectRole = subjectRole.String
	return rule, err
}

func scanPrinterACLRules(rows *sql.Rows) ([]PrinterACLRule, error) {
	var rules []PrinterACLRule
	for rows.Next() {
		rule, err := scanPrinterACLRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func normalizeCreatePrinterACLRuleInput(input CreatePrinterACLRuleInput) CreatePrinterACLRuleInput {
	input.PrinterURI = strings.TrimSpace(input.PrinterURI)
	input.SubjectType = strings.ToLower(strings.TrimSpace(input.SubjectType))
	input.SubjectRole = strings.TrimSpace(input.SubjectRole)
	input.Effect = strings.ToLower(strings.TrimSpace(input.Effect))
	return input
}

func validateCreatePrinterACLRuleInput(input CreatePrinterACLRuleInput) error {
	if input.PrinterURI == "" {
		return fmt.Errorf("printer uri is required")
	}
	if input.Effect != PrinterACLEffectAllow && input.Effect != PrinterACLEffectDeny {
		return fmt.Errorf("unsupported printer ACL effect %q", input.Effect)
	}

	switch input.SubjectType {
	case PrinterACLSubjectRole:
		if input.SubjectRole == "" {
			return fmt.Errorf("subject role is required")
		}
		if input.SubjectUserID != 0 {
			return fmt.Errorf("subject user id must be empty for role rule")
		}
		if input.SubjectGroupID != 0 {
			return fmt.Errorf("subject group id must be empty for role rule")
		}
	case PrinterACLSubjectGroup:
		if input.SubjectGroupID <= 0 {
			return fmt.Errorf("subject group id must be positive")
		}
		if input.SubjectRole != "" {
			return fmt.Errorf("subject role must be empty for group rule")
		}
		if input.SubjectUserID != 0 {
			return fmt.Errorf("subject user id must be empty for group rule")
		}
	case PrinterACLSubjectUser:
		if input.SubjectUserID <= 0 {
			return fmt.Errorf("subject user id must be positive")
		}
		if input.SubjectRole != "" {
			return fmt.Errorf("subject role must be empty for user rule")
		}
		if input.SubjectGroupID != 0 {
			return fmt.Errorf("subject group id must be empty for user rule")
		}
	default:
		return fmt.Errorf("unsupported printer ACL subject type %q", input.SubjectType)
	}

	return nil
}

func nullableInt64Value(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
