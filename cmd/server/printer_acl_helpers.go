package main

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"

	"cups-web/internal/acl"
	"cups-web/internal/auth"
	"cups-web/internal/ipp"
	"cups-web/internal/store"
)

const errForbiddenPrinterMessage = "forbidden printer"

var (
	errSessionUserNotFound = errors.New("session user not found")
	errInvalidPrinter      = errors.New("invalid printer")
)

func withPrinterACLUser(r *http.Request, fn func(*sql.Tx, store.User, acl.PrinterAuthorizer) error) error {
	sess, err := auth.GetSession(r)
	if err != nil {
		return err
	}

	return appStore.WithTx(r.Context(), true, func(tx *sql.Tx) error {
		user, err := store.GetUserByID(r.Context(), tx, sess.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return errSessionUserNotFound
		}
		if err != nil {
			return err
		}
		return fn(tx, user, acl.NewPrinterAuthorizer())
	})
}

func canUsePrinter(r *http.Request, printerURI string) (store.User, bool, error) {
	var user store.User
	var allowed bool
	err := withPrinterACLUser(r, func(tx *sql.Tx, currentUser store.User, authorizer acl.PrinterAuthorizer) error {
		var err error
		user = currentUser
		allowed, err = authorizer.CanUsePrinter(r.Context(), tx, currentUser, printerURI)
		return err
	})
	return user, allowed, err
}

func filterVisiblePrinters(r *http.Request, printers []ipp.Printer) ([]ipp.Printer, error) {
	var visible []ipp.Printer
	err := withPrinterACLUser(r, func(tx *sql.Tx, user store.User, authorizer acl.PrinterAuthorizer) error {
		var err error
		visible, err = authorizer.FilterPrinters(r.Context(), tx, user, printers)
		return err
	})
	return visible, err
}

func configuredCUPSHost() string {
	cupsHost := strings.TrimSpace(os.Getenv("CUPS_HOST"))
	if cupsHost == "" {
		return "localhost"
	}
	return cupsHost
}

func resolveTrustedPrinterURI(printerURI string) (string, error) {
	printerURI = strings.TrimSpace(printerURI)
	if printerURI == "" {
		return "", errInvalidPrinter
	}

	printers, err := ipp.ListPrinters(configuredCUPSHost())
	if err != nil {
		return "", err
	}
	for _, printer := range printers {
		if printer.URI == printerURI {
			return printer.URI, nil
		}
	}
	return "", errInvalidPrinter
}
