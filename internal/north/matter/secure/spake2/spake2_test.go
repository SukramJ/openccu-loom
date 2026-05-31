// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package spake2

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

const (
	testPasscode   = uint32(20202021) // Matter test passcode (Core Spec §5.1.1.6)
	testIterations = 1000
)

func testSalt() []byte { return []byte("SPAKE2P Key Salt") }

// --- Round-trip ---

// TestRoundTripDerivesSameKe is the central correctness assertion:
// when prover and verifier hold the same passcode, both end the
// exchange with the same 16-byte session key Ke.
func TestRoundTripDerivesSameKe(t *testing.T) {
	salt := testSalt()
	vc, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}

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

	verifierKey := verifier.SharedSecret()
	proverKey := prover.SharedSecret()
	if !bytes.Equal(verifierKey, proverKey) {
		t.Fatalf("Ke mismatch:\n verifier=% X\n prover  =% X", verifierKey, proverKey)
	}
	if len(verifierKey) != SharedSecretSize {
		t.Fatalf("Ke size = %d, want %d", len(verifierKey), SharedSecretSize)
	}
}

// TestWrongPasscodeFailsConfirmation — the prover uses a different
// passcode; the verifier's Pake2 cB must mismatch the prover's
// expectation, so [Prover.ProcessPake2] returns ErrConfirmationFailed.
func TestWrongPasscodeFailsConfirmation(t *testing.T) {
	salt := testSalt()
	vc, err := NewVerifierContext(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}

	prover, err := NewProver(99999999, salt, testIterations, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatal(err)
	}

	verifier := NewVerifier(vc, nil, nil, nil)
	pake2, err := verifier.ProcessPake1(pA)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := prover.ProcessPake2(pake2.Y, pake2.CB); !errors.Is(err, ErrConfirmationFailed) {
		t.Fatalf("err = %v, want ErrConfirmationFailed", err)
	}
	if got := prover.SharedSecret(); got != nil {
		t.Fatalf("SharedSecret leaked after failed confirmation: % X", got)
	}
}

// TestVerifierRejectsTamperedCA — even if the prover sends a valid-
// looking cA from the wrong passcode, the verifier's tag check must
// reject. We simulate this by handing the verifier a corrupted cA.
func TestVerifierRejectsTamperedCA(t *testing.T) {
	salt := testSalt()
	vc, _ := NewVerifierContext(testPasscode, salt, testIterations)
	prover, _ := NewProver(testPasscode, salt, testIterations, nil, nil, nil)
	pA, _ := prover.GeneratePake1()
	verifier := NewVerifier(vc, nil, nil, nil)
	pake2, _ := verifier.ProcessPake1(pA)
	cA, _ := prover.ProcessPake2(pake2.Y, pake2.CB)
	tampered := append([]byte(nil), cA...)
	tampered[0] ^= 0x01
	if err := verifier.ProcessPake3(tampered); !errors.Is(err, ErrConfirmationFailed) {
		t.Fatalf("err = %v, want ErrConfirmationFailed", err)
	}
}

// TestRejectsInvalidPake1Point catches malformed peer points before
// they reach the curve arithmetic.
func TestRejectsInvalidPake1Point(t *testing.T) {
	salt := testSalt()
	vc, _ := NewVerifierContext(testPasscode, salt, testIterations)
	verifier := NewVerifier(vc, nil, nil, nil)

	cases := [][]byte{
		nil,
		make([]byte, PointSize-1), // wrong length
		make([]byte, PointSize),   // valid length but identity element (all zeros, no 0x04 prefix)
		append([]byte{0x04}, make([]byte, PointSize-1)...), // 0,0 — the identity
	}
	for i, bad := range cases {
		if _, err := verifier.ProcessPake1(bad); !errors.Is(err, ErrInvalidPoint) {
			t.Errorf("case %d: err=%v, want ErrInvalidPoint", i, err)
		}
	}
}

// --- API state machine ---

// TestProcessPake1OnlyOnce locks the state machine.
func TestProcessPake1OnlyOnce(t *testing.T) {
	salt := testSalt()
	vc, _ := NewVerifierContext(testPasscode, salt, testIterations)
	prover, _ := NewProver(testPasscode, salt, testIterations, nil, nil, nil)
	pA, _ := prover.GeneratePake1()
	verifier := NewVerifier(vc, nil, nil, nil)
	if _, err := verifier.ProcessPake1(pA); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.ProcessPake1(pA); !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestProcessPake3BeforePake1Fails — invariant guard.
func TestProcessPake3BeforePake1Fails(t *testing.T) {
	salt := testSalt()
	vc, _ := NewVerifierContext(testPasscode, salt, testIterations)
	verifier := NewVerifier(vc, nil, nil, nil)
	if err := verifier.ProcessPake3(make([]byte, ConfirmTagSize)); !errors.Is(err, ErrSessionState) {
		t.Fatalf("err=%v, want ErrSessionState", err)
	}
}

// TestSharedSecretBeforeFinishReturnsNil keeps the API safe against
// callers who forget the confirmation step.
func TestSharedSecretBeforeFinishReturnsNil(t *testing.T) {
	salt := testSalt()
	vc, _ := NewVerifierContext(testPasscode, salt, testIterations)
	prover, _ := NewProver(testPasscode, salt, testIterations, nil, nil, nil)
	pA, _ := prover.GeneratePake1()
	verifier := NewVerifier(vc, nil, nil, nil)
	_, _ = verifier.ProcessPake1(pA)
	if got := verifier.SharedSecret(); got != nil {
		t.Fatalf("SharedSecret returned %v before ProcessPake3", got)
	}
}

// TestPBKDFOutputDeterministic locks the (passcode, salt, iter) →
// (w0, w1) mapping. Drift here would invalidate every previously
// stored verifier context.
func TestPBKDFOutputDeterministic(t *testing.T) {
	salt := testSalt()
	w0a, w1a, err := PBKDF(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	w0b, w1b, err := PBKDF(testPasscode, salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	if w0a.Cmp(w0b) != 0 || w1a.Cmp(w1b) != 0 {
		t.Fatal("PBKDF non-deterministic")
	}
}

// TestSeparateProverAndVerifierWithDifferentSalt rejects when the
// shared salt drifts between sides — the (w0, w1) values diverge.
func TestSeparateProverAndVerifierWithDifferentSalt(t *testing.T) {
	vc, _ := NewVerifierContext(testPasscode, []byte("salt-A"), testIterations)
	prover, _ := NewProver(testPasscode, []byte("salt-B"), testIterations, nil, nil, nil)
	pA, _ := prover.GeneratePake1()
	verifier := NewVerifier(vc, nil, nil, nil)
	pake2, _ := verifier.ProcessPake1(pA)
	if _, err := prover.ProcessPake2(pake2.Y, pake2.CB); !errors.Is(err, ErrConfirmationFailed) {
		t.Fatalf("err = %v, want ErrConfirmationFailed", err)
	}
}

// --- hexToBytes / hexNibble ---

// --- PBKDF error paths ---

// TestPBKDF_ZeroIterationsReturnsError verifies that iterations <= 0
// is rejected by PBKDF.
func TestPBKDF_ZeroIterationsReturnsError(t *testing.T) {
	_, _, err := PBKDF(testPasscode, testSalt(), 0)
	if err == nil {
		t.Fatal("expected error for iterations=0, got nil")
	}
	if !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("err = %v, want wrapping ErrInvalidPasscode", err)
	}
}

// TestPBKDF_NegativeIterationsReturnsError verifies that negative
// iterations is also rejected.
func TestPBKDF_NegativeIterationsReturnsError(t *testing.T) {
	_, _, err := PBKDF(testPasscode, testSalt(), -1)
	if err == nil {
		t.Fatal("expected error for iterations=-1, got nil")
	}
}

// TestNewVerifierContext_InvalidIterationsReturnsError verifies that
// NewVerifierContext propagates the PBKDF error.
func TestNewVerifierContext_InvalidIterationsReturnsError(t *testing.T) {
	_, err := NewVerifierContext(testPasscode, testSalt(), 0)
	if err == nil {
		t.Fatal("expected error for iterations=0, got nil")
	}
}

// TestNewProver_InvalidIterationsReturnsError verifies that NewProver
// propagates the PBKDF error.
func TestNewProver_InvalidIterationsReturnsError(t *testing.T) {
	_, err := NewProver(testPasscode, testSalt(), 0, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for iterations=0, got nil")
	}
}

// TestGeneratePake1_CalledTwiceReturnsError verifies the state-machine
// guard in GeneratePake1.
func TestGeneratePake1_CalledTwiceReturnsError(t *testing.T) {
	prover, err := NewProver(testPasscode, testSalt(), testIterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	if _, err := prover.GeneratePake1(); err != nil {
		t.Fatalf("first GeneratePake1: %v", err)
	}
	if _, err := prover.GeneratePake1(); !errors.Is(err, ErrSessionState) {
		t.Fatalf("second GeneratePake1: err=%v, want ErrSessionState", err)
	}
}

// TestProcessPake2_BeforeGeneratePake1ReturnsError verifies the
// state-machine guard in ProcessPake2.
func TestProcessPake2_BeforeGeneratePake1ReturnsError(t *testing.T) {
	prover, err := NewProver(testPasscode, testSalt(), testIterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	// ProcessPake2 without calling GeneratePake1 first.
	yBytes := make([]byte, PointSize)
	yBytes[0] = 0x04 // valid-looking prefix
	_, err = prover.ProcessPake2(yBytes, make([]byte, ConfirmTagSize))
	if !errors.Is(err, ErrSessionState) {
		t.Fatalf("err = %v, want ErrSessionState", err)
	}
}

// --- hexToBytes / hexNibble ---

// TestHexToBytes_Valid verifies that hexToBytes correctly decodes
// well-formed lowercase hex strings.
func TestHexToBytes_Valid(t *testing.T) {
	got, err := hexToBytes("deadbeef")
	if err != nil {
		t.Fatalf("hexToBytes: %v", err)
	}
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

// TestHexToBytes_UpperCase verifies uppercase hex is accepted.
func TestHexToBytes_UpperCase(t *testing.T) {
	got, err := hexToBytes("AABBCCDD")
	if err != nil {
		t.Fatalf("hexToBytes upper: %v", err)
	}
	want := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

// TestHexToBytes_OddLength returns an error for an odd-length hex string.
func TestHexToBytes_OddLength(t *testing.T) {
	_, err := hexToBytes("abc")
	if err == nil {
		t.Fatal("expected error for odd-length hex string, got nil")
	}
}

// TestHexToBytes_BadNibble returns an error when a non-hex character
// is encountered.
func TestHexToBytes_BadNibble(t *testing.T) {
	_, err := hexToBytes("0g")
	if err == nil {
		t.Fatal("expected error for bad nibble, got nil")
	}
}

// TestHexNibble_AllValid exercises every valid hex nibble character.
func TestHexNibble_AllValid(t *testing.T) {
	cases := []struct {
		b    byte
		want byte
	}{
		{'0', 0},
		{'9', 9},
		{'a', 10},
		{'f', 15},
		{'A', 10},
		{'F', 15},
	}
	for _, tc := range cases {
		got, err := hexNibble(tc.b)
		if err != nil {
			t.Errorf("hexNibble(%q): %v", tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("hexNibble(%q) = %d, want %d", tc.b, got, tc.want)
		}
	}
}

// TestHexNibble_Invalid rejects non-hex bytes.
func TestHexNibble_Invalid(t *testing.T) {
	for _, b := range []byte{'g', 'z', ' ', '!', 0x00} {
		if _, err := hexNibble(b); err == nil {
			t.Errorf("hexNibble(%q): expected error, got nil", b)
		}
	}
}

// TestUnmarshalUncompressed_InvalidHex returns an error for a hex
// string that produces a bad nibble.
func TestUnmarshalUncompressed_InvalidHex(t *testing.T) {
	_, err := unmarshalUncompressed("ZZ")
	if err == nil {
		t.Fatal("expected error for invalid hex, got nil")
	}
}

// TestUnmarshalUncompressed_NotAPoint returns ErrInvalidPoint when the
// decoded bytes are all zeros (not a valid point).
func TestUnmarshalUncompressed_NotAPoint(t *testing.T) {
	// 66 zero bytes — 0x04 prefix is missing, so elliptic.Unmarshal
	// returns nil, nil which maps to ErrInvalidPoint.
	zeros := make([]byte, 66)
	hex := fmt.Sprintf("%x", zeros)
	_, err := unmarshalUncompressed(hex)
	if !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("err = %v, want ErrInvalidPoint", err)
	}
}
