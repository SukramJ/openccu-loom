// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

var t0 = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

// TestAckTrackerConstants asserts the three protocol constants match the
// Matter Core Spec values (locks the spec mapping against silent drift).
func TestAckTrackerConstants(t *testing.T) {
	if mrp.SecureChannelProtocolID != 0x0000 {
		t.Errorf("SecureChannelProtocolID = 0x%04X, want 0x0000", mrp.SecureChannelProtocolID)
	}
	if mrp.StandaloneAckOpcode != 0x10 {
		t.Errorf("StandaloneAckOpcode = 0x%02X, want 0x10", mrp.StandaloneAckOpcode)
	}
	if mrp.DefaultStandaloneAckDelay != 200*time.Millisecond {
		t.Errorf("DefaultStandaloneAckDelay = %v, want 200ms", mrp.DefaultStandaloneAckDelay)
	}
}

// TestAckTrackerOweAndPending verifies that a single Owe call raises
// Pending from 0 to 1.
func TestAckTrackerOweAndPending(t *testing.T) {
	tracker := mrp.NewAckTracker(0)
	if tracker.Pending() != 0 {
		t.Fatalf("Pending() = %d on fresh tracker, want 0", tracker.Pending())
	}
	tracker.Owe(100, 1, true, t0)
	if tracker.Pending() != 1 {
		t.Fatalf("Pending() = %d after Owe, want 1", tracker.Pending())
	}
}

// TestAckTrackerDischarge confirms Discharge returns true the first time
// (entry found), false the second (already removed), and Pending drops to 0.
func TestAckTrackerDischarge(t *testing.T) {
	tracker := mrp.NewAckTracker(0)
	tracker.Owe(100, 1, true, t0)
	if !tracker.Discharge(1) {
		t.Fatal("Discharge(1) = false on first call, want true")
	}
	if tracker.Pending() != 0 {
		t.Fatalf("Pending() = %d after Discharge, want 0", tracker.Pending())
	}
	if tracker.Discharge(1) {
		t.Fatal("Discharge(1) = true on second call, want false")
	}
}

// TestAckTrackerDueWithDelay confirms that an obligation is not returned
// before the delay has elapsed but is returned (and correct) once past it.
func TestAckTrackerDueWithDelay(t *testing.T) {
	const delay = 100 * time.Millisecond
	tracker := mrp.NewAckTracker(delay)
	tracker.Owe(100, 1, true, t0)

	// Before delay — nothing due.
	before := tracker.Due(t0)
	if len(before) != 0 {
		t.Fatalf("Due at t0: got %d obligations, want 0", len(before))
	}

	// Exactly at deadline — should be due now.
	after := tracker.Due(t0.Add(delay))
	if len(after) != 1 {
		t.Fatalf("Due at t0+delay: got %d obligations, want 1", len(after))
	}
	if after[0].AckCounter != 100 {
		t.Errorf("AckCounter = %d, want 100", after[0].AckCounter)
	}
}

// TestAckTrackerDueRemovesEntries drains obligations via Due and verifies
// the tracker is empty afterwards; a second Due call returns nil.
func TestAckTrackerDueRemovesEntries(t *testing.T) {
	tracker := mrp.NewAckTracker(0)
	tracker.Owe(42, 7, false, t0)

	first := tracker.Due(t0)
	if len(first) != 1 {
		t.Fatalf("first Due: got %d obligations, want 1", len(first))
	}
	if tracker.Pending() != 0 {
		t.Fatalf("Pending() = %d after Due, want 0", tracker.Pending())
	}
	second := tracker.Due(t0)
	if second != nil {
		t.Fatalf("second Due: got %v, want nil", second)
	}
}

// TestAckTrackerOweUpgradeKeepsLatest verifies that owing a higher counter
// on the same exchange replaces the previous entry (cumulative-ACK collapse);
// Pending stays at 1 and Due returns the latest counter.
func TestAckTrackerOweUpgradeKeepsLatest(t *testing.T) {
	tracker := mrp.NewAckTracker(0)
	tracker.Owe(100, 1, true, t0)
	tracker.Owe(150, 1, true, t0)

	if tracker.Pending() != 1 {
		t.Fatalf("Pending() = %d after two Owes on same exchange, want 1", tracker.Pending())
	}
	obligations := tracker.Due(t0)
	if len(obligations) != 1 {
		t.Fatalf("Due: got %d obligations, want 1", len(obligations))
	}
	if obligations[0].AckCounter != 150 {
		t.Errorf("AckCounter = %d, want 150 (latest counter)", obligations[0].AckCounter)
	}
}

// TestAckTrackerOweOlderCounterIsIgnored confirms that owing an older
// counter on an existing exchange is a no-op: the higher counter and its
// original DueAt are preserved.
func TestAckTrackerOweOlderCounterIsIgnored(t *testing.T) {
	const delay = 50 * time.Millisecond
	tracker := mrp.NewAckTracker(delay)
	tracker.Owe(150, 1, true, t0)
	// Attempt to overwrite with a lower counter at a later time.
	tracker.Owe(100, 1, true, t0.Add(50*time.Millisecond))

	if tracker.Pending() != 1 {
		t.Fatalf("Pending() = %d, want 1", tracker.Pending())
	}
	// DueAt was set to t0+delay by the first Owe; the second Owe must not
	// slide it forward. Draining at t0+delay should yield the original counter.
	obligations := tracker.Due(t0.Add(delay))
	if len(obligations) != 1 {
		t.Fatalf("Due: got %d obligations, want 1", len(obligations))
	}
	if obligations[0].AckCounter != 150 {
		t.Errorf("AckCounter = %d, want 150 (older counter rejected)", obligations[0].AckCounter)
	}
}

// TestAckTrackerMultipleExchangesIndependent confirms that two separate
// exchanges each keep their own obligation and that all fields survive the
// round-trip through the tracker.
func TestAckTrackerMultipleExchangesIndependent(t *testing.T) {
	tracker := mrp.NewAckTracker(0)
	tracker.Owe(100, 1, true, t0)
	tracker.Owe(200, 2, false, t0)

	if tracker.Pending() != 2 {
		t.Fatalf("Pending() = %d, want 2", tracker.Pending())
	}

	obligations := tracker.Due(t0)
	if len(obligations) != 2 {
		t.Fatalf("Due: got %d obligations, want 2", len(obligations))
	}

	// Build a map for order-independent assertion.
	byExchange := make(map[uint16]mrp.AckObligation, 2)
	for _, obl := range obligations {
		byExchange[obl.ExchangeID] = obl
	}

	obl1, ok1 := byExchange[1]
	if !ok1 {
		t.Fatal("no obligation for ExchangeID 1")
	}
	if obl1.AckCounter != 100 {
		t.Errorf("ExchangeID 1: AckCounter = %d, want 100", obl1.AckCounter)
	}
	if !obl1.Initiator {
		t.Error("ExchangeID 1: Initiator = false, want true")
	}

	obl2, ok2 := byExchange[2]
	if !ok2 {
		t.Fatal("no obligation for ExchangeID 2")
	}
	if obl2.AckCounter != 200 {
		t.Errorf("ExchangeID 2: AckCounter = %d, want 200", obl2.AckCounter)
	}
	if obl2.Initiator {
		t.Error("ExchangeID 2: Initiator = true, want false")
	}
}
