// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import "time"

// PerDPState is the JSON-wrapper schema published on every per-DP
// state topic (`values/<param>/state`, `master/<param>/state`,
// `calculated/<name>/state`). The wrapper carries only live state —
// descriptor metadata (unit, type, min, max, default, value_list,
// source) lives on the retained companion `/config` topic and is
// not duplicated here on every value event.
//
// All time fields are seconds since the Unix epoch as a float64
// (sub-second precision).
//
// `Value` is `any` because a parameter can be a bool, int, float,
// string, or string-list depending on its descriptor. The bridge
// marshals it via `encoding/json` directly.
type PerDPState struct {
	// Value is the parameter's current observed value. nil when the
	// parameter has never been observed (rare — bridge gates such
	// publishes upstream).
	Value any `json:"value"`

	// Available reports whether the bridge currently considers the parameter
	// readable.
	Available bool `json:"available"`

	// ModifiedAt is the timestamp of the last value change. Updated
	// only when the new value differs from the previous one.
	ModifiedAt float64 `json:"modified_at,omitempty"`

	// RefreshedAt is the timestamp of the last CCU observation,
	// regardless of value change.
	RefreshedAt float64 `json:"refreshed_at,omitempty"`

	// AdditionalInformation carries enriched model metadata (e.g. battery
	// type / quantity / low-voltage limits for a battery-backed device)
	// when the data point provides it. nil for plain scalar data points —
	// elided from JSON via omitempty, so a non-metadata DP's payload is
	// byte-identical to before. Additive only.
	AdditionalInformation map[string]any `json:"additional_information,omitempty"`
}

// EpochSeconds renders a Go time.Time as the seconds-since-epoch
// float64 the wrapper expects. Returns 0.0 for the zero time.
func EpochSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}
