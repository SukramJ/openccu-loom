// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import "sync"

var (
	defaultOnce     sync.Once
	defaultRegistry *Registry
)

// DefaultRegistry returns the process-wide profile registry populated
// by [RegisterProfiles]. Package consumers should fetch the
// registry through this accessor rather than constructing their own,
// unless they need an isolated instance (tests, alternate profile
// sets).
//
// First call lazily registers every generated profile; subsequent
// calls reuse the cached registry.
func DefaultRegistry() *Registry {
	defaultOnce.Do(func() {
		defaultRegistry = NewRegistry()
		RegisterProfiles(defaultRegistry)
		registerDefaultBlacklist(defaultRegistry)
	})
	return defaultRegistry
}

// registerDefaultBlacklist mirrors the per-module blacklist entries
// Emitted by g.
// `model/custom/climate.py:963 DeviceProfileRegistry.blacklist("HmIP-STHO")`).
// The generator does not emit blacklist calls today, so the list is
// kept here as a hand-curated extension. Closes gap
// (`channel.group_no` drift on VCU4523900 / VCU8255833 caused
// by openccu-loom still materialising a Climate custom DP for HmIP-STHO
// While
//
// When a future
// generator should pick it up; until then, append it below.
func registerDefaultBlacklist(r *Registry) {
	r.Blacklist(
		// Climate
		"HmIP-STHO",
	)
}
