// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package commissioning_test

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/commissioning"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// minimalAttestationInput returns a valid AttestationInput using the test chain's DAC key.
func minimalAttestationInput(dacKey *ecdsa.PrivateKey) commissioning.AttestationInput {
	return commissioning.AttestationInput{
		CertificationDeclaration: []byte("cd-bytes"),
		AttestationNonce:         validNonce32(),
		AttestationChallenge:     validChallenge(),
		DACPrivateKey:            dacKey,
		Now:                      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestBuildAttestation_ShortNonce rejects nonces that are not 32 bytes.
func TestBuildAttestation_ShortNonce(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.AttestationNonce = make([]byte, 31)
	_, err := commissioning.BuildAttestation(in)
	if !errors.Is(err, commissioning.ErrAttestationNonce) {
		t.Fatalf("err=%v, want ErrAttestationNonce", err)
	}
}

// TestBuildAttestation_LongNonce rejects nonces longer than 32 bytes.
func TestBuildAttestation_LongNonce(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.AttestationNonce = make([]byte, 33)
	_, err := commissioning.BuildAttestation(in)
	if !errors.Is(err, commissioning.ErrAttestationNonce) {
		t.Fatalf("err=%v, want ErrAttestationNonce", err)
	}
}

// TestBuildAttestation_ShortChallenge rejects challenges that are not 16 bytes.
func TestBuildAttestation_ShortChallenge(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.AttestationChallenge = make([]byte, 15)
	_, err := commissioning.BuildAttestation(in)
	if !errors.Is(err, commissioning.ErrAttestationChallenge) {
		t.Fatalf("err=%v, want ErrAttestationChallenge", err)
	}
}

// TestBuildAttestation_LongChallenge rejects challenges longer than 16 bytes.
func TestBuildAttestation_LongChallenge(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.AttestationChallenge = make([]byte, 17)
	_, err := commissioning.BuildAttestation(in)
	if !errors.Is(err, commissioning.ErrAttestationChallenge) {
		t.Fatalf("err=%v, want ErrAttestationChallenge", err)
	}
}

// TestBuildAttestation_NilPrivateKey rejects a nil DAC private key.
func TestBuildAttestation_NilPrivateKey(t *testing.T) {
	t.Parallel()
	in := minimalAttestationInput(nil)
	in.DACPrivateKey = nil
	_, err := commissioning.BuildAttestation(in)
	if err == nil {
		t.Fatal("expected error for nil DAC key, got nil")
	}
}

// TestBuildAttestation_Success verifies Elements is non-empty and Signature is 64 bytes.
func TestBuildAttestation_Success(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)

	res, err := commissioning.BuildAttestation(in)
	if err != nil {
		t.Fatalf("BuildAttestation: %v", err)
	}
	if len(res.Elements) == 0 {
		t.Fatal("Elements is empty")
	}
	if len(res.Signature) != 64 {
		t.Fatalf("Signature length=%d, want 64", len(res.Signature))
	}
}

// TestBuildAttestation_SignatureVerifiable checks that Signature is a valid
// ECDSA-P256 signature over SHA256(elements || challenge).
func TestBuildAttestation_SignatureVerifiable(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)

	res, err := commissioning.BuildAttestation(in)
	if err != nil {
		t.Fatalf("BuildAttestation: %v", err)
	}

	h := sha256.New()
	h.Write(res.Elements)
	h.Write(in.AttestationChallenge)
	digest := h.Sum(nil)

	r := new(big.Int).SetBytes(res.Signature[:32])
	s := new(big.Int).SetBytes(res.Signature[32:])

	if !ecdsa.Verify(&tc.DACKey.PublicKey, digest, r, s) {
		t.Fatal("signature does not verify against DACPrivateKey.PublicKey")
	}
}

// TestBuildAttestation_WithFirmwareInformation verifies Tag 4 is present when
// FirmwareInformation is set.
func TestBuildAttestation_WithFirmwareInformation(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.FirmwareInformation = []byte("fw-info-blob")

	res, err := commissioning.BuildAttestation(in)
	if err != nil {
		t.Fatalf("BuildAttestation: %v", err)
	}

	if !tlvContainsContextTag(t, res.Elements, 0x04) {
		t.Fatal("TLV Elements does not contain Tag 4 (FirmwareInformation)")
	}
}

// TestBuildAttestation_WithoutFirmwareInformation verifies Tag 4 is absent when
// FirmwareInformation is empty.
func TestBuildAttestation_WithoutFirmwareInformation(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.FirmwareInformation = nil

	res, err := commissioning.BuildAttestation(in)
	if err != nil {
		t.Fatalf("BuildAttestation: %v", err)
	}

	if tlvContainsContextTag(t, res.Elements, 0x04) {
		t.Fatal("TLV Elements unexpectedly contains Tag 4 with empty FirmwareInformation")
	}
}

// TestBuildAttestation_ZeroNow uses time.Now() when Now is the zero value.
func TestBuildAttestation_ZeroNow(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalAttestationInput(tc.DACKey)
	in.Now = time.Time{} // zero → falls back to time.Now()

	before := time.Now().Unix()
	res, err := commissioning.BuildAttestation(in)
	if err != nil {
		t.Fatalf("BuildAttestation: %v", err)
	}
	after := time.Now().Unix()

	ts := tlvDecodeTimestamp(t, res.Elements)
	if ts < before || ts > after {
		t.Fatalf("embedded timestamp %d out of range [%d, %d]", ts, before, after)
	}
}

// --- TLV helpers for attestation_test.go ---

// tlvContainsContextTag decodes a flat TLV structure and reports whether
// a context tag with the given number exists anywhere in the top-level struct.
func tlvContainsContextTag(t *testing.T, data []byte, tagNumber uint8) bool {
	t.Helper()
	dec := tlv.NewDecoder(data)
	// Consume the anonymous struct open element.
	first, err := dec.Next()
	if err != nil {
		t.Fatalf("tlv.Next (struct open): %v", err)
	}
	if !first.IsContainer {
		t.Fatalf("expected struct container, got type=%v", first.Type)
	}

	for {
		el, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("tlv.Next: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(tagNumber) {
			return true
		}
	}
	return false
}

// tlvDecodeTimestamp finds the Tag 3 (timestamp) field in an attestation
// elements TLV and returns its uint64 value.
func tlvDecodeTimestamp(t *testing.T, data []byte) int64 {
	t.Helper()
	dec := tlv.NewDecoder(data)
	// Consume opening struct.
	if _, err := dec.Next(); err != nil {
		t.Fatalf("tlv.Next (struct open): %v", err)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("tlv.Next: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		// Tag 3 is the timestamp.
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 0x03 {
			return int64(el.Uint) //nolint:gosec // G115: unix-epoch seconds; safe cast
		}
	}
	t.Fatal("Tag 3 (timestamp) not found in attestation elements TLV")
	return 0
}
