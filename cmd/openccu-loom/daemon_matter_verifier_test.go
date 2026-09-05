// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"bytes"
	"crypto/elliptic"
	"log/slog"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/secure/spake2"
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

// reaperGoroutines counts the live per-exchange PASE reaper goroutines by
// name in the runtime's own goroutine profile. Counting by stack rather than
// by total goroutine count keeps the assertion immune to whatever else the
// test binary is running.
func reaperGoroutines(t *testing.T) int {
	t.Helper()
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		t.Fatalf("goroutine profile: %v", err)
	}
	return strings.Count(buf.String(), "PerExchangePaseProvider).StartReaper.func")
}

// awaitReaperGoroutines waits for the reaper count to settle on want.
func awaitReaperGoroutines(t *testing.T, want int, because string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := reaperGoroutines(t)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d PASE reaper goroutines alive, want %d", because, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestVerifierInstallerStopsThePaseProvidersItReplaces pins the lifecycle of
// the per-exchange PASE providers in concurrent-pairings mode.
//
// Each provider runs a 30s-ticker reaper goroutine that only Stop ends. While
// nothing stopped them, every commissioning window opened through the
// AdministratorCommissioning cluster leaked one goroutine on open and another
// on close, and a daemon that had been re-paired a few dozen times carried
// them all until the process exited.
func TestVerifierInstallerStopsThePaseProvidersItReplaces(t *testing.T) {
	mgr := buildTestOperationalManager(t)
	salt := []byte("openccu-loom-dev0")
	verifier := verifierBytesFromPasscode(t, 20202021, salt, 1000)

	base := reaperGoroutines(t)
	installer := newMatterVerifierInstaller(
		&matterbridge.Bridge{}, mgr, nil, nil,
		func() *matterbridge.PaseAdapter { return nil },
		slog.New(slog.DiscardHandler),
	)
	t.Cleanup(installer.Close)

	restore, err := installer.InstallVerifier(verifier, 1000, salt)
	if err != nil {
		t.Fatalf("InstallVerifier: %v", err)
	}
	awaitReaperGoroutines(t, base+1, "after opening a window")

	// Re-opening a window before the previous one was restored must not
	// stack providers: the multi-admin path installs a fresh verifier.
	restore2, err := installer.InstallVerifier(verifier, 1000, salt)
	if err != nil {
		t.Fatalf("second InstallVerifier: %v", err)
	}
	awaitReaperGoroutines(t, base+1, "after re-opening a window")

	// Closing the window swaps in the between-windows provider; the window's
	// own provider must go with it.
	restore2()
	awaitReaperGoroutines(t, base+1, "after closing the window")
	restore()
	awaitReaperGoroutines(t, base+1, "after a stale restore")

	installer.Close()
	awaitReaperGoroutines(t, base, "after the bridge teardown closed the installer")
}
