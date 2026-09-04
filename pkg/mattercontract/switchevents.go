// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercontract

// SwitchEventEmitter is the press-event sink of the Matter §1.13
// GenericSwitch cluster (0x003B), as seen from the model side. The
// bridge's `cluster/wire.GenericSwitch` implements the four Fire*
// methods; the model's button group calls them while narrating one
// physical HM press as the ordered Matter gesture sequence
// (InitialPress → ShortRelease, or InitialPress → LongPress →
// LongRelease).
//
// It lives here rather than on either side because both sides have to
// name the SAME interface type: Go interface satisfaction requires
// identical parameter types, so a private copy on the bridge side
// could never be satisfied by the model's `WireMatterSwitchHandler`
// method set. The bridge's type assertion would then silently never
// match and no press would ever reach a commissioner — a failure with
// no compile error and no runtime error, only silence.
//
// Mirrors matter.js packages/node/src/behaviors/switch/SwitchServer.ts,
// which derives the same sequence from one currentPosition stream per
// switch.
// loom:reachable:reason="called through a type assertion in internal/north/matter/bridge when a cluster server is wired to its press source; an interface type has no construction site the analyzer can follow"
type SwitchEventEmitter interface {
	// FireInitialPress reports the switch leaving its idle position.
	FireInitialPress(newPosition uint8)

	// FireShortRelease closes a short-press gesture.
	FireShortRelease(previousPosition uint8)

	// FireLongPress reports the hold threshold being crossed.
	FireLongPress(newPosition uint8)

	// FireLongRelease closes a long-press gesture.
	FireLongRelease(previousPosition uint8)
}
