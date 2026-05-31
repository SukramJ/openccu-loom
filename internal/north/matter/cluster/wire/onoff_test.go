// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeOnWithTimedOffRoundTrip encodes all three fields and checks
// the decoded struct matches exactly.
func TestDecodeOnWithTimedOffRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 1)
		e.PutUint(tlv.ContextTag(1), 100)
		e.PutUint(tlv.ContextTag(2), 200)
	})
	got, err := wire.DecodeOnWithTimedOff(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OnOffControl != 1 {
		t.Errorf("OnOffControl = %d, want 1", got.OnOffControl)
	}
	if got.OnTime != 100 {
		t.Errorf("OnTime = %d, want 100", got.OnTime)
	}
	if got.OffWaitTime != 200 {
		t.Errorf("OffWaitTime = %d, want 200", got.OffWaitTime)
	}
}

// TestDecodeOnWithTimedOffMissingFields verifies that absent fields
// default to their zero values without error.
func TestDecodeOnWithTimedOffMissingFields(t *testing.T) {
	t.Parallel()
	// Encode only field 0; fields 1 and 2 are absent.
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 3)
	})
	got, err := wire.DecodeOnWithTimedOff(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OnOffControl != 3 {
		t.Errorf("OnOffControl = %d, want 3", got.OnOffControl)
	}
	if got.OnTime != 0 {
		t.Errorf("OnTime = %d, want 0 (zero-value)", got.OnTime)
	}
	if got.OffWaitTime != 0 {
		t.Errorf("OffWaitTime = %d, want 0 (zero-value)", got.OffWaitTime)
	}
}

// TestDecodeOnWithTimedOffNonStructureTop rejects a payload whose
// top-level element is not a Structure.
func TestDecodeOnWithTimedOffNonStructureTop(t *testing.T) {
	t.Parallel()
	// Encode a bare unsigned integer instead of a struct.
	e := tlv.NewEncoder()
	e.PutUint(tlv.AnonymousTag(), 42)
	payload, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, gotErr := wire.DecodeOnWithTimedOff(payload)
	if !errors.Is(gotErr, wire.ErrOnOffMalformed) {
		t.Fatalf("err = %v, want ErrOnOffMalformed", gotErr)
	}
}

// TestDecodeOnWithTimedOffTruncatedPayload verifies that a truncated
// wire payload wraps ErrOnOffMalformed.
func TestDecodeOnWithTimedOffTruncatedPayload(t *testing.T) {
	t.Parallel()
	// Only the structure open byte — value bytes are missing.
	payload := []byte{0x15} // TypeStructure, anonymous tag
	_, gotErr := wire.DecodeOnWithTimedOff(payload)
	if !errors.Is(gotErr, wire.ErrOnOffMalformed) {
		t.Fatalf("err = %v, want wrapped ErrOnOffMalformed", gotErr)
	}
}

// TestDecodeOffWithEffectRoundTrip encodes EffectIdentifier=2,
// EffectVariant=1 and verifies the decoded struct.
func TestDecodeOffWithEffectRoundTrip(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 2)
		e.PutUint(tlv.ContextTag(1), 1)
	})
	got, err := wire.DecodeOffWithEffect(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.EffectIdentifier != 2 {
		t.Errorf("EffectIdentifier = %d, want 2", got.EffectIdentifier)
	}
	if got.EffectVariant != 1 {
		t.Errorf("EffectVariant = %d, want 1", got.EffectVariant)
	}
}

// TestDecodeOnWithTimedOffIgnoresNonContextTags verifies that elements
// with a non-context tag kind are silently skipped.
func TestDecodeOnWithTimedOffIgnoresNonContextTags(t *testing.T) {
	t.Parallel()
	// Build manually: struct open, a common-profile element, two context
	// tags, struct close.
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())
	e.PutUint(tlv.CommonTag(99), 0xFF) // non-context tag → must be ignored
	e.PutUint(tlv.ContextTag(1), 50)
	e.PutUint(tlv.ContextTag(2), 75)
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	payload, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, gotErr := wire.DecodeOnWithTimedOff(payload)
	if gotErr != nil {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	if got.OnOffControl != 0 {
		t.Errorf("OnOffControl = %d, want 0 (non-context tag skipped)", got.OnOffControl)
	}
	if got.OnTime != 50 {
		t.Errorf("OnTime = %d, want 50", got.OnTime)
	}
	if got.OffWaitTime != 75 {
		t.Errorf("OffWaitTime = %d, want 75", got.OffWaitTime)
	}
}
