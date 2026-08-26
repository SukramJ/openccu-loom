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

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// UserPreferencesService is the per-user preference store surface the
// SPA needs. *sqlite.UserPreferencesStore satisfies it. Values are
// opaque JSON owned by the SPA (e.g. the "favorites" pinned-items list).
type UserPreferencesService interface {
	Get(ctx context.Context, subject, key string) (string, error)
	Set(ctx context.Context, subject, key, valueJSON string) error
	Delete(ctx context.Context, subject, key string) error
}

// maxPreferenceBytes caps a single preference blob so a user cannot
// stuff arbitrary amounts of data into the store.
const maxPreferenceBytes = 256 * 1024

// preferenceResponse wraps the raw JSON value so the SPA gets a stable
// envelope. Value is sent as a raw JSON message (not a re-encoded
// string), preserving whatever shape the SPA stored.
type preferenceResponse struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func preferenceSubject(w http.ResponseWriter, r *http.Request) (string, bool) {
	ident, ok := auth.IdentityFrom(r.Context())
	if !ok || ident.Subject == "" {
		problem.Write(w, http.StatusUnauthorized,
			problem.New(problem.TypeUnauthorized, r, "Not authenticated", ""))
		return "", false
	}
	return ident.Subject, true
}

// GetPreference returns the caller's stored value for {key}.
//
// An unset key is not an error: the store is a key-value surface whose
// keys the SPA invents, so "nothing stored yet" is the state every key
// starts in. It answers 200 with a null value, which is what a fresh
// session reads for every preference it asks about — favorites and
// start_route on the very first page load. Reporting that as 404 put a
// warn-level line in the log for ordinary use.
func GetPreference(svc UserPreferencesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "preferences unwired", ""))
			return
		}
		subject, ok := preferenceSubject(w, r)
		if !ok {
			return
		}
		key := chi.URLParam(r, "key")
		value, err := svc.Get(r.Context(), subject, key)
		if errors.Is(err, sqlite.ErrPreferenceNotFound) {
			JSON(w, http.StatusOK, preferenceResponse{Key: key, Value: json.RawMessage("null")})
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Preference read failed", err)
			return
		}
		JSON(w, http.StatusOK, preferenceResponse{Key: key, Value: json.RawMessage(value)})
	}
}

// PutPreference stores the request body verbatim as the caller's value
// for {key}. The body must be valid JSON and within the size cap.
func PutPreference(svc UserPreferencesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "preferences unwired", ""))
			return
		}
		subject, ok := preferenceSubject(w, r)
		if !ok {
			return
		}
		key := chi.URLParam(r, "key")
		body, err := io.ReadAll(io.LimitReader(r.Body, maxPreferenceBytes+1))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Read failed", err.Error()))
			return
		}
		if len(body) > maxPreferenceBytes {
			problem.Write(w, http.StatusRequestEntityTooLarge,
				problem.New(problem.TypeValidation, r, "Preference too large", ""))
			return
		}
		if !json.Valid(body) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Body must be valid JSON", ""))
			return
		}
		if err := svc.Set(r.Context(), subject, key, string(body)); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Preference write failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeletePreference removes the caller's value for {key}. Deleting a
// missing key is a no-op success.
func DeletePreference(svc UserPreferencesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "preferences unwired", ""))
			return
		}
		subject, ok := preferenceSubject(w, r)
		if !ok {
			return
		}
		key := chi.URLParam(r, "key")
		if err := svc.Delete(r.Context(), subject, key); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Preference delete failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
