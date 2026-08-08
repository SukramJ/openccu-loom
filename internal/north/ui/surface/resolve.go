// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import "github.com/SukramJ/openccu-loom/internal/config"

// Resolution is the outcome of applying a configuration to the
// registry: which profile is live and what it resolves to.
type Resolution struct {
	// Profile names the live profile.
	Profile string
	// Visible maps every registered surface to its resolved visibility.
	// Runtime capability gates (Matter, history) and role gates are NOT
	// folded in here — they are the client's to apply, and they answer a
	// different question. Resolution says what the operator configured;
	// the gates say what the daemon can currently serve.
	Visible map[ID]bool
	// Ignored lists stored ids the registry does not know. A profile
	// written by a newer release must not break a downgrade, so unknown
	// ids are reported rather than rejected.
	Ignored []string
	// Refused lists stored ids whose override was dropped because the
	// surface is floor in this profile. The server is the authority on
	// the floor: a hand-edited YAML must not be able to hide a surface
	// the API refuses to hide.
	Refused []ID
}

// Resolve applies ui's active profile to the registry.
func Resolve(ui config.NorthUI) Resolution {
	return ResolveProfile(ui, ui.ActiveProfile())
}

// ResolveProfile applies the named profile, which need not be the live
// one — the editor previews the inactive profile with the same code
// path that serves the live one, so the preview cannot drift from the
// behaviour it promises.
func ResolveProfile(ui config.NorthUI, profile string) Resolution {
	overrides := ui.SurfaceOverrides(profile)
	res := Resolution{
		Profile: profile,
		Visible: make(map[ID]bool, len(registry)),
	}
	for i := range registry {
		s := &registry[i]
		visible := s.DefaultFor(profile)
		if state, ok := overrides[string(s.ID)]; ok {
			want := state == config.SurfaceVisible
			if !want && s.IsFloor(profile) {
				res.Refused = append(res.Refused, s.ID)
			} else {
				visible = want
			}
		}
		// A child is never more visible than its parent. The registry
		// lists parents before children, so one pass suffices.
		if visible && s.Parent != "" && !res.Visible[s.Parent] {
			visible = false
		}
		res.Visible[s.ID] = visible
	}
	for id := range overrides {
		if _, known := byID[ID(id)]; !known {
			res.Ignored = append(res.Ignored, id)
		}
	}
	return res
}

// IsVisible reports the resolved visibility of id. An unknown id reads
// as visible: a surface the registry does not know cannot have been
// configured away, and hiding it would make a future view invisible on
// an older binary for no reason.
func (r Resolution) IsVisible(id ID) bool {
	v, ok := r.Visible[id]
	if !ok {
		return true
	}
	return v
}

// HiddenIDs lists the resolved-hidden surfaces in registry order.
func (r Resolution) HiddenIDs() []ID {
	var out []ID
	for i := range registry {
		s := &registry[i]
		if !r.IsVisible(s.ID) {
			out = append(out, s.ID)
		}
	}
	return out
}

// Normalize drops entries a profile must not carry, returning the
// sparse form the daemon persists: overrides equal to the shipped
// default are removed, floor violations and unknown ids are dropped.
//
// Storing a redundant entry is not merely untidy — it pins today's
// default forever. An operator who "confirms" the shipped visible state
// of a view would keep it visible even after a later release decides it
// belongs to Home Assistant.
func Normalize(profile string, overrides map[string]config.SurfaceState) map[string]config.SurfaceState {
	out := make(map[string]config.SurfaceState, len(overrides))
	for id, state := range overrides {
		s, known := byID[ID(id)]
		if !known {
			continue
		}
		want := state == config.SurfaceVisible
		if !want && s.IsFloor(profile) {
			continue
		}
		if want == s.DefaultFor(profile) {
			continue
		}
		out[id] = state
	}
	return out
}

// FloorViolations lists the ids in overrides that try to hide a floor
// surface of the named profile. The write handler rejects rather than
// silently normalising these: an operator who asked for something
// impossible deserves to be told, and a client that sends one has a bug.
func FloorViolations(profile string, overrides map[string]config.SurfaceState) []string {
	var out []string
	for id, state := range overrides {
		s, known := byID[ID(id)]
		if !known {
			continue
		}
		if state == config.SurfaceHidden && s.IsFloor(profile) {
			out = append(out, id)
		}
	}
	return out
}

// UnknownIDs lists ids in overrides the registry does not know.
func UnknownIDs(overrides map[string]config.SurfaceState) []string {
	var out []string
	for id := range overrides {
		if _, known := byID[ID(id)]; !known {
			out = append(out, id)
		}
	}
	return out
}
