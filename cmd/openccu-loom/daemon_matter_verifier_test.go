// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// daemon_matter_verifier_test.go covers buildPaseAdapterFromVerifier — the
// Enhanced Commissioning Window path that builds a PASE acceptor from a
// commissioner-supplied Matter §3.10.5 PAKE passcode verifier (w0 || L)
// instead of a locally-known passcode. The spake2 verifier/prover round
// trip itself is covered by internal/north/matter/secure/spake2; this test
// only exercises the wiring: does a valid 97-byte verifier build an
// adapter, and is a malformed-length verifier rejected before it reaches
// spake2.NewVerifierFromValue.

import (
	"crypto/elliptic"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
)

// verifierBytesFromPasscode derives the same 97-byte (w0 || L) wire
// representation a real commissioner would compute for a chosen passcode
// and hands it back as raw bytes — mirroring what OpenCommissioningWindow's
// PAKEPasscodeVerifier field carries. w0 is the 32-byte P-256 scalar;
// L = w1*G is the 65-byte uncompressed point, matching
// spake2.VerifierW0Size / spake2.VerifierLSize.
func verifierBytesFromPasscode(t *testing.T, passcode uint32, salt []byte, iterations int) []byte {
	t.Helper()
	vc, err := spake2.NewVerifierContext(passcode, salt, iterations)
	if err != nil {
		t.Fatalf("spake2.NewVerifierContext: %v", err)
	}
	w0 := vc.W0.FillBytes(make([]byte, spake2.VerifierW0Size))
	l := elliptic.Marshal(elliptic.P256(), vc.L.X, vc.L.Y) //nolint:staticcheck // SA1019: matches production curve() usage
	if len(l) != spake2.VerifierLSize {
		t.Fatalf("marshalled L length=%d, want %d", len(l), spake2.VerifierLSize)
	}
	verifier := make([]byte, 0, spake2.VerifierW0Size+spake2.VerifierLSize)
	verifier = append(verifier, w0...)
	verifier = append(verifier, l...)
	return verifier
}

// TestBuildPaseAdapterFromVerifier_ValidVerifier_ReturnsAdapter verifies
// that a properly-sized verifier derived from a known passcode builds a
// usable PaseAdapter — the same code path the Enhanced Commissioning
// Window's PaseVerifierInstaller drives via matterVerifierInstaller.
func TestBuildPaseAdapterFromVerifier_ValidVerifier_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	salt := []byte("openccu-loom-dev0")
	const iterations = 1000
	verifier := verifierBytesFromPasscode(t, 20202021, salt, iterations)

	adapter, err := buildPaseAdapterFromVerifier(verifier, salt, iterations, mgr, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildPaseAdapterFromVerifier: %v", err)
	}
	if adapter == nil {
		t.Error("expected non-nil PaseAdapter")
	}
}

// TestBuildPaseAdapterFromVerifier_WrongLength_ReturnsError verifies that a
// verifier that is not exactly 97 bytes (VerifierW0Size + VerifierLSize) is
// rejected before spake2.NewVerifierFromValue ever runs the curve-point
// validation — a truncated verifier must not silently build an adapter
// bound to different (or garbage) key material.
func TestBuildPaseAdapterFromVerifier_WrongLength_ReturnsError(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	salt := []byte("openccu-loom-dev0")
	const iterations = 1000
	verifier := verifierBytesFromPasscode(t, 20202021, salt, iterations)
	short := verifier[:len(verifier)-1] // 96 bytes — one short of the required 97

	adapter, err := buildPaseAdapterFromVerifier(short, salt, iterations, mgr, nil, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("expected error for a 96-byte verifier, got nil")
	}
	if adapter != nil {
		t.Error("expected nil PaseAdapter on length error")
	}
}
