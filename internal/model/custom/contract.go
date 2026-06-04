// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmtypes"

// AggregateDataPoint is the contract every multi-slot custom data point
// implements.
//
// - IsRefreshed: at least one slot has been observed - StateUncertain: at
// least one slot is currently held optimistic - SubDataPointKeys: every
// slot's wire identity, for diagnostics
//
// North-bound adapters (REST, MQTT, UI) read the aggregate status before
// rendering "available / pending / unknown" badges so they don't have to know
// each custom-DP's slot list.
//
// Concrete custom types are not forced to implement this interface; it is
// opt-in. The default implementations on the embedded generic.DataPoint cover
// the single-slot case (Switch, Valve.Irrigation, FixedColorLight) — they
// need no extra wiring.
type AggregateDataPoint interface {
	// IsRefreshed reports whether the aggregate has observed at
	// least one of its underlying slots.
	IsRefreshed() bool
	// StateUncertain reports whether any underlying slot is
	// currently in an optimistic-update window awaiting CCU
	// confirmation.
	StateUncertain() bool
	// SubDataPointKeys returns the wire identifiers of every slot
	// the aggregate composes. Used by REST diagnostic endpoints to
	// expose the underlying parameter list without copying every
	// slot's full state.
	SubDataPointKeys() []hmtypes.DataPointKey
}

// StateChanger is the contract for service methods that benefit from
// short-circuiting when the requested target equals the last-observed value.
//
// IsStateChange's signature varies by target type — every concrete custom-DP
// attaches its own typed variant (Switch.IsStateChange(bool),
// Cover.IsStateChange(float64), Climate.IsStateChange(Mode, Profile,
// float64), …). This interface only documents the convention, no shared
// method.
type StateChanger any
