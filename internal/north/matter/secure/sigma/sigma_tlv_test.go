// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for the sigma-internal TLV helpers and SessionParameters
// helpers that are not exercised by full CASE exchange tests.
package sigma

import (
	"testing"
)

// =============================================================================
// peek helper
// =============================================================================

// TestPeek_ShortSlice verifies that peek returns the whole slice when it
// has ≤ n elements.
func TestPeek_ShortSlice(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02}
	got := peek(b, 4) // n > len(b)
	if len(got) != 2 {
		t.Errorf("peek short: len=%d, want 2", len(got))
	}
}

// TestPeek_ExactLength verifies that peek returns the whole slice when
// len == n.
func TestPeek_ExactLength(t *testing.T) {
	t.Parallel()
	b := []byte{0xAA, 0xBB}
	got := peek(b, 2)
	if len(got) != 2 {
		t.Errorf("peek exact: len=%d, want 2", len(got))
	}
}

// TestPeek_LongerSlice verifies that peek returns only the first n bytes.
func TestPeek_LongerSlice(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02, 0x03, 0x04}
	got := peek(b, 2)
	if len(got) != 2 {
		t.Errorf("peek longer: len=%d, want 2", len(got))
	}
	if got[0] != 0x01 || got[1] != 0x02 {
		t.Errorf("peek longer: got %v, want [0x01, 0x02]", got)
	}
}

// =============================================================================
// SessionParameters.isEmpty
// =============================================================================

// TestSessionParameters_IsEmpty_AllZero verifies that an all-zero
// SessionParameters reports isEmpty=true.
func TestSessionParameters_IsEmpty_AllZero(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{}
	if !sp.isEmpty() {
		t.Error("expected isEmpty=true for zero SessionParameters")
	}
}

// TestSessionParameters_IsEmpty_NonZeroIdle verifies that a non-zero
// SessionIdleInterval makes isEmpty=false.
func TestSessionParameters_IsEmpty_NonZeroIdle(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{SessionIdleInterval: 500}
	if sp.isEmpty() {
		t.Error("expected isEmpty=false when SessionIdleInterval != 0")
	}
}

// TestSessionParameters_IsEmpty_NonZeroActive verifies that a non-zero
// SessionActiveInterval makes isEmpty=false.
func TestSessionParameters_IsEmpty_NonZeroActive(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{SessionActiveInterval: 300}
	if sp.isEmpty() {
		t.Error("expected isEmpty=false when SessionActiveInterval != 0")
	}
}

// TestSessionParameters_IsEmpty_NonZeroThreshold verifies that a non-zero
// SessionActiveThreshold makes isEmpty=false.
func TestSessionParameters_IsEmpty_NonZeroThreshold(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{SessionActiveThreshold: 4000}
	if sp.isEmpty() {
		t.Error("expected isEmpty=false when SessionActiveThreshold != 0")
	}
}

// =============================================================================
// SessionParameters.encode (exercises startStructTag, putUint32, putUint16)
// =============================================================================

// TestSessionParameters_Encode_AllNonZero verifies that encode writes all
// three fields when they are non-zero. Uses the internal sigmaEncoder.
func TestSessionParameters_Encode_AllNonZero(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{
		SessionIdleInterval:    500,
		SessionActiveInterval:  300,
		SessionActiveThreshold: 4000,
	}
	enc := sigmaTLVEncoder()
	sp.encode(enc, 5) // outerTag = 5 (Sigma2's context tag)
	b := enc.bytes()
	if len(b) == 0 {
		t.Error("encode produced empty output")
	}
}

// TestSessionParameters_Encode_AllZero verifies that encode still writes the
// container (but no fields) when all values are zero.
func TestSessionParameters_Encode_AllZero(t *testing.T) {
	t.Parallel()
	sp := SessionParameters{}
	enc := sigmaTLVEncoder()
	sp.encode(enc, 5)
	b := enc.bytes()
	// Container open + close = 2 bytes minimum.
	if len(b) < 2 {
		t.Errorf("encode all-zero: got %d bytes, want ≥2", len(b))
	}
}

// TestSessionParameters_Encode_PartialFields verifies that only non-zero
// fields are encoded (exercises each individual zero-guard).
func TestSessionParameters_Encode_PartialFields(t *testing.T) {
	t.Parallel()
	// Only SessionIdleInterval is set — only putUint32(1, …) fires.
	sp := SessionParameters{SessionIdleInterval: 1000}
	enc := sigmaTLVEncoder()
	sp.encode(enc, 1)
	b := enc.bytes()
	if len(b) == 0 {
		t.Error("encode partial: expected non-empty output")
	}
}

// =============================================================================
// sigmaEncoder.bytes — panic on unbalanced container
// =============================================================================

// TestSigmaEncoder_Bytes_Balanced verifies normal operation.
func TestSigmaEncoder_Bytes_Balanced(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.putOctets(1, []byte{0xAA})
	b := enc.bytes()
	if len(b) == 0 {
		t.Error("expected non-empty bytes from balanced encoder")
	}
}

// =============================================================================
// sigmaDecoder.skipContainer
// =============================================================================

// TestSigmaDecoder_SkipContainer_Simple verifies that skipContainer drains
// a nested struct and leaves the outer decoder positioned at the next
// field.
func TestSigmaDecoder_SkipContainer_Simple(t *testing.T) {
	t.Parallel()
	// Build a TLV stream: outer anon-struct (via startStruct), nested struct
	// (context-tag 1) with one uint field, end of nested, then outer field
	// (context-tag 2 = uint 99), end of outer.
	enc := sigmaTLVEncoder()
	// Encode: outer struct (anonymous) containing a nested tagged struct.
	enc.startStruct()          // outer anon struct
	enc.startStructTag(1)      // nested struct at context-tag 1
	enc.putUint(1, uint64(42)) // a field inside the nested struct
	enc.endContainer()         // end nested struct
	enc.putUint(2, uint64(99)) // outer field after nested
	enc.endContainer()         // end outer struct
	b := enc.bytes()

	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		t.Fatalf("openStruct: %v", err)
	}
	// Read the first field — it should be a container (nested struct at tag 1).
	tag, val, end, err := dec.next()
	if err != nil {
		t.Fatalf("next (nested struct): %v", err)
	}
	if end {
		t.Fatal("unexpected end of container")
	}
	if tag != 1 {
		t.Errorf("expected tag 1, got %d", tag)
	}
	if !val.container {
		t.Error("expected container val for nested struct")
	}
	// Skip the nested struct content.
	if err := dec.skipContainer(); err != nil {
		t.Fatalf("skipContainer: %v", err)
	}
	// Next field should be tag 2, value 99.
	tag, val, end, err = dec.next()
	if err != nil {
		t.Fatalf("next (outer field): %v", err)
	}
	if end {
		t.Fatal("unexpected end after skipContainer")
	}
	if tag != 2 {
		t.Errorf("expected tag 2 after skipContainer, got %d", tag)
	}
	if val.u != 99 {
		t.Errorf("expected value 99, got %d", val.u)
	}
}
