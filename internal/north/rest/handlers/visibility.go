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

// VisibilityCandidateGroupProvider is the grouped counterpart to
// [VisibilityCandidateProvider]: it returns one entry per (parameter,
// paramset) with the affected models, channels and the rule that hid
// it, instead of the cross-product of pattern strings.
//
// Handlers probe for it by type-assertion on the candidate provider, so
// a provider that only implements the flat surface still serves the
// legacy `candidates` field.
type VisibilityCandidateGroupProvider interface {
	UnIgnoreCandidateGroups(centralName string, paramsets []hmenum.ParamsetKey) []visibility.CandidateGroup
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
//
// `candidates` is the flat pattern list and honours `include_master`;
// `groups` always carries both paramsets, each group tagged with its
// own, so the picker can offer a paramset filter without a second
// round-trip. `reasons` enumerates every category the groups can carry
// so the UI builds its filter chips from the server's vocabulary
// instead of a hard-coded copy.
type UnIgnoreCandidateListDTO struct {
	Candidates    []string                    `json:"candidates"`
	IncludeMaster bool                        `json:"include_master"`
	Groups        []UnIgnoreCandidateGroupDTO `json:"groups"`
	Reasons       []string                    `json:"reasons"`
}

// UnIgnoreCandidateGroupDTO is one hidden parameter with every scope it
// occurs in.
type UnIgnoreCandidateGroupDTO struct {
	Parameter string `json:"parameter"`
	// Label is the localised parameter name from the CCU translations,
	// empty when the catalogue has no entry.
	Label    string `json:"label,omitempty"`
	Paramset string `json:"paramset"`
	// Reason is the primary category; Reasons lists every rule that
	// matched anywhere in the fleet.
	Reason  string   `json:"reason"`
	Reasons []string `json:"reasons"`
	// SimplePattern re-enables the parameter fleet-wide. Empty for
	// MASTER, which has no short pattern form.
	SimplePattern string                      `json:"simple_pattern,omitempty"`
	Models        []UnIgnoreCandidateModelDTO `json:"models"`
	DeviceCount   int                         `json:"device_count"`
	ChannelCount  int                         `json:"channel_count"`
}

// UnIgnoreCandidateModelDTO is one device model within a candidate
// group, with the channels the parameter occurs on.
type UnIgnoreCandidateModelDTO struct {
	Model string `json:"model"`
	// WildcardPattern covers every channel of the model. Empty for
	// MASTER.
	WildcardPattern string                        `json:"wildcard_pattern,omitempty"`
	Channels        []UnIgnoreCandidateChannelDTO `json:"channels"`
	DeviceCount     int                           `json:"device_count"`
}

// UnIgnoreCandidateChannelDTO is one channel number and the pattern
// that re-enables the parameter exactly there.
type UnIgnoreCandidateChannelDTO struct {
	Channel int    `json:"channel"`
	Pattern string `json:"pattern"`
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
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Visibility error: list un-ignore", err)
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
			problem.Write(w, DecodeJSONStatus(err), problem.New(problem.TypeBadRequest, r, "Bad request", "decode request body: "+err.Error()))
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
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Visibility error: read current un-ignore", err)
			return
		}
		added, removed := diffPatterns(before, valid)

		user := identitySubject(r.Context())
		if err := store.Replace(r.Context(), req.CentralName, valid, user); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Visibility error: persist un-ignore", err)
			return
		}

		affected, loaderErrs, err := loader.LoadUnIgnore(req.CentralName, valid)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Visibility error: apply un-ignore", err)
			return
		}
		parseErrors = append(parseErrors, loaderErrs...)

		// Audit log — per notes/concepts/ui/unignore-concept.md (resolved Q3).
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
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Visibility error: read-back un-ignore", err)
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
	labels ParameterLabeler,
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
			Groups:        candidateGroups(centrals, provider, labels),
			Reasons:       hiddenReasonNames(),
		})
	}
}

// candidateGroups merges the grouped candidate view across every
// central. Returns an empty slice when the provider does not implement
// the grouped surface, which keeps the flat `candidates` field working
// on its own.
//
// Both paramsets are always collected: after grouping, MASTER adds ~10
// entries rather than the ~940 pattern strings of the flat form, so
// gating it behind `include_master` would buy nothing and cost the UI a
// second round-trip to offer a paramset filter.
func candidateGroups(
	centrals VisibilityCentralLister,
	provider VisibilityCandidateProvider,
	labels ParameterLabeler,
) []UnIgnoreCandidateGroupDTO {
	grouped, ok := provider.(VisibilityCandidateGroupProvider)
	if !ok {
		return []UnIgnoreCandidateGroupDTO{}
	}
	paramsets := []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster}
	// Several centrals can carry the same parameter on the same model;
	// merge on (parameter, paramset) so the operator sees one row and
	// the device counts add up across the fleet.
	merged := make(map[string]*visibility.CandidateGroup)
	order := make([]string, 0, 64)
	for _, name := range centrals.Names() {
		fromCentral := grouped.UnIgnoreCandidateGroups(name, paramsets)
		for i := range fromCentral {
			g := &fromCentral[i]
			key := g.Parameter + "\x00" + string(g.Paramset)
			existing, seen := merged[key]
			if !seen {
				clone := *g
				merged[key] = &clone
				order = append(order, key)
				continue
			}
			mergeCandidateGroup(existing, *g)
		}
	}
	sort.Strings(order)
	out := make([]UnIgnoreCandidateGroupDTO, 0, len(order))
	for _, key := range order {
		out = append(out, candidateGroupDTO(*merged[key], labels))
	}
	return out
}

// mergeCandidateGroup folds src into dst: reasons union, model scopes
// merged by model name, channel lists deduplicated.
func mergeCandidateGroup(dst *visibility.CandidateGroup, src visibility.CandidateGroup) {
	dst.Reasons = visibility.MergeReasons(dst.Reasons, src.Reasons)
	if len(dst.Reasons) > 0 {
		dst.Reason = dst.Reasons[0]
	}
	dst.Devices += src.Devices
	byModel := make(map[string]int, len(dst.Models))
	for i, m := range dst.Models {
		byModel[m.Model] = i
	}
	for _, m := range src.Models {
		idx, ok := byModel[m.Model]
		if !ok {
			dst.Models = append(dst.Models, m)
			byModel[m.Model] = len(dst.Models) - 1
			dst.Channels += len(m.Channels)
			continue
		}
		target := &dst.Models[idx]
		target.Devices += m.Devices
		known := make(map[int]struct{}, len(target.Channels))
		for _, ch := range target.Channels {
			known[ch] = struct{}{}
		}
		for _, ch := range m.Channels {
			if _, dup := known[ch]; dup {
				continue
			}
			target.Channels = append(target.Channels, ch)
			if target.ChannelPatterns == nil {
				target.ChannelPatterns = make(map[int]string, len(m.Channels))
			}
			target.ChannelPatterns[ch] = m.ChannelPatterns[ch]
			dst.Channels++
		}
		sort.Ints(target.Channels)
	}
	sort.Slice(dst.Models, func(i, j int) bool { return dst.Models[i].Model < dst.Models[j].Model })
}

// candidateGroupDTO converts one domain group into its wire shape and
// resolves the localised parameter label.
func candidateGroupDTO(g visibility.CandidateGroup, labels ParameterLabeler) UnIgnoreCandidateGroupDTO {
	dto := UnIgnoreCandidateGroupDTO{
		Parameter:     g.Parameter,
		Paramset:      string(g.Paramset),
		Reason:        string(g.Reason),
		Reasons:       make([]string, 0, len(g.Reasons)),
		SimplePattern: g.SimplePattern,
		Models:        make([]UnIgnoreCandidateModelDTO, 0, len(g.Models)),
		DeviceCount:   g.Devices,
		ChannelCount:  g.Channels,
	}
	if labels != nil {
		dto.Label = labels.ParameterLabel(g.Parameter)
	}
	for _, r := range g.Reasons {
		dto.Reasons = append(dto.Reasons, string(r))
	}
	for _, m := range g.Models {
		model := UnIgnoreCandidateModelDTO{
			Model:           m.Model,
			WildcardPattern: m.WildcardPattern,
			DeviceCount:     m.Devices,
			Channels:        make([]UnIgnoreCandidateChannelDTO, 0, len(m.Channels)),
		}
		for _, ch := range m.Channels {
			model.Channels = append(model.Channels, UnIgnoreCandidateChannelDTO{
				Channel: ch,
				Pattern: m.ChannelPatterns[ch],
			})
		}
		dto.Models = append(dto.Models, model)
	}
	return dto
}

// hiddenReasonNames returns the reason vocabulary as wire strings.
func hiddenReasonNames() []string {
	reasons := visibility.AllHiddenReasons()
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, string(r))
	}
	return out
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
