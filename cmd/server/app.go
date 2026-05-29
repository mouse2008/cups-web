package main

import (
	"context"

	ldapauth "cups-web/internal/ldap"
	"cups-web/internal/store"
)

var appStore *store.Store
var ldapService ldapAuthenticator
var uploadDir string

type ldapAuthenticator interface {
	AuthenticateOrProvision(ctx context.Context, cfg ldapauth.Config, username string, password string) (store.User, error)
}

func currentLDAPService() ldapAuthenticator {
	if ldapService == nil && appStore != nil {
		ldapService = ldapauth.NewService(appStore, nil)
	}
	return ldapService
}
