package ldap

import (
	"context"
	"database/sql"
	"strings"

	"cups-web/internal/store"
)

type Config struct {
	Enabled             bool
	URL                 string
	BindDN              string
	BindPassword        string
	BaseDN              string
	UserFilter          string
	LoginAttr           string
	DisplayNameAttr     string
	EmailAttr           string
	PhoneAttr           string
	SyncIntervalMinutes int64
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.URL) == "" ||
		strings.TrimSpace(c.BaseDN) == "" ||
		strings.TrimSpace(c.UserFilter) == "" ||
		strings.TrimSpace(c.LoginAttr) == "" {
		return ErrInvalidConfig
	}
	return nil
}

func (c Config) ValidateForSync() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Enabled && c.SyncIntervalMinutes <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

func LoadConfig(ctx context.Context, s *store.Store) (Config, error) {
	var cfg Config
	err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		var err error

		cfg.Enabled, err = store.GetSettingBool(ctx, tx, store.SettingLDAPEnabled, false)
		if err != nil {
			return err
		}
		cfg.URL, err = store.GetSettingString(ctx, tx, store.SettingLDAPURL, "")
		if err != nil {
			return err
		}
		cfg.BindDN, err = store.GetSettingString(ctx, tx, store.SettingLDAPBindDN, "")
		if err != nil {
			return err
		}
		cfg.BindPassword, err = store.GetSettingString(ctx, tx, store.SettingLDAPBindPassword, "")
		if err != nil {
			return err
		}
		cfg.BaseDN, err = store.GetSettingString(ctx, tx, store.SettingLDAPBaseDN, "")
		if err != nil {
			return err
		}
		cfg.UserFilter, err = store.GetSettingString(ctx, tx, store.SettingLDAPUserFilter, "(objectClass=person)")
		if err != nil {
			return err
		}
		cfg.LoginAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPLoginAttr, "uid")
		if err != nil {
			return err
		}
		cfg.DisplayNameAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPDisplayNameAttr, "cn")
		if err != nil {
			return err
		}
		cfg.EmailAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPEmailAttr, "mail")
		if err != nil {
			return err
		}
		cfg.PhoneAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPPhoneAttr, "telephoneNumber")
		if err != nil {
			return err
		}
		cfg.SyncIntervalMinutes, err = store.GetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, 60)
		return err
	})
	return cfg, err
}
