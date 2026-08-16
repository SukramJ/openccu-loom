// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package spake2

import (
	"bytes"
	"crypto/elliptic"
	"errors"
	"testing"
)

// verifierBytesFor serialises a VerifierContext into the Matter §3.10.5
// PAKE passcode verifier wire form: w0 (32-byte scalar) || L (65-byte
// uncompressed P-256 point). It mirrors how a commissioner would encode
// the verifier it computed from a chosen passcode before handing it to
// the device in OpenCommissioningWindow.
func verifierBytesFor(vc *VerifierContext) (w0Bytes, lBytes []byte) {
	w0Bytes = vc.W0.FillBytes(make([]byte, VerifierW0Size))
	lBytes = elliptic.Marshal(curve(), vc.L.X, vc.L.Y) //nolint:staticcheck // SA1019: matches curve() usage elsewhere in this package
	return w0Bytes, lBytes
}

// TestNewVerifierFromValue_RoundTripDerivesSameKe is the C6 correctness
// assertion: a verifier reconstructed from the (w0, L) wire bytes runs a
// full PASE exchange against a prover holding the ORIGINAL passcode and
// derives the same session key Ke. This is exactly the Enhanced
// Commissioning Window path — the commissioner computes the verifier from
// a passcode it chose, the device rebuilds it from (w0, L) and never sees
// the passcode, yet PASE against that passcode succeeds.
func TestNewVerifierFromValue_RoundTripDerivesSameKe(t *testing.T) {
	salt := testSalt()
	orig, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	w0Bytes, lBytes := verifierBytesFor(orig)

	vc, err := NewVerifierFromValue(w0Bytes, lBytes)
	if err != nil {
		t.Fatalf("NewVerifierFromValue: %v", err)
	}
	// Re-encode the reconstructed context and compare wire bytes (routes
	// the coordinate access through verifierBytesFor's marshal) — a clean
	// structural equality check without touching the deprecated raw
	// ecdsa.PublicKey coordinates directly.
	w0Back, lBack := verifierBytesFor(vc)
	if !bytes.Equal(w0Back, w0Bytes) {
		t.Fatalf("w0 mismatch after round-trip:\n got  =% X\n want =% X", w0Back, w0Bytes)
	}
	if !bytes.Equal(lBack, lBytes) {
		t.Fatalf("L mismatch after round-trip:\n got  =% X\n want =% X", lBack, lBytes)
	}

	// Full PASE handshake: prover(passcode) <-> verifier(from-value).
	prover, err := NewProver(testPasscode, salt, testIterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	verifier := NewVerifier(vc, nil, nil, nil)
	pake2, err := verifier.ProcessPake1(pA)
	if err != nil {
		t.Fatalf("ProcessPake1: %v", err)
	}
	cA, err := prover.ProcessPake2(pake2.Y, pake2.CB)
	if err != nil {
		t.Fatalf("ProcessPake2: %v", err)
	}
	if err := verifier.ProcessPake3(cA); err != nil {
		t.Fatalf("ProcessPake3: %v", err)
	}
	if got, want := verifier.SharedSecret(), prover.SharedSecret(); !bytes.Equal(got, want) {
		t.Fatalf("Ke mismatch:\n verifier=% X\n prover  =% X", got, want)
	}
}

// TestNewVerifierFromValue_WrongPasscodeFails confirms the reconstructed
// verifier still rejects a prover with the wrong passcode (the confirmation
// step fails), i.e. the from-value path preserves the security property.
func TestNewVerifierFromValue_WrongPasscodeFails(t *testing.T) {
	salt := testSalt()
	orig, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	w0Bytes, lBytes := verifierBytesFor(orig)
	vc, err := NewVerifierFromValue(w0Bytes, lBytes)
	if err != nil {
		t.Fatalf("NewVerifierFromValue: %v", err)
	}

	prover, err := NewProver(99999999, salt, testIterations, nil, nil, nil) // wrong passcode
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	verifier := NewVerifier(vc, nil, nil, nil)
	pake2, err := verifier.ProcessPake1(pA)
	if err != nil {
		t.Fatalf("ProcessPake1: %v", err)
	}
	if _, err := prover.ProcessPake2(pake2.Y, pake2.CB); err == nil {
		t.Fatal("wrong-passcode prover must fail Pake2 confirmation, got nil error")
	}
}

func TestNewVerifierFromValue_RejectsMalformedInput(t *testing.T) {
	salt := testSalt()
	orig, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	w0Bytes, lBytes := verifierBytesFor(orig)

	t.Run("short w0", func(t *testing.T) {
		if _, err := NewVerifierFromValue(w0Bytes[:VerifierW0Size-1], lBytes); !errors.Is(err, ErrInvalidPasscode) {
			t.Fatalf("short w0: err = %v, want ErrInvalidPasscode", err)
		}
	})
	t.Run("short L", func(t *testing.T) {
		if _, err := NewVerifierFromValue(w0Bytes, lBytes[:VerifierLSize-1]); !errors.Is(err, ErrInvalidPoint) {
			t.Fatalf("short L: err = %v, want ErrInvalidPoint", err)
		}
	})
	t.Run("off-curve L", func(t *testing.T) {
		bad := append([]byte(nil), lBytes...)
		bad[VerifierLSize-1] ^= 0xFF // corrupt Y so the point is no longer on P-256
		if _, err := NewVerifierFromValue(w0Bytes, bad); !errors.Is(err, ErrInvalidPoint) {
			t.Fatalf("off-curve L: err = %v, want ErrInvalidPoint", err)
		}
	})
}

// TestNewVerifierFromValue_RejectsDegenerateW0 pins the guard against a
// commissioner-supplied verifier whose w0 is zero modulo the group
// order. Accepting it makes w0*M the point at infinity, which
// crypto/elliptic represents as (0, 0) and panics on in the Pake1
// arithmetic — a single malformed OpenCommissioningWindow would then
// kill every inbound Pake1 for the life of the window.
func TestNewVerifierFromValue_RejectsDegenerateW0(t *testing.T) {
	t.Parallel()
	salt := testSalt()
	orig, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	_, lBytes := verifierBytesFor(orig)

	for name, w0Bytes := range map[string][]byte{
		"zero":        make([]byte, VerifierW0Size),
		"group-order": curve().Params().N.FillBytes(make([]byte, VerifierW0Size)),
	} {
		if _, err := NewVerifierFromValue(w0Bytes, lBytes); !errors.Is(err, ErrInvalidPasscode) {
			t.Errorf("%s w0: err = %v, want ErrInvalidPasscode", name, err)
		}
	}
}

// TestProcessPake1_RejectsIdentityDifference pins the second half of the
// same guard: a prover that sends exactly X = w0*M makes X - w0*M the
// identity, which the following ScalarMult would panic on.
func TestProcessPake1_RejectsIdentityDifference(t *testing.T) {
	t.Parallel()
	salt := testSalt()
	vc, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	v := NewVerifier(vc, nil, nil, nil)

	// X = w0*M — a valid curve point, so unmarshalAndValidate accepts it.
	mx, my := curve().ScalarMult(mPoint.X, mPoint.Y, vc.W0.Bytes()) //nolint:staticcheck // SA1019: matches curve() usage elsewhere in this package
	pA := elliptic.Marshal(curve(), mx, my)                         //nolint:staticcheck // SA1019: matches curve() usage elsewhere in this package

	if _, err := v.ProcessPake1(pA); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("ProcessPake1(w0*M): err = %v, want ErrInvalidPoint", err)
	}
}
