// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"sort"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// RequiredParameters aggregates every Parameter referenced anywhere in
// the generated catalogs (DefaultDataPoints, all ProfileConfigs) and in
// Extended configs of all registered profiles.
//
// The resulting slice acts as a whitelist in the Visibility-Decider:
// a parameter that appears in IGNORED_PARAMETERS but also here must NOT
// be filtered out.
//
// Sources (in order):
//  1. DefaultDataPoints — all channel-offset entries.
//  2. ProfileConfigs — for every *ProfileConfig in ProfileConfigs:
//     - ChannelGroup.Fields (all FieldValues → Parameter)
//     - ChannelGroup.ChannelFields (all sub-maps)
//     - ChannelGroup.FixedChannelFields
//     - AdditionalDataPoints
//  3. AllExtendedConfigs — all registered profiles whose Extended != nil
//     → Extended.RequiredParameters().
//
// Output: deduplicated + alphabetically sorted.
func (r *Registry) RequiredParameters() []hmenum.Parameter {
	seen := make(map[hmenum.Parameter]struct{})

	// 1. DefaultDataPoints.
	for _, params := range DefaultDataPoints {
		for _, p := range params {
			seen[p] = struct{}{}
		}
	}

	// 2. ProfileConfigs (the global generated catalog).
	for _, pc := range ProfileConfigs {
		if pc == nil {
			continue
		}
		collectFromProfileConfig(pc, seen)
	}

	// 3. Extended configs from registered profiles.
	r.mu.RLock()
	for _, profile := range r.items {
		if profile.Extended == nil {
			continue
		}
		for _, p := range profile.Extended.RequiredParameters() {
			seen[p] = struct{}{}
		}
	}
	r.mu.RUnlock()

	return sortedParameters(seen)
}

// RequiredParameters returns the parameters required by this Profile
// (its ProfileConfig + its Extended config). The Materializer uses
// this per-device to compute the per-device required-parameter set, which
// is a subset of the global Registry.RequiredParameters().
//
// A Profile with no Config and no Extended returns an empty (non-nil) slice.
func (p Profile) RequiredParameters() []hmenum.Parameter {
	seen := make(map[hmenum.Parameter]struct{})

	if p.Config != nil {
		collectFromProfileConfig(p.Config, seen)
	}
	for _, param := range p.Extended.RequiredParameters() {
		seen[param] = struct{}{}
	}

	return sortedParameters(seen)
}

// collectFromProfileConfig adds all parameters referenced by pc into seen.
func collectFromProfileConfig(pc *ProfileConfig, seen map[hmenum.Parameter]struct{}) {
	// ChannelGroup.Fields
	for _, fv := range pc.ChannelGroup.Fields {
		seen[fv.Parameter] = struct{}{}
	}
	// ChannelGroup.ChannelFields
	for _, fieldMap := range pc.ChannelGroup.ChannelFields {
		for _, fv := range fieldMap {
			seen[fv.Parameter] = struct{}{}
		}
	}
	// ChannelGroup.FixedChannelFields
	for _, fieldMap := range pc.ChannelGroup.FixedChannelFields {
		for _, fv := range fieldMap {
			seen[fv.Parameter] = struct{}{}
		}
	}
	// AdditionalDataPoints
	for _, params := range pc.AdditionalDataPoints {
		for _, param := range params {
			seen[param] = struct{}{}
		}
	}
}

// sortedParameters converts a parameter set into a sorted slice.
func sortedParameters(seen map[hmenum.Parameter]struct{}) []hmenum.Parameter {
	if len(seen) == 0 {
		return []hmenum.Parameter{}
	}
	out := make([]hmenum.Parameter, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
