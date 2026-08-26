// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// Diagram-config handler sentinels. The cmd-side adapter translates the
// store's sentinels to these so the handler stays store-agnostic.
var (
	ErrDiagramNotFound  = errors.New("handlers: diagram not found")
	ErrDiagramForbidden = errors.New("handlers: diagram forbidden")
	ErrDiagramInvalid   = errors.New("handlers: diagram invalid")
)

// maxDiagramBytes caps a diagram config blob mirroring the store limit.
const maxDiagramBytes = 64 * 1024

// DiagramConfig is the handler-facing view of a stored diagram.
type DiagramConfig struct {
	ID           string
	OwnerSubject string
	Name         string
	Visibility   string
	ConfigJSON   string
	CreatedAtMs  int64
	UpdatedAtMs  int64
}

// DiagramConfigService is the store facade the /diagrams endpoints use.
type DiagramConfigService interface {
	List(ctx context.Context, subject string) ([]DiagramConfig, error)
	Get(ctx context.Context, id, subject string, isAdmin bool) (DiagramConfig, error)
	Create(ctx context.Context, subject, name, visibility, configJSON string) (DiagramConfig, error)
	Update(ctx context.Context, id, subject string, isAdmin bool, name, visibility, configJSON string) (DiagramConfig, error)
	Delete(ctx context.Context, id, subject string, isAdmin bool) error
}

type diagramResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Visibility  string          `json:"visibility"`
	Owner       string          `json:"owner"`
	Config      json.RawMessage `json:"config"`
	CreatedAtMs int64           `json:"created_at_ms"`
	UpdatedAtMs int64           `json:"updated_at_ms"`
}

type diagramWriteRequest struct {
	Name       string          `json:"name"`
	Visibility string          `json:"visibility"`
	Config     json.RawMessage `json:"config"`
}

func toDiagramResponse(d DiagramConfig) diagramResponse {
	cfg := json.RawMessage(d.ConfigJSON)
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return diagramResponse{
		ID: d.ID, Name: d.Name, Visibility: d.Visibility, Owner: d.OwnerSubject,
		Config: cfg, CreatedAtMs: d.CreatedAtMs, UpdatedAtMs: d.UpdatedAtMs,
	}
}

func diagramIdentity(w http.ResponseWriter, r *http.Request) (subject string, isAdmin, ok bool) {
	ident, found := auth.IdentityFrom(r.Context())
	if !found || ident.Subject == "" {
		problem.Write(w, http.StatusUnauthorized,
			problem.New(problem.TypeUnauthorized, r, "Not authenticated", ""))
		return "", false, false
	}
	return ident.Subject, ident.Role == auth.RoleAdmin, true
}

func writeDiagramError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDiagramNotFound):
		problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "Diagram not found", ""))
	case errors.Is(err, ErrDiagramForbidden):
		problem.Write(w, http.StatusForbidden, problem.New(problem.TypeForbidden, r, "Diagram not accessible", ""))
	case errors.Is(err, ErrDiagramInvalid):
		problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeValidation, r, "Invalid diagram", err.Error()))
	default:
		problem.WriteFromError(w, r, err)
	}
}

// ListDiagrams GET /api/v1/diagrams — the caller's own + shared diagrams.
func ListDiagrams(svc DiagramConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagrams unavailable", ""))
			return
		}
		subject, _, ok := diagramIdentity(w, r)
		if !ok {
			return
		}
		list, err := svc.List(r.Context(), subject)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		out := make([]diagramResponse, 0, len(list))
		for _, d := range list {
			out = append(out, toDiagramResponse(d))
		}
		JSON(w, http.StatusOK, map[string]any{"diagrams": out})
	}
}

// GetDiagram GET /api/v1/diagrams/{id}.
func GetDiagram(svc DiagramConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagrams unavailable", ""))
			return
		}
		subject, isAdmin, ok := diagramIdentity(w, r)
		if !ok {
			return
		}
		d, err := svc.Get(r.Context(), chi.URLParam(r, "id"), subject, isAdmin)
		if err != nil {
			writeDiagramError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, toDiagramResponse(d))
	}
}

func decodeDiagramWrite(w http.ResponseWriter, r *http.Request) (diagramWriteRequest, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDiagramBytes+1))
	if err != nil {
		problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r, "Read body failed", ""))
		return diagramWriteRequest{}, false
	}
	if len(body) > maxDiagramBytes {
		problem.Write(w, http.StatusRequestEntityTooLarge,
			problem.New(problem.TypeValidation, r, "Diagram too large", ""))
		return diagramWriteRequest{}, false
	}
	var req diagramWriteRequest
	if err := json.Unmarshal(body, &req); err != nil {
		problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r, "Invalid JSON", ""))
		return diagramWriteRequest{}, false
	}
	if req.Visibility == "" {
		req.Visibility = "private"
	}
	return req, true
}

func diagramAudit(rec audit.Recorder, r *http.Request, note string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: audit.ActionDiagramConfig,
		Note:   note,
	})
}

// CreateDiagram POST /api/v1/diagrams.
func CreateDiagram(svc DiagramConfigService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagrams unavailable", ""))
			return
		}
		subject, _, ok := diagramIdentity(w, r)
		if !ok {
			return
		}
		req, ok := decodeDiagramWrite(w, r)
		if !ok {
			return
		}
		d, err := svc.Create(r.Context(), subject, req.Name, req.Visibility, string(req.Config))
		if err != nil {
			writeDiagramError(w, r, err)
			return
		}
		diagramAudit(rec, r, "create "+d.Name)
		JSON(w, http.StatusCreated, toDiagramResponse(d))
	}
}

// UpdateDiagram PUT /api/v1/diagrams/{id}.
func UpdateDiagram(svc DiagramConfigService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagrams unavailable", ""))
			return
		}
		subject, isAdmin, ok := diagramIdentity(w, r)
		if !ok {
			return
		}
		req, ok := decodeDiagramWrite(w, r)
		if !ok {
			return
		}
		d, err := svc.Update(r.Context(), chi.URLParam(r, "id"), subject, isAdmin, req.Name, req.Visibility, string(req.Config))
		if err != nil {
			writeDiagramError(w, r, err)
			return
		}
		diagramAudit(rec, r, "update "+d.Name)
		JSON(w, http.StatusOK, toDiagramResponse(d))
	}
}

// DeleteDiagram DELETE /api/v1/diagrams/{id}.
func DeleteDiagram(svc DiagramConfigService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagrams unavailable", ""))
			return
		}
		subject, isAdmin, ok := diagramIdentity(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id, subject, isAdmin); err != nil {
			writeDiagramError(w, r, err)
			return
		}
		diagramAudit(rec, r, "delete "+id)
		w.WriteHeader(http.StatusNoContent)
	}
}
