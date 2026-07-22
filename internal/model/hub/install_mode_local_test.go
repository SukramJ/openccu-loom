// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// install_mode_local_test.go — tests for InstallMode.EnableLocal: the
// keyserver-less HmIP LOCAL teach-in (SGTIN + device-key whitelist).

package hub

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubLocalInstallWriter implements [LocalInstallModeWriter] so EnableLocal
// tests can assert the normalised SGTIN/key values that reach the writer.
type stubLocalInstallWriter struct {
	stubInstall // satisfies base InstallModeWriter
	gotSGTIN    string
	gotKey      string
	gotDuration time.Duration
	err         error
}

func (s *stubLocalInstallWriter) SetInstallModeLocal(_ context.Context, _ string, duration time.Duration, sgtin, keyHex string) error {
	s.gotDuration = duration
	s.gotSGTIN = sgtin
	s.gotKey = keyHex
	return s.err
}

// TestEnableLocalRejectsZeroDuration verifies the duration guard fires
// before any normalisation or writer call.
func TestEnableLocalRejectsZeroDuration(t *testing.T) {
	t.Parallel()
	w := &stubLocalInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	err := m.EnableLocal(context.Background(), 0, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrInstallModeInvalidDuration) {
		t.Fatalf("EnableLocal zero duration: got %v, want ErrInstallModeInvalidDuration", err)
	}
	if w.gotSGTIN != "" {
		t.Fatal("writer must not be called when duration is invalid")
	}
}

// TestEnableLocalFallsBackToErrorWhenWriterLacksInterface verifies that a
// plain [InstallModeWriter] (no LocalInstallModeWriter extension) makes
// EnableLocal fail with ErrLocalInstallModeUnsupported — there is
// deliberately no broadcast fallback for the whitelisted teach-in.
func TestEnableLocalFallsBackToErrorWhenWriterLacksInterface(t *testing.T) {
	t.Parallel()
	w := &stubInstall{} // plain InstallModeWriter only
	m := NewInstallMode("HmIP-RF", w)
	err := m.EnableLocal(context.Background(), 60*time.Second, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrLocalInstallModeUnsupported) {
		t.Fatalf("EnableLocal unsupported writer: got %v, want ErrLocalInstallModeUnsupported", err)
	}
	enabled, _, _ := m.InstallState()
	if enabled {
		t.Fatal("install mode must not become active when the writer refuses LOCAL teach-in")
	}
}

// TestEnableLocalRejectsInvalidSGTIN verifies that a malformed SGTIN is
// rejected before the writer is ever called, wrapped in
// ErrInstallModeInvalidLocalInput.
func TestEnableLocalRejectsInvalidSGTIN(t *testing.T) {
	t.Parallel()
	w := &stubLocalInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	err := m.EnableLocal(context.Background(), 60*time.Second, "not-a-valid-sgtin", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrInstallModeInvalidLocalInput) {
		t.Fatalf("EnableLocal bad SGTIN: got %v, want ErrInstallModeInvalidLocalInput", err)
	}
	if w.gotSGTIN != "" {
		t.Fatal("writer must not be called when SGTIN normalisation fails")
	}
}

// TestEnableLocalRejectsInvalidKey verifies that a malformed device key is
// rejected before the writer is called, wrapped in
// ErrInstallModeInvalidLocalInput.
func TestEnableLocalRejectsInvalidKey(t *testing.T) {
	t.Parallel()
	w := &stubLocalInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	// "DIOV" contains characters excluded from the label alphabet.
	err := m.EnableLocal(context.Background(), 60*time.Second, "3014F711A061A7D569892A67", "DIOV")
	if !errors.Is(err, ErrInstallModeInvalidLocalInput) {
		t.Fatalf("EnableLocal bad key: got %v, want ErrInstallModeInvalidLocalInput", err)
	}
	if w.gotKey != "" {
		t.Fatal("writer must not be called when key normalisation fails")
	}
}

// TestEnableLocalNormalisesAndActivates verifies the happy path end-to-end:
// a dashed, lowercase SGTIN and a short Base32 label-form key both arrive at
// the writer fully normalised (24-char uppercase hex SGTIN, 32-char
// uppercase hex key), and the install-mode state reports active afterwards.
func TestEnableLocalNormalisesAndActivates(t *testing.T) {
	t.Parallel()
	w := &stubLocalInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	const (
		dashedLowerSGTIN = "3014-f711-a061-a7d5-6989-2a67"
		labelFormKey     = "0123456789abcefghjklmnpqrs"
	)
	if err := m.EnableLocal(context.Background(), 300*time.Second, dashedLowerSGTIN, labelFormKey); err != nil {
		t.Fatalf("EnableLocal: unexpected error: %v", err)
	}
	const wantSGTIN = "3014F711A061A7D569892A67"
	const wantKey = "0110C8531D0952D8D73E1194E95B5F19"
	if w.gotSGTIN != wantSGTIN {
		t.Fatalf("writer got sgtin=%q, want %q", w.gotSGTIN, wantSGTIN)
	}
	if w.gotKey != wantKey {
		t.Fatalf("writer got key=%q, want %q", w.gotKey, wantKey)
	}
	if w.gotDuration != 300*time.Second {
		t.Fatalf("writer got duration=%v, want 300s", w.gotDuration)
	}
	enabled, remaining, observed := m.InstallState()
	if !enabled || !observed {
		t.Fatalf("InstallState() = (enabled=%v, observed=%v), want (true, true)", enabled, observed)
	}
	if remaining <= 0 {
		t.Fatalf("remaining=%v, want > 0", remaining)
	}
}

// TestEnableLocalPropagatesWriterError verifies that a writer-side failure
// (e.g. the CCU rejects the whitelist call) surfaces unwrapped and does not
// mark install mode as active.
func TestEnableLocalPropagatesWriterError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("ccu rejected whitelist")
	w := &stubLocalInstallWriter{err: sentinel}
	m := NewInstallMode("HmIP-RF", w)
	err := m.EnableLocal(context.Background(), 60*time.Second, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnableLocal writer error: got %v, want %v", err, sentinel)
	}
	enabled, _, _ := m.InstallState()
	if enabled {
		t.Fatal("install mode must not become active when the writer call fails")
	}
}
