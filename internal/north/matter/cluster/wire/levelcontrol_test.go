// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeMoveToLevelAllFields verifies the full field set decodes
// correctly: Level=128, TransitionTime=10, OptionsMask=1,
// OptionsOverride=0.
func TestDecodeMoveToLevelAllFields(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 128)
		e.PutUint(tlv.ContextTag(1), 10)
		e.PutUint(tlv.ContextTag(2), 1)
		e.PutUint(tlv.ContextTag(3), 0)
	})
	got, err := wire.DecodeMoveToLevel(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Level != 128 {
		t.Errorf("Level = %d, want 128", got.Level)
	}
	if got.TransitionTime == nil {
		t.Fatal("TransitionTime is nil, want pointer to 10")
	}
	if *got.TransitionTime != 10 {
		t.Errorf("*TransitionTime = %d, want 10", *got.TransitionTime)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 0 {
		t.Errorf("OptionsOverride = %d, want 0", got.OptionsOverride)
	}
}

// TestDecodeMoveToLevelNullTransitionTime verifies that a TLV Null at
// tag 1 leaves TransitionTime as nil.
func TestDecodeMoveToLevelNullTransitionTime(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 64)
		e.PutNull(tlv.ContextTag(1))
		e.PutUint(tlv.ContextTag(2), 0)
		e.PutUint(tlv.ContextTag(3), 0)
	})
	got, err := wire.DecodeMoveToLevel(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TransitionTime != nil {
		t.Errorf("TransitionTime = %v, want nil", got.TransitionTime)
	}
}

// TestDecodeMoveToLevelTruncated verifies a truncated payload wraps
// ErrLevelControlMalformed.
func TestDecodeMoveToLevelTruncated(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeMoveToLevel([]byte{0x15}) // structure open only
	if !errors.Is(err, wire.ErrLevelControlMalformed) {
		t.Fatalf("err = %v, want ErrLevelControlMalformed", err)
	}
}

// TestDecodeMoveModeMoveUp verifies Move with MoveMode=Up and Rate=50.
func TestDecodeMoveModeMoveUp(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.LevelMoveModeUp))
		e.PutUint(tlv.ContextTag(1), 50)
		e.PutUint(tlv.ContextTag(2), 0)
		e.PutUint(tlv.ContextTag(3), 0)
	})
	got, err := wire.DecodeMove(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MoveMode != wire.LevelMoveModeUp {
		t.Errorf("MoveMode = %d, want %d (Up)", got.MoveMode, wire.LevelMoveModeUp)
	}
	if got.Rate == nil {
		t.Fatal("Rate is nil, want pointer to 50")
	}
	if *got.Rate != 50 {
		t.Errorf("*Rate = %d, want 50", *got.Rate)
	}
}

// TestDecodeMoveNullRate verifies that a TLV Null at tag 1 leaves Rate
// as nil.
func TestDecodeMoveNullRate(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.LevelMoveModeDown))
		e.PutNull(tlv.ContextTag(1))
		e.PutUint(tlv.ContextTag(2), 0)
		e.PutUint(tlv.ContextTag(3), 0)
	})
	got, err := wire.DecodeMove(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Rate != nil {
		t.Errorf("Rate = %v, want nil", got.Rate)
	}
}

// TestDecodeMoveTruncated verifies a truncated payload wraps
// ErrLevelControlMalformed.
func TestDecodeMoveTruncated(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeMove([]byte{0x15})
	if !errors.Is(err, wire.ErrLevelControlMalformed) {
		t.Fatalf("err = %v, want ErrLevelControlMalformed", err)
	}
}

// TestDecodeStepAllFields verifies Step with all fields set:
// StepMode=Down, StepSize=20, TransitionTime=15, OptionsMask=2,
// OptionsOverride=2.
func TestDecodeStepAllFields(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.LevelStepModeDown))
		e.PutUint(tlv.ContextTag(1), 20)
		e.PutUint(tlv.ContextTag(2), 15)
		e.PutUint(tlv.ContextTag(3), 2)
		e.PutUint(tlv.ContextTag(4), 2)
	})
	got, err := wire.DecodeStep(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StepMode != wire.LevelStepModeDown {
		t.Errorf("StepMode = %d, want %d (Down)", got.StepMode, wire.LevelStepModeDown)
	}
	if got.StepSize != 20 {
		t.Errorf("StepSize = %d, want 20", got.StepSize)
	}
	if got.TransitionTime == nil {
		t.Fatal("TransitionTime is nil, want pointer to 15")
	}
	if *got.TransitionTime != 15 {
		t.Errorf("*TransitionTime = %d, want 15", *got.TransitionTime)
	}
	if got.OptionsMask != 2 {
		t.Errorf("OptionsMask = %d, want 2", got.OptionsMask)
	}
	if got.OptionsOverride != 2 {
		t.Errorf("OptionsOverride = %d, want 2", got.OptionsOverride)
	}
}

// TestDecodeStepNullTransitionTime verifies that a TLV Null at tag 2
// leaves TransitionTime as nil.
func TestDecodeStepNullTransitionTime(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.LevelStepModeUp))
		e.PutUint(tlv.ContextTag(1), 10)
		e.PutNull(tlv.ContextTag(2))
		e.PutUint(tlv.ContextTag(3), 0)
		e.PutUint(tlv.ContextTag(4), 0)
	})
	got, err := wire.DecodeStep(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TransitionTime != nil {
		t.Errorf("TransitionTime = %v, want nil", got.TransitionTime)
	}
}

// TestDecodeStepTruncated verifies a truncated payload wraps
// ErrLevelControlMalformed.
func TestDecodeStepTruncated(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeStep([]byte{0x15})
	if !errors.Is(err, wire.ErrLevelControlMalformed) {
		t.Fatalf("err = %v, want ErrLevelControlMalformed", err)
	}
}

// TestDecodeStopAllFields verifies Stop with OptionsMask=1,
// OptionsOverride=1.
func TestDecodeStopAllFields(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 1)
		e.PutUint(tlv.ContextTag(1), 1)
	})
	got, err := wire.DecodeStop(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OptionsMask != 1 {
		t.Errorf("OptionsMask = %d, want 1", got.OptionsMask)
	}
	if got.OptionsOverride != 1 {
		t.Errorf("OptionsOverride = %d, want 1", got.OptionsOverride)
	}
}

// TestDecodeStopTruncated verifies a truncated payload wraps
// ErrLevelControlMalformed.
func TestDecodeStopTruncated(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeStop([]byte{0x15})
	if !errors.Is(err, wire.ErrLevelControlMalformed) {
		t.Fatalf("err = %v, want ErrLevelControlMalformed", err)
	}
}

// TestLevelControl_OnLevel_NullableWireEncoding verifies that OnLevel
// (Matter §1.6.6.11, attribute 0x0011) is correctly encoded as TLV null
// when the cluster server returns (nil, true). The nullable uint8 attribute
// uses 0xFF as the spec NULL sentinel; encoding a Go nil cluster value as
// TLV-null rather than 0xFF is the conformant path per matter.js
// packages/model/src/standard/elements/level-control.element.ts —
// OnLevel quality X (nullable). The round-trip test encodes null and
// decodes it back to verify the TLV encoder / decoder pair preserves
// the nullable contract.
func TestLevelControl_OnLevel_NullableWireEncoding(t *testing.T) {
	t.Parallel()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())
	// Encode OnLevel as TLV null at context tag 0 (simulating the IM
	// attribute response path for a nullable uint8 with Go nil value).
	e.PutNull(tlv.ContextTag(0))
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(raw)
	open, err := dec.Next()
	if err != nil {
		t.Fatalf("decode struct open: %v", err)
	}
	if open.Type != tlv.TypeStructure {
		t.Fatalf("expected struct, got 0x%02X", open.Type)
	}
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("decode null element: %v", err)
	}
	if !el.IsNull {
		t.Errorf("OnLevel nullable wire: IsNull = false, want true (TLV null encoding)")
	}
	if el.Tag.Number != 0 {
		t.Errorf("OnLevel context tag = %d, want 0", el.Tag.Number)
	}
}
