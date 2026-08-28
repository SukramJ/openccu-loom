// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// WSEnvelopeKind tags the event family on the WebSocket envelope's
// `kind` member (ADR 0022). The vocabulary was previously spelled out
// only as prose in `assets/wsapi.json`, which kept it out of the
// generated enum catalogue and left external clients to hard-code the
// three literals; naming the type publishes it through
// `assets/schemas/enums.json` like every other wire vocabulary.
//
// The wire is closed over these three values: a producer may leave the
// hub-side kind empty, but the hub rewrites an empty kind to
// [WSEnvelopeKindChange] before the frame is serialised.
//
// loom:reachable:reason="the type of ws.Event.Kind, ws.outboundEvent.Kind and ws.ValueChangedArgs.EnvelopeKind, and the parameter type of five EventBridge publish paths, so production carries it on every WebSocket frame; a string type whose methods production never calls, which the analyzer's type heuristic (reachable only via its methods) cannot see used"
type WSEnvelopeKind string

// WSEnvelopeKind values. The string form is the wire token.
const (
	// WSEnvelopeKindInitial marks the first observation of a value —
	// the seed a subscriber receives before any delta.
	WSEnvelopeKindInitial WSEnvelopeKind = "initial"
	// WSEnvelopeKindChange marks a delta. Dominant case, and the
	// value an empty producer-side kind is rewritten to.
	WSEnvelopeKindChange WSEnvelopeKind = "change"
	// WSEnvelopeKindRefresh marks a periodic re-emit of an unchanged
	// value.
	WSEnvelopeKindRefresh WSEnvelopeKind = "refresh"
)

// String returns the wire representation.
func (k WSEnvelopeKind) String() string { return string(k) }
