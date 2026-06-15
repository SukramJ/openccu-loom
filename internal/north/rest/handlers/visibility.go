// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// VisibilityUnIgnoreStore is the narrow facade the REST handlers
// consult. Implemented by [*sqlite.VisibilityUnIgnoreStore]; tests
// substitute a fake.
type VisibilityUnIgnoreStore interface {
	List(ctx context.Context, centralName string) ([]sqlite.UnIgnoreEntry, error)
	Patterns(ctx context.Context, centralName string) ([]string, error)
	Replace(ctx context.Context, centralName string, patterns []string, updatedBy string) error
}

// VisibilityCentralLister returns the names of every registered central.
// Implemented by [*central.Registry]; tests inject a slice.
type VisibilityCentralLister interface {
	Names() []string
}

// VisibilityCandidateProvider returns the candidate set of hidden
// parameter names per (central, paramset). Implemented by a daemon-side
// adapter that wraps every central's [*central.QueryFacade].
type VisibilityCandidateProvider interface {
	UnIgnoreCandidates(centralName string, paramset hmenum.ParamsetKey) []string
}

// VisibilityRegistryLoader applies a fresh un-ignore pattern list to
// the live decider and re-runs the suppression marks on every device
// of the given central. Returns the number of devices touched.
type VisibilityRegistryLoader interface {
	LoadUnIgnore(centralName string, patterns []string) (affectedDevices int, parseErrors []string, err error)
}

// UnIgnoreEntryDTO is the JSON shape returned for one persisted row.
type UnIgnoreEntryDTO struct {
	Pattern   string `json:"pattern"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// UnIgnoreCentralPatternsDTO groups a central's full pattern list.
type UnIgnoreCentralPatternsDTO struct {
	CentralName string             `json:"central_name"`
	Patterns    []UnIgnoreEntryDTO `json:"patterns"`
}

// UnIgnoreListResponseDTO is the body of GET /api/v1/visibility/unignore.
type UnIgnoreListResponseDTO struct {
	Centrals []UnIgnoreCentralPatternsDTO `json:"centrals"`
}

// UnIgnoreUpdateRequestDTO is the body of PUT /api/v1/visibility/unignore.
type UnIgnoreUpdateRequestDTO struct {
	CentralName string   `json:"central_name"`
	Patterns    []string `json:"patterns"`
}

// UnIgnoreUpdateResponseDTO is the body returned by the PUT.
type UnIgnoreUpdateResponseDTO struct {
	AppliedCount    int                `json:"applied_count"`
	ParseErrors     []string           `json:"parse_errors,omitempty"`
	AffectedDevices int                `json:"affected_devices"`
	Patterns        []UnIgnoreEntryDTO `json:"patterns"`
}

// UnIgnoreCandidateListDTO is the body of
// GET /api/v1/visibility/unignore/candidates.
type UnIgnoreCandidateListDTO struct {
	Candidates    []string `json:"candidates"`
	IncludeMaster bool     `json:"include_master"`
}

// ListVisibilityUnIgnore returns the currently-active un-ignore patterns
// for every central. Wires GET /api/v1/visibility/unignore.
func ListVisibilityUnIgnore(centrals VisibilityCentralLister, store VisibilityUnIgnoreStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || centrals == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Visibility unavailable", "visibility store not wired"))
			return
		}
		resp := UnIgnoreListResponseDTO{Centrals: []UnIgnoreCentralPatternsDTO{}}
		for _, name := range centrals.Names() {
			entries, err := store.List(r.Context(), name)
			if err != nil {
				problem.Write(w, http.StatusInternalServerError, problem.New(problem.TypeInternal, r, "Visibility error", "list un-ignore: "+err.Error()))
				return
			}
			rows := make([]UnIgnoreEntryDTO, 0, len(entries))
			for _, e := range entries {
				ts := ""
				if !e.UpdatedAt.IsZero() {
					ts = e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
				}
				rows = append(rows, UnIgnoreEntryDTO{
					Pattern:   e.Pattern,
					UpdatedAt: ts,
					UpdatedBy: e.UpdatedBy,
				})
			}
			resp.Centrals = append(resp.Centrals, UnIgnoreCentralPatternsDTO{
				CentralName: name,
				Patterns:    rows,
			})
		}
		JSON(w, http.StatusOK, resp)
	}
}

// UpdateVisibilityUnIgnore replaces the un-ignore list for one central.
// Patterns are pre-validated via visibility.ParseUnIgnoreLine; entries
// that fail parsing surface in `parse_errors` but the well-formed subset
// still applies. Audit-logs the (added / removed) diff. Wires
// PUT /api/v1/visibility/unignore.
func UpdateVisibilityUnIgnore( //nolint:funlen // single-purpose visibility update handler with many validation/diff branches
	store VisibilityUnIgnoreStore,
	loader VisibilityRegistryLoader,
	auditRec audit.Recorder,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || loader == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Visibility unavailable", "visibility store not wired"))
			return
		}
		var req UnIgnoreUpdateRequestDTO
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r, "Bad request", "decode request body: "+err.Error()))
			return
		}
		req.CentralName = strings.TrimSpace(req.CentralName)
		if req.CentralName == "" {
			problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeBadRequest, r, "Bad request", "central_name is required"))
			return
		}

		// Pre-validate every line; collect parse errors but proceed with
		// the well-formed subset so one typo does not block the whole save.
		valid := make([]string, 0, len(req.Patterns))
		var parseErrors []string
		seen := make(map[string]struct{}, len(req.Patterns))
		for _, raw := range req.Patterns {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			parsed := visibility.ParseUnIgnoreLine(line)
			if parsed.Entry == nil || parsed.Err != "" {
				msg := parsed.Err
				if msg == "" {
					msg = "no parameter parsed"
				}
				parseErrors = append(parseErrors, fmt.Sprintf("invalid pattern %q: %s", raw, msg))
				continue
			}
			if _, dup := seen[line]; dup {
				continue
			}
			seen[line] = struct{}{}
			valid = append(valid, line)
		}
		sort.Strings(valid)

		// Compute diff vs. current persisted state for the audit entry.
		before, err := store.Patterns(r.Context(), req.CentralName)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError, problem.New(problem.TypeInternal, r, "Visibility error", "read current un-ignore: "+err.Error()))
			return
		}
		added, removed := diffPatterns(before, valid)

		user := identitySubject(r.Context())
		if err := store.Replace(r.Context(), req.CentralName, valid, user); err != nil {
			problem.Write(w, http.StatusInternalServerError, problem.New(problem.TypeInternal, r, "Visibility error", "persist un-ignore: "+err.Error()))
			return
		}

		affected, loaderErrs, err := loader.LoadUnIgnore(req.CentralName, valid)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError, problem.New(problem.TypeInternal, r, "Visibility error", "apply un-ignore: "+err.Error()))
			return
		}
		parseErrors = append(parseErrors, loaderErrs...)

		// Audit log — per docs/ui/unignore-concept.md (resolved Q3).
		// Only emit when at least one of (added, removed) is non-empty
		// so a no-op save does not pollute the log.
		if auditRec != nil && (len(added) > 0 || len(removed) > 0) {
			changes := make([]audit.Change, 0, len(added)+len(removed))
			for _, p := range added {
				changes = append(changes, audit.Change{Parameter: p, Before: nil, After: "active"})
			}
			for _, p := range removed {
				changes = append(changes, audit.Change{Parameter: p, Before: "active", After: nil})
			}
			auditRec.Record(audit.Entry{
				User:    user,
				Action:  audit.ActionUnIgnoreUpdate,
				Note:    fmt.Sprintf("central=%s added=%d removed=%d affected_devices=%d", req.CentralName, len(added), len(removed), affected),
				Changes: changes,
			})
		}

		// Read back so the response surfaces updated_at + updated_by.
		entries, err := store.List(r.Context(), req.CentralName)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError, problem.New(problem.TypeInternal, r, "Visibility error", "read-back un-ignore: "+err.Error()))
			return
		}
		respPatterns := make([]UnIgnoreEntryDTO, 0, len(entries))
		for _, e := range entries {
			ts := ""
			if !e.UpdatedAt.IsZero() {
				ts = e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			respPatterns = append(respPatterns, UnIgnoreEntryDTO{
				Pattern:   e.Pattern,
				UpdatedAt: ts,
				UpdatedBy: e.UpdatedBy,
			})
		}
		JSON(w, http.StatusOK, UnIgnoreUpdateResponseDTO{
			AppliedCount:    len(valid),
			ParseErrors:     parseErrors,
			AffectedDevices: affected,
			Patterns:        respPatterns,
		})
	}
}

// ListVisibilityUnIgnoreCandidates returns the per-central candidate
// list — parameter names currently hidden but eligible for un-ignore.
// Wires GET /api/v1/visibility/unignore/candidates.
func ListVisibilityUnIgnoreCandidates(
	centrals VisibilityCentralLister,
	provider VisibilityCandidateProvider,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if centrals == nil || provider == nil {
			problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Visibility unavailable", "candidate provider not wired"))
			return
		}
		includeMaster := strings.EqualFold(r.URL.Query().Get("include_master"), "true")
		seen := make(map[string]struct{})
		for _, name := range centrals.Names() {
			for _, p := range provider.UnIgnoreCandidates(name, hmenum.ParamsetKeyValues) {
				seen[p] = struct{}{}
			}
			if includeMaster {
				for _, p := range provider.UnIgnoreCandidates(name, hmenum.ParamsetKeyMaster) {
					seen[p] = struct{}{}
				}
			}
		}
		out := make([]string, 0, len(seen))
		for p := range seen {
			out = append(out, p)
		}
		sort.Strings(out)
		JSON(w, http.StatusOK, UnIgnoreCandidateListDTO{
			Candidates:    out,
			IncludeMaster: includeMaster,
		})
	}
}

// diffPatterns returns (added, removed) — pattern strings present in
// after-but-not-before and before-but-not-after respectively. Both
// lists come out alphabetically sorted.
func diffPatterns(before, after []string) (added, removed []string) {
	beforeSet := make(map[string]struct{}, len(before))
	for _, p := range before {
		beforeSet[p] = struct{}{}
	}
	afterSet := make(map[string]struct{}, len(after))
	for _, p := range after {
		afterSet[p] = struct{}{}
	}
	for p := range afterSet {
		if _, ok := beforeSet[p]; !ok {
			added = append(added, p)
		}
	}
	for p := range beforeSet {
		if _, ok := afterSet[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// identitySubject pulls the authenticated principal's subject from
// ctx, or "" when no identity is attached (test paths / unauth).
func identitySubject(ctx context.Context) string {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return ""
	}
	return id.Subject
}
