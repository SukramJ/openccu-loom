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
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
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

// TestBridge_PaseLockedOutAtCap pins that the cap actually STOPS PASE.
// Revoking the commissioning window is not enough on its own: the
// configured-passcode acceptor is a long-lived fallback that stays armed
// with no window open, and RevokeWindow on a closed window is a no-op —
// so before the latch every paseMaxErrors failures merely reset the
// counter and guessing continued for the daemon's lifetime.
func TestBridge_PaseLockedOutAtCap(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.randomSource = func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} }
	b.AttachPaseHandler(a)

	for range paseMaxErrors {
		b.recordPaseFailure()
	}
	if !b.paseLockedOut() {
		t.Fatal("paseLockedOut = false at the cap; PASE would keep accepting guesses")
	}

	// A well-formed PBKDFParamRequest must now be dropped without
	// reaching the handler.
	before := b.paseFailures.Load()
	reqBytes := buildTestPBKDFParamRequest(t, bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize))
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, 9, false, 0), reqBytes); err != nil {
		t.Fatalf("dispatch while locked out: %v", err)
	}
	if got := b.paseFailures.Load(); got != before {
		t.Errorf("paseFailures = %d, want %d — a locked-out PASE datagram must not reach the handler", got, before)
	}

	// A fresh acceptor (a new commissioning window) lifts the lock.
	b.AttachPaseHandler(a)
	if b.paseLockedOut() {
		t.Fatal("paseLockedOut still true after a fresh acceptor was installed; the operator has no way back")
	}
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, 10, false, 0), reqBytes); err != nil {
		t.Fatalf("dispatch after re-attach: %v", err)
	}
	if got := b.paseFailures.Load(); got != 0 {
		t.Errorf("paseFailures = %d after a valid request on a fresh acceptor, want 0", got)
	}
}

// TestBridge_PaseInFlightSlotReleasedOnPake1Failure pins that a handshake
// dying before Pake3 releases the single-active-PASE slot. The Pake3
// branch is the only other release, so a failed Pake1 used to hold the
// slot for the full pasePairingTimeout: the operator's immediate retry
// opens a NEW exchange id, is refused as pase_busy, and pairing is
// impossible for up to a minute.
func TestBridge_PaseInFlightSlotReleasedOnPake1Failure(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	a := NewPaseAdapterWithFactory(newVerifierFactory(t, nil))
	a.SetPBKDFParams(failureCountTestIterations, failureCountTestSalt(), 1)
	a.randomSource = func() [spake2.PBKDFRandomSize]byte { return [spake2.PBKDFRandomSize]byte{0x11} }
	b.AttachPaseHandler(a)

	const firstExchange = uint16(11)
	reqBytes := buildTestPBKDFParamRequest(t, bytes.Repeat([]byte{0x22}, spake2.PBKDFRandomSize))
	if err := b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePBKDFParamRequest, firstExchange, false, 0), reqBytes); err != nil {
		t.Fatalf("dispatch PBKDFParamRequest: %v", err)
	}
	badPake1 := spake2.EncodePake1(make([]byte, 10)) // wrong pA length
	_ = b.dispatchSecureChannel(loopbackSrc(), scHdr(), scProto(mrp.SCOpcodePake1, firstExchange, false, 0), badPake1)

	// The commissioner retries on a fresh exchange id.
	if !b.claimPaseInFlight(firstExchange + 1) {
		t.Fatal("single-active-PASE slot still held after a failed Pake1; a retry would be refused as pase_busy")
	}
}

// TestBridge_PaseLockoutExpiresOnItsOwn pins that the brute-force refusal
// is a cooldown, not a permanent latch. An uncommissioned bridge answers
// PASE from its configured passcode with no window ever opened, so a
// refusal that only a fresh acceptor could clear let any LAN host disable
// pairing for the daemon's lifetime with paseMaxErrors malformed
// datagrams.
func TestBridge_PaseLockoutExpiresOnItsOwn(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	now := time.Now()
	b.nowFn = func() time.Time { return now }

	for range paseMaxErrors {
		b.recordPaseFailure()
	}
	if !b.paseLockedOut() {
		t.Fatal("paseLockedOut = false at the cap; PASE would keep accepting guesses")
	}

	// Just before the cooldown ends PASE is still refused …
	now = now.Add(paseLockoutCooldown - time.Second)
	if !b.paseLockedOut() {
		t.Fatal("lockout lifted before the cooldown expired")
	}
	// … and once it has passed the bridge answers PASE again without any
	// operator action.
	now = now.Add(2 * time.Second)
	if b.paseLockedOut() {
		t.Fatal("PASE still locked out after the cooldown expired — an unauthenticated peer can disable pairing permanently")
	}
	if got := b.paseFailures.Load(); got != 0 {
		t.Errorf("paseFailures = %d after the lockout expired, want 0 — the next window of guesses must start from a full budget", got)
	}
}

// TestBridge_PaseLockoutBacksOffOnRepeatedCaps pins the doubling backoff:
// a host that keeps guessing through the cooldown pays an ever longer
// refusal, capped at paseLockoutMaxCooldown, while the first lockout stays
// short enough for an operator who simply mistyped the code.
func TestBridge_PaseLockoutBacksOffOnRepeatedCaps(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	now := time.Now()
	b.nowFn = func() time.Time { return now }

	trip := func() time.Duration {
		t.Helper()
		start := now
		for range paseMaxErrors {
			b.recordPaseFailure()
		}
		b.mu.RLock()
		until := b.paseLockoutUntil
		b.mu.RUnlock()
		return until.Sub(start)
	}

	if got := trip(); got != paseLockoutCooldown {
		t.Errorf("first lockout = %v, want %v", got, paseLockoutCooldown)
	}
	now = now.Add(paseLockoutCooldown)
	if got := trip(); got != 2*paseLockoutCooldown {
		t.Errorf("second lockout = %v, want %v", got, 2*paseLockoutCooldown)
	}
	// Run the streak far past the ceiling; the cooldown must not grow
	// without bound.
	for range 20 {
		now = now.Add(paseLockoutMaxCooldown)
		_ = trip()
	}
	now = now.Add(paseLockoutMaxCooldown)
	if got := trip(); got != paseLockoutMaxCooldown {
		t.Errorf("lockout after a long streak = %v, want the ceiling %v", got, paseLockoutMaxCooldown)
	}

	// Opening a pairing window (a fresh acceptor) clears both the refusal
	// and the accumulated backoff.
	b.AttachPaseHandler(nil)
	if b.paseLockedOut() {
		t.Fatal("paseLockedOut still true after a fresh acceptor was installed; the operator has no way back")
	}
	if got := trip(); got != paseLockoutCooldown {
		t.Errorf("lockout after an operator intervention = %v, want the base cooldown %v", got, paseLockoutCooldown)
	}
}
