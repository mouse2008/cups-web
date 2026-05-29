package store

import (
	"context"
	"database/sql"
	"strconv"
)

func GetSettingInt(ctx context.Context, tx *sql.Tx, key string, defaultVal int64) (int64, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return defaultVal, nil
	}
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func SetSettingInt(ctx context.Context, tx *sql.Tx, key string, value int64) error {
	_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO settings(key, value) VALUES (?, ?)", key, strconv.FormatInt(value, 10))
	return err
}

func GetSettingBool(ctx context.Context, tx *sql.Tx, key string, defaultVal bool) (bool, error) {
	raw, err := GetSettingString(ctx, tx, key, "")
	if err != nil {
		return false, err
	}
	if raw == "" {
		return defaultVal, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err == nil {
		return parsed, nil
	}
	switch raw {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, err
	}
}

func SetSettingBool(ctx context.Context, tx *sql.Tx, key string, value bool) error {
	if value {
		return SetSettingString(ctx, tx, key, "1")
	}
	return SetSettingString(ctx, tx, key, "0")
}

func GetSettingString(ctx context.Context, tx *sql.Tx, key string, defaultVal string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return defaultVal, nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func SetSettingString(ctx context.Context, tx *sql.Tx, key string, value string) error {
	_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO settings(key, value) VALUES (?, ?)", key, value)
	return err
}
