// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"net"
	"testing"
	"time"
)

// TestMRPRTTMeasuresFirstTryOnly is the negative control for the controller
// round-trip: a datagram acknowledged on its first transmission is timed, one
// acknowledged after a retransmit is not.
//
// This is Karn's algorithm, and MRP needs it for the same reason TCP does.
// After a resend there is no way to tell whether the ACK answers the original
// or the retransmission. Timing it from the first send would add whole backoff
// intervals — MRP's first retransmit alone waits hundreds of milliseconds — so
// a single lost datagram would report the controller as an order of magnitude
// slower than it is, which is precisely the reading an operator would act on.
func TestMRPRTTMeasuresFirstTryOnly(t *testing.T) {
	t.Parallel()

	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5540}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	t.Run("a first-try ACK is measured", func(t *testing.T) {
		t.Parallel()
		tr := newOutboundReliableTracker(nil)
		tr.Track(1, 7, 9, []byte{0x01}, dest, now)

		if !tr.Ack(7, 1) {
			t.Fatal("Ack reported no pending entry for a counter that was just tracked")
		}

		got := tr.RTTStats()
		if got.Total != 1 {
			t.Errorf("RTTStats() = %+v, want one measurement for a first-try ACK", got)
		}
	})

	t.Run("an ACK after a retransmit is not measured", func(t *testing.T) {
		t.Parallel()
		tr := newOutboundReliableTracker(nil)
		tr.Track(2, 7, 9, []byte{0x02}, dest, now)
		// Drive the retransmit the way the scheduler does, so `retries` is
		// incremented by production code rather than by the test reaching in.
		if sent := tr.Tick(now.Add(time.Hour), func(_ *net.UDPAddr, _ []byte) error { return nil }); len(sent) == 0 {
			t.Fatal("Tick issued no retransmit, so this case never reaches the state it means to test")
		}

		if !tr.Ack(7, 2) {
			t.Fatal("Ack reported no pending entry after the retransmit")
		}

		if got := tr.RTTStats(); got.Total != 0 {
			t.Errorf("RTTStats() = %+v after an ACK that followed a retransmit, want no samples: the ACK "+
				"cannot be attributed to either transmission", got)
		}
	})
}

// TestMRPRTTStatsEmptyWithoutPairing pins the honest empty answer. A bridge
// nobody has commissioned never sends a reliable message, so it has nothing to
// report — and must say so rather than reporting a zero that reads as an
// instantaneous controller.
func TestMRPRTTStatsEmptyWithoutPairing(t *testing.T) {
	t.Parallel()

	tr := newOutboundReliableTracker(nil)
	if got := tr.RTTStats(); got.Total != 0 || got.MedianMs != 0 {
		t.Errorf("RTTStats() = %+v on an untouched tracker, want the zero value", got)
	}
	var nilTracker *outboundReliableTracker
	if got := nilTracker.RTTStats(); got.Total != 0 {
		t.Errorf("nil tracker RTTStats() = %+v, want the zero value", got)
	}
}

// TestMRPRTTWindowRollsAndSummarises pins the summary maths and the bound on
// retained samples.
func TestMRPRTTWindowRollsAndSummarises(t *testing.T) {
	t.Parallel()

	var w mrpRTTWindow
	// A non-positive sample is not a round-trip; it would drag the median.
	w.record(0)
	w.record(-time.Millisecond)
	if got := w.stats(); got.Total != 0 {
		t.Fatalf("stats() = %+v after only non-positive samples, want the zero value", got)
	}

	for i := 1; i <= mrpRTTWindowSize+4; i++ {
		w.record(time.Duration(i) * time.Millisecond)
	}

	got := w.stats()
	if got.Total != uint64(mrpRTTWindowSize+4) {
		t.Errorf("Total = %d, want %d", got.Total, mrpRTTWindowSize+4)
	}
	if got.MaxMs != float64(mrpRTTWindowSize+4) {
		t.Errorf("MaxMs = %v, want %v", got.MaxMs, float64(mrpRTTWindowSize+4))
	}
	// Retained window is 5ms … 68ms; even count → mean of the two middles.
	if want := 36.5; got.MedianMs != want {
		t.Errorf("MedianMs = %v, want %v", got.MedianMs, want)
	}
}
