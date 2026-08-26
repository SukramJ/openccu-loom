// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// ignoreCacheKey is the composite key used to memoise
// [ParameterDecider.IsParameterIgnored] lookups.
//
// Using a typed struct as the map key (instead of a concatenated string)
// eliminates separator-ambiguity (model names that contain '|') and avoids
// a heap allocation per lookup after warm-up.
//
// central carries the querying central's name so a verdict computed for one
// CCU's un-ignore rules is never served back to a different CCU sharing the
// same decider instance (multi-CCU is first class, ADR 0002).
//
// Mirrors the Python reference implementation's IgnoreCacheKey.
type ignoreCacheKey struct {
	central     string
	model       string
	channelType string
	channelNo   int
	paramsetKey hmenum.ParamsetKey
	parameter   hmenum.Parameter
}

// IgnoreCacheKey is the exported variant of [ignoreCacheKey] for consumers
// that need to inspect or replicate memoisation key contents (e.g. diagnostic
// endpoints).
type IgnoreCacheKey struct {
	Central     string
	Model       string
	ChannelType string
	ChannelNo   int
	ParamsetKey hmenum.ParamsetKey
	Parameter   hmenum.Parameter
}

// UnIgnoreCacheKey is the composite key an un-ignore lookup memoises on, for
// consumers that need to inspect or replicate the memoisation (e.g. a
// diagnostics endpoint).
//
// Central is part of the key because an un-ignore rule is scoped to one CCU: a
// key without it answers a question about the fleet that was only ever asked
// about one central, and hands one CCU's re-enable decision to every other.
// CustomOnly distinguishes a lookup that considers only operator-provided
// rules from one that includes the built-in device rules.
type UnIgnoreCacheKey struct {
	Central     string
	Model       string
	ChannelType string
	ParamsetKey hmenum.ParamsetKey
	Parameter   hmenum.Parameter
	CustomOnly  bool
}

// ParsedUnIgnoreRules is the container returned by [ParseUnIgnore]. It wraps
// the parsed entries and carries a count of lines that were skipped due to
// parse errors.
type ParsedUnIgnoreRules struct {
	// Entries holds the successfully parsed un-ignore rules.
	Entries []UnIgnoreEntry
	// SkippedLines is the number of non-empty, non-comment lines that
	// could not be parsed.
	SkippedLines int
}
