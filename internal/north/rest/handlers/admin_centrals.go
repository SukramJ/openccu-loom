// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CentralAdminService is the DI surface for the /api/v1/admin/centrals
// endpoints. The router wires a [*sqlite.CentralsStore] underneath.
type CentralAdminService interface {
	// Put creates or replaces a central row.
	Put(ctx context.Context, row sqlite.CentralRow) error
	// Get returns one central by name. Returns [sqlite.ErrCentralNotFound]
	// when the name is unknown.
	Get(ctx context.Context, name string) (sqlite.CentralRow, error)
	// Delete removes a central by name. Returns [sqlite.ErrCentralNotFound]
	// when the name is unknown.
	Delete(ctx context.Context, name string) error
	// List returns every central sorted by name.
	List(ctx context.Context) ([]sqlite.CentralRow, error)
}

// maskCentralRow returns row with its cleartext CCU password replaced by
// the mask sentinel, so a central row is safe to return in an API
// response / log. The store decrypts password_plain on read; without this
// mask GET /admin/centrals would leak the live CCU credential in the clear.
// password_env holds only the env-variable NAME (not a secret) and stays
// visible so the operator can see which variable is referenced.
// [restoreCentralSecret] swaps the sentinel back on write.
func maskCentralRow(row sqlite.CentralRow) sqlite.CentralRow {
	if row.PasswordPlain != "" {
		row.PasswordPlain = maskSentinel
	}
	return row
}

// writeCentralSecretRefusal answers a store refusal to persist a CCU
// password that cannot be encrypted at rest, reporting whether it handled
// err. The condition is the operator's configuration — no master key plus
// `security.allow_plaintext_secrets: false` — so it is a 400 naming the knob
// to change, not a 500 that invites a retry of a write that will never
// succeed.
func writeCentralSecretRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, sqlite.ErrPlaintextSecretNotAllowed) {
		return false
	}
	problem.Write(w, http.StatusBadRequest,
		problem.New(problem.TypeValidation, r, "Password cannot be stored", err.Error()))
	return true
}

// ListCentrals handles GET /admin/centrals. Returns every central row
// sorted by name.
func ListCentrals(svc CentralAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central list failed", err)
			return
		}
		masked := make([]sqlite.CentralRow, 0, len(rows))
		for i := range rows {
			masked = append(masked, maskCentralRow(rows[i]))
		}
		JSON(w, http.StatusOK, masked)
	}
}

// GetCentral handles GET /admin/centrals/{name}. Returns 404 when the
// central is unknown.
func GetCentral(svc CentralAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		row, err := svc.Get(r.Context(), name)
		if errors.Is(err, sqlite.ErrCentralNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", name))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central lookup failed", err)
			return
		}
		JSON(w, http.StatusOK, maskCentralRow(row))
	}
}

// CreateCentral handles POST /admin/centrals. The request body is a
// [sqlite.CentralRow] JSON object. Returns 201 on success.
func CreateCentral(svc CentralAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var row sqlite.CentralRow
		if err := DecodeJSON(r, &row); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		if row.Name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "central name is required"))
			return
		}
		// The name becomes a path segment of the callback URL announced to
		// the CCU. Refusing it here is the only place an operator gets an
		// explanation — at callback time the symptom is a CCU that pushes
		// nothing, with nothing pointing at the name.
		if err := hmtypes.ValidateCentralName(row.Name); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid name", err.Error()))
			return
		}
		if row.Host == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing host", "central host is required"))
			return
		}
		// A fresh central has no stored credential to restore; the sentinel
		// is not a real password, so drop it rather than persist "***".
		if row.PasswordPlain == maskSentinel {
			row.PasswordPlain = ""
		}
		if err := svc.Put(r.Context(), row); err != nil {
			if writeCentralSecretRefusal(w, r, err) {
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central creation failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralCreate,
				Note:   "name=" + row.Name,
			})
		}
		JSON(w, http.StatusCreated, maskCentralRow(row))
	}
}

// UpdateCentral handles PUT /admin/centrals/{name}. Performs a
// full-replace upsert. Returns 204 on success.
func UpdateCentral(svc CentralAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		var row sqlite.CentralRow
		if err := DecodeJSON(r, &row); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		// URL path name takes precedence so the body name field cannot
		// accidentally create a different central.
		row.Name = name
		if err := hmtypes.ValidateCentralName(row.Name); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid name", err.Error()))
			return
		}
		if row.Host == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing host", "central host is required"))
			return
		}
		// The GET path masks password_plain to the sentinel; a save that
		// echoes the sentinel back means "unchanged" and must restore the
		// stored credential rather than overwrite it with the literal mask.
		if row.PasswordPlain == maskSentinel {
			existing, err := svc.Get(r.Context(), name)
			if err != nil && !errors.Is(err, sqlite.ErrCentralNotFound) {
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central lookup failed", err)
				return
			}
			row.PasswordPlain = existing.PasswordPlain
		}
		if err := svc.Put(r.Context(), row); err != nil {
			if writeCentralSecretRefusal(w, r, err) {
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central update failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralUpdate,
				Note:   "name=" + name,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteCentral handles DELETE /admin/centrals/{name}. Returns 404
// when the central is unknown.
func DeleteCentral(svc CentralAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		err := svc.Delete(r.Context(), name)
		if errors.Is(err, sqlite.ErrCentralNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", name))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central deletion failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralDelete,
				Note:   "name=" + name,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
