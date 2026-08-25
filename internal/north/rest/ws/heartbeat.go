// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// The heartbeat round-trip is the one latency the daemon cannot infer from
// its own work: the path from a browser, a Home Assistant add-on or a remote
// proxy to this process and back. It is deliberately NOT a hub data point or
// an MQTT sensor — the distance is a property of the viewer, not of the CCU,
// and the same daemon serves a SPA on the LAN, an ingress-tunnelled add-on
// and a public host at once. Publishing one number for all of them would name
// a distance that does not exist.
//
// So it travels two ways, both scoped to who it belongs to: back down the
// connection that was measured (so a client can render its own latency), and
// into the diagnostics gauge as a fleet aggregate an operator reads.

// buildPing serialises the heartbeat frame and arms the round-trip
// measurement. Called only from writePump, so a second ping cannot race the
// arming.
//
// The token is a per-connection counter, not a timestamp. A timestamp would be
// the obvious choice — the pong could then be timed without storing anything —
// but the first token of a connection is an elapsed duration of very nearly
// zero, and zero is the value this code reserves for "no ping outstanding".
// The counter starts at 1 and only grows, so no live token is ever mistaken
// for an empty slot.
//
// The send time is kept beside the token under a mutex rather than in a second
// atomic. Reading them independently lets the next ping overwrite the time
// between the pong's compare-and-swap and its subtraction, which would report
// a round-trip measured against the wrong send.
//
// Both readings come from the same monotonic source on this side of the wire,
// so neither a clock difference between the two ends nor an NTP step on either
// can distort the result. The client treats the token as opaque.
func (c *client) buildPing() []byte {
	echo := c.pingSeq.Add(1)
	c.pendingMu.Lock()
	c.pendingEcho = echo
	c.pendingSentAt = c.age()
	c.pendingMu.Unlock()

	op := outboundOp{Op: "ping", Echo: strconv.FormatUint(echo, 10)}
	if rtt := c.lastRTT.Load(); rtt > 0 {
		ms := float64(rtt) / float64(time.Millisecond)
		op.RTTMs = &ms
	}
	buf, err := json.Marshal(op)
	if err != nil {
		// outboundOp holds only scalars and a *float64; marshalling it cannot
		// fail. Fall back to the bare heartbeat rather than dropping the frame,
		// because a ping the client never sees costs it the connection.
		return []byte(`{"op":"ping"}`)
	}
	return buf
}

// noteHeartbeat closes the round-trip a `pong` answers. An empty or
// unparseable echo, and an echo that does not match the outstanding ping,
// leave the measurement untouched: the frame still counted as liveness (the
// read deadline was already extended by the read itself), it simply carries
// no timing.
//
// Clearing the pending token on a match is what makes a stale echo harmless. A
// client that answers late — after the next ping has gone out — echoes a token
// that no longer matches, and measuring it would report a full ping interval
// as this client's latency.
func (c *client) noteHeartbeat(echo string) {
	if echo == "" {
		return
	}
	sent, err := strconv.ParseUint(echo, 10, 64)
	if err != nil || sent == 0 {
		return
	}
	now := c.age()
	c.pendingMu.Lock()
	matched := c.pendingEcho == sent
	sentAt := c.pendingSentAt
	if matched {
		c.pendingEcho = 0
	}
	c.pendingMu.Unlock()
	if !matched {
		return
	}
	rtt := now - sentAt
	if rtt < 0 {
		return
	}
	// A round-trip the monotonic clock could not resolve is still a round-trip
	// that happened. Floor it at one nanosecond so it reports as the fastest
	// measurable connection rather than as lastRTT's "never measured" zero —
	// on a loopback client the two readings can genuinely land in the same
	// tick, and dropping those samples would leave the fastest connections
	// looking unmeasured.
	c.lastRTT.Store(max(int64(rtt), 1))
}

// HeartbeatRTT is the fleet view of client→daemon latency: one entry per
// connection that has completed at least one timed heartbeat.
type HeartbeatRTT struct {
	// Samples is the number of connections contributing a measurement. It is
	// not the number of connected clients — a client that never echoes is
	// connected but unmeasured, and reporting it as a zero-latency sample
	// would drag every aggregate below the truth.
	Samples int
	// MedianMs is the middle of the contributing connections' most recent
	// round-trips.
	MedianMs float64
	// MaxMs is the slowest of them. Reported beside the median because the
	// gap between the two is what says "one client is on a bad link" rather
	// than "the daemon is slow for everyone".
	MaxMs float64
}

// HeartbeatRTTs summarises the last measured round-trip of every connected
// client. The median rather than the mean is the headline: one client behind
// a slow tunnel is the normal case, and it should not move the number an
// operator reads for everyone else.
func (h *Hub) HeartbeatRTTs() HeartbeatRTT {
	h.mu.RLock()
	samples := make([]float64, 0, len(h.clients))
	for c := range h.clients {
		if rtt := c.lastRTT.Load(); rtt > 0 {
			samples = append(samples, float64(rtt)/float64(time.Millisecond))
		}
	}
	h.mu.RUnlock()
	if len(samples) == 0 {
		return HeartbeatRTT{}
	}
	sort.Float64s(samples)
	mid := len(samples) / 2
	median := samples[mid]
	if len(samples)%2 == 0 {
		median = (samples[mid-1] + samples[mid]) / 2
	}
	return HeartbeatRTT{
		Samples:  len(samples),
		MedianMs: median,
		MaxMs:    samples[len(samples)-1],
	}
}

// age is the connection's elapsed monotonic time, through the test seam when
// one is installed. See [client.elapsed].
func (c *client) age() time.Duration {
	if c.elapsed != nil {
		return c.elapsed()
	}
	return time.Since(c.born)
}
