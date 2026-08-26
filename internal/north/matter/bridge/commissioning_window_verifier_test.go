// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// ─── fakePaseVerifierInstaller ───────────────────────────────────────────────

// fakePaseVerifierInstaller records every InstallVerifier call so tests can
// assert the Enhanced Commissioning Window path wires the commissioner's
// verifier bytes through unmodified, and that the returned restore closure
// fires exactly once per window close.
type fakePaseVerifierInstaller struct {
	calls      atomic.Int32
	restored   atomic.Int32
	verifier   []byte
	iterations uint32
	salt       []byte
	err        error
}

func (f *fakePaseVerifierInstaller) InstallVerifier(verifier []byte, iterations uint32, salt []byte) (func(), error) {
	f.calls.Add(1)
	f.verifier = verifier
	f.iterations = iterations
	f.salt = salt
	if f.err != nil {
		return nil, f.err
	}
	return func() { f.restored.Add(1) }, nil
}

// newTestVerifier returns a dummy 97-byte Matter PAKE passcode verifier
// (w0 || L per Matter §3.10.5). CommissioningWindow.OpenWindow does not
// length-validate the verifier itself — that is buildPaseAdapterFromVerifier's
// job in cmd/openccu-loom — so any non-empty byte slice exercises the wiring
// path under test here.
func newTestVerifier() []byte {
	return bytes.Repeat([]byte{0x42}, 97)
}

func newTestSalt() []byte {
	return bytes.Repeat([]byte{0x07}, 16)
}

// ─── PaseVerifierInstaller wiring ────────────────────────────────────────────

// TestCommissioningWindow_EnhancedWindow_InstallsVerifier verifies that
// opening an Enhanced Commissioning Window with a commissioner-supplied
// PAKE verifier calls InstallVerifier exactly once with the verifier,
// iteration count, and salt taken verbatim from OpenWindowParams.
func TestCommissioningWindow_EnhancedWindow_InstallsVerifier(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	installer := &fakePaseVerifierInstaller{}
	w.SetPaseVerifierInstaller(installer)

	verifier := newTestVerifier()
	salt := newTestSalt()
	const iterations uint32 = 1000

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
		PAKEPasscodeVerifier:        verifier,
		Iterations:                  iterations,
		Salt:                        salt,
		IsBasicWindow:               false,
	})
	if err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}

	if n := installer.calls.Load(); n != 1 {
		t.Fatalf("InstallVerifier call count = %d, want 1", n)
	}
	if !bytes.Equal(installer.verifier, verifier) {
		t.Errorf("InstallVerifier verifier = %x, want %x", installer.verifier, verifier)
	}
	if installer.iterations != iterations {
		t.Errorf("InstallVerifier iterations = %d, want %d", installer.iterations, iterations)
	}
	if !bytes.Equal(installer.salt, salt) {
		t.Errorf("InstallVerifier salt = %x, want %x", installer.salt, salt)
	}
}

// TestCommissioningWindow_RevokeFiresVerifierRestore verifies that revoking
// an Enhanced Commissioning Window opened with a verifier fires the restore
// closure InstallVerifier returned, re-installing the bridge's configured
// long-lived PASE acceptor.
func TestCommissioningWindow_RevokeFiresVerifierRestore(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	installer := &fakePaseVerifierInstaller{}
	w.SetPaseVerifierInstaller(installer)

	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
		PAKEPasscodeVerifier:        newTestVerifier(),
		Iterations:                  1000,
		Salt:                        newTestSalt(),
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if n := installer.calls.Load(); n != 1 {
		t.Fatalf("InstallVerifier call count before revoke = %d, want 1", n)
	}
	if n := installer.restored.Load(); n != 0 {
		t.Fatalf("restore call count before revoke = %d, want 0", n)
	}

	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}

	if n := installer.restored.Load(); n != 1 {
		t.Errorf("restore call count after revoke = %d, want 1", n)
	}
}

// TestCommissioningWindow_BasicWindow_DoesNotInstall verifies that
// InstallVerifier is not called on the Basic Commissioning Window path
// (IsBasicWindow=true), nor when no verifier was supplied at all — both
// leave the bridge's configured PASE acceptor untouched.
func TestCommissioningWindow_BasicWindow_DoesNotInstall(t *testing.T) {
	t.Parallel()

	t.Run("IsBasicWindow", func(t *testing.T) {
		t.Parallel()
		w := bridge.NewCommissioningWindow()
		installer := &fakePaseVerifierInstaller{}
		w.SetPaseVerifierInstaller(installer)

		if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
			CommissioningTimeoutSeconds: 600,
			PAKEPasscodeVerifier:        newTestVerifier(),
			Iterations:                  1000,
			Salt:                        newTestSalt(),
			IsBasicWindow:               true,
		}); err != nil {
			t.Fatalf("OpenWindow: unexpected error: %v", err)
		}
		if n := installer.calls.Load(); n != 0 {
			t.Errorf("InstallVerifier call count for basic window = %d, want 0", n)
		}
	})

	t.Run("EmptyVerifier", func(t *testing.T) {
		t.Parallel()
		w := bridge.NewCommissioningWindow()
		installer := &fakePaseVerifierInstaller{}
		w.SetPaseVerifierInstaller(installer)

		if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
			CommissioningTimeoutSeconds: 600,
			IsBasicWindow:               false,
		}); err != nil {
			t.Fatalf("OpenWindow: unexpected error: %v", err)
		}
		if n := installer.calls.Load(); n != 0 {
			t.Errorf("InstallVerifier call count for empty verifier = %d, want 0", n)
		}
	})
}

// TestCommissioningWindow_InstallError_RevokesAndReturnsError verifies that
// a PaseVerifierInstaller error aborts the open: OpenWindow returns a
// non-nil error wrapping the installer's error, and the window is left
// closed rather than dangling half-open with no PASE acceptor able to
// service it.
func TestCommissioningWindow_InstallError_RevokesAndReturnsError(t *testing.T) {
	t.Parallel()
	w := bridge.NewCommissioningWindow()
	installErr := errors.New("malformed verifier")
	installer := &fakePaseVerifierInstaller{err: installErr}
	w.SetPaseVerifierInstaller(installer)

	err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 600,
		PAKEPasscodeVerifier:        newTestVerifier(),
		Iterations:                  1000,
		Salt:                        newTestSalt(),
	})
	if err == nil {
		t.Fatal("OpenWindow: want error when InstallVerifier fails, got nil")
	}
	if !errors.Is(err, installErr) {
		t.Errorf("OpenWindow error = %v, want wrapping %v", err, installErr)
	}

	if snap := w.CurrentWindow(); snap.Status != wire.WindowStatusClosed {
		t.Errorf("Status after install error = %v, want Closed", snap.Status)
	}
}
