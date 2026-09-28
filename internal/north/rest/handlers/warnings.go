// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

// WarningsPort is the slice of internal/warnings the routes need.
// Silences are per user, so every method takes the caller's username.
type WarningsPort interface {
	ForUser(ctx context.Context, username string) ([]warnings.Warning, error)
	SilenceWarning(ctx context.Context, username, warningID string, days int) error
	UnsilenceWarning(ctx context.Context, username, warningID string) error
}

// ListWarnings serves GET /warnings: the active operator warnings,
// annotated with the calling user's silences. A nil service (reduced
// test wiring) answers an empty list, matching the other list routes.
func ListWarnings(svc WarningsPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			JSON(w, http.StatusOK, warningsListResponse{Items: []warnings.Warning{}})
			return
		}
		username := ""
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			username = id.Subject
		}
		items, err := svc.ForUser(r.Context(), username)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		if items == nil {
			items = []warnings.Warning{}
		}
		JSON(w, http.StatusOK, warningsListResponse{Items: items})
	}
}

type warningsListResponse struct {
	Items []warnings.Warning `json:"items"`
}

// silenceRequest is the PUT body: how long the warning stays muted.
type silenceRequest struct {
	Days int `json:"days"`
}

// SilenceWarning serves PUT /warnings/{id}/silence for the calling user.
func SilenceWarning(svc WarningsPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := requireSubject(w, r)
		if !ok {
			return
		}
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Warnings unavailable", "no warnings service wired"))
			return
		}
		var req silenceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid body", err.Error()))
			return
		}
		err := svc.SilenceWarning(r.Context(), username, chi.URLParam(r, "id"), req.Days)
		switch {
		case errors.Is(err, warnings.ErrUnknownWarning):
			problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "No such active warning", ""))
		case errors.Is(err, warnings.ErrBadPeriod):
			problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeValidation, r, "Invalid silence period", err.Error()))
		case err != nil:
			problem.WriteFromError(w, r, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// UnsilenceWarning serves DELETE /warnings/{id}/silence.
func UnsilenceWarning(svc WarningsPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := requireSubject(w, r)
		if !ok {
			return
		}
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Warnings unavailable", "no warnings service wired"))
			return
		}
		if err := svc.UnsilenceWarning(r.Context(), username, chi.URLParam(r, "id")); err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// requireSubject resolves the authenticated username. The auth middleware
// guards these routes in production; this is the defense for a router
// built without one, so a silence never lands on the empty user.
func requireSubject(w http.ResponseWriter, r *http.Request) (string, bool) {
	if id, ok := auth.IdentityFrom(r.Context()); ok && id.Subject != "" {
		return id.Subject, true
	}
	problem.Write(w, http.StatusUnauthorized,
		problem.New(problem.TypeUnauthorized, r, "Authentication required", "silences are per user"))
	return "", false
}
