// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"testing"
	"time"
)

// TestSessionMissBurst_FiresAtThreshold locks the rule that the eighth
// miss within the rolling window promotes the receive path's per-miss
// DEBUG row to a single INFO. Below the threshold the receive path
// stays silent — the iPhone-cache fingerprint only matters once a
// commissioner is genuinely retransmitting on a stale session-id, not
// after one or two stragglers.
func TestSessionMissBurst_FiresAtThreshold(t *testing.T) {
	t.Parallel()

	var b sessionMissBurst
	now := time.Now()

	// First seven misses must not emit — that's a normal MRP retransmit
	// clutch (capped at four retries per Matter §4.11.2.1) plus a
	// little headroom for cross-stack overlap.
	for i := uint32(0); i < sessionMissBurstThreshold-1; i++ {
		if b.record(42, now.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatalf("miss %d emitted prematurely; threshold is %d", i+1, sessionMissBurstThreshold)
		}
	}

	// Eighth miss inside the window must emit.
	if !b.record(42, now.Add(100*time.Millisecond)) {
		t.Fatalf("threshold miss did not emit")
	}
}

// TestSessionMissBurst_CooldownSuppressesRepeats locks the cooldown
// rule: once a burst has been logged for a given session-id the next
// burst is silent until the cooldown elapses. A permanently wedged
// iPhone must not produce an INFO row per datagram.
func TestSessionMissBurst_CooldownSuppressesRepeats(t *testing.T) {
	t.Parallel()

	var b sessionMissBurst
	now := time.Now()

	// Drive the first burst to emission.
	for i := uint32(0); i < sessionMissBurstThreshold; i++ {
		b.record(7, now.Add(time.Duration(i)*time.Millisecond))
	}

	// Further misses within the cooldown stay silent even if they
	// would otherwise cross the threshold again (the count carries
	// forward inside the same rolling window).
	for i := uint32(0); i < sessionMissBurstThreshold*3; i++ {
		if b.record(7, now.Add(200*time.Millisecond+time.Duration(i)*time.Millisecond)) {
			t.Fatalf("repeat miss %d emitted inside cooldown window", i)
		}
	}

	// After the cooldown elapses the next threshold-crossing miss
	// emits again — operator sees one row per remediation cycle.
	pastCooldown := now.Add(sessionMissBurstCooldown).Add(time.Second)
	// Reset accumulator via the window-aged-out branch first.
	if b.record(7, pastCooldown) {
		t.Fatalf("first post-cooldown miss should reset the window, not emit")
	}
	// Pump the counter to threshold-2 so the final record below is the
	// one that crosses the threshold (sessionMissBurstThreshold-2
	// additional misses leave the counter at threshold-1; the final
	// record bumps it to threshold).
	for i := uint32(0); i < sessionMissBurstThreshold-2; i++ {
		if b.record(7, pastCooldown.Add(time.Duration(i+1)*time.Millisecond)) {
			t.Fatalf("pre-threshold miss %d emitted prematurely", i+1)
		}
	}
	if !b.record(7, pastCooldown.Add(time.Second)) {
		t.Fatalf("post-cooldown threshold miss should re-emit")
	}
}

// TestSessionMissBurst_WindowResetsAfterQuiet locks the rolling-window
// rule: a single late retransmit after a quiet period must not chain
// onto a previous near-miss burst. A noisy night followed by silence
// followed by one stray packet must not emit.
func TestSessionMissBurst_WindowResetsAfterQuiet(t *testing.T) {
	t.Parallel()

	var b sessionMissBurst
	now := time.Now()

	// Accumulate up to one-below-threshold inside the window.
	for i := uint32(0); i < sessionMissBurstThreshold-1; i++ {
		b.record(99, now.Add(time.Duration(i)*time.Millisecond))
	}

	// Quiet period exceeding the rolling window must reset the
	// accumulator on the next miss.
	pastWindow := now.Add(sessionMissBurstWindow).Add(time.Second)
	if b.record(99, pastWindow) {
		t.Fatalf("first miss after window-aged-out reset should not emit")
	}
}

// TestSessionMissBurst_PerSessionIDIsolation locks the rule that
// bursts are tracked per session-id, not globally. Two simultaneous
// wedged controllers must each get their own INFO row at threshold.
func TestSessionMissBurst_PerSessionIDIsolation(t *testing.T) {
	t.Parallel()

	var b sessionMissBurst
	now := time.Now()

	// Drive session 1 to one-below-threshold; session 2 should still
	// be at zero misses.
	for i := uint32(0); i < sessionMissBurstThreshold-1; i++ {
		b.record(1, now.Add(time.Duration(i)*time.Millisecond))
	}
	if b.record(2, now) {
		t.Fatalf("session 2 emitted on first miss")
	}

	// One more miss on session 1 emits; session 2 still at one miss
	// stays silent.
	if !b.record(1, now.Add(10*time.Millisecond)) {
		t.Fatalf("session 1 did not emit at threshold")
	}
	if b.record(2, now.Add(20*time.Millisecond)) {
		t.Fatalf("session 2 emitted at miss 2")
	}
}
