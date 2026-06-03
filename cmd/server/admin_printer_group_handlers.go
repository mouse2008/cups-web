package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"cups-web/internal/store"
)

type adminPrinterGroupPayload struct {
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	MemberUserIDs []int64 `json:"memberUserIds"`
}

type adminPrinterGroupResponse struct {
	ID            int64                   `json:"id"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	MemberUserIDs []int64                 `json:"memberUserIds"`
	Members       []adminPrinterGroupUser `json:"members"`
	CreatedAt     string                  `json:"createdAt"`
	UpdatedAt     string                  `json:"updatedAt"`
}

type adminPrinterGroupUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func adminListPrinterGroupsHandler(w http.ResponseWriter, r *http.Request) {
	var resp []adminPrinterGroupResponse
	err := appStore.WithTx(r.Context(), true, func(tx *sql.Tx) error {
		groups, err := store.ListPrinterGroups(r.Context(), tx)
		if err != nil {
			return err
		}
		resp, err = loadAdminPrinterGroupResponses(r.Context(), tx, groups)
		return err
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list printer groups")
		return
	}
	writeJSON(w, resp)
}

func adminCreatePrinterGroupHandler(w http.ResponseWriter, r *http.Request) {
	var payload adminPrinterGroupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	var resp adminPrinterGroupResponse
	err := appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		group, err := store.CreatePrinterGroup(r.Context(), tx, store.CreatePrinterGroupInput{
			Name:        payload.Name,
			Description: payload.Description,
		})
		if err != nil {
			return err
		}
		if err := store.ReplacePrinterGroupMembers(r.Context(), tx, group.ID, payload.MemberUserIDs); err != nil {
			return err
		}
		resp, err = loadAdminPrinterGroupResponse(r.Context(), tx, group)
		return err
	})
	if err != nil {
		writeAdminPrinterGroupError(w, err, "failed to create printer group")
		return
	}
	writeJSON(w, resp)
}

func adminUpdatePrinterGroupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid printer group id")
		return
	}

	var payload adminPrinterGroupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	var resp adminPrinterGroupResponse
	err = appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		group, err := store.UpdatePrinterGroup(r.Context(), tx, id, store.UpdatePrinterGroupInput{
			Name:        payload.Name,
			Description: payload.Description,
		})
		if err != nil {
			return err
		}
		if err := store.ReplacePrinterGroupMembers(r.Context(), tx, id, payload.MemberUserIDs); err != nil {
			return err
		}
		resp, err = loadAdminPrinterGroupResponse(r.Context(), tx, group)
		return err
	})
	if err != nil {
		writeAdminPrinterGroupError(w, err, "failed to update printer group")
		return
	}
	writeJSON(w, resp)
}

func adminDeletePrinterGroupHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid printer group id")
		return
	}

	err = appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		return store.DeletePrinterGroup(r.Context(), tx, id)
	})
	if err != nil {
		if errors.Is(err, store.ErrPrinterGroupNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to delete printer group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAdminPrinterGroupError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrPrinterGroupNameRequired),
		errors.Is(err, store.ErrPrinterGroupMemberUserNotFound):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrPrinterGroupNameExists):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrPrinterGroupNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, fallback)
	}
}

func loadAdminPrinterGroupResponses(ctx context.Context, tx *sql.Tx, groups []store.PrinterGroup) ([]adminPrinterGroupResponse, error) {
	resp := make([]adminPrinterGroupResponse, 0, len(groups))
	for _, group := range groups {
		item, err := loadAdminPrinterGroupResponse(ctx, tx, group)
		if err != nil {
			return nil, err
		}
		resp = append(resp, item)
	}
	return resp, nil
}

func loadAdminPrinterGroupResponse(ctx context.Context, tx *sql.Tx, group store.PrinterGroup) (adminPrinterGroupResponse, error) {
	members, err := store.ListPrinterGroupMembers(ctx, tx, group.ID)
	if err != nil {
		return adminPrinterGroupResponse{}, err
	}
	resp := adminPrinterGroupResponse{
		ID:            group.ID,
		Name:          group.Name,
		Description:   group.Description,
		MemberUserIDs: make([]int64, 0, len(members)),
		Members:       make([]adminPrinterGroupUser, 0, len(members)),
		CreatedAt:     group.CreatedAt,
		UpdatedAt:     group.UpdatedAt,
	}
	for _, member := range members {
		resp.MemberUserIDs = append(resp.MemberUserIDs, member.ID)
		resp.Members = append(resp.Members, adminPrinterGroupUser{ID: member.ID, Username: member.Username, Role: member.Role})
	}
	return resp, nil
}
