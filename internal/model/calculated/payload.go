// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import "github.com/SukramJ/openccu-loom/internal/payload"

// Compile-time guarantees that calculated sensors satisfy the
// universal Source contract through method promotion from the
// embedded *generic.Sensor[T]. ADR 0007 step 7.
//
// Calculated sensors are read-only — they expose no service methods
// of their own. The promoted ServiceRegistry from *generic.Sensor[T]
// stays empty (Sensor itself registers no methods), so
// ServiceMethodNames returns nil and Invoke always returns
// ErrUnknownServiceMethod.
//
// State semantics inherit the generic-DP shape (`value`, `available`,
// `modified_at`, `refreshed_at`) — this is the correct projection for
// a derived numeric reading. The aggregated state_uncertain across
// source DPs is exposed through the [SourceSink.StateUncertain]
// surface for callers that want the calculated-aware diagnostic.
var (
	_ payload.Source = (*DewPointSensor)(nil)
	_ payload.Source = (*DewPointSpreadSensor)(nil)
	_ payload.Source = (*FrostPointSensor)(nil)
	_ payload.Source = (*VaporConcentrationSensor)(nil)
	_ payload.Source = (*EnthalpySensor)(nil)
	_ payload.Source = (*ApparentTemperatureSensor)(nil)
	_ payload.Source = (*DerivedBinarySensor)(nil)
	_ payload.Source = (*OperatingVoltageLevelSensor)(nil)
)
