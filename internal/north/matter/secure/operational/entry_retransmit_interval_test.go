// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package operational_test

import (
	"testing"
	"time"
)

// TestEntry_RetransmitBaseInterval_FreshDefaultsToIdle verifies that a
// freshly-opened Entry (no peer-advertised MRP hints, no inbound
// traffic yet) reports the spec SESSION_IDLE_INTERVAL default (500ms)
// as its retransmit base. Mirrors matter.js MRP.ts:129-135
// retransmissionIntervalOf / chip GetMRPBaseTimeout — an unknown peer
// is treated as idle until proven active.
func TestEntry_RetransmitBaseInterval_FreshDefaultsToIdle(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager()
	e, err := m.OpenFromSigma(1, 10, 20, testKeys())
	if err != nil {
		t.Fatalf("OpenFromSigma: %v", err)
	}
	got := e.RetransmitBaseInterval(time.Now())
	if got != 500*time.Millisecond {
		t.Errorf("RetransmitBaseInterval (fresh entry) = %v, want 500ms (SessionIdleIntervalDefault)", got)
	}
}

// TestEntry_RetransmitBaseInterval_ActiveAfterMarkActiveRx verifies
// that once the peer's MRP hints are recorded AND the peer has sent a
// message (MarkActiveRx), RetransmitBaseInterval switches to the
// peer's advertised active interval.
func TestEntry_RetransmitBaseInterval_ActiveAfterMarkActiveRx(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager()
	e, err := m.OpenFromSigma(1, 10, 20, testKeys())
	if err != nil {
		t.Fatalf("OpenFromSigma: %v", err)
	}
	e.SetPeerMRPIntervals(5000, 1000, 2000)
	e.MarkActiveRx()
	got := e.RetransmitBaseInterval(time.Now())
	if got != 1000*time.Millisecond {
		t.Errorf("RetransmitBaseInterval (active peer) = %v, want 1000ms (peer active interval)", got)
	}
}

// TestEntry_RetransmitBaseInterval_IdleWithoutRx verifies that
// advertised MRP hints alone (without any inbound activity) do NOT
// switch the entry to the active interval — lastPeerActivity stays
// the zero time, so "peer active within the threshold" is false.
func TestEntry_RetransmitBaseInterval_IdleWithoutRx(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager()
	e, err := m.OpenFromSigma(1, 10, 20, testKeys())
	if err != nil {
		t.Fatalf("OpenFromSigma: %v", err)
	}
	e.SetPeerMRPIntervals(5000, 1000, 2000)
	// No MarkActiveRx call.
	got := e.RetransmitBaseInterval(time.Now())
	if got != 5000*time.Millisecond {
		t.Errorf("RetransmitBaseInterval (no peer rx yet) = %v, want 5000ms (peer idle interval)", got)
	}
}

// TestEntry_RetransmitBaseInterval_MarkActiveTxDoesNotFlipToActive
// verifies that outbound-only traffic (MarkActiveTx) does NOT count
// as peer activity for the active/idle determination — only inbound
// decrypts (MarkActiveRx) do. Mirrors chip SecureSession's distinct
// GetLastPeerActivityTime (Rx-only) vs the general last-activity
// timestamp (Rx+Tx) used for idle-session eviction.
func TestEntry_RetransmitBaseInterval_MarkActiveTxDoesNotFlipToActive(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager()
	e, err := m.OpenFromSigma(1, 10, 20, testKeys())
	if err != nil {
		t.Fatalf("OpenFromSigma: %v", err)
	}
	e.SetPeerMRPIntervals(5000, 1000, 2000)
	e.MarkActiveTx()
	got := e.RetransmitBaseInterval(time.Now())
	if got != 5000*time.Millisecond {
		t.Errorf("RetransmitBaseInterval (Tx-only entry) = %v, want 5000ms (idle) — MarkActiveTx must not flip peer-active state", got)
	}
}
