// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// ValueSource is the wire-side lifecycle token surfaced through REST,
// MQTT and the SPA. It distinguishes whether a data point's current
// value comes from a live CCU push, a restored persistent cache, an
// older snapshot frozen by a connection outage, or whether the data
// point has never been observed at all.
//
// See ADR 0018 for the full lifecycle.
type ValueSource string

// Lifecycle states a wire-side data point can be in.
const (
	// ValueSourceUnobserved — no value has ever been seen, neither
	// from the live wire nor from a persisted cache.
	ValueSourceUnobserved ValueSource = "unobserved"

	// ValueSourceCache — the value was restored from the persistent
	// cache at boot. No live event has confirmed it yet; the next
	// fetch_all_device_data round or push event will move it to
	// `live`.
	ValueSourceCache ValueSource = "cache"

	// ValueSourceLive — the value was last set by a live CCU push or
	// fetch_all_device_data round and the connection is healthy.
	ValueSourceLive ValueSource = "live"

	// ValueSourceStale — the connection to the CCU went down after
	// the last live update; the value is the last known good but
	// not necessarily current. Transitions back to `live` when the
	// next recovery.completed fires.
	ValueSourceStale ValueSource = "stale"
)

// String implements fmt.Stringer.
func (s ValueSource) String() string { return string(s) }
