// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ResetConfigField reverts a single config field to its default by
// removing just that leaf from the owning section's persisted JSON
// (pruning now-empty parent objects, and dropping the whole section row
// when nothing is left). The overlay then re-fills the field from the
// built-in default, so its source flips back to "default" while the
// rest of the section keeps its overrides. This is the per-field
// counterpart to DeleteConfigSection's whole-section reset.
func ResetConfigField(svc ConfigAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
			return
		}
		path := strings.TrimSpace(chi.URLParam(r, "path"))
		section := owningSection(path)
		if section == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Unknown config field", path))
			return
		}

		row, err := svc.GetSection(r.Context(), section)
		if errors.Is(err, sqlite.ErrSectionNotFound) {
			// No override stored → already at default. Idempotent success.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Section read failed", err.Error()))
			return
		}

		rel := strings.TrimPrefix(strings.TrimPrefix(path, string(section)), ".")
		actor := identityFromCtx(r.Context())

		// path == section → whole-section reset (same as DELETE section).
		if rel == "" {
			if derr := svc.DeleteSection(r.Context(), section); derr != nil && !errors.Is(derr, sqlite.ErrSectionNotFound) {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "Section delete failed", derr.Error()))
				return
			}
			recordFieldReset(rec, actor, path)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var obj map[string]any
		if len(row.ValueJSON) > 0 {
			if uerr := json.Unmarshal(row.ValueJSON, &obj); uerr != nil {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "Stored section is not an object", uerr.Error()))
				return
			}
		}
		if obj == nil || !deleteLeaf(obj, strings.Split(rel, ".")) {
			// Field was not present in the override → already default.
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if len(obj) == 0 {
			if derr := svc.DeleteSection(r.Context(), section); derr != nil && !errors.Is(derr, sqlite.ErrSectionNotFound) {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "Section delete failed", derr.Error()))
				return
			}
		} else {
			next, merr := json.Marshal(obj)
			if merr != nil {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "Section encode failed", merr.Error()))
				return
			}
			if _, perr := svc.PutSection(r.Context(), section, next, actor); perr != nil {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, r, "Section write failed", perr.Error()))
				return
			}
		}
		recordFieldReset(rec, actor, path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func recordFieldReset(rec audit.Recorder, actor, path string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   actor,
		Action: audit.ActionConfigSectionUpdate,
		Note:   "field reset: " + path,
	})
}

// owningSection returns the longest known section that is a prefix of
// the dotted field path (so "north.rest.auth.oidc.client_secret" maps to
// the "north.rest.auth.oidc" section rather than "north.rest"). Empty
// when no section owns the path.
func owningSection(path string) configstore.Section {
	if path == "" {
		return ""
	}
	var best configstore.Section
	for _, s := range configstore.AllSections() {
		ss := string(s)
		if path == ss || strings.HasPrefix(path, ss+".") {
			if len(ss) > len(string(best)) {
				best = s
			}
		}
	}
	return best
}

// deleteLeaf removes the leaf reached by parts from a decoded JSON
// object, pruning parent objects left empty by the deletion. Returns
// true when a value was actually removed.
func deleteLeaf(obj map[string]any, parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		if _, ok := obj[parts[0]]; !ok {
			return false
		}
		delete(obj, parts[0])
		return true
	}
	child, ok := obj[parts[0]].(map[string]any)
	if !ok {
		return false
	}
	deleted := deleteLeaf(child, parts[1:])
	if deleted && len(child) == 0 {
		delete(obj, parts[0])
	}
	return deleted
}
