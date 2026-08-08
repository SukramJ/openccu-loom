// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import "github.com/SukramJ/openccu-loom/internal/config"

// Resolution is the outcome of applying a configuration to the
// registry: which profile is live and what it resolves to.
//
// loom:reachable:reason="returned by Resolve and held by Policy, which the write middleware consults on every request; a data struct the analyzer's type heuristic cannot see used"
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
	// Fleet carries the runtime facts that moved a shipped default.
	Fleet Fleet
}

// Fleet describes how much of this daemon Home Assistant can possibly
// own. It is the one runtime input a shipped default depends on.
//
// loom:reachable:reason="a field of Resolution, filled by the composition root's central counter and read by the REST payload; a method-less struct the analyzer's type heuristic cannot see used"
type Fleet struct {
	// Centrals is the number of CCUs this daemon serves. Zero means
	// "unknown" — during boot, or in a caller that has no registry — and
	// is treated as the single-CCU case, which is the shipped default.
	Centrals int
}

// MultiCentral reports whether the daemon serves more than one CCU.
func (f Fleet) MultiCentral() bool { return f.Centrals > 1 }

// Resolve applies ui's active profile to the registry, for a daemon
// serving a single CCU. Callers that know the fleet size use
// [ResolveFleet]; this shorthand keeps the many call sites that do not
// care (tests, previews of a single-CCU setup) readable.
func Resolve(ui config.NorthUI) Resolution {
	return ResolveFleet(ui, Fleet{})
}

// ResolveFleet applies ui's active profile with the fleet size that
// decides two shipped defaults — see [Surface.MultiCentralVisible].
func ResolveFleet(ui config.NorthUI, fleet Fleet) Resolution {
	return resolveProfile(ui, ui.ActiveProfile(), fleet)
}

// ResolveProfile applies the named profile, which need not be the live
// one — the editor previews the inactive profile with the same code
// path that serves the live one, so the preview cannot drift from the
// behaviour it promises.
func ResolveProfile(ui config.NorthUI, profile string) Resolution {
	return resolveProfile(ui, profile, Fleet{})
}

// ResolveProfileFleet is [ResolveProfile] with an explicit fleet size,
// so the editor's preview of the inactive profile shows the same
// defaults the daemon would apply if that profile went live.
func ResolveProfileFleet(ui config.NorthUI, profile string, fleet Fleet) Resolution {
	return resolveProfile(ui, profile, fleet)
}

func resolveProfile(ui config.NorthUI, profile string, fleet Fleet) Resolution {
	overrides := ui.SurfaceOverrides(profile)
	res := Resolution{
		Profile: profile,
		Visible: make(map[ID]bool, len(registry)),
		Fleet:   fleet,
	}
	for i := range registry {
		s := &registry[i]
		visible := s.DefaultForFleet(profile, fleet)
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
func Normalize(profile string, overrides map[string]config.SurfaceState, fleet Fleet) map[string]config.SurfaceState {
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
		if want == s.DefaultForFleet(profile, fleet) {
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
