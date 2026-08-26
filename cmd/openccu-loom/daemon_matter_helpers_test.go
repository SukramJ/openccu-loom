// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

// ── computeFabricCompressedID ─────────────────────────────────────────────────

func TestComputeFabricCompressedID_ValidInput(t *testing.T) {
	t.Parallel()
	// Build a 65-byte uncompressed public key (0x04 prefix + 64 bytes).
	rootPubKey := make([]byte, 65)
	rootPubKey[0] = 0x04
	for i := 1; i < 65; i++ {
		rootPubKey[i] = byte(i)
	}
	out, err := computeFabricCompressedID(rootPubKey, 0xCAFEBABEDEAD0001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Output must be 8 non-zero bytes (HKDF with non-trivial input
	// should not produce all-zero output).
	allZero := true
	for _, b := range out {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("expected non-zero compressed fabric ID")
	}
}

func TestComputeFabricCompressedID_Deterministic(t *testing.T) {
	t.Parallel()
	rootPubKey := make([]byte, 65)
	rootPubKey[0] = 0x04
	for i := 1; i < 65; i++ {
		rootPubKey[i] = byte(i * 3)
	}
	fabricID := uint64(0xDEADBEEF12345678)
	a, err := computeFabricCompressedID(rootPubKey, fabricID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	b, err := computeFabricCompressedID(rootPubKey, fabricID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if a != b {
		t.Errorf("not deterministic: %x != %x", a, b)
	}
}

func TestComputeFabricCompressedID_InvalidKeyLength(t *testing.T) {
	t.Parallel()
	// Wrong length.
	_, err := computeFabricCompressedID(make([]byte, 33), 1)
	if err == nil {
		t.Fatal("expected error for 33-byte key")
	}
}

func TestComputeFabricCompressedID_InvalidPrefix(t *testing.T) {
	t.Parallel()
	// Correct length but wrong prefix byte.
	key := make([]byte, 65)
	key[0] = 0x02 // compressed — not 0x04 (uncompressed)
	_, err := computeFabricCompressedID(key, 1)
	if err == nil {
		t.Fatal("expected error for non-0x04 prefix")
	}
}

func TestComputeFabricCompressedID_FabricIDZero(t *testing.T) {
	t.Parallel()
	rootPubKey := make([]byte, 65)
	rootPubKey[0] = 0x04
	// fabricID == 0 should still produce a valid (non-error) result.
	_, err := computeFabricCompressedID(rootPubKey, 0)
	if err != nil {
		t.Fatalf("fabricID=0: unexpected error: %v", err)
	}
}

// ── buildDevAttestation ────────────────────────────────────────────────────────

func TestBuildDevAttestation_ReturnsValidMaterial(t *testing.T) {
	t.Parallel()
	dacKey, dac, pai, cd, err := buildDevAttestation(0xFFF1, 0x8000)
	if err != nil {
		t.Fatalf("buildDevAttestation: %v", err)
	}
	if dacKey == nil {
		t.Error("expected non-nil DAC private key")
	}
	if len(dac) == 0 {
		t.Error("expected non-empty DAC DER bytes")
	}
	if len(pai) == 0 {
		t.Error("expected non-empty PAI DER bytes")
	}
	// In dev mode DAC == PAI.
	if !bytes.Equal(dac, pai) {
		t.Error("expected DAC == PAI in dev-attestation mode")
	}
	// CD is empty in dev mode — chip-tool doesn't verify it in dev runs.
	_ = cd
}

func TestBuildDevAttestation_DifferentIDsProduceDifferentKeys(t *testing.T) {
	t.Parallel()
	key1, _, _, _, err := buildDevAttestation(0xFFF1, 0x0001)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	key2, _, _, _, err := buildDevAttestation(0xFFF1, 0x0002)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Different cert serial / CN, but underlying key pair is random —
	// both must be non-nil and structurally valid.
	if key1 == nil || key2 == nil {
		t.Fatal("expected non-nil keys from both calls")
	}
	// The keys must not be the same (astronomically unlikely from rand).
	if key1.D.Cmp(key2.D) == 0 { //nolint:staticcheck // .D deprecated in Go 1.26; test-only randomness check until we migrate to crypto/ecdh
		t.Error("two buildDevAttestation calls produced identical private keys")
	}
}

// ── failSafeArmerAdapter ──────────────────────────────────────────────────────

func TestFailSafeArmerAdapter_NilGC_LogsSkippedAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := &failSafeArmerAdapter{gc: nil, logger: logger}

	err := a.ArmFailSafeFor(context.Background(), 60, 1)
	if err != nil {
		t.Errorf("expected nil error when gc=nil, got %v", err)
	}
	if !containsSubstring(buf.String(), "failsafe.arm.skipped") {
		t.Errorf("expected 'failsafe.arm.skipped' log; got:\n%s", buf.String())
	}
}

// ── paseSessionCloserAdapter ─────────────────────────────────────────────────

func TestPaseSessionCloserAdapter_NilOpMgr_LogsSkippedAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := &paseSessionCloserAdapter{opMgr: nil, logger: logger}

	err := a.ClosePaseSessions(context.Background())
	if err != nil {
		t.Errorf("expected nil error when opMgr=nil, got %v", err)
	}
	if !containsSubstring(buf.String(), "pase.close.skipped") {
		t.Errorf("expected 'pase.close.skipped' log; got:\n%s", buf.String())
	}
}

func TestPaseSessionCloserAdapter_WithOpMgr_ClosesAndLogs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr := buildTestOperationalManager(t)
	a := &paseSessionCloserAdapter{opMgr: mgr, logger: logger}

	err := a.ClosePaseSessions(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !containsSubstring(buf.String(), "pase.sessions.closed") {
		t.Errorf("expected 'pase.sessions.closed' log; got:\n%s", buf.String())
	}
}
