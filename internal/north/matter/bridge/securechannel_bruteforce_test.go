// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the PASE brute-force cap: recordPaseFailure,
// resetPaseFailures, and paseMaxErrors. Lives in package bridge (not
// bridge_test) so it can call the unexported counter methods directly
// instead of driving them indirectly through dispatchSecureChannel.
// Mirrors matter.js PaseServer.ts PASE_COMMISSIONING_MAX_ERRORS — see
// securechannel.go's recordPaseFailure doc comment for the full citation.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// openedWindow attaches a fresh CommissioningWindow to b and opens it,
// returning the window for status assertions.
func openedWindow(t *testing.T, b *Bridge) *CommissioningWindow {
	t.Helper()
	w := NewCommissioningWindow()
	b.AttachCommissioningWindow(w)
	if err := w.OpenWindow(context.Background(), wire.OpenWindowParams{
		CommissioningTimeoutSeconds: 300,
		Discriminator:               0xABC,
	}); err != nil {
		t.Fatalf("OpenWindow: unexpected error: %v", err)
	}
	if got := w.CurrentWindow().Status; got != wire.WindowStatusEnhanced {
		t.Fatalf("precondition: window Status = %v, want Enhanced (open)", got)
	}
	return w
}

// TestBridge_PaseFailures_RevokesWindowAtCap verifies that recording
// paseMaxErrors consecutive PASE failures revokes the attached
// commissioning window (Status transitions to Closed).
func TestBridge_PaseFailures_RevokesWindowAtCap(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	w := openedWindow(t, b)

	for range paseMaxErrors {
		b.recordPaseFailure()
	}

	if got := w.CurrentWindow().Status; got != wire.WindowStatusClosed {
		t.Errorf("Status after %d failures = %v, want Closed (revoked)", paseMaxErrors, got)
	}
}

// TestBridge_PaseFailures_BelowCapKeepsWindowOpen verifies that
// paseMaxErrors-1 failures do not trip the cap — the window stays open.
func TestBridge_PaseFailures_BelowCapKeepsWindowOpen(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	w := openedWindow(t, b)

	for range paseMaxErrors - 1 {
		b.recordPaseFailure()
	}

	if got := w.CurrentWindow().Status; got != wire.WindowStatusEnhanced {
		t.Errorf("Status after %d failures = %v, want Enhanced (still open)", paseMaxErrors-1, got)
	}
}

// TestBridge_PaseFailures_ResetGivesFreshBudget verifies that
// resetPaseFailures (as called by AttachPaseHandler on a fresh
// acceptor) clears the counter, so a second run of paseMaxErrors-1
// failures after a reset still does not revoke the window — two
// sub-cap bursts around a reset must not sum past the cap.
func TestBridge_PaseFailures_ResetGivesFreshBudget(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	w := openedWindow(t, b)

	for range paseMaxErrors - 1 {
		b.recordPaseFailure()
	}
	b.resetPaseFailures()
	for range paseMaxErrors - 1 {
		b.recordPaseFailure()
	}

	if got := w.CurrentWindow().Status; got != wire.WindowStatusEnhanced {
		t.Errorf("Status after reset + %d more failures = %v, want Enhanced (fresh budget, still open)", paseMaxErrors-1, got)
	}
}

// TestBridge_PaseFailures_NoWindowIsNilSafe verifies that
// recordPaseFailure is a no-op (not a panic) when no commissioning
// window is attached — the missing-window guard in recordPaseFailure
// must short-circuit before touching a nil *CommissioningWindow.
func TestBridge_PaseFailures_NoWindowIsNilSafe(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	if b.CommissioningWindow() != nil {
		t.Fatal("precondition: expected no commissioning window attached")
	}

	for range paseMaxErrors {
		b.recordPaseFailure()
	}
}
