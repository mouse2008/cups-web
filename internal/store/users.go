package store

import (
	"context"
	"database/sql"
)

const (
	authSourceLocal = "local"
	authSourceLDAP  = "ldap"
)

type User struct {
	ID              int64
	Username        string
	PasswordHash    string
	Role            string
	Protected       bool
	ContactName     string
	Phone           string
	Email           string
	AuthSource      string
	LDAPDN          string
	LDAPUID         string
	LDAPSyncEnabled bool
	LDAPPresent     bool
	LastLDAPSyncAt  string
	LastLoginAt     string
	CreatedAt       string
	UpdatedAt       string
}

type CreateUserInput struct {
	Username        string
	PasswordHash    string
	Role            string
	Protected       bool
	ContactName     string
	Phone           string
	Email           string
	AuthSource      string
	LDAPDN          string
	LDAPUID         string
	LDAPSyncEnabled bool
	LDAPPresent     bool
	LastLDAPSyncAt  string
	LastLoginAt     string
}

type UpdateUserInput struct {
	ID           int64
	Username     string
	PasswordHash *string
	Role         string
	ContactName  string
	Phone        string
	Email        string
}

type UpsertLDAPUserInput struct {
	Username    string
	LDAPUID     string
	LDAPDN      string
	ContactName string
	Phone       string
	Email       string
	DefaultRole string
}

const userSelectColumns = `
	id, username, password_hash, role, protected, contact_name, phone, email,
	auth_source, ldap_dn, ldap_uid, ldap_sync_enabled, ldap_present,
	last_ldap_sync_at, last_login_at, created_at, updated_at`

func CountUsers(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(1) FROM users").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func GetUserByUsername(ctx context.Context, tx *sql.Tx, username string) (User, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+userSelectColumns+` FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func GetUserByID(ctx context.Context, tx *sql.Tx, id int64) (User, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+userSelectColumns+` FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func ListUsers(ctx context.Context, tx *sql.Tx) ([]User, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+userSelectColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func CreateUser(ctx context.Context, tx *sql.Tx, input CreateUserInput) (User, error) {
	now := nowUTC()
	authSource := normalizeAuthSource(input.AuthSource)
	ldapPresent := input.LDAPPresent
	if authSource == authSourceLocal {
		ldapPresent = true
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users (
		username, password_hash, role, protected, contact_name, phone, email,
		auth_source, ldap_dn, ldap_uid, ldap_sync_enabled, ldap_present, last_ldap_sync_at, last_login_at,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Username, input.PasswordHash, input.Role, input.Protected, input.ContactName, input.Phone, input.Email,
		authSource, nullableStringValue(input.LDAPDN), nullableStringValue(input.LDAPUID), input.LDAPSyncEnabled, ldapPresent,
		nullableStringValue(input.LastLDAPSyncAt), nullableStringValue(input.LastLoginAt),
		now, now,
	)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return GetUserByID(ctx, tx, id)
}

func UpdateUser(ctx context.Context, tx *sql.Tx, input UpdateUserInput) (User, error) {
	now := nowUTC()
	if input.PasswordHash != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET
			username = ?, password_hash = ?, role = ?, contact_name = ?, phone = ?, email = ?,
			updated_at = ?
			WHERE id = ?`,
			input.Username, *input.PasswordHash, input.Role, input.ContactName, input.Phone, input.Email,
			now, input.ID,
		); err != nil {
			return User{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET
			username = ?, role = ?, contact_name = ?, phone = ?, email = ?,
			updated_at = ?
			WHERE id = ?`,
			input.Username, input.Role, input.ContactName, input.Phone, input.Email,
			now, input.ID,
		); err != nil {
			return User{}, err
		}
	}
	return GetUserByID(ctx, tx, input.ID)
}

func GetUserByLDAPUIDOrDN(ctx context.Context, tx *sql.Tx, ldapUID string, ldapDN string) (User, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+userSelectColumns+`
		FROM users
		WHERE (? <> '' AND ldap_uid = ?)
		   OR (? <> '' AND ldap_dn = ?)
		ORDER BY id
		LIMIT 1`, ldapUID, ldapUID, ldapDN, ldapDN)
	return scanUser(row)
}

func UpsertLDAPUser(ctx context.Context, tx *sql.Tx, input UpsertLDAPUserInput) (User, error) {
	now := nowUTC()

	current, err := GetUserByLDAPUIDOrDN(ctx, tx, input.LDAPUID, input.LDAPDN)
	if err == sql.ErrNoRows {
		current, err = GetUserByUsername(ctx, tx, input.Username)
	}
	if err == sql.ErrNoRows {
		return CreateUser(ctx, tx, CreateUserInput{
			Username:        input.Username,
			PasswordHash:    "",
			Role:            normalizeRoleOrDefault(input.DefaultRole, RoleUser),
			Protected:       false,
			ContactName:     input.ContactName,
			Phone:           input.Phone,
			Email:           input.Email,
			AuthSource:      authSourceLDAP,
			LDAPDN:          input.LDAPDN,
			LDAPUID:         input.LDAPUID,
			LDAPSyncEnabled: true,
			LDAPPresent:     true,
			LastLDAPSyncAt:  now,
		})
	}
	if err != nil {
		return User{}, err
	}

	contactName := current.ContactName
	if contactName == "" {
		contactName = input.ContactName
	}
	phone := current.Phone
	if phone == "" {
		phone = input.Phone
	}
	email := current.Email
	if email == "" {
		email = input.Email
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET
		username = ?, auth_source = ?, ldap_uid = ?, ldap_dn = ?,
		ldap_sync_enabled = 1, ldap_present = 1, last_ldap_sync_at = ?,
		contact_name = ?, phone = ?, email = ?, updated_at = ?
		WHERE id = ?`,
		input.Username, authSourceLDAP, nullableStringValue(input.LDAPUID), nullableStringValue(input.LDAPDN),
		now, contactName, phone, email, now, current.ID,
	); err != nil {
		return User{}, err
	}
	return GetUserByID(ctx, tx, current.ID)
}

func TouchLastLogin(ctx context.Context, tx *sql.Tx, id int64) error {
	now := nowUTC()
	_, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func MarkMissingLDAPUsers(ctx context.Context, tx *sql.Tx, seen map[string]struct{}) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, ldap_uid
		FROM users
		WHERE auth_source = ? AND ldap_sync_enabled = 1 AND ldap_present = 1`, authSourceLDAP)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var ldapUID sql.NullString
		if err := rows.Scan(&id, &ldapUID); err != nil {
			return 0, err
		}
		if _, ok := seen[ldapUID.String]; !ok {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	now := nowUTC()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET ldap_present = 0, updated_at = ? WHERE id = ?`, now, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func DeleteUser(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return err
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanUser(s scanner) (User, error) {
	var user User
	var contactName sql.NullString
	var phone sql.NullString
	var email sql.NullString
	var authSource sql.NullString
	var ldapDN sql.NullString
	var ldapUID sql.NullString
	var lastLDAPSyncAt sql.NullString
	var lastLoginAt sql.NullString
	err := s.Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.Protected, &contactName, &phone, &email,
		&authSource, &ldapDN, &ldapUID, &user.LDAPSyncEnabled, &user.LDAPPresent,
		&lastLDAPSyncAt, &lastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	user.ContactName = contactName.String
	user.Phone = phone.String
	user.Email = email.String
	user.AuthSource = normalizeAuthSource(authSource.String)
	user.LDAPDN = ldapDN.String
	user.LDAPUID = ldapUID.String
	user.LastLDAPSyncAt = lastLDAPSyncAt.String
	user.LastLoginAt = lastLoginAt.String
	return user, err
}

func normalizeAuthSource(source string) string {
	if source == authSourceLDAP {
		return authSourceLDAP
	}
	return authSourceLocal
}

func normalizeRoleOrDefault(role string, defaultRole string) string {
	if role != "" {
		return role
	}
	return defaultRole
}

func nullableStringValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}
