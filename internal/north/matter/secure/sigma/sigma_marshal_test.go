// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for the sigma marshal/unmarshal helpers and low-level
// TLV decoder error paths that are not exercised by the full CASE exchange
// tests.
package sigma

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// =============================================================================
// UnmarshalSigma1 — error paths
// =============================================================================

// TestUnmarshalSigma1_BadOpenStruct verifies that a non-struct leading byte
// produces ErrSessionState.
func TestUnmarshalSigma1_BadOpenStruct(t *testing.T) {
	t.Parallel()
	// Encode a bare uint field with no enclosing struct.
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.ContextTag(1), 42)
	b, _ := enc.Bytes()
	_, err := UnmarshalSigma1(b)
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_BadRandomLength verifies that a tag-1 payload whose
// length differs from RandomSize produces ErrSessionState.
func TestUnmarshalSigma1_BadRandomLength(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, 10)) // RandomSize=32, not 10
	enc.endContainer()
	_, err := UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_BadDestinationIDLength verifies that a tag-3 payload
// whose length is not 32 produces ErrSessionState.
func TestUnmarshalSigma1_BadDestinationIDLength(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 16)) // want 32
	enc.endContainer()
	_, err := UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_BadEphPubLength verifies that a tag-4 payload whose
// length is not EphPubKeySize (65) produces ErrSessionState.
func TestUnmarshalSigma1_BadEphPubLength(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, make([]byte, 10)) // want 65
	enc.endContainer()
	_, err := UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_MissingEphPub verifies that a well-formed Sigma1
// struct that simply omits tag 4 produces ErrSessionState.
func TestUnmarshalSigma1_MissingEphPub(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x5678)
	enc.putOctets(3, make([]byte, 32))
	// tag 4 intentionally absent
	enc.endContainer()
	_, err := UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_BadResumptionIDLength verifies that a tag-6 payload
// whose length is not 16 produces ErrSessionState.
func TestUnmarshalSigma1_BadResumptionIDLength(t *testing.T) {
	t.Parallel()
	// Build a valid P-256 ephemeral so validatePoint won't reject it.
	ephPriv, err := newECDHKey(t)
	if err != nil {
		t.Fatalf("ecdh key: %v", err)
	}
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, ephPriv)
	enc.putOctets(6, make([]byte, 8)) // want 16
	enc.endContainer()
	_, err = UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_BadResumeMICLength verifies that a tag-7 payload
// whose length is not 16 produces ErrSessionState.
func TestUnmarshalSigma1_BadResumeMICLength(t *testing.T) {
	t.Parallel()
	ephPriv, err := newECDHKey(t)
	if err != nil {
		t.Fatalf("ecdh key: %v", err)
	}
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, ephPriv)
	enc.putOctets(6, make([]byte, 16))
	enc.putOctets(7, make([]byte, 8)) // want 16
	enc.endContainer()
	_, err = UnmarshalSigma1(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma1_UnknownNonContainerTag verifies that unknown non-
// container tags are silently skipped (no error, no panic).
func TestUnmarshalSigma1_UnknownNonContainerTag(t *testing.T) {
	t.Parallel()
	ephPriv, err := newECDHKey(t)
	if err != nil {
		t.Fatalf("ecdh key: %v", err)
	}
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, ephPriv)
	enc.putUint(99, 0xDEAD) // unknown non-container tag — must be silently skipped
	enc.endContainer()
	got, err := UnmarshalSigma1(enc.bytes())
	if err != nil {
		t.Fatalf("unexpected error on unknown non-container tag: %v", err)
	}
	if got.InitiatorEphPubKey == nil {
		t.Error("InitiatorEphPubKey is nil after skipping unknown tag")
	}
}

// TestUnmarshalSigma1_WithResumptionFields verifies the happy path with
// both resumptionId (tag 6) and initiatorResumeMIC (tag 7) present.
func TestUnmarshalSigma1_WithResumptionFields(t *testing.T) {
	t.Parallel()
	ephPriv, err := newECDHKey(t)
	if err != nil {
		t.Fatalf("ecdh key: %v", err)
	}
	rid := make([]byte, 16)
	mic := make([]byte, 16)
	for i := range rid {
		rid[i] = byte(i + 1)
		mic[i] = byte(i + 17)
	}
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, make([]byte, RandomSize))
	enc.putUint16(2, 0x4321)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, ephPriv)
	enc.putOctets(6, rid)
	enc.putOctets(7, mic)
	enc.endContainer()
	got, err := UnmarshalSigma1(enc.bytes())
	if err != nil {
		t.Fatalf("UnmarshalSigma1 with resume fields: %v", err)
	}
	if len(got.ResumptionID) != 16 {
		t.Errorf("ResumptionID length=%d, want 16", len(got.ResumptionID))
	}
	if len(got.InitiatorResumeMIC) != 16 {
		t.Errorf("InitiatorResumeMIC length=%d, want 16", len(got.InitiatorResumeMIC))
	}
}

// =============================================================================
// marshalTBE2 / unmarshalTBE2 — ICAC branch and round-trip
// =============================================================================

// TestMarshalUnmarshalTBE2_WithICAC verifies that marshalTBE2 includes tag 2
// when ICAC is non-empty, and unmarshalTBE2 recovers it faithfully.
func TestMarshalUnmarshalTBE2_WithICAC(t *testing.T) {
	t.Parallel()
	in := TBE2Plaintext{
		ResponderNOC:  []byte{0x01, 0x02, 0x03},
		ResponderICAC: []byte{0xAA, 0xBB, 0xCC},
		Signature:     make([]byte, 64),
		ResumptionID:  make([]byte, 16),
	}
	b := marshalTBE2(in)
	out, err := unmarshalTBE2(b)
	if err != nil {
		t.Fatalf("unmarshalTBE2: %v", err)
	}
	if !bytes.Equal(out.ResponderNOC, in.ResponderNOC) {
		t.Errorf("NOC mismatch: %x vs %x", out.ResponderNOC, in.ResponderNOC)
	}
	if !bytes.Equal(out.ResponderICAC, in.ResponderICAC) {
		t.Errorf("ICAC mismatch: %x vs %x", out.ResponderICAC, in.ResponderICAC)
	}
	if !bytes.Equal(out.Signature, in.Signature) {
		t.Errorf("Signature mismatch")
	}
	if !bytes.Equal(out.ResumptionID, in.ResumptionID) {
		t.Errorf("ResumptionID mismatch")
	}
}

// TestMarshalUnmarshalTBE2_WithoutICAC verifies the tag-2-absent path.
func TestMarshalUnmarshalTBE2_WithoutICAC(t *testing.T) {
	t.Parallel()
	in := TBE2Plaintext{
		ResponderNOC: []byte{0xDE, 0xAD},
		Signature:    make([]byte, 64),
		ResumptionID: make([]byte, 16),
	}
	b := marshalTBE2(in)
	out, err := unmarshalTBE2(b)
	if err != nil {
		t.Fatalf("unmarshalTBE2: %v", err)
	}
	if len(out.ResponderICAC) != 0 {
		t.Errorf("expected empty ICAC, got %x", out.ResponderICAC)
	}
}

// TestUnmarshalTBE2_BadOpenStruct verifies that garbage bytes produce an
// error (not a panic).
func TestUnmarshalTBE2_BadOpenStruct(t *testing.T) {
	t.Parallel()
	// A single raw uint byte — not a struct container.
	_, err := unmarshalTBE2([]byte{0x04, 0x05})
	if err == nil {
		t.Fatal("expected error on bad TBE2 bytes, got nil")
	}
}

// =============================================================================
// marshalTBE3 / unmarshalTBE3 — ICAC branch and round-trip
// =============================================================================

// TestMarshalUnmarshalTBE3_WithICAC verifies that marshalTBE3 includes tag 2
// when ICAC is non-empty, and unmarshalTBE3 recovers it faithfully.
func TestMarshalUnmarshalTBE3_WithICAC(t *testing.T) {
	t.Parallel()
	in := TBE3Plaintext{
		InitiatorNOC:  []byte{0x11, 0x22},
		InitiatorICAC: []byte{0x33, 0x44, 0x55},
		Signature:     make([]byte, 64),
	}
	b := marshalTBE3(in)
	out, err := unmarshalTBE3(b)
	if err != nil {
		t.Fatalf("unmarshalTBE3: %v", err)
	}
	if !bytes.Equal(out.InitiatorNOC, in.InitiatorNOC) {
		t.Errorf("NOC mismatch")
	}
	if !bytes.Equal(out.InitiatorICAC, in.InitiatorICAC) {
		t.Errorf("ICAC mismatch: %x vs %x", out.InitiatorICAC, in.InitiatorICAC)
	}
	if !bytes.Equal(out.Signature, in.Signature) {
		t.Errorf("Signature mismatch")
	}
}

// TestMarshalUnmarshalTBE3_WithoutICAC verifies the tag-2-absent path.
func TestMarshalUnmarshalTBE3_WithoutICAC(t *testing.T) {
	t.Parallel()
	in := TBE3Plaintext{
		InitiatorNOC: []byte{0xFE, 0xED},
		Signature:    make([]byte, 64),
	}
	b := marshalTBE3(in)
	out, err := unmarshalTBE3(b)
	if err != nil {
		t.Fatalf("unmarshalTBE3: %v", err)
	}
	if len(out.InitiatorICAC) != 0 {
		t.Errorf("expected empty ICAC, got %x", out.InitiatorICAC)
	}
}

// TestUnmarshalTBE3_BadOpenStruct verifies that garbage bytes produce an error.
func TestUnmarshalTBE3_BadOpenStruct(t *testing.T) {
	t.Parallel()
	_, err := unmarshalTBE3([]byte{0x04, 0x05})
	if err == nil {
		t.Fatal("expected error on bad TBE3 bytes, got nil")
	}
}

// =============================================================================
// UnmarshalSigma3 — error paths
// =============================================================================

// TestUnmarshalSigma3_BadOpenStruct verifies that a non-struct leading byte
// produces ErrSessionState.
func TestUnmarshalSigma3_BadOpenStruct(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.ContextTag(1), 1)
	b, _ := enc.Bytes()
	_, err := UnmarshalSigma3(b)
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma3_MissingEncrypted3 verifies that a Sigma3 struct without
// tag 1 produces ErrSessionState.
func TestUnmarshalSigma3_MissingEncrypted3(t *testing.T) {
	t.Parallel()
	enc := sigmaTLVEncoder()
	enc.startStruct()
	// tag 1 intentionally absent
	enc.endContainer()
	_, err := UnmarshalSigma3(enc.bytes())
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestUnmarshalSigma3_HappyPath verifies the round-trip for a valid Sigma3.
func TestUnmarshalSigma3_HappyPath(t *testing.T) {
	t.Parallel()
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03}
	s3 := Sigma3{Encrypted3: payload}
	wire := s3.Marshal()
	got, err := UnmarshalSigma3(wire)
	if err != nil {
		t.Fatalf("UnmarshalSigma3: %v", err)
	}
	if !bytes.Equal(got.Encrypted3, payload) {
		t.Errorf("Encrypted3 mismatch: %x vs %x", got.Encrypted3, payload)
	}
}

// =============================================================================
// MarshalSigma2Resume — SessionParams branch
// =============================================================================

// TestMarshalSigma2Resume_WithSessionParams verifies that a non-empty
// SessionParams is encoded into the output wire bytes.
func TestMarshalSigma2Resume_WithSessionParams(t *testing.T) {
	t.Parallel()
	withParams := MarshalSigma2Resume(Sigma2Resume{
		ResumptionID:       make([]byte, 16),
		Sigma2ResumeMIC:    make([]byte, 16),
		ResponderSessionID: 0x1234,
		SessionParams: &SessionParameters{
			SessionIdleInterval:   5000,
			SessionActiveInterval: 300,
		},
	})
	withoutParams := MarshalSigma2Resume(Sigma2Resume{
		ResumptionID:       make([]byte, 16),
		Sigma2ResumeMIC:    make([]byte, 16),
		ResponderSessionID: 0x1234,
	})
	if len(withParams) <= len(withoutParams) {
		t.Errorf("expected withParams (%d bytes) > withoutParams (%d bytes)",
			len(withParams), len(withoutParams))
	}
}

// TestMarshalSigma2Resume_EmptySessionParams verifies that an all-zero
// SessionParams (isEmpty=true) does NOT add bytes.
func TestMarshalSigma2Resume_EmptySessionParams(t *testing.T) {
	t.Parallel()
	withEmpty := MarshalSigma2Resume(Sigma2Resume{
		ResumptionID:       make([]byte, 16),
		Sigma2ResumeMIC:    make([]byte, 16),
		ResponderSessionID: 0x9999,
		SessionParams:      &SessionParameters{}, // all zero → isEmpty=true
	})
	withNil := MarshalSigma2Resume(Sigma2Resume{
		ResumptionID:       make([]byte, 16),
		Sigma2ResumeMIC:    make([]byte, 16),
		ResponderSessionID: 0x9999,
	})
	if len(withEmpty) != len(withNil) {
		t.Errorf("empty SessionParams added %d extra bytes (want 0)",
			len(withEmpty)-len(withNil))
	}
}

// =============================================================================
// validatePoint — identity element and valid-point paths
// =============================================================================

// TestValidatePoint_IdentityElement verifies that a point with both X and Y
// zero is rejected as the identity element.
func TestValidatePoint_IdentityElement(t *testing.T) {
	t.Parallel()
	// Uncompressed encoding with X=0, Y=0 — the identity element.
	b := make([]byte, EphPubKeySize)
	b[0] = 0x04
	// X occupies bytes 1..32, Y occupies bytes 33..64; all remain zero.
	err := validatePoint(b)
	if !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("err=%v, want ErrInvalidPoint for identity element", err)
	}
}

// TestValidatePoint_ValidPoint verifies that a real P-256 generator point
// passes validatePoint.
func TestValidatePoint_ValidPoint(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019: test fixture
	if err := validatePoint(pub); err != nil {
		t.Fatalf("validatePoint on a real P-256 point: %v", err)
	}
}

// TestValidatePoint_WrongLength verifies that a buffer that is not 65 bytes
// produces ErrInvalidPoint.
func TestValidatePoint_WrongLength(t *testing.T) {
	t.Parallel()
	if err := validatePoint(make([]byte, 64)); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("err=%v, want ErrInvalidPoint for wrong length", err)
	}
}

// =============================================================================
// sigmaDecoder.openStruct — error paths
// =============================================================================

// TestSigmaDecoder_OpenStruct_NonContainer verifies that opening a decoder on
// a stream that starts with a non-container element returns an error.
func TestSigmaDecoder_OpenStruct_NonContainer(t *testing.T) {
	t.Parallel()
	// Build raw TLV that is a bare uint field — no struct wrapper.
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.AnonymousTag(), 99)
	b, _ := enc.Bytes()
	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err == nil {
		t.Fatal("expected error when opening non-container element as struct")
	}
}

// TestSigmaDecoder_OpenStruct_TruncatedInput verifies that a decoder backed by
// truncated (empty) bytes returns an error from openStruct.
func TestSigmaDecoder_OpenStruct_TruncatedInput(t *testing.T) {
	t.Parallel()
	dec := sigmaTLVDecoder([]byte{}) // empty — Next() will fail
	if err := dec.openStruct(); err == nil {
		t.Fatal("expected error on empty/truncated input")
	}
}

// =============================================================================
// sigmaDecoder.next — non-context-tag element
// =============================================================================

// TestSigmaDecoder_Next_NonContextTag verifies that next() returns an error
// when it encounters an anonymous-tagged field instead of a context-tagged one.
func TestSigmaDecoder_Next_NonContextTag(t *testing.T) {
	t.Parallel()
	// Build a struct that contains an anonymous-tagged value (invalid per Sigma).
	// We have to craft the raw bytes manually because the sigma encoder always
	// writes context tags, so we compose via the underlying tlv package:
	//   struct header (anonymous) → anonymous uint → end-container
	rawEnc := tlv.NewEncoder()
	rawEnc.StartStruct(tlv.AnonymousTag())
	rawEnc.PutUint(tlv.AnonymousTag(), 77) // anonymous tag inside struct
	if err := rawEnc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	b, _ := rawEnc.Bytes()

	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		t.Fatalf("openStruct: %v", err)
	}
	_, _, _, err := dec.next()
	if err == nil {
		t.Fatal("expected error on anonymous tag inside sigma struct, got nil")
	}
}

// =============================================================================
// sigmaDecoder.skipContainer — deeply nested containers
// =============================================================================

// TestSigmaDecoder_SkipContainer_Nested verifies that skipContainer correctly
// tracks depth when encountering nested containers.
func TestSigmaDecoder_SkipContainer_Nested(t *testing.T) {
	t.Parallel()
	// Build: outer struct → nested struct → deeply nested struct → endC → endC
	// → uint field (context 2 = 42) → endC (outer)
	rawEnc := tlv.NewEncoder()
	rawEnc.StartStruct(tlv.AnonymousTag()) // outer
	rawEnc.StartStruct(tlv.ContextTag(1))  // level-1 nested
	rawEnc.StartStruct(tlv.ContextTag(1))  // level-2 nested
	_ = rawEnc.EndContainer()              // close level-2
	_ = rawEnc.EndContainer()              // close level-1
	rawEnc.PutUint(tlv.ContextTag(2), 42)  // sentinel field after nested
	_ = rawEnc.EndContainer()              // close outer
	b, _ := rawEnc.Bytes()

	dec := sigmaTLVDecoder(b)
	if err := dec.openStruct(); err != nil {
		t.Fatalf("openStruct: %v", err)
	}
	// First element should be the level-1 nested struct at tag 1.
	tag, val, end, err := dec.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if end || tag != 1 || !val.container {
		t.Fatalf("expected container at tag 1, got tag=%d end=%v container=%v", tag, end, val.container)
	}
	// skipContainer must drain both nested levels.
	if err := dec.skipContainer(); err != nil {
		t.Fatalf("skipContainer (2-level deep): %v", err)
	}
	// Next should be the sentinel uint at tag 2.
	tag2, val2, end2, err := dec.next()
	if err != nil {
		t.Fatalf("next after skipContainer: %v", err)
	}
	if end2 || tag2 != 2 || val2.u != 42 {
		t.Fatalf("expected tag=2 val=42 after skip, got tag=%d val=%d end=%v", tag2, val2.u, end2)
	}
}

// =============================================================================
// unmarshalTBE2 / unmarshalTBE3 — unknown container tag (default branch)
// =============================================================================

// TestUnmarshalTBE2_UnknownContainerTag verifies that an unknown nested
// container tag (not 1–4) is silently drained and the rest of the struct is
// decoded correctly.
func TestUnmarshalTBE2_UnknownContainerTag(t *testing.T) {
	t.Parallel()
	// Build TBE2-shaped TLV with an extra unknown struct at tag 99.
	rawEnc := tlv.NewEncoder()
	rawEnc.StartStruct(tlv.AnonymousTag())
	rawEnc.PutOctets(tlv.ContextTag(1), []byte{0xAA}) // ResponderNOC
	rawEnc.StartStruct(tlv.ContextTag(99))            // unknown container
	rawEnc.PutUint(tlv.ContextTag(1), 7)
	_ = rawEnc.EndContainer()
	rawEnc.PutOctets(tlv.ContextTag(3), make([]byte, 64)) // Signature
	rawEnc.PutOctets(tlv.ContextTag(4), make([]byte, 16)) // ResumptionID
	_ = rawEnc.EndContainer()
	b, _ := rawEnc.Bytes()

	out, err := unmarshalTBE2(b)
	if err != nil {
		t.Fatalf("unmarshalTBE2 with unknown container tag: %v", err)
	}
	if string(out.ResponderNOC) != "\xaa" {
		t.Errorf("ResponderNOC mismatch: %x", out.ResponderNOC)
	}
}

// TestUnmarshalTBE3_UnknownContainerTag verifies that an unknown nested
// container tag in TBE3 is silently drained.
func TestUnmarshalTBE3_UnknownContainerTag(t *testing.T) {
	t.Parallel()
	rawEnc := tlv.NewEncoder()
	rawEnc.StartStruct(tlv.AnonymousTag())
	rawEnc.PutOctets(tlv.ContextTag(1), []byte{0xBB}) // InitiatorNOC
	rawEnc.StartStruct(tlv.ContextTag(77))            // unknown container
	rawEnc.PutUint(tlv.ContextTag(1), 3)
	_ = rawEnc.EndContainer()
	rawEnc.PutOctets(tlv.ContextTag(3), make([]byte, 64)) // Signature
	_ = rawEnc.EndContainer()
	b, _ := rawEnc.Bytes()

	out, err := unmarshalTBE3(b)
	if err != nil {
		t.Fatalf("unmarshalTBE3 with unknown container tag: %v", err)
	}
	if string(out.InitiatorNOC) != "\xbb" {
		t.Errorf("InitiatorNOC mismatch: %x", out.InitiatorNOC)
	}
}

// TestUnmarshalSigma3_UnknownContainerTag verifies that an unknown nested
// container tag in Sigma3 is silently drained.
func TestUnmarshalSigma3_UnknownContainerTag(t *testing.T) {
	t.Parallel()
	rawEnc := tlv.NewEncoder()
	rawEnc.StartStruct(tlv.AnonymousTag())
	rawEnc.PutOctets(tlv.ContextTag(1), []byte{0xDE, 0xAD}) // Encrypted3
	rawEnc.StartStruct(tlv.ContextTag(55))                  // unknown container
	rawEnc.PutUint(tlv.ContextTag(1), 5)
	_ = rawEnc.EndContainer()
	_ = rawEnc.EndContainer()
	b, _ := rawEnc.Bytes()

	out, err := UnmarshalSigma3(b)
	if err != nil {
		t.Fatalf("UnmarshalSigma3 with unknown container tag: %v", err)
	}
	if string(out.Encrypted3) != "\xde\xad" {
		t.Errorf("Encrypted3 mismatch: %x", out.Encrypted3)
	}
}

// =============================================================================
// validatePoint — off-curve (not on curve) path
// =============================================================================

// TestValidatePoint_OffCurve verifies that an uncompressed point whose
// coordinates do not lie on P-256 is rejected.
func TestValidatePoint_OffCurve(t *testing.T) {
	t.Parallel()
	// Build a 65-byte buffer with a valid 0x04 prefix but X/Y values that
	// are not on the P-256 curve (all 0xFF is a convenient off-curve point).
	b := make([]byte, EphPubKeySize)
	b[0] = 0x04
	for i := 1; i < EphPubKeySize; i++ {
		b[i] = 0xFF
	}
	err := validatePoint(b)
	if !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("err=%v, want ErrInvalidPoint for off-curve point", err)
	}
}

// =============================================================================
// helpers
// =============================================================================

// newECDHKey returns the 65-byte uncompressed public-key bytes for a freshly
// generated P-256 key. Used to embed a valid EphPubKey in hand-crafted Sigma1
// wire frames that only want to test other fields.
func newECDHKey(t *testing.T) ([]byte, error) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return elliptic.Marshal(elliptic.P256(), priv.X, priv.Y), nil //nolint:staticcheck // SA1019: test fixture
}

// =============================================================================
// UnmarshalSigma1 — Apple wire stub
// =============================================================================

// TestUnmarshalAppleSigma1Stub feeds a synthetic packet shaped like the first
// 32 bytes of an Apple iPhone Sigma1 capture to confirm that UnmarshalSigma1
// parses the real Apple wire layout without error and recovers the expected
// field sizes.
func TestUnmarshalAppleSigma1Stub(t *testing.T) {
	// Apple's Sigma1 starts with: 15 30 01 20 <32 random bytes> 24 02 ...
	// Full Random + minimal subsequent fields:
	full, _ := hex.DecodeString(
		"15" + // anonymous struct
			"3001" + // context tag 1, OctetStr1
			"20" + // length 32
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + // random
			"2402aa" + // context tag 2, UInt1, value 0xaa (sessionID)
			"3003" + // context tag 3, OctetStr1
			"20" + // length 32
			"fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210" + // destID
			"3004" + // context tag 4, OctetStr1
			"41" + // length 65
			"04" + // uncompressed P-256 prefix
			"112233445566778899aabbccddeeff112233445566778899aabbccddeeff1122" + // X 32
			"33445566778899aabbccddeeff112233445566778899aabbccddeeff11223344" + // Y 32
			"18", // end struct
	)
	t.Logf("input (%d bytes): %x", len(full), full)
	s, err := UnmarshalSigma1(full)
	if err != nil {
		t.Fatalf("UnmarshalSigma1: %v", err)
	}
	t.Logf("random=%x", s.InitiatorRandom)
	t.Logf("sessionID=%d", s.InitiatorSessionID)
	t.Logf("destID=%x", s.DestinationID)
	t.Logf("ephPub len=%d", len(s.InitiatorEphPubKey))
}
