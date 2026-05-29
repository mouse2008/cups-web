package ldap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"cups-web/internal/store"

	gldap "github.com/go-ldap/ldap/v3"
)

var (
	ErrLDAPDisabled           = errors.New("ldap is disabled")
	ErrInvalidConfig          = errors.New("ldap config is invalid")
	ErrInvalidCredentials     = errors.New("ldap credentials are invalid")
	ErrAmbiguousSearchResult  = errors.New("ldap search returned multiple matches")
	ErrMalformedDirectoryUser = errors.New("ldap directory user is malformed")
	ErrProvisioningConflict   = errors.New("ldap user provisioning conflict")
)

type Service struct {
	store  *store.Store
	client Client
}

type SyncReport struct {
	Upserted      int
	Skipped       int
	MissingMarked int
}

func NewService(s *store.Store, client Client) *Service {
	if client == nil {
		client = NewClient()
	}
	return &Service{
		store:  s,
		client: client,
	}
}

func (s *Service) AuthenticateOrProvision(ctx context.Context, cfg Config, username string, password string) (store.User, error) {
	if !cfg.Enabled {
		return store.User{}, ErrLDAPDisabled
	}
	if err := cfg.Validate(); err != nil {
		return store.User{}, err
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return store.User{}, ErrInvalidCredentials
	}

	matches, err := s.client.SearchUser(ctx, cfg, username)
	if err != nil {
		return store.User{}, err
	}
	if len(matches) == 0 {
		return store.User{}, ErrInvalidCredentials
	}
	if len(matches) > 1 {
		return store.User{}, ErrAmbiguousSearchResult
	}

	match := matches[0]
	if strings.TrimSpace(match.Username) == "" || strings.TrimSpace(match.DN) == "" {
		return store.User{}, ErrMalformedDirectoryUser
	}
	if err := s.client.BindUser(ctx, cfg, match.DN, password); err != nil {
		if isInvalidCredentialsError(err) {
			return store.User{}, ErrInvalidCredentials
		}
		return store.User{}, err
	}

	var user store.User
	err = s.store.WithTx(ctx, false, func(tx *sql.Tx) error {
		existing, err := store.GetUserByUsername(ctx, tx, match.Username)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && existing.AuthSource != "ldap" {
			return fmt.Errorf("%w: username %q is already used by a local user", ErrProvisioningConflict, match.Username)
		}

		provisioned, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
			Username:    match.Username,
			LDAPUID:     match.UID,
			LDAPDN:      match.DN,
			ContactName: match.DisplayName,
			Phone:       match.Phone,
			Email:       match.Email,
		})
		if err != nil {
			if isUsernameConstraintError(err) {
				return fmt.Errorf("%w: username %q is already used", ErrProvisioningConflict, match.Username)
			}
			return err
		}
		if err := store.TouchLastLogin(ctx, tx, provisioned.ID); err != nil {
			return err
		}
		user, err = store.GetUserByID(ctx, tx, provisioned.ID)
		return err
	})
	return user, err
}

func (s *Service) SyncAll(ctx context.Context, cfg Config) (SyncReport, error) {
	if !cfg.Enabled {
		return SyncReport{}, ErrLDAPDisabled
	}
	if err := cfg.ValidateForSync(); err != nil {
		return SyncReport{}, err
	}

	users, err := s.client.SearchAllUsers(ctx, cfg)
	if err != nil {
		return SyncReport{}, err
	}

	report := SyncReport{}
	err = s.store.WithTx(ctx, false, func(tx *sql.Tx) error {
		seen := map[string]struct{}{}
		for _, user := range users {
			if strings.TrimSpace(user.Username) == "" || (strings.TrimSpace(user.UID) == "" && strings.TrimSpace(user.DN) == "") {
				report.Skipped++
				continue
			}

			addSeenIdentity(seen, user.UID, user.DN)
			if _, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
				Username:    user.Username,
				LDAPUID:     user.UID,
				LDAPDN:      user.DN,
				ContactName: user.DisplayName,
				Phone:       user.Phone,
				Email:       user.Email,
			}); err != nil {
				return err
			}
			report.Upserted++
		}

		missing, err := store.MarkMissingLDAPUsers(ctx, tx, seen)
		if err != nil {
			return err
		}
		report.MissingMarked = missing
		return nil
	})
	return report, err
}

func isInvalidCredentialsError(err error) bool {
	return gldap.IsErrorWithCode(err, gldap.LDAPResultInvalidCredentials)
}

func isUsernameConstraintError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") && strings.Contains(message, "users.username")
}

func addSeenIdentity(seen map[string]struct{}, values ...string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
}
