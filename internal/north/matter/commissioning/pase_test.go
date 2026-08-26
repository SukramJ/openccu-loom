// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package commissioning_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/commissioning"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
)

// validPASEConfig returns a minimal valid PASEConfig for positive tests.
func validPASEConfig() commissioning.PASEConfig {
	return commissioning.PASEConfig{
		Passcode:    20202021,
		Salt:        bytes.Repeat([]byte{0xAB}, 32),
		Iterations:  1000,
		LocalNodeID: 0x01,
		PeerNodeID:  0x02,
	}
}

// TestNewPASEResponder_InvalidPasscode_Zero rejects passcode 0.
func TestNewPASEResponder_InvalidPasscode_Zero(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Passcode = 0
	_, err := commissioning.NewPASEResponder(cfg)
	if !errors.Is(err, commissioning.ErrPASEInvalidPasscode) {
		t.Fatalf("err=%v, want ErrPASEInvalidPasscode", err)
	}
}

// TestNewPASEResponder_InvalidPasscode_Max rejects passcode 99999999.
func TestNewPASEResponder_InvalidPasscode_Max(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Passcode = 99999999
	_, err := commissioning.NewPASEResponder(cfg)
	if !errors.Is(err, commissioning.ErrPASEInvalidPasscode) {
		t.Fatalf("err=%v, want ErrPASEInvalidPasscode", err)
	}
}

// TestNewPASEResponder_InvalidIterations_TooLow rejects iterations < 1000.
func TestNewPASEResponder_InvalidIterations_TooLow(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Iterations = 999
	_, err := commissioning.NewPASEResponder(cfg)
	if !errors.Is(err, commissioning.ErrPASEInvalidIterations) {
		t.Fatalf("err=%v, want ErrPASEInvalidIterations", err)
	}
}

// TestNewPASEResponder_InvalidIterations_TooHigh rejects iterations > 100000.
func TestNewPASEResponder_InvalidIterations_TooHigh(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Iterations = 100001
	_, err := commissioning.NewPASEResponder(cfg)
	if !errors.Is(err, commissioning.ErrPASEInvalidIterations) {
		t.Fatalf("err=%v, want ErrPASEInvalidIterations", err)
	}
}

// TestNewPASEResponder_InvalidSalt_TooShort rejects a salt shorter than 16 bytes.
func TestNewPASEResponder_InvalidSalt_TooShort(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Salt = bytes.Repeat([]byte{0xAA}, 15)
	_, err := commissioning.NewPASEResponder(cfg)
	if err == nil {
		t.Fatal("expected error for 15-byte salt, got nil")
	}
}

// TestNewPASEResponder_InvalidSalt_TooLong rejects a salt longer than 32 bytes.
func TestNewPASEResponder_InvalidSalt_TooLong(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	cfg.Salt = bytes.Repeat([]byte{0xAA}, 33)
	_, err := commissioning.NewPASEResponder(cfg)
	if err == nil {
		t.Fatal("expected error for 33-byte salt, got nil")
	}
}

// TestNewPASEResponder_ValidConfig constructs a responder without error.
func TestNewPASEResponder_ValidConfig(t *testing.T) {
	t.Parallel()
	_, err := commissioning.NewPASEResponder(validPASEConfig())
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}
}

// TestPASEResponder_RoundTrip exercises the full Prover→Responder handshake
// and verifies Session() is returned without error.
func TestPASEResponder_RoundTrip(t *testing.T) {
	t.Parallel()

	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	prover, err := spake2.NewProver(cfg.Passcode, cfg.Salt, cfg.Iterations, cfg.IDA, cfg.IDB, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}

	// Step 1: Prover generates Pake1 (pA).
	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}

	// Step 2: Responder processes Pake1, returns Pake2 (Y + cB).
	pake2, err := responder.HandlePake1(pake1)
	if err != nil {
		t.Fatalf("HandlePake1: %v", err)
	}

	// Step 3: Prover processes Pake2, returns cA.
	cA, err := prover.ProcessPake2(pake2.Y, pake2.CB)
	if err != nil {
		t.Fatalf("ProcessPake2: %v", err)
	}

	// Step 4: Responder processes Pake3 (cA).
	if err := responder.HandlePake3(cA); err != nil {
		t.Fatalf("HandlePake3: %v", err)
	}

	// Session must be available.
	sess, err := responder.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sess == nil {
		t.Fatal("Session returned nil")
	}
}

// TestPASEResponder_HandlePake3BeforePake1 verifies the ordering guard:
// HandlePake3 before HandlePake1 returns ErrPASEStateMismatch.
func TestPASEResponder_HandlePake3BeforePake1(t *testing.T) {
	t.Parallel()

	responder, err := commissioning.NewPASEResponder(validPASEConfig())
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	if err := responder.HandlePake3(make([]byte, 32)); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("expected ErrPASEStateMismatch, got %v", err)
	}
}

// TestPASEResponder_SessionBeforePake3 returns ErrPASEStateMismatch.
func TestPASEResponder_SessionBeforePake3(t *testing.T) {
	t.Parallel()

	responder, err := commissioning.NewPASEResponder(validPASEConfig())
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	if _, err := responder.Session(); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("err=%v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_AttestationChallengeBeforePake3 returns ErrPASEStateMismatch.
func TestPASEResponder_AttestationChallengeBeforePake3(t *testing.T) {
	t.Parallel()

	responder, err := commissioning.NewPASEResponder(validPASEConfig())
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	if _, err := responder.AttestationChallenge(); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("err=%v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_AttestationChallenge_Length verifies the challenge is exactly 16 bytes
// and that calling AttestationChallenge() twice on the same completed responder
// returns the same value (HKDF is deterministic given the same shared secret).
func TestPASEResponder_AttestationChallenge_Length(t *testing.T) {
	t.Parallel()

	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}
	prover, err := spake2.NewProver(cfg.Passcode, cfg.Salt, cfg.Iterations, cfg.IDA, cfg.IDB, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}

	pake1, _ := prover.GeneratePake1()
	pake2, err := responder.HandlePake1(pake1)
	if err != nil {
		t.Fatalf("HandlePake1: %v", err)
	}
	cA, err := prover.ProcessPake2(pake2.Y, pake2.CB)
	if err != nil {
		t.Fatalf("ProcessPake2: %v", err)
	}
	if err := responder.HandlePake3(cA); err != nil {
		t.Fatalf("HandlePake3: %v", err)
	}

	ch1, err := responder.AttestationChallenge()
	if err != nil {
		t.Fatalf("AttestationChallenge (1st): %v", err)
	}
	if len(ch1) != 16 {
		t.Fatalf("challenge length=%d, want 16", len(ch1))
	}

	// Second call on the same responder must return the identical bytes
	// (HKDF-Expand over the same Ke is deterministic).
	ch2, err := responder.AttestationChallenge()
	if err != nil {
		t.Fatalf("AttestationChallenge (2nd): %v", err)
	}
	if !bytes.Equal(ch1, ch2) {
		t.Fatalf("AttestationChallenge not idempotent: %X vs %X", ch1, ch2)
	}
}

// TestPASEResponder_WrongPasscode verifies that the Prover using a wrong
// passcode causes HandlePake3 to fail (cA mismatch → ErrConfirmationFailed
// wrapped inside the commissioning error).
func TestPASEResponder_WrongPasscode(t *testing.T) {
	t.Parallel()

	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	// Prover with a different passcode.
	wrongPasscode := cfg.Passcode + 1
	prover, err := spake2.NewProver(wrongPasscode, cfg.Salt, cfg.Iterations, cfg.IDA, cfg.IDB, nil)
	if err != nil {
		t.Fatalf("NewProver (wrong passcode): %v", err)
	}

	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	pake2, err := responder.HandlePake1(pake1)
	if err != nil {
		t.Fatalf("HandlePake1: %v", err)
	}

	// ProcessPake2 on the prover with wrong key will fail at cB verification.
	// If by chance it does not (cB check skipped), we still expect HandlePake3 to fail.
	cA, err := prover.ProcessPake2(pake2.Y, pake2.CB)
	if err != nil {
		// Expected: prover itself fails due to cB mismatch — test passes.
		t.Logf("ProcessPake2 failed (expected with wrong passcode): %v", err)
		return
	}

	// If prover accepted (shouldn't happen with wrong passcode but guard anyway),
	// the responder must reject cA.
	if err := responder.HandlePake3(cA); err == nil {
		t.Fatal("expected HandlePake3 to fail for wrong passcode, got nil")
	}
}
