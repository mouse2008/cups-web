package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrPrinterGroupNameRequired       = errors.New("printer group name is required")
	ErrPrinterGroupNameExists         = errors.New("printer group name already exists")
	ErrPrinterGroupNotFound           = errors.New("printer group not found")
	ErrPrinterGroupMemberUserNotFound = errors.New("printer group member user not found")
)

type PrinterGroup struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   string
	UpdatedAt   string
}

type PrinterGroupMember struct {
	ID        int64
	GroupID   int64
	UserID    int64
	CreatedAt string
	UpdatedAt string
}

type CreatePrinterGroupInput struct {
	Name        string
	Description string
}

type UpdatePrinterGroupInput struct {
	Name        string
	Description string
}

const printerGroupSelectColumns = `
	id, name, description, created_at, updated_at`

func CreatePrinterGroup(ctx context.Context, tx *sql.Tx, input CreatePrinterGroupInput) (PrinterGroup, error) {
	input = normalizePrinterGroupInput(input.Name, input.Description)
	if input.Name == "" {
		return PrinterGroup{}, ErrPrinterGroupNameRequired
	}

	now := nowUTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO printer_groups (
		name, description, created_at, updated_at
	) VALUES (?, ?, ?, ?)`, input.Name, input.Description, now, now)
	if err != nil {
		if isUniqueConstraintError(err) {
			return PrinterGroup{}, ErrPrinterGroupNameExists
		}
		return PrinterGroup{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return PrinterGroup{}, err
	}
	return GetPrinterGroupByID(ctx, tx, id)
}

func UpdatePrinterGroup(ctx context.Context, tx *sql.Tx, id int64, input UpdatePrinterGroupInput) (PrinterGroup, error) {
	normalized := normalizePrinterGroupInput(input.Name, input.Description)
	if normalized.Name == "" {
		return PrinterGroup{}, ErrPrinterGroupNameRequired
	}

	now := nowUTC()
	res, err := tx.ExecContext(ctx, `UPDATE printer_groups SET
		name = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		normalized.Name, normalized.Description, now, id,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return PrinterGroup{}, ErrPrinterGroupNameExists
		}
		return PrinterGroup{}, err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return PrinterGroup{}, ErrPrinterGroupNotFound
	}
	if err != nil {
		return PrinterGroup{}, err
	}

	return GetPrinterGroupByID(ctx, tx, id)
}

func DeletePrinterGroup(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := ensurePrinterGroupExists(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM printer_acl_rules WHERE subject_type = ? AND subject_group_id = ?`, PrinterACLSubjectGroup, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM printer_group_members WHERE group_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM printer_groups WHERE id = ?`, id)
	return err
}

func GetPrinterGroupByID(ctx context.Context, tx *sql.Tx, id int64) (PrinterGroup, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+printerGroupSelectColumns+` FROM printer_groups WHERE id = ?`, id)
	return scanPrinterGroup(row)
}

func ListPrinterGroups(ctx context.Context, tx *sql.Tx) ([]PrinterGroup, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+printerGroupSelectColumns+` FROM printer_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []PrinterGroup
	for rows.Next() {
		group, err := scanPrinterGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func ListPrinterGroupMembers(ctx context.Context, tx *sql.Tx, groupID int64) ([]User, error) {
	rows, err := tx.QueryContext(ctx, `SELECT
			users.id, users.username, users.password_hash, users.role, users.protected, users.contact_name, users.phone, users.email,
			users.auth_source, users.ldap_dn, users.ldap_uid, users.ldap_sync_enabled, users.ldap_present,
			users.last_ldap_sync_at, users.last_login_at, users.created_at, users.updated_at
		FROM users
		INNER JOIN printer_group_members ON printer_group_members.user_id = users.id
		WHERE printer_group_members.group_id = ?
		ORDER BY users.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func ReplacePrinterGroupMembers(ctx context.Context, tx *sql.Tx, groupID int64, userIDs []int64) error {
	if err := ensurePrinterGroupExists(ctx, tx, groupID); err != nil {
		return err
	}

	uniqueUserIDs, err := normalizePrinterGroupMemberUserIDs(userIDs)
	if err != nil {
		return err
	}
	if err := ensureUsersExist(ctx, tx, uniqueUserIDs); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM printer_group_members WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	if len(uniqueUserIDs) == 0 {
		return nil
	}

	now := nowUTC()
	for _, userID := range uniqueUserIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO printer_group_members (
			group_id, user_id, created_at, updated_at
		) VALUES (?, ?, ?, ?)`, groupID, userID, now, now); err != nil {
			return err
		}
	}
	return nil
}

func ListPrinterGroupIDsByUserID(ctx context.Context, tx *sql.Tx, userID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT printer_group_members.group_id
		FROM printer_group_members
		INNER JOIN printer_groups ON printer_groups.id = printer_group_members.group_id
		WHERE printer_group_members.user_id = ?
		ORDER BY printer_group_members.group_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groupIDs []int64
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func scanPrinterGroup(s scanner) (PrinterGroup, error) {
	var group PrinterGroup
	var description sql.NullString
	err := s.Scan(&group.ID, &group.Name, &description, &group.CreatedAt, &group.UpdatedAt)
	group.Description = description.String
	return group, err
}

func normalizePrinterGroupInput(name string, description string) CreatePrinterGroupInput {
	return CreatePrinterGroupInput{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
	}
}

func normalizePrinterGroupMemberUserIDs(userIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(userIDs))
	unique := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			return nil, ErrPrinterGroupMemberUserNotFound
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique, nil
}

func ensurePrinterGroupExists(ctx context.Context, tx *sql.Tx, groupID int64) error {
	if groupID <= 0 {
		return ErrPrinterGroupNotFound
	}
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM printer_groups WHERE id = ?`, groupID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPrinterGroupNotFound
	}
	return err
}

func ensureUsersExist(ctx context.Context, tx *sql.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(userIDs))
	args := make([]any, 0, len(userIDs))
	for _, userID := range userIDs {
		placeholders = append(placeholders, "?")
		args = append(args, userID)
	}

	query := fmt.Sprintf(`SELECT COUNT(1) FROM users WHERE id IN (%s)`, strings.Join(placeholders, ", "))
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return err
	}
	if count != len(userIDs) {
		return ErrPrinterGroupMemberUserNotFound
	}
	return nil
}

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
