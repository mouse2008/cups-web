package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cups-web/internal/store"
)

type adminPrinterACLUpdatePayload struct {
	Rules []adminPrinterACLUpdateRule `json:"rules"`
}

type adminPrinterACLUpdateRule struct {
	SubjectType    string `json:"subjectType"`
	SubjectRole    string `json:"subjectRole"`
	SubjectUserID  *int64 `json:"subjectUserId"`
	SubjectGroupID *int64 `json:"subjectGroupId"`
	Effect         string `json:"effect"`
}

type adminPrinterACLConfigResponse struct {
	PrinterURI string                      `json:"printerUri"`
	Rules      []adminPrinterACLConfigRule `json:"rules"`
}

type adminPrinterACLConfigRule struct {
	ID             int64  `json:"id"`
	SubjectType    string `json:"subjectType"`
	SubjectRole    string `json:"subjectRole"`
	SubjectUserID  *int64 `json:"subjectUserId"`
	SubjectGroupID *int64 `json:"subjectGroupId"`
	Effect         string `json:"effect"`
}

func adminGetPrinterACLHandler(w http.ResponseWriter, r *http.Request) {
	printerURI := strings.TrimSpace(r.URL.Query().Get("printer"))
	if printerURI == "" {
		writeJSONError(w, http.StatusBadRequest, "printer is required")
		return
	}

	var resp adminPrinterACLConfigResponse
	err := appStore.WithTx(r.Context(), true, func(tx *sql.Tx) error {
		rules, err := store.ListPrinterACLRulesByPrinter(r.Context(), tx, printerURI)
		if err != nil {
			return err
		}
		resp = mapAdminPrinterACLConfig(printerURI, rules)
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load printer ACL")
		return
	}
	writeJSON(w, resp)
}

func adminUpdatePrinterACLHandler(w http.ResponseWriter, r *http.Request) {
	printerURI := strings.TrimSpace(r.URL.Query().Get("printer"))
	if printerURI == "" {
		writeJSONError(w, http.StatusBadRequest, "printer is required")
		return
	}

	var payload adminPrinterACLUpdatePayload
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	var resp adminPrinterACLConfigResponse
	err := appStore.WithTx(r.Context(), false, func(tx *sql.Tx) error {
		inputs, err := validateAdminPrinterACLRules(r, tx, printerURI, payload.Rules)
		if err != nil {
			return err
		}

		if err := store.DeletePrinterACLRulesByPrinter(r.Context(), tx, printerURI); err != nil {
			return err
		}
		for _, input := range inputs {
			if _, err := store.CreatePrinterACLRule(r.Context(), tx, input); err != nil {
				return err
			}
		}

		rules, err := store.ListPrinterACLRulesByPrinter(r.Context(), tx, printerURI)
		if err != nil {
			return err
		}
		resp = mapAdminPrinterACLConfig(printerURI, rules)
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errPrinterACLInvalidSubjectType),
			errors.Is(err, errPrinterACLRoleRequiresRole),
			errors.Is(err, errPrinterACLRoleHasUser),
			errors.Is(err, errPrinterACLRoleHasGroup),
			errors.Is(err, errPrinterACLInvalidRole),
			errors.Is(err, errPrinterACLUserRequiresID),
			errors.Is(err, errPrinterACLUserHasRole),
			errors.Is(err, errPrinterACLUserHasGroup),
			errors.Is(err, errPrinterACLUserNotFound),
			errors.Is(err, errPrinterACLGroupRequiresID),
			errors.Is(err, errPrinterACLGroupHasRole),
			errors.Is(err, errPrinterACLGroupHasUser),
			errors.Is(err, errPrinterACLGroupNotFound),
			errors.Is(err, errPrinterACLInvalidEffect):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			writeJSONError(w, http.StatusInternalServerError, "failed to save printer ACL")
		}
		return
	}

	writeJSON(w, resp)
}

var (
	errPrinterACLInvalidSubjectType = errors.New("invalid subjectType")
	errPrinterACLRoleRequiresRole   = errors.New("role rule requires subjectRole")
	errPrinterACLRoleHasUser        = errors.New("role rule must not include subjectUserId")
	errPrinterACLRoleHasGroup       = errors.New("role rule must not include subjectGroupId")
	errPrinterACLInvalidRole        = errors.New("invalid subjectRole")
	errPrinterACLUserRequiresID     = errors.New("user rule requires subjectUserId")
	errPrinterACLUserHasRole        = errors.New("user rule must not include subjectRole")
	errPrinterACLUserHasGroup       = errors.New("user rule must not include subjectGroupId")
	errPrinterACLUserNotFound       = errors.New("user not found")
	errPrinterACLGroupRequiresID    = errors.New("group rule requires subjectGroupId")
	errPrinterACLGroupHasRole       = errors.New("group rule must not include subjectRole")
	errPrinterACLGroupHasUser       = errors.New("group rule must not include subjectUserId")
	errPrinterACLGroupNotFound      = errors.New("printer group not found")
	errPrinterACLInvalidEffect      = errors.New("invalid effect")
)

func validateAdminPrinterACLRules(r *http.Request, tx *sql.Tx, printerURI string, rules []adminPrinterACLUpdateRule) ([]store.CreatePrinterACLRuleInput, error) {
	inputs := make([]store.CreatePrinterACLRuleInput, 0, len(rules))
	for _, rule := range rules {
		subjectType := strings.ToLower(strings.TrimSpace(rule.SubjectType))
		effect := strings.ToLower(strings.TrimSpace(rule.Effect))
		rawSubjectRole := strings.ToLower(strings.TrimSpace(rule.SubjectRole))

		input := store.CreatePrinterACLRuleInput{
			PrinterURI:  printerURI,
			SubjectType: subjectType,
			SubjectRole: rawSubjectRole,
			Effect:      effect,
		}

		switch subjectType {
		case store.PrinterACLSubjectRole:
			if rawSubjectRole == "" {
				return nil, errPrinterACLRoleRequiresRole
			}
			if rule.SubjectUserID != nil {
				return nil, errPrinterACLRoleHasUser
			}
			if rule.SubjectGroupID != nil {
				return nil, errPrinterACLRoleHasGroup
			}
			if rawSubjectRole != store.RoleAdmin && rawSubjectRole != store.RoleUser {
				return nil, errPrinterACLInvalidRole
			}
		case store.PrinterACLSubjectUser:
			if rule.SubjectUserID == nil || *rule.SubjectUserID <= 0 {
				return nil, errPrinterACLUserRequiresID
			}
			if strings.TrimSpace(rule.SubjectRole) != "" {
				return nil, errPrinterACLUserHasRole
			}
			if rule.SubjectGroupID != nil {
				return nil, errPrinterACLUserHasGroup
			}
			if _, err := store.GetUserByID(r.Context(), tx, *rule.SubjectUserID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, errPrinterACLUserNotFound
				}
				return nil, err
			}
			input.SubjectUserID = *rule.SubjectUserID
			input.SubjectRole = ""
		case store.PrinterACLSubjectGroup:
			if rule.SubjectGroupID == nil || *rule.SubjectGroupID <= 0 {
				return nil, errPrinterACLGroupRequiresID
			}
			if strings.TrimSpace(rule.SubjectRole) != "" {
				return nil, errPrinterACLGroupHasRole
			}
			if rule.SubjectUserID != nil {
				return nil, errPrinterACLGroupHasUser
			}
			if _, err := store.GetPrinterGroupByID(r.Context(), tx, *rule.SubjectGroupID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, errPrinterACLGroupNotFound
				}
				return nil, err
			}
			input.SubjectGroupID = *rule.SubjectGroupID
			input.SubjectRole = ""
		default:
			return nil, errPrinterACLInvalidSubjectType
		}

		if effect != store.PrinterACLEffectAllow && effect != store.PrinterACLEffectDeny {
			return nil, errPrinterACLInvalidEffect
		}

		inputs = append(inputs, input)
	}
	return inputs, nil
}

func mapAdminPrinterACLConfig(printerURI string, rules []store.PrinterACLRule) adminPrinterACLConfigResponse {
	resp := adminPrinterACLConfigResponse{
		PrinterURI: printerURI,
		Rules:      make([]adminPrinterACLConfigRule, 0, len(rules)),
	}
	for _, rule := range rules {
		resp.Rules = append(resp.Rules, mapAdminPrinterACLRule(rule))
	}
	return resp
}

func mapAdminPrinterACLRule(rule store.PrinterACLRule) adminPrinterACLConfigRule {
	resp := adminPrinterACLConfigRule{
		ID:          rule.ID,
		SubjectType: rule.SubjectType,
		SubjectRole: rule.SubjectRole,
		Effect:      rule.Effect,
	}
	if rule.SubjectUserID.Valid {
		subjectUserID := rule.SubjectUserID.Int64
		resp.SubjectUserID = &subjectUserID
	}
	if rule.SubjectGroupID.Valid {
		subjectGroupID := rule.SubjectGroupID.Int64
		resp.SubjectGroupID = &subjectGroupID
	}
	return resp
}
