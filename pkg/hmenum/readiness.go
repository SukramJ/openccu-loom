// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// ReadinessPhase tracks where a central is in its readiness-gated southbound
// bring-up. It lets north-bound adapters distinguish "still initializing"
// from "offline", per central.
type ReadinessPhase string

// ReadinessPhase values.
const (
	// ReadinessUnknown is the zero-ish default before bring-up is observed.
	ReadinessUnknown ReadinessPhase = "unknown"
	// ReadinessWaitingForCCU means the CCU boot marker (checkrega.cgi) is
	// not yet OK.
	ReadinessWaitingForCCU ReadinessPhase = "waiting_for_ccu"
	// ReadinessLoadingHub means hub metadata (names/rooms/functions) is
	// loading.
	ReadinessLoadingHub ReadinessPhase = "loading_hub"
	// ReadinessLoadingDevices means per-interface device loading is in
	// progress.
	ReadinessLoadingDevices ReadinessPhase = "loading_devices"
	// ReadinessReady means southbound bring-up has completed.
	ReadinessReady ReadinessPhase = "ready"
)

// String returns the wire representation.
func (p ReadinessPhase) String() string { return string(p) }

// Valid reports whether p is one of the defined readiness phases.
func (p ReadinessPhase) Valid() bool {
	switch p {
	case ReadinessUnknown, ReadinessWaitingForCCU, ReadinessLoadingHub, ReadinessLoadingDevices, ReadinessReady:
		return true
	default:
		return false
	}
}
