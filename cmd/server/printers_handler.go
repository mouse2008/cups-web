package main

import (
	"errors"
	"net/http"

	"cups-web/internal/ipp"
)

func listPrintersHandler(w http.ResponseWriter, r *http.Request) {
	printers, err := ipp.ListPrinters(configuredCUPSHost())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list printers: "+err.Error())
		return
	}

	visible, err := filterVisiblePrinters(r, printers)
	if err != nil {
		if errors.Is(err, errSessionUserNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to filter printers")
		return
	}

	writeJSON(w, visible)
}
