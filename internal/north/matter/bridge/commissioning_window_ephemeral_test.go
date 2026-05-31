// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// ─── fakeEphemeralProvider ────────────────────────────────────────────────────

type fakeEphemeralProvider struct {
	creds   bridge.EphemeralCredentials
	err     error
	calls   atomic.Int32
	restore *atomic.Int32 // shared counter; provider returns a Restore that increments this
}

func (f *fakeEphemeralProvider) GenerateAndInstall(_ context.Context) (bridge.EphemeralCredentials, error) {
	f.calls.Add(1)
	if f.err != nil {
		return bridge.EphemeralCredentials{}, f.err
	}
	creds := f.creds
	if f.restore != nil {
		ctr := f.restore
		creds.Restore = func() { ctr.Add(1) }
	}
	return creds, nil
}

// ─── RandomDiscriminator ─────────────────────────────────────────────────────

func TestRandomDiscriminator_HighBitsClear(t *testing.T) {
	t.Parallel()
	for i := 0; i < 100; i++ {
		d, err := bridge.RandomDiscriminator()
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if d > 0x0FFF {
			t.Errorf("iteration %d: discriminator 0x%X exceeds 12-bit range (0x0FFF)", i, d)
		}
	}
}

// ─── RandomPasscode ───────────────────────────────────────────────────────────

func TestRandomPasscode_RangeAndInvalidSet(t *testing.T) {
	t.Parallel()
	invalid := map[uint32]struct{}{
		0:        {},
		11111111: {},
		22222222: {},
		33333333: {},
		44444444: {},
		55555555: {},
		66666666: {},
		77777777: {},
		88888888: {},
		99999999: {},
		12345678: {},
		87654321: {},
	}
	for i := 0; i < 200; i++ {
		p, err := bridge.RandomPasscode()
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if p > 99999998 {
			t.Errorf("iteration %d: passcode %d exceeds 99999998", i, p)
		}
		if _, bad := invalid[p]; bad {
			t.Errorf("iteration %d: passcode %d is in the invalid set", i, p)
		}
	}
}

// ─── RandomSalt ───────────────────────────────────────────────────────────────

func TestRandomSalt_Length(t *testing.T) {
	t.Parallel()
	s1, err := bridge.RandomSalt()
	if err != nil {
		t.Fatalf("first RandomSalt: unexpected error: %v", err)
	}
	if len(s1) != 16 {
		t.Errorf("len(salt) = %d, want 16", len(s1))
	}

	s2, err := bridge.RandomSalt()
	if err != nil {
		t.Fatalf("second RandomSalt: unexpected error: %v", err)
	}
	if bytes.Equal(s1, s2) {
		t.Error("two RandomSalt calls returned identical values (probability 1/2^128 — almost certainly a bug)")
	}
}

// ─── CommissioningWindowOpener ephemeral mode ─────────────────────────────────

func TestCommissioningWindowOpener_Ephemeral_UsesProviderCreds(t *testing.T) {
	t.Parallel()
	const (
		disc     uint16 = 0x123
		passcode uint32 = 20202022
	)
	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, 0, 0x1234, 0x5678)

	fake := &fakeEphemeralProvider{
		creds: bridge.EphemeralCredentials{
			Discriminator: disc,
			Passcode:      passcode,
		},
	}
	opener.SetEphemeralProvider(fake)

	result, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow: unexpected error: %v", err)
	}

	if result.Discriminator != disc {
		t.Errorf("Discriminator = 0x%X, want 0x%X", result.Discriminator, disc)
	}
	if result.Passcode != passcode {
		t.Errorf("Passcode = %d, want %d", result.Passcode, passcode)
	}
	if len(result.ManualCode) != 11 {
		t.Errorf("ManualCode = %q (len=%d), want 11 chars", result.ManualCode, len(result.ManualCode))
	}
	if !strings.HasPrefix(result.QRCode, "MT:") {
		t.Errorf("QRCode = %q, want prefix \"MT:\"", result.QRCode)
	}
}

func TestCommissioningWindowOpener_Ephemeral_RestoreCalledOnRevoke(t *testing.T) {
	t.Parallel()
	var restoreCtr atomic.Int32

	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, 0, 0x1234, 0x5678)

	fake := &fakeEphemeralProvider{
		creds: bridge.EphemeralCredentials{
			Discriminator: 0x456,
			Passcode:      20202021,
		},
		restore: &restoreCtr,
	}
	opener.SetEphemeralProvider(fake)

	if _, err := opener.OpenCommissioningWindow(context.Background(), 600); err != nil {
		t.Fatalf("OpenCommissioningWindow: unexpected error: %v", err)
	}

	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("RevokeWindow: unexpected error: %v", err)
	}
	if n := restoreCtr.Load(); n != 1 {
		t.Errorf("restore call count after RevokeWindow = %d, want 1", n)
	}

	// A second RevokeWindow must not re-fire restore (closure is nilled on close).
	if err := w.RevokeWindow(context.Background()); err != nil {
		t.Fatalf("second RevokeWindow: unexpected error: %v", err)
	}
	if n := restoreCtr.Load(); n != 1 {
		t.Errorf("restore call count after second RevokeWindow = %d, want still 1", n)
	}
}

func TestCommissioningWindowOpener_Ephemeral_ProviderErrorNoSwap(t *testing.T) {
	t.Parallel()
	var restoreCtr atomic.Int32
	providerErr := errors.New("spake2+ gen failed")

	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, 99999997, 0x1234, 0x5678)

	fake := &fakeEphemeralProvider{
		err:     providerErr,
		restore: &restoreCtr,
	}
	opener.SetEphemeralProvider(fake)

	_, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if err == nil {
		t.Fatal("OpenCommissioningWindow: expected error, got nil")
	}
	if !errors.Is(err, providerErr) {
		t.Errorf("error = %v; want it to wrap %v", err, providerErr)
	}

	snap := w.CurrentWindow()
	if snap.Status != wire.WindowStatusClosed {
		t.Errorf("window status = %v after provider error, want Closed", snap.Status)
	}

	if n := restoreCtr.Load(); n != 0 {
		t.Errorf("restore call count = %d, want 0 (provider errored, no swap happened)", n)
	}
	if n := fake.calls.Load(); n != 1 {
		t.Errorf("provider call count = %d, want 1", n)
	}
}

func TestCommissioningWindowOpener_Ephemeral_PasscodeZeroFromProvider(t *testing.T) {
	t.Parallel()
	var restoreCtr atomic.Int32

	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, 0, 0x1234, 0x5678)

	fake := &fakeEphemeralProvider{
		creds: bridge.EphemeralCredentials{
			Discriminator: 0x789,
			Passcode:      0, // invalid
		},
		restore: &restoreCtr,
	}
	opener.SetEphemeralProvider(fake)

	_, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if !errors.Is(err, bridge.ErrCommissioningWindowNotConfigured) {
		t.Errorf("want ErrCommissioningWindowNotConfigured, got %v", err)
	}

	// Restore must have been invoked exactly once: the provider installed
	// the adapter; the opener must revert it.
	if n := restoreCtr.Load(); n != 1 {
		t.Errorf("restore call count = %d, want 1 (opener must revert the swap)", n)
	}
}

// TestCommissioningWindowOpener_SetEphemeralProvider_Nil verifies that
// SetEphemeralProvider(nil) clears the provider and subsequent calls to
// OpenCommissioningWindow use the opener's static passcode instead.
func TestCommissioningWindowOpener_SetEphemeralProvider_Nil(t *testing.T) {
	t.Parallel()
	const passcode uint32 = 20202021

	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, passcode, 0x1234, 0x5678)

	// Install an ephemeral provider, then clear it.
	fake := &fakeEphemeralProvider{
		creds: bridge.EphemeralCredentials{
			Discriminator: 0x111,
			Passcode:      11111111, // would be invalid — we never expect it to be used
		},
	}
	opener.SetEphemeralProvider(fake)
	opener.SetEphemeralProvider(nil) // clear

	// Without an ephemeral provider the opener uses the static passcode.
	result, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow after SetEphemeralProvider(nil): unexpected error: %v", err)
	}
	if result.Passcode != passcode {
		t.Errorf("Passcode = %d, want %d (static passcode)", result.Passcode, passcode)
	}
	// The fake provider must not have been called after it was cleared.
	if n := fake.calls.Load(); n != 0 {
		t.Errorf("fakeEphemeralProvider call count = %d, want 0 (provider was cleared)", n)
	}
}

// TestCommissioningWindowOpener_Ephemeral_AlreadyOpenWithRestore verifies
// that when OpenWindow fails with ErrAdmCommBusy (window already open) and
// an ephemeral provider was active, the restore closure is called and the
// error is wrapped as ErrCommissioningWindowAlreadyOpen.
func TestCommissioningWindowOpener_Ephemeral_AlreadyOpenWithRestore(t *testing.T) {
	t.Parallel()
	var restoreCtr atomic.Int32

	w := bridge.NewCommissioningWindow()
	opener := bridge.NewCommissioningWindowOpener(w, 0xABC, 0, 0x1234, 0x5678)

	fake := &fakeEphemeralProvider{
		creds: bridge.EphemeralCredentials{
			Discriminator: 0x456,
			Passcode:      20202021,
		},
		restore: &restoreCtr,
	}
	opener.SetEphemeralProvider(fake)

	// First open succeeds.
	if _, err := opener.OpenCommissioningWindow(context.Background(), 600); err != nil {
		t.Fatalf("first OpenCommissioningWindow: unexpected error: %v", err)
	}

	// Second open must fail with ErrCommissioningWindowAlreadyOpen and call restore.
	restoresBefore := restoreCtr.Load()
	_, err := opener.OpenCommissioningWindow(context.Background(), 600)
	if !errors.Is(err, bridge.ErrCommissioningWindowAlreadyOpen) {
		t.Errorf("second OpenCommissioningWindow: want ErrCommissioningWindowAlreadyOpen, got %v", err)
	}
	// The restore must have been called once for the second attempt (to undo
	// the ephemeral swap done before OpenWindow returned busy).
	if n := restoreCtr.Load(); n != restoresBefore+1 {
		t.Errorf("restore call count after busy-window attempt = %d, want %d", restoreCtr.Load(), restoresBefore+1)
	}
}
