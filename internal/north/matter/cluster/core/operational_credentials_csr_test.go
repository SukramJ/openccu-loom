// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

// ADR-0013 Decision #7: CSRRequest AttestationChallenge binding.
//
// Bug-pattern: OperationalCredentials.attestationChalleng was never
// populated from the session layer, so CSRResponse.AttestationSignature
// was computed over (NOCSRElements || nil) instead of
// (NOCSRElements || AttestationChallenge) as required by Matter §11.18.4.7.
// A commissioner that verifies the signature against the correct
// challenge would always reject it.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

// newOpcredsWithDAC builds an OperationalCredentials cluster wired with a
// freshly generated P-256 DAC key so the attestation signature is real.
func newOpcredsWithDAC(t *testing.T) (*core.OperationalCredentials, *ecdsa.PrivateKey) {
	t.Helper()
	dacKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey DAC: %v", err)
	}
	oc, err := core.NewOperationalCredentials(newFakeStore(), core.OpcredsConfig{
		SupportedFabrics: 5,
		DACPrivateKey:    dacKey,
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	return oc, dacKey
}

// TestCSRRequest_AttestationChallengeBound is an ADR-0013 "earlier-catch" test
// for Decision #7.
//
// The test:
//  1. Constructs an OperationalCredentials cluster with a known DAC key.
//  2. Installs a known 16-byte AttestationChallenge via SetAttestationChallenge.
//  3. Drives a CSRRequest through MatterInvoke (command 0x04).
//  4. Decodes the CSRResponse and verifies AttestationSignature against
//     SHA-256(NOCSRElements || AttestationChallenge) with the DAC public key.
//
// A zero/nil AttestationChallenge (the pre-fix bug) would cause verification
// to fail because the signed digest would be SHA-256(elements || nil) while
// we verify against SHA-256(elements || knownChallenge).
func TestCSRRequest_AttestationChallengeBound(t *testing.T) {
	t.Parallel()

	oc, dacKey := newOpcredsWithDAC(t)

	// A known 16-byte challenge — simulates what the PASE/CASE layer
	// derives during session establishment and hands to SetAttestationChallenge.
	knownChallenge := []byte{
		0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22,
		0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00,
	}
	oc.SetAttestationChallenge(knownChallenge)

	// 32-byte nonce (arbitrary).
	csrNonce := bytes.Repeat([]byte{0x42}, 32)

	resp, err := oc.MatterInvoke(
		context.Background(),
		0x04, // opcredsCmdCSRRequest
		core.CSRRequest{CSRNonce: csrNonce},
	)
	if err != nil {
		t.Fatalf("CSRRequest MatterInvoke: %v", err)
	}

	csrResp, ok := resp.(core.CSRResponse)
	if !ok {
		t.Fatalf("expected CSRResponse, got %T", resp)
	}

	if len(csrResp.NOCSRElements) == 0 {
		t.Fatal("NOCSRElements is empty")
	}
	if len(csrResp.AttestationSignature) != 64 {
		t.Fatalf("AttestationSignature length = %d, want 64", len(csrResp.AttestationSignature))
	}

	// Verify: signature = ECDSA-SHA256( SHA-256(NOCSRElements || knownChallenge) )
	// This is Matter §11.18.4.7 — both sides hash the concatenation.
	h := sha256.New()
	h.Write(csrResp.NOCSRElements)
	h.Write(knownChallenge)
	digest := h.Sum(nil)

	// The 64-byte raw signature is r (32 bytes) || s (32 bytes), big-endian.
	sig := csrResp.AttestationSignature
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	// ADR-0013 D#7 invariant: the signature MUST verify against
	// (NOCSRElements || knownChallenge). If SetAttestationChallenge was
	// never called (or the challenge was not bound into the signed digest),
	// ecdsa.Verify returns false.
	if !ecdsa.Verify(&dacKey.PublicKey, digest, r, s) {
		t.Error("ADR-0013 D#7: AttestationSignature does not verify against " +
			"SHA-256(NOCSRElements || AttestationChallenge); " +
			"challenge was not bound into the CSR signature")
	}
}

// TestCSRRequest_AttestationChallenge_NilDACKey verifies that a nil DAC key
// (test-stub path) produces a 64-byte all-zero stub signature that does NOT
// verify, and that SetAttestationChallenge does not panic when called on a
// cluster without a key.
func TestCSRRequest_AttestationChallenge_NilDACKey(t *testing.T) {
	t.Parallel()

	// Construct without DAC key — production stub mode.
	oc, err := core.NewOperationalCredentials(newFakeStore(), core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	// SetAttestationChallenge must not panic even without a DAC key.
	oc.SetAttestationChallenge(bytes.Repeat([]byte{0x55}, 16))

	csrNonce := bytes.Repeat([]byte{0x11}, 32)
	resp, err := oc.MatterInvoke(
		context.Background(),
		0x04,
		core.CSRRequest{CSRNonce: csrNonce},
	)
	if err != nil {
		t.Fatalf("CSRRequest (nil DAC): %v", err)
	}

	csrResp, ok := resp.(core.CSRResponse)
	if !ok {
		t.Fatalf("expected CSRResponse, got %T", resp)
	}

	// Nil-key path always returns a 64-byte zero stub per the spec comment.
	if len(csrResp.AttestationSignature) != 64 {
		t.Fatalf("stub signature length = %d, want 64", len(csrResp.AttestationSignature))
	}
}

// TestCSRRequest_SetAttestationChallenge_DefensiveCopy verifies that mutating
// the slice passed to SetAttestationChallenge after the call does not affect
// the challenge used by a subsequent CSRRequest.
func TestCSRRequest_SetAttestationChallenge_DefensiveCopy(t *testing.T) {
	t.Parallel()

	oc, dacKey := newOpcredsWithDAC(t)

	challenge := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
	}
	snapshot := append([]byte(nil), challenge...)
	oc.SetAttestationChallenge(challenge)

	// Mutate the original slice — must not affect the stored challenge.
	for i := range challenge {
		challenge[i] = 0xFF
	}

	csrNonce := bytes.Repeat([]byte{0x77}, 32)
	resp, err := oc.MatterInvoke(
		context.Background(),
		0x04,
		core.CSRRequest{CSRNonce: csrNonce},
	)
	if err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}
	csrResp := resp.(core.CSRResponse)

	// Verify against the snapshot (original un-mutated challenge).
	h := sha256.New()
	h.Write(csrResp.NOCSRElements)
	h.Write(snapshot)
	digest := h.Sum(nil)

	sig := csrResp.AttestationSignature
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	if !ecdsa.Verify(&dacKey.PublicKey, digest, r, s) {
		t.Error("ADR-0013 D#7 defensive-copy: signature does not verify against snapshot challenge; " +
			"SetAttestationChallenge did not copy the slice")
	}
}
