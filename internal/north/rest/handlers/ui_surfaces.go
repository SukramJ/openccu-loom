// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"cmp"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// SurfaceInfo describes one addressable Config-UI surface: what it is,
// what the shipped default says, and what the operator may do with it.
//
// The SPA needs all of it in one payload. A navigation gate could work
// from the effective map alone, but the editor has to explain itself —
// which default a row deviates from, why a row is locked, which switch
// would make an unavailable row available — and deriving that a second
// time on the client is how the two answers drift apart.
type SurfaceInfo struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	// Defaults maps profile name → shipped visibility.
	Defaults map[string]bool `json:"defaults"`
	// Floor is "", "always" or "standalone".
	Floor string `json:"floor,omitempty"`
	// Gate names a runtime capability the surface additionally needs
	// ("matter", "history"), or "" when it has none.
	Gate string `json:"gate,omitempty"`
	// Warn names the condition under which hiding asks first.
	Warn string `json:"warn,omitempty"`
	// WarnProfile limits Warn to one profile when set.
	WarnProfile string `json:"warn_profile,omitempty"`
	// Parent names the surface this one lives inside.
	Parent string `json:"parent,omitempty"`
	// RoleAdmin marks surfaces only admins ever see.
	RoleAdmin bool `json:"role_admin,omitempty"`
	// WriteGated marks surfaces whose embedded-profile entry also
	// decides whether the Ingress passthrough identity may write.
	WriteGated bool `json:"write_gated,omitempty"`
	// HAOwns marks surfaces Home Assistant provides itself.
	HAOwns bool `json:"ha_owns,omitempty"`
}

// SurfacesResponse is `GET /api/v1/ui/surfaces`.
type SurfacesResponse struct {
	// Embedded reports the master toggle.
	Embedded bool `json:"embedded"`
	// Profile names the live profile.
	Profile string `json:"profile"`
	// Profiles carries the stored, sparse overrides per profile.
	Profiles map[string]map[string]string `json:"profiles"`
	// Effective is the resolved visibility of the live profile.
	// Capability and role gates are NOT folded in — the client applies
	// those, and they answer a different question.
	Effective map[string]bool `json:"effective"`
	// Surfaces is the registry.
	Surfaces []SurfaceInfo `json:"surfaces"`
}

// SurfacesRequest is `PUT /api/v1/ui/surfaces`. Both fields are
// optional: the editor saves rows without touching the mode, and the
// master toggle flips the mode without resending every row.
type SurfacesRequest struct {
	Embedded *bool                        `json:"embedded,omitempty"`
	Profiles map[string]map[string]string `json:"profiles,omitempty"`
}

// GetUISurfaces serves the registry plus the resolved live profile.
func GetUISurfaces(svc ConfigAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ui, ok := effectiveUI(w, r, svc)
		if !ok {
			return
		}
		JSON(w, http.StatusOK, surfacesResponse(ui))
	}
}

// SurfacePolicyUpdater refreshes the live surface policy the write
// middleware consults. Without this the write boundary would only move
// on the next daemon start, and the editor would report a save that has
// not taken effect — the exact gap between "declared" and "in force"
// that makes a security-relevant setting untrustworthy.
type SurfacePolicyUpdater interface {
	Set(ui config.NorthUI)
}

// PutUISurfaces persists the master toggle and/or profile overrides.
func PutUISurfaces(svc ConfigAdminService, rec audit.Recorder, policy SurfacePolicyUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SurfacesRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid JSON", err.Error()))
			return
		}
		cur, ok := effectiveConfig(w, r, svc)
		if !ok {
			return
		}
		next := cloneConfig(cur)
		if req.Embedded != nil {
			v := *req.Embedded
			next.North.UI.Embedded = &v
		}
		if req.Profiles != nil {
			profiles, perr := normalizeProfiles(req.Profiles)
			if perr != nil {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Surface profile rejected", perr.Error()))
				return
			}
			next.North.UI.Profiles = profiles
		}
		next.ApplyDefaults()
		if err := next.Validate(); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Surface profile validation failed", err.Error()))
			return
		}
		merged, okMarshal, mErr := configstore.MarshalSection(configstore.SectionUI, next)
		if mErr != nil || !okMarshal {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Surface profile save failed", cmp.Or(mErr, errEffectiveConfigEmpty))
			return
		}
		updatedBy := identityFromCtx(r.Context())
		row, err := svc.PutSection(r.Context(), configstore.SectionUI, merged, updatedBy)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Surface profile save failed", err)
			return
		}
		// Move the live boundary in the same request that persisted it.
		// A save that only took effect on the next restart would leave
		// Home Assistant writing to a surface the operator just hid,
		// while the UI reported success.
		if policy != nil {
			policy.Set(next.North.UI)
		}
		// The profile decides an authorization outcome in the embedded
		// profile, so the audit row is load-bearing rather than tidy:
		// "who handed Home Assistant paramset writes back, and when" has
		// to stay answerable.
		if rec != nil {
			rec.Record(audit.Entry{
				Timestamp: row.UpdatedAt,
				User:      updatedBy,
				Action:    audit.ActionConfigSectionUpdate,
				Note:      "section=" + string(configstore.SectionUI) + " surfaces profile=" + next.North.UI.ActiveProfile(),
			})
		}
		JSON(w, http.StatusOK, surfacesResponse(next.North.UI))
	}
}

// normalizeProfiles validates the request's profiles and reduces them to
// the sparse stored form.
//
// Floor violations are REJECTED rather than normalised away: an operator
// who asked to hide the way back deserves to be told, and a client that
// sends one has a bug worth surfacing. Everything else — redundant
// entries, ids this binary does not know — is dropped silently, because
// both are legitimate states for a client to send (a stale form, a
// profile written by a newer release).
func normalizeProfiles(in map[string]map[string]string) (map[string]map[string]config.SurfaceState, error) {
	out := make(map[string]map[string]config.SurfaceState, len(in))
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name != config.ProfileStandalone && name != config.ProfileEmbedded {
			return nil, &surfaceError{msg: "unknown profile " + name}
		}
		typed := make(map[string]config.SurfaceState, len(in[name]))
		for id, state := range in[name] {
			typed[id] = config.SurfaceState(state)
			if typed[id] != config.SurfaceVisible && typed[id] != config.SurfaceHidden {
				return nil, &surfaceError{msg: "surface " + id + " in profile " + name +
					`: state must be "visible" or "hidden", got "` + state + `"`}
			}
		}
		if bad := surface.FloorViolations(name, typed); len(bad) > 0 {
			sort.Strings(bad)
			return nil, &surfaceError{msg: "these surfaces can never be hidden in profile " +
				name + ": " + joinComma(bad)}
		}
		normalized := surface.Normalize(name, typed)
		if len(normalized) > 0 {
			out[name] = normalized
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// surfaceError carries a validation message without pulling a sentinel
// into hmerr for a purely local, operator-facing string.
type surfaceError struct{ msg string }

func (e *surfaceError) Error() string { return e.msg }

// surfacesResponse renders the payload from a resolved NorthUI.
func surfacesResponse(ui config.NorthUI) SurfacesResponse {
	res := surface.Resolve(ui)
	out := SurfacesResponse{
		Embedded:  ui.IsEmbedded(),
		Profile:   res.Profile,
		Profiles:  map[string]map[string]string{},
		Effective: make(map[string]bool, len(res.Visible)),
		Surfaces:  make([]SurfaceInfo, 0, len(surface.Registry())),
	}
	for _, name := range []string{config.ProfileStandalone, config.ProfileEmbedded} {
		stored := ui.SurfaceOverrides(name)
		if len(stored) == 0 {
			continue
		}
		flat := make(map[string]string, len(stored))
		for id, state := range stored {
			flat[id] = string(state)
		}
		out.Profiles[name] = flat
	}
	for id, visible := range res.Visible {
		out.Effective[string(id)] = visible
	}
	reg := surface.Registry()
	for i := range reg {
		s := &reg[i]
		out.Surfaces = append(out.Surfaces, SurfaceInfo{
			ID:          string(s.ID),
			Group:       string(s.Group),
			Defaults:    s.Defaults,
			Floor:       string(s.Floor),
			Gate:        string(s.Gate),
			Warn:        string(s.Warn),
			WarnProfile: s.WarnProfile,
			Parent:      string(s.Parent),
			RoleAdmin:   s.RoleAdmin,
			WriteGated:  s.WriteGated,
			HAOwns:      s.HAOwns,
		})
	}
	return out
}

// effectiveConfig fetches the runtime config or writes the failure.
func effectiveConfig(w http.ResponseWriter, r *http.Request, svc ConfigAdminService) (*config.Config, bool) {
	if svc == nil {
		problem.Write(w, http.StatusServiceUnavailable,
			problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
		return nil, false
	}
	cur, err := svc.Effective(r.Context())
	if cur == nil || cur.Config == nil {
		err = cmp.Or(err, errEffectiveConfigEmpty)
	}
	if err != nil {
		writeServerError(w, r, http.StatusServiceUnavailable, problem.TypeServiceUnready,
			"Effective config unavailable", err)
		return nil, false
	}
	return cur.Config, true
}

func effectiveUI(w http.ResponseWriter, r *http.Request, svc ConfigAdminService) (config.NorthUI, bool) {
	cfg, ok := effectiveConfig(w, r, svc)
	if !ok {
		return config.NorthUI{}, false
	}
	return cfg.North.UI, true
}

// compile-time guard that the response stays JSON-encodable without a
// custom marshaller — the SPA's generated types depend on it.
var _ = json.Marshal
