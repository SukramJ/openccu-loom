// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package engine implements the alarm-panel arm-state machine: one
// machine per alarm zone, driven by normalized sensor activations and
// countdown timers on the injected clock seam.
//
// The engine is domain core (notes/concepts/alarm-concept.md §14): it owns
// states, verbs (arm / disarm / silence / acknowledge), readiness,
// incidents, and the restart-restore semantics of
// notes/concepts/alarm-concept.md §10.2. It talks to the outside world through
// narrow ports only: persistence stores, an OutputPort (the output
// drivers), an EventSink (bus publishing), and a Journal facade. The
// hard safety invariants of notes/concepts/alarm-concept.md §2 shape the write
// ordering throughout: activation accounting is persisted before
// outputs fire (S1), a persisted silence is never lost or overwritten
// (S3), and every degradation is journaled instead of swallowed (S7).
package engine
