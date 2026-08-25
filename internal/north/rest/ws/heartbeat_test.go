// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"encoding/json"
	"testing"
	"time"
)

// decodePing unpacks a serialised heartbeat frame.
func decodePing(t *testing.T, buf []byte) outboundOp {
	t.Helper()
	var op outboundOp
	if err := json.Unmarshal(buf, &op); err != nil {
		t.Fatalf("unmarshal ping: %v", err)
	}
	if op.Op != "ping" {
		t.Fatalf("op = %q, want \"ping\"", op.Op)
	}
	return op
}

// TestHeartbeatMeasuresRoundTripOnlyWhenEchoed is the negative control for the
// client→daemon latency measurement: an echoing client is timed, a client that
// answers the heartbeat without echoing is not.
//
// The distinction matters because the second case is not hypothetical — every
// client written against wsapi 1.1 answers with a bare `{"op":"pong"}`, and
// wsapi 1.2 keeps that a valid heartbeat. Reporting a number for it would mean
// reporting one measured from whatever token happened to be outstanding, which
// is a latency the daemon never observed.
func TestHeartbeatMeasuresRoundTripOnlyWhenEchoed(t *testing.T) {
	t.Parallel()

	t.Run("a bare pong leaves the connection unmeasured", func(t *testing.T) {
		t.Parallel()
		c := &client{born: time.Now()}
		decodePing(t, c.buildPing())

		c.noteHeartbeat("")

		if got := c.lastRTT.Load(); got != 0 {
			t.Errorf("lastRTT = %d after a pong with no echo, want 0", got)
		}
		if op := decodePing(t, c.buildPing()); op.RTTMs != nil {
			t.Errorf("the next ping reported rtt_ms = %v for a client that was never timed", *op.RTTMs)
		}
	})

	t.Run("an echoed pong is measured and reported on the next ping", func(t *testing.T) {
		t.Parallel()
		c := &client{born: time.Now()}
		first := decodePing(t, c.buildPing())
		if first.Echo == "" {
			t.Fatal("the heartbeat carries no echo token, so no client can be timed")
		}
		if first.RTTMs != nil {
			t.Errorf("the first ping reported rtt_ms = %v before anything was measured", *first.RTTMs)
		}

		c.noteHeartbeat(first.Echo)

		if c.lastRTT.Load() <= 0 {
			t.Fatal("an echoed pong produced no round-trip measurement")
		}
		second := decodePing(t, c.buildPing())
		if second.RTTMs == nil {
			t.Fatal("the measured round-trip was not reported back to the client")
		}
		if *second.RTTMs <= 0 {
			t.Errorf("reported rtt_ms = %v, want a positive duration", *second.RTTMs)
		}
		if second.Echo == first.Echo {
			t.Error("consecutive pings reuse the same echo token, so a late pong for the first would be " +
				"credited to the second")
		}
	})
}

// TestHeartbeatIgnoresStaleEcho pins the compare-and-swap that makes a late
// pong harmless. A client that answers after the next ping has already gone out
// echoes a token that is no longer outstanding; timing it from its original
// send would report a full ping interval as the client's latency, turning a
// briefly stalled tab into a permanently "slow" connection.
func TestHeartbeatIgnoresStaleEcho(t *testing.T) {
	t.Parallel()

	c := &client{born: time.Now()}
	stale := decodePing(t, c.buildPing()).Echo
	// The next heartbeat goes out before the client answered the first.
	decodePing(t, c.buildPing())

	c.noteHeartbeat(stale)

	if got := c.lastRTT.Load(); got != 0 {
		t.Errorf("a pong for a superseded ping was measured (rtt = %d ns), want ignored", got)
	}
}

// TestHeartbeatSubTickRoundTripStillCounts pins the floor on an unresolvable
// round-trip. A loopback client can answer inside the same monotonic tick the
// ping was stamped in, making the measured duration exactly zero — and zero is
// the value lastRTT reserves for "never measured". Dropping those samples would
// leave the fastest connections indistinguishable from the ones that never
// answered, which is the wrong way round.
//
// The clock is pinned through [client.elapsed] rather than raced against.
// An earlier version of this test set the send time to "now" and hoped the
// subtraction landed on zero; it never did, so it passed with the floor
// removed — a check that cannot produce the failure it names.
func TestHeartbeatSubTickRoundTripStillCounts(t *testing.T) {
	t.Parallel()

	t.Run("a same-tick round-trip reports the floor", func(t *testing.T) {
		t.Parallel()
		// Both readings return the same instant: ping and pong inside one tick.
		c := &client{elapsed: func() time.Duration { return 5 * time.Second }}
		echo := decodePing(t, c.buildPing()).Echo

		c.noteHeartbeat(echo)

		if got := c.lastRTT.Load(); got <= 0 {
			t.Errorf("lastRTT = %d for a round-trip the clock could not resolve, want a positive floor: "+
				"the connection answered, so it must not read as never measured", got)
		}
	})

	t.Run("a negative round-trip is discarded", func(t *testing.T) {
		t.Parallel()
		// The pong reads earlier than the ping — a bookkeeping fault, not a
		// fast connection, and the floor must not launder it into a sample.
		reading := 5 * time.Second
		c := &client{elapsed: func() time.Duration { return reading }}
		echo := decodePing(t, c.buildPing()).Echo
		reading = 4 * time.Second

		c.noteHeartbeat(echo)

		if got := c.lastRTT.Load(); got != 0 {
			t.Errorf("lastRTT = %d for a pong that reads earlier than its ping, want 0 (discarded)", got)
		}
	})

	t.Run("an ordinary round-trip reports its real duration", func(t *testing.T) {
		t.Parallel()
		reading := time.Second
		c := &client{elapsed: func() time.Duration { return reading }}
		echo := decodePing(t, c.buildPing()).Echo
		reading = time.Second + 42*time.Millisecond

		c.noteHeartbeat(echo)

		if got := c.lastRTT.Load(); got != int64(42*time.Millisecond) {
			t.Errorf("lastRTT = %d ns, want %d — the floor must not replace a real measurement",
				got, int64(42*time.Millisecond))
		}
	})
}

// TestHeartbeatRTTsReportsOnlyMeasuredConnections guards the fleet aggregate
// against the most tempting wrong answer: counting every connected client and
// treating the unmeasured ones as zero, which drags the median below anything
// real. Only connections that completed a timed heartbeat may contribute.
func TestHeartbeatRTTsReportsOnlyMeasuredConnections(t *testing.T) {
	t.Parallel()

	h := &Hub{clients: map[*client]struct{}{}}
	measured := &client{born: time.Now()}
	measured.lastRTT.Store(int64(40 * time.Millisecond))
	slow := &client{born: time.Now()}
	slow.lastRTT.Store(int64(300 * time.Millisecond))
	unmeasured := &client{born: time.Now()}
	h.clients[measured] = struct{}{}
	h.clients[slow] = struct{}{}
	h.clients[unmeasured] = struct{}{}

	got := h.HeartbeatRTTs()

	if got.Samples != 2 {
		t.Errorf("Samples = %d, want 2 — the never-timed connection must not contribute", got.Samples)
	}
	if got.MaxMs != 300 {
		t.Errorf("MaxMs = %v, want 300", got.MaxMs)
	}
	if got.MedianMs != 170 {
		t.Errorf("MedianMs = %v, want 170 (the mean of the two measured samples)", got.MedianMs)
	}
}

// TestHeartbeatRTTsEmptyWithoutSamples pins the honest empty answer: a hub
// nobody has connected to reports no samples rather than a zero that reads as
// "instantaneous".
func TestHeartbeatRTTsEmptyWithoutSamples(t *testing.T) {
	t.Parallel()

	h := &Hub{clients: map[*client]struct{}{}}
	if got := h.HeartbeatRTTs(); got.Samples != 0 || got.MedianMs != 0 {
		t.Errorf("HeartbeatRTTs() = %+v, want the zero value", got)
	}
}
