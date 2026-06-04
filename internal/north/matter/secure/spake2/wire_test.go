// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package spake2_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// nonStructPayload builds a TLV payload whose top-level element is a
// boolean (not a Structure) so the Decode* functions see a wrong type.
func nonStructPayload() []byte {
	enc := tlv.NewEncoder()
	enc.PutBool(tlv.AnonymousTag(), true)
	b, _ := enc.Bytes()
	return b
}

// emptyStructPayload builds a TLV Structure with no inner fields.
func emptyStructPayload() []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	_ = enc.EndContainer()
	b, _ := enc.Bytes()
	return b
}

// TestPake1RoundTrip — encode 65-byte pA, decode, byte-equal.
func TestPake1RoundTrip(t *testing.T) {
	t.Parallel()
	pA := bytes.Repeat([]byte{0xAA}, spake2.PointSize)
	// pA must look like a valid uncompressed point prefix for DecodesPake1
	// to accept it. However wire_test only checks wire shape, not curve
	// validity — DecodePake1 only checks *length*, not curve membership.
	// So any 65-byte slice is sufficient.
	payload := spake2.EncodePake1(pA)
	got, err := spake2.DecodePake1(payload)
	if err != nil {
		t.Fatalf("DecodePake1: %v", err)
	}
	if !bytes.Equal(got, pA) {
		t.Fatalf("round-trip mismatch: got %x, want %x", got, pA)
	}
}

// TestPake1WrongLength — encode 64 bytes, decode → ErrWireMalformed.
func TestPake1WrongLength(t *testing.T) {
	t.Parallel()
	pA := bytes.Repeat([]byte{0xBB}, spake2.PointSize-1) // 64 bytes
	payload := spake2.EncodePake1(pA)
	_, err := spake2.DecodePake1(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake1NonStructTop — top-level bool → ErrWireMalformed.
func TestPake1NonStructTop(t *testing.T) {
	t.Parallel()
	_, err := spake2.DecodePake1(nonStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake1MissingField — empty struct → ErrWireMalformed (missing field).
func TestPake1MissingField(t *testing.T) {
	t.Parallel()
	_, err := spake2.DecodePake1(emptyStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake1TruncatedPayload — bare struct-start byte → ErrWireMalformed.
func TestPake1TruncatedPayload(t *testing.T) {
	t.Parallel()
	// 0x15 is the control byte for an anonymous Structure with no body.
	_, err := spake2.DecodePake1([]byte{0x15})
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake1DefensiveCopy — mutate the payload after decode; decoded
// slice must be unchanged.
func TestPake1DefensiveCopy(t *testing.T) {
	t.Parallel()
	pA := bytes.Repeat([]byte{0xCC}, spake2.PointSize)
	payload := spake2.EncodePake1(pA)
	got, err := spake2.DecodePake1(payload)
	if err != nil {
		t.Fatalf("DecodePake1: %v", err)
	}
	// Save a snapshot before poisoning the source buffer.
	want := append([]byte(nil), got...)
	// Poison the encoded payload in-place.
	for i := range payload {
		payload[i] = 0xFF
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded slice changed after payload mutation: got %x, want %x", got, want)
	}
}

// TestPake2RoundTrip — encode Pake2Output, decode; both fields match.
func TestPake2RoundTrip(t *testing.T) {
	t.Parallel()
	out := &spake2.Pake2Output{
		Y:  bytes.Repeat([]byte{0x11}, spake2.PointSize),
		CB: bytes.Repeat([]byte{0x22}, spake2.ConfirmTagSize),
	}
	payload := spake2.EncodePake2(out)
	pB, cB, err := spake2.DecodePake2(payload)
	if err != nil {
		t.Fatalf("DecodePake2: %v", err)
	}
	if !bytes.Equal(pB, out.Y) {
		t.Fatalf("pB mismatch: got %x, want %x", pB, out.Y)
	}
	if !bytes.Equal(cB, out.CB) {
		t.Fatalf("cB mismatch: got %x, want %x", cB, out.CB)
	}
}

// TestPake2WrongPBLength — encode with Y of 64 bytes → ErrWireMalformed.
func TestPake2WrongPBLength(t *testing.T) {
	t.Parallel()
	out := &spake2.Pake2Output{
		Y:  bytes.Repeat([]byte{0x33}, spake2.PointSize-1), // 64 bytes
		CB: bytes.Repeat([]byte{0x44}, spake2.ConfirmTagSize),
	}
	payload := spake2.EncodePake2(out)
	_, _, err := spake2.DecodePake2(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake2WrongCBLength — encode with CB of 31 bytes → ErrWireMalformed.
func TestPake2WrongCBLength(t *testing.T) {
	t.Parallel()
	out := &spake2.Pake2Output{
		Y:  bytes.Repeat([]byte{0x55}, spake2.PointSize),
		CB: bytes.Repeat([]byte{0x66}, spake2.ConfirmTagSize-1), // 31 bytes
	}
	payload := spake2.EncodePake2(out)
	_, _, err := spake2.DecodePake2(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake3RoundTrip — encode 32-byte cA, decode, equal.
func TestPake3RoundTrip(t *testing.T) {
	t.Parallel()
	cA := bytes.Repeat([]byte{0x77}, spake2.ConfirmTagSize)
	payload := spake2.EncodePake3(cA)
	got, err := spake2.DecodePake3(payload)
	if err != nil {
		t.Fatalf("DecodePake3: %v", err)
	}
	if !bytes.Equal(got, cA) {
		t.Fatalf("round-trip mismatch: got %x, want %x", got, cA)
	}
}

// TestPake3WrongLength — encode 31 bytes → ErrWireMalformed.
func TestPake3WrongLength(t *testing.T) {
	t.Parallel()
	cA := bytes.Repeat([]byte{0x88}, spake2.ConfirmTagSize-1) // 31 bytes
	payload := spake2.EncodePake3(cA)
	_, err := spake2.DecodePake3(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPake3MissingField — empty struct → ErrWireMalformed.
func TestPake3MissingField(t *testing.T) {
	t.Parallel()
	_, err := spake2.DecodePake3(emptyStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// random32 returns a deterministic 32-byte slice used as a stand-in for
// InitiatorRandom / ResponderRandom in PBKDF tests.
func random32() []byte {
	b := make([]byte, spake2.PBKDFRandomSize)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}

// buildPBKDFParamRequestPayload builds a raw TLV PBKDFParamRequest
// payload using the encoder API (no production EncodePBKDFParamRequest
// helper exists — only the decode side is public for this message).
func buildPBKDFParamRequestPayload(rand32 []byte, sessionID, passcodeID uint16, hasPBKDF bool) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), rand32)
	enc.PutUint(tlv.ContextTag(2), uint64(sessionID))
	enc.PutUint(tlv.ContextTag(3), uint64(passcodeID))
	enc.PutBool(tlv.ContextTag(4), hasPBKDF)
	_ = enc.EndContainer()
	b, _ := enc.Bytes()
	return b
}

// TestPBKDFParamRequestRoundTrip — build a Request struct manually
// via TLV, decode, verify all fields match.
func TestPBKDFParamRequestRoundTrip(t *testing.T) {
	t.Parallel()
	rand32 := random32()
	payload := buildPBKDFParamRequestPayload(rand32, 7, 0, true)
	req, err := spake2.DecodePBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("DecodePBKDFParamRequest: %v", err)
	}
	if !bytes.Equal(req.InitiatorRandom, rand32) {
		t.Fatalf("InitiatorRandom mismatch: got %x, want %x", req.InitiatorRandom, rand32)
	}
	if req.InitiatorSessionID != 7 {
		t.Fatalf("InitiatorSessionID = %d, want 7", req.InitiatorSessionID)
	}
	if req.PasscodeID != 0 {
		t.Fatalf("PasscodeID = %d, want 0", req.PasscodeID)
	}
	if !req.HasPBKDFParameters {
		t.Fatal("HasPBKDFParameters = false, want true")
	}
}

// TestPBKDFParamRequestNonStructTop — a top-level boolean element
// must produce ErrWireMalformed.
func TestPBKDFParamRequestNonStructTop(t *testing.T) {
	t.Parallel()
	_, err := spake2.DecodePBKDFParamRequest(nonStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPBKDFParamRequestWrongInitiatorRandomLength — InitiatorRandom of
// 31 bytes (one short) must produce ErrWireMalformed.
func TestPBKDFParamRequestWrongInitiatorRandomLength(t *testing.T) {
	t.Parallel()
	short := bytes.Repeat([]byte{0xAB}, spake2.PBKDFRandomSize-1) // 31 bytes
	payload := buildPBKDFParamRequestPayload(short, 0, 0, false)
	_, err := spake2.DecodePBKDFParamRequest(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPBKDFParamRequestSkipsUnknownStructField — a payload that includes
// a stray nested Structure at tag 5 (MRPParams slot) must decode
// successfully with no error surfaced.
func TestPBKDFParamRequestSkipsUnknownStructField(t *testing.T) {
	t.Parallel()
	rand32 := random32()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), rand32)
	enc.PutUint(tlv.ContextTag(2), 42)
	enc.PutUint(tlv.ContextTag(3), 0)
	enc.PutBool(tlv.ContextTag(4), false)
	// tag 5 = optional MRPParams nested Structure
	enc.StartStruct(tlv.ContextTag(5))
	enc.PutOctets(tlv.ContextTag(1), []byte{0xDE, 0xAD})
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	payload, _ := enc.Bytes()

	req, err := spake2.DecodePBKDFParamRequest(payload)
	if err != nil {
		t.Fatalf("DecodePBKDFParamRequest with tag-5 struct: %v", err)
	}
	if !bytes.Equal(req.InitiatorRandom, rand32) {
		t.Fatalf("InitiatorRandom mismatch after skip: got %x, want %x", req.InitiatorRandom, rand32)
	}
}

// TestPBKDFParamResponseRoundTrip — marshal a full Response that
// includes PBKDFParameters, decode it, and assert every field.
func TestPBKDFParamResponseRoundTrip(t *testing.T) {
	t.Parallel()
	initRand := random32()
	respRand := bytes.Repeat([]byte{0xFF}, spake2.PBKDFRandomSize)
	salt := []byte("salt-16-bytes!!!")
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    initRand,
		ResponderRandom:    respRand,
		ResponderSessionID: 7,
		Parameters: &spake2.PBKDFParameters{
			Iterations: 1000,
			Salt:       salt,
		},
	}
	wire := resp.Marshal()
	got, err := spake2.DecodePBKDFParamResponse(wire)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if !bytes.Equal(got.InitiatorRandom, initRand) {
		t.Fatalf("InitiatorRandom mismatch: got %x, want %x", got.InitiatorRandom, initRand)
	}
	if !bytes.Equal(got.ResponderRandom, respRand) {
		t.Fatalf("ResponderRandom mismatch: got %x, want %x", got.ResponderRandom, respRand)
	}
	if got.ResponderSessionID != 7 {
		t.Fatalf("ResponderSessionID = %d, want 7", got.ResponderSessionID)
	}
	if got.Parameters == nil {
		t.Fatal("Parameters is nil, want non-nil")
	}
	if got.Parameters.Iterations != 1000 {
		t.Fatalf("Iterations = %d, want 1000", got.Parameters.Iterations)
	}
	if !bytes.Equal(got.Parameters.Salt, salt) {
		t.Fatalf("Salt mismatch: got %x, want %x", got.Parameters.Salt, salt)
	}
}

// TestPBKDFParamResponseNoParameters — a Response with nil Parameters
// marshals without the nested struct; re-decoding must yield nil Parameters.
func TestPBKDFParamResponseNoParameters(t *testing.T) {
	t.Parallel()
	initRand := random32()
	respRand := bytes.Repeat([]byte{0x01}, spake2.PBKDFRandomSize)
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    initRand,
		ResponderRandom:    respRand,
		ResponderSessionID: 3,
		Parameters:         nil,
	}
	wire := resp.Marshal()
	got, err := spake2.DecodePBKDFParamResponse(wire)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if got.Parameters != nil {
		t.Fatalf("Parameters = %+v, want nil", got.Parameters)
	}
}

// TestPBKDFParamResponseWrongResponderRandomLength — encoding a Response
// with a 31-byte ResponderRandom and decoding it must return ErrWireMalformed.
func TestPBKDFParamResponseWrongResponderRandomLength(t *testing.T) {
	t.Parallel()
	initRand := random32()
	shortRand := bytes.Repeat([]byte{0xBB}, spake2.PBKDFRandomSize-1) // 31 bytes
	// Build raw TLV manually so we can supply an undersized ResponderRandom.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), initRand)
	enc.PutOctets(tlv.ContextTag(2), shortRand)
	enc.PutUint(tlv.ContextTag(3), 0)
	_ = enc.EndContainer()
	wire, _ := enc.Bytes()

	_, err := spake2.DecodePBKDFParamResponse(wire)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPBKDFParamResponseDefensiveSaltCopy — mutating the source payload
// after decode must not affect the decoded Salt slice (appendOctets
// copies on every read). Use a 16-byte salt (smallest spec-valid
// length per Matter §3.10.3) so the decoder accepts it.
func TestPBKDFParamResponseDefensiveSaltCopy(t *testing.T) {
	t.Parallel()
	initRand := random32()
	respRand := bytes.Repeat([]byte{0x02}, spake2.PBKDFRandomSize)
	salt := make([]byte, spake2.PBKDFMinSaltSize)
	for i := range salt {
		salt[i] = 0xAA
	}
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    initRand,
		ResponderRandom:    respRand,
		ResponderSessionID: 1,
		Parameters: &spake2.PBKDFParameters{
			Iterations: spake2.PBKDFMinIterations,
			Salt:       salt,
		},
	}
	wire := resp.Marshal()
	got, err := spake2.DecodePBKDFParamResponse(wire)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	wantSalt := append([]byte(nil), got.Parameters.Salt...)

	// Poison: flip any 0xAA byte in the encoded wire buffer.
	for i, b := range wire {
		if b == 0xAA {
			wire[i] = 0x00
		}
	}
	if !bytes.Equal(got.Parameters.Salt, wantSalt) {
		t.Fatalf("Salt changed after payload mutation: got %x, want %x", got.Parameters.Salt, wantSalt)
	}
}

// TestPBKDFParamResponse_MarshalWithMRPParams — Marshal must include the
// ResponderMRPParams nested struct when the field is non-nil, and each
// optional sub-field must appear in the output. DecodePBKDFParamResponse
// consumes the response and must reconstruct every pointer field.
func TestPBKDFParamResponse_MarshalWithMRPParams(t *testing.T) {
	t.Parallel()
	initRand := random32()
	respRand := bytes.Repeat([]byte{0xAB}, spake2.PBKDFRandomSize)
	salt := bytes.Repeat([]byte{0x55}, spake2.PBKDFMinSaltSize)
	idleMs := uint16(500)
	activeMs := uint16(300)
	thresholdMs := uint16(4000)
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    initRand,
		ResponderRandom:    respRand,
		ResponderSessionID: 42,
		Parameters: &spake2.PBKDFParameters{
			Iterations: spake2.PBKDFMinIterations,
			Salt:       salt,
		},
		ResponderMRPParams: &spake2.MRPParameters{
			IdleRetransTimeoutMs:   &idleMs,
			ActiveRetransTimeoutMs: &activeMs,
			ActiveThresholdTimeMs:  &thresholdMs,
		},
	}
	wire := resp.Marshal()
	got, err := spake2.DecodePBKDFParamResponse(wire)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if got.ResponderMRPParams == nil {
		t.Fatal("ResponderMRPParams is nil after decode, want non-nil")
	}
	if got.ResponderMRPParams.IdleRetransTimeoutMs == nil || *got.ResponderMRPParams.IdleRetransTimeoutMs != idleMs {
		t.Errorf("IdleRetransTimeoutMs = %v, want %d", got.ResponderMRPParams.IdleRetransTimeoutMs, idleMs)
	}
	if got.ResponderMRPParams.ActiveRetransTimeoutMs == nil || *got.ResponderMRPParams.ActiveRetransTimeoutMs != activeMs {
		t.Errorf("ActiveRetransTimeoutMs = %v, want %d", got.ResponderMRPParams.ActiveRetransTimeoutMs, activeMs)
	}
	if got.ResponderMRPParams.ActiveThresholdTimeMs == nil || *got.ResponderMRPParams.ActiveThresholdTimeMs != thresholdMs {
		t.Errorf("ActiveThresholdTimeMs = %v, want %d", got.ResponderMRPParams.ActiveThresholdTimeMs, thresholdMs)
	}
}

// TestPBKDFParamResponse_MarshalWithPartialMRPParams — when only some
// optional MRPParameters fields are set, the others must be nil after
// the decode round-trip (absent from wire → pointer remains nil).
func TestPBKDFParamResponse_MarshalWithPartialMRPParams(t *testing.T) {
	t.Parallel()
	initRand := random32()
	respRand := bytes.Repeat([]byte{0xCC}, spake2.PBKDFRandomSize)
	idleMs := uint16(200)
	resp := spake2.PBKDFParamResponse{
		InitiatorRandom:    initRand,
		ResponderRandom:    respRand,
		ResponderSessionID: 5,
		ResponderMRPParams: &spake2.MRPParameters{
			IdleRetransTimeoutMs: &idleMs,
			// ActiveRetransTimeoutMs and ActiveThresholdTimeMs absent.
		},
	}
	// Note: Parameters is nil so the decode will fail the mandatory random
	// length check — we must add Parameters to make the payload valid.
	salt := bytes.Repeat([]byte{0x11}, spake2.PBKDFMinSaltSize)
	resp.Parameters = &spake2.PBKDFParameters{
		Iterations: spake2.PBKDFMinIterations,
		Salt:       salt,
	}
	wire := resp.Marshal()
	got, err := spake2.DecodePBKDFParamResponse(wire)
	if err != nil {
		t.Fatalf("DecodePBKDFParamResponse: %v", err)
	}
	if got.ResponderMRPParams == nil {
		t.Fatal("ResponderMRPParams is nil")
	}
	if got.ResponderMRPParams.IdleRetransTimeoutMs == nil || *got.ResponderMRPParams.IdleRetransTimeoutMs != idleMs {
		t.Errorf("IdleRetransTimeoutMs = %v, want %d", got.ResponderMRPParams.IdleRetransTimeoutMs, idleMs)
	}
	if got.ResponderMRPParams.ActiveRetransTimeoutMs != nil {
		t.Errorf("ActiveRetransTimeoutMs = %v, want nil", got.ResponderMRPParams.ActiveRetransTimeoutMs)
	}
	if got.ResponderMRPParams.ActiveThresholdTimeMs != nil {
		t.Errorf("ActiveThresholdTimeMs = %v, want nil", got.ResponderMRPParams.ActiveThresholdTimeMs)
	}
}

// TestPBKDFParamResponseNonStructTop — top-level non-struct → ErrWireMalformed.
func TestPBKDFParamResponseNonStructTop(t *testing.T) {
	t.Parallel()
	_, err := spake2.DecodePBKDFParamResponse(nonStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestPBKDFParamResponseWrongInitiatorRandomLength — InitiatorRandom of
// 31 bytes must produce ErrWireMalformed.
func TestPBKDFParamResponseWrongInitiatorRandomLength(t *testing.T) {
	t.Parallel()
	shortRand := bytes.Repeat([]byte{0xAA}, spake2.PBKDFRandomSize-1)
	respRand := random32()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), shortRand)
	enc.PutOctets(tlv.ContextTag(2), respRand)
	enc.PutUint(tlv.ContextTag(3), 0)
	_ = enc.EndContainer()
	wire, _ := enc.Bytes()
	_, err := spake2.DecodePBKDFParamResponse(wire)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestDecodePake2_TruncatedAfterStructureOpen — a payload that opens a
// Structure but is immediately truncated (no inner elements, no
// EndContainer) must return ErrWireMalformed from the inner Next() call.
func TestDecodePake2_TruncatedAfterStructureOpen(t *testing.T) {
	t.Parallel()
	// 0x15 is the control byte for an anonymous Structure open; no body follows.
	_, _, err := spake2.DecodePake2([]byte{0x15})
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestDecodePake2_NonStructTop — a boolean top-level element must return
// ErrWireMalformed.
func TestDecodePake2_NonStructTop(t *testing.T) {
	t.Parallel()
	_, _, err := spake2.DecodePake2(nonStructPayload())
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed", err)
	}
}

// TestDecodePake2_MissingCBField — pB present, cB absent → ErrWireMalformed.
func TestDecodePake2_MissingCBField(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), bytes.Repeat([]byte{0xAA}, spake2.PointSize))
	_ = enc.EndContainer()
	payload, _ := enc.Bytes()
	_, _, err := spake2.DecodePake2(payload)
	if !errors.Is(err, spake2.ErrWireMalformed) {
		t.Fatalf("err = %v, want ErrWireMalformed (missing cB)", err)
	}
}

// TestDecodePBKDFParameters_TruncatedPayload — if the nested
// PBKDFParameters structure is opened but the decoder runs out of
// bytes, decodePBKDFParameters (called from DecodePBKDFParamResponse)
// must surface ErrWireMalformed.
func TestDecodePBKDFParameters_TruncatedPayload(t *testing.T) {
	t.Parallel()
	// Build a PBKDFParamResponse that embeds a PBKDFParameters
	// struct that starts but never ends (truncated). We do this by
	// hand-crafting the TLV rather than via the encoder, since the
	// encoder always emits valid EndContainers.
	//
	// Layout: AnonymousStruct { tag1=InitiatorRandom, tag2=ResponderRandom,
	//   tag3=SessionID, tag4=PBKDFParameters-struct-start (no EndContainer) }
	initRand := random32()
	respRand := bytes.Repeat([]byte{0xBB}, spake2.PBKDFRandomSize)
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), initRand)
	enc.PutOctets(tlv.ContextTag(2), respRand)
	enc.PutUint(tlv.ContextTag(3), 42)
	// Nest an inner struct for tag 4 (PBKDFParameters), then truncate
	// by not closing it and also not closing the outer struct.
	enc.StartStruct(tlv.ContextTag(4))
	// Do NOT call EndContainer — leave the inner struct open.
	wire, _ := enc.Bytes()
	// Truncate at the current end so the decoder will hit EOF mid-parse.
	_, err := spake2.DecodePBKDFParamResponse(wire)
	// We expect either an error (any error will do, since the TLV is malformed)
	// or a malformed error specifically.
	if err == nil {
		t.Fatal("expected error for truncated PBKDFParameters, got nil")
	}
}

// TestPBKDFParameters_ValidateBounds — Validate accepts the spec
// boundaries and rejects out-of-range values. Decoder rejects via
// ErrWireMalformed when peer-supplied params exceed Matter §3.10.3.
func TestPBKDFParameters_ValidateBounds(t *testing.T) {
	t.Parallel()
	good := bytes.Repeat([]byte{0x55}, spake2.PBKDFMinSaltSize)
	cases := []struct {
		name string
		p    spake2.PBKDFParameters
		ok   bool
	}{
		{"min ok", spake2.PBKDFParameters{Iterations: spake2.PBKDFMinIterations, Salt: good}, true},
		{"max ok", spake2.PBKDFParameters{Iterations: spake2.PBKDFMaxIterations, Salt: bytes.Repeat([]byte{0x66}, spake2.PBKDFMaxSaltSize)}, true},
		{"iterations too low", spake2.PBKDFParameters{Iterations: spake2.PBKDFMinIterations - 1, Salt: good}, false},
		{"iterations too high", spake2.PBKDFParameters{Iterations: spake2.PBKDFMaxIterations + 1, Salt: good}, false},
		{"salt too short", spake2.PBKDFParameters{Iterations: spake2.PBKDFMinIterations, Salt: bytes.Repeat([]byte{0x77}, spake2.PBKDFMinSaltSize-1)}, false},
		{"salt too long", spake2.PBKDFParameters{Iterations: spake2.PBKDFMinIterations, Salt: bytes.Repeat([]byte{0x88}, spake2.PBKDFMaxSaltSize+1)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.p.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate: unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("Validate: want error, got nil")
			}
		})
	}
}
