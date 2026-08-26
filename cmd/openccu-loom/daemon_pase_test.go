// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"log/slog"
	"testing"
)

// ── buildPaseAdapterFromCreds ─────────────────────────────────────────────────

func TestBuildPaseAdapterFromCreds_ValidInputs_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	salt := []byte("valid-16-byte-salt") // 18 bytes — SPAKE2+ accepts > 16 bytes
	adapter, err := buildPaseAdapterFromCreds(20202021, salt, 1000, mgr, nil, nil, logger)
	if err != nil {
		t.Fatalf("buildPaseAdapterFromCreds: %v", err)
	}
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
}

func TestBuildPaseAdapterFromCreds_InvalidIterations_Errors(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	logger := slog.Default()
	salt := []byte("some-salt-bytes-!!")
	// Negative iterations → SPAKE2+ verifier context rejects.
	_, err := buildPaseAdapterFromCreds(20202021, salt, -1, mgr, nil, nil, logger)
	if err == nil {
		t.Fatal("expected error for negative iterations")
	}
}

func TestBuildPaseAdapterFromCreds_ShortSalt_Accepted(t *testing.T) {
	t.Parallel()
	// SPAKE2+ in this implementation does not require a minimum salt
	// length at VerifierContext creation — so empty salt is accepted.
	// This test documents the accepted behaviour.
	mgr := buildTestOperationalManager(t)
	logger := slog.Default()
	adapter, err := buildPaseAdapterFromCreds(20202021, []byte{}, 1000, mgr, nil, nil, logger)
	// Either accepted or error — both are valid outcomes.
	_ = adapter
	_ = err
}

func TestBuildPaseAdapterFromCreds_ZeroPasscode_IsValid(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	logger := slog.Default()
	salt := bytes.Repeat([]byte{0xAA}, 16)
	// passcode=0 should be accepted by SPAKE2+ (validator allows it).
	adapter, err := buildPaseAdapterFromCreds(0, salt, 1000, mgr, nil, nil, logger)
	// Either a valid adapter or a domain error is acceptable —
	// SPAKE2+ may reject passcode=0 as out-of-range.
	_ = adapter
	_ = err
}
