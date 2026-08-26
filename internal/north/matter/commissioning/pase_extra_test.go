// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package commissioning_test

// Extra coverage tests for PASE error branches not exercised by pase_test.go.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/commissioning"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
)

// TestPASEResponder_HandlePake1_Duplicate verifies that calling HandlePake1
// a second time after it already succeeded returns ErrPASEStateMismatch.
// This exercises the `r.pake1Done || r.finished` branch.
func TestPASEResponder_HandlePake1_Duplicate(t *testing.T) {
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

	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}

	// First call must succeed.
	if _, err := responder.HandlePake1(pake1); err != nil {
		t.Fatalf("first HandlePake1: %v", err)
	}

	// Second call must fail with ErrPASEStateMismatch.
	if _, err := responder.HandlePake1(pake1); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("duplicate HandlePake1: got %v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_HandlePake1_BadPayload verifies that a malformed Pake1
// payload causes HandlePake1 to return a non-nil error (propagated from
// spake2.ProcessPake1).
func TestPASEResponder_HandlePake1_BadPayload(t *testing.T) {
	t.Parallel()

	responder, err := commissioning.NewPASEResponder(validPASEConfig())
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}

	// A zero-length or corrupted pA triggers an error inside ProcessPake1.
	if _, err := responder.HandlePake1(make([]byte, 65)); err == nil {
		t.Fatal("expected error for bad Pake1 payload, got nil")
	}
}

// TestPASEResponder_HandlePake3_BadPayload verifies that a malformed Pake3
// payload causes HandlePake3 to return a non-nil error after a valid Pake1.
func TestPASEResponder_HandlePake3_BadPayload(t *testing.T) {
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

	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
	if _, err := responder.HandlePake1(pake1); err != nil {
		t.Fatalf("HandlePake1: %v", err)
	}

	// A malformed / wrong-key cA must cause ProcessPake3 to return an error.
	wrongCa := bytes.Repeat([]byte{0xFF}, 32)
	if err := responder.HandlePake3(wrongCa); err == nil {
		t.Fatal("expected error for bad Pake3 payload, got nil")
	}
}

// TestPASEResponder_HandlePake1_Finished verifies that HandlePake1 returns
// ErrPASEStateMismatch after the handshake is complete (r.finished=true).
func TestPASEResponder_HandlePake1_Finished(t *testing.T) {
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

	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
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

	// After r.finished=true, HandlePake1 must reject with ErrPASEStateMismatch.
	if _, err := responder.HandlePake1(pake1); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("post-finish HandlePake1: got %v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_HandlePake3_Finished verifies that HandlePake3 returns
// ErrPASEStateMismatch after the handshake is already complete.
func TestPASEResponder_HandlePake3_Finished(t *testing.T) {
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

	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
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

	// Second HandlePake3 call must fail.
	if err := responder.HandlePake3(cA); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("post-finish HandlePake3: got %v, want ErrPASEStateMismatch", err)
	}
}

// helper: run a complete PASE handshake and return the responder.
func completedHandshake(t *testing.T) *commissioning.PASEResponder {
	t.Helper()
	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}
	prover, err := spake2.NewProver(cfg.Passcode, cfg.Salt, cfg.Iterations, cfg.IDA, cfg.IDB, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pake1, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}
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
	return responder
}

// TestPASEResponder_Session_BeforeFinished verifies that Session() returns
// ErrPASEStateMismatch when called before HandlePake3.
func TestPASEResponder_Session_BeforeFinished(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}
	if _, err := responder.Session(); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("Session before finish: got %v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_Session_AfterFinished verifies that Session() returns a
// non-nil session after a complete handshake.
func TestPASEResponder_Session_AfterFinished(t *testing.T) {
	t.Parallel()
	r := completedHandshake(t)
	sess, err := r.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sess == nil {
		t.Fatal("Session returned nil session")
	}
}

// TestPASEResponder_AttestationChallenge_BeforeFinished verifies that
// AttestationChallenge() returns ErrPASEStateMismatch when not yet finished.
func TestPASEResponder_AttestationChallenge_BeforeFinished(t *testing.T) {
	t.Parallel()
	cfg := validPASEConfig()
	responder, err := commissioning.NewPASEResponder(cfg)
	if err != nil {
		t.Fatalf("NewPASEResponder: %v", err)
	}
	if _, err := responder.AttestationChallenge(); !errors.Is(err, commissioning.ErrPASEStateMismatch) {
		t.Fatalf("AttestationChallenge before finish: got %v, want ErrPASEStateMismatch", err)
	}
}

// TestPASEResponder_AttestationChallenge_AfterFinished verifies that
// AttestationChallenge() returns a 16-byte slice after a complete handshake.
func TestPASEResponder_AttestationChallenge_AfterFinished(t *testing.T) {
	t.Parallel()
	r := completedHandshake(t)
	ch, err := r.AttestationChallenge()
	if err != nil {
		t.Fatalf("AttestationChallenge: %v", err)
	}
	if len(ch) != 16 {
		t.Fatalf("AttestationChallenge: len=%d, want 16", len(ch))
	}
}
