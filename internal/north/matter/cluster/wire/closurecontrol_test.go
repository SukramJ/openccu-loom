// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeClosureMoveToPosition verifies that field 0 carries the
// TargetPositionEnum, matching matter.js closure-control.element.ts:80
// (Position id 0x0).
func TestDecodeClosureMoveToPosition(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ClosureTargetPositionMoveToVentilationPosition))
	})
	got, err := wire.DecodeClosureMoveTo(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Position == nil {
		t.Fatal("Position = nil, want MoveToVentilationPosition")
	}
	if *got.Position != wire.ClosureTargetPositionMoveToVentilationPosition {
		t.Errorf("Position = %d, want %d", *got.Position, wire.ClosureTargetPositionMoveToVentilationPosition)
	}
	if got.Latch != nil || got.Speed != nil {
		t.Error("fields the request omitted must stay nil, not take their zero value")
	}
}

// TestDecodeClosureMoveToAllFields verifies the field-tag assignment for
// Latch (1) and Speed (2) per matter.js closure-control.element.ts:81-82.
//
// Latch is the one that would go unnoticed if the tags were swapped: a
// bool decoded off the wrong tag still produces a valid-looking request.
func TestDecodeClosureMoveToAllFields(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ClosureTargetPositionMoveToFullyOpen))
		e.PutBool(tlv.ContextTag(1), true)
		e.PutUint(tlv.ContextTag(2), 2)
	})
	got, err := wire.DecodeClosureMoveTo(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Position == nil || *got.Position != wire.ClosureTargetPositionMoveToFullyOpen {
		t.Errorf("Position = %v, want MoveToFullyOpen", got.Position)
	}
	if got.Latch == nil || !*got.Latch {
		t.Errorf("Latch = %v, want true", got.Latch)
	}
	if got.Speed == nil || *got.Speed != 2 {
		t.Errorf("Speed = %v, want 2", got.Speed)
	}
}

// TestDecodeClosureMoveToLatchOnly verifies a request carrying only Latch
// decodes rather than being rejected: the "O.a+" conformance is satisfied
// by any one field, and deciding whether this server supports Latch is
// the cluster server's job, not the decoder's.
func TestDecodeClosureMoveToLatchOnly(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutBool(tlv.ContextTag(1), false)
	})
	got, err := wire.DecodeClosureMoveTo(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Latch == nil || *got.Latch {
		t.Errorf("Latch = %v, want false", got.Latch)
	}
	if got.Position != nil {
		t.Error("Position must stay nil when the request omitted it")
	}
}

// TestDecodeClosureMoveToEmptyIsMalformed pins the "O.a+" conformance:
// every field is optional but at least one must be present, so a request
// asking for nothing is a controller bug rather than an expensive no-op.
func TestDecodeClosureMoveToEmptyIsMalformed(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(_ *tlv.Encoder) {})
	_, err := wire.DecodeClosureMoveTo(payload)
	if !errors.Is(err, wire.ErrClosureControlMalformed) {
		t.Fatalf("err = %v, want ErrClosureControlMalformed", err)
	}
}

// TestDecodeClosureMoveToNullFieldIsAbsent pins that an explicitly null
// field is treated as absent rather than as its zero value.
//
// A null Position taken as present would decode to MoveToFullyClosed —
// the zero of TargetPositionEnum — and shut a door the controller only
// meant to leave alone.
func TestDecodeClosureMoveToNullFieldIsAbsent(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutNull(tlv.ContextTag(0))
		e.PutBool(tlv.ContextTag(1), true)
	})
	got, err := wire.DecodeClosureMoveTo(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Position != nil {
		t.Errorf("Position = %d, want nil — a null field is absent, and the zero value is MoveToFullyClosed",
			*got.Position)
	}
}

// TestDecodeClosureMoveToMalformedTopLevel pins that a payload whose top
// element is not a structure wraps the sentinel.
func TestDecodeClosureMoveToMalformedTopLevel(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.AnonymousTag(), 1)
	payload, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	if _, derr := wire.DecodeClosureMoveTo(payload); !errors.Is(derr, wire.ErrClosureControlMalformed) {
		t.Fatalf("err = %v, want ErrClosureControlMalformed", derr)
	}
}
