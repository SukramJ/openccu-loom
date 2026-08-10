// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// newBackpressureClient builds a pipe-backed client with a tiny outbound
// buffer so a couple of enqueues reach the overflow branch, plus the
// buffer the warnings land in.
func newBackpressureClient(t *testing.T, capacity int) (*client, *bytes.Buffer, *Hub) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	var logs bytes.Buffer
	hub := NewHub()
	c := &client{
		conn:   serverConn,
		br:     bufio.NewReader(serverConn),
		bw:     bufio.NewWriter(serverConn),
		hub:    hub,
		logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		out:    make(chan Event, capacity),
		ctrl:   make(chan wireMsg, capacity),
		closed: make(chan struct{}),
	}
	hub.register(c)
	return c, &logs, hub
}

// countWarnings returns how many ws.backpressure records the logger saw.
func countWarnings(t *testing.T, logs *bytes.Buffer) int {
	t.Helper()
	n := 0
	for line := range bytes.SplitSeq(bytes.TrimRight(logs.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not valid JSON: %v (%q)", err, line)
		}
		if rec["msg"] == "ws.backpressure" {
			n++
		}
	}
	return n
}

// drainCtrl collects the control frames queued for the writer.
func drainCtrl(c *client) []outboundOp {
	var ops []outboundOp
	for {
		select {
		case msg := <-c.ctrl:
			var op outboundOp
			if err := json.Unmarshal(msg.payload, &op); err == nil {
				ops = append(ops, op)
			}
		default:
			return ops
		}
	}
}

// TestEnqueueOverflowKeepsTheConnection pins the backpressure policy: a
// client that cannot keep up loses events, not its connection.
//
// The old policy closed the socket. On a large installation that made a
// daemon restart cut every open SPA session: the boot snapshot fans out
// one event per data point to a `*` subscriber, which on a 1000-device
// CCU is far past any per-client buffer. Dropping the tail and telling
// the client to resync keeps the session alive and matches the resume
// semantics the protocol already defines.
func TestEnqueueOverflowKeepsTheConnection(t *testing.T) {
	t.Parallel()
	c, _, _ := newBackpressureClient(t, 4)

	for i := range 12 {
		c.enqueue(Event{Topic: "device.X.channels.1.data_points.LEVEL", Seq: uint64(i + 1)})
	}

	select {
	case <-c.closed:
		t.Fatal("overflow closed the connection; the client must survive and resync instead")
	default:
	}
}

// TestEnqueueOverflowSignalsResyncOnce pins that the client is told its
// stream has a gap — exactly once per episode, not once per dropped
// event.
//
// The once-per-episode part is not cosmetic. The old code logged inside
// the overflow branch and left the client registered on the hub, so the
// publisher kept fanning out to it: one overflowing session produced 413
// warnings in two seconds in a real installation's log.
func TestEnqueueOverflowSignalsResyncOnce(t *testing.T) {
	t.Parallel()
	c, logs, _ := newBackpressureClient(t, 4)

	for i := range 20 {
		c.enqueue(Event{Topic: "device.X.channels.1.data_points.LEVEL", Seq: uint64(i + 1)})
	}

	if got := countWarnings(t, logs); got != 1 {
		t.Errorf("ws.backpressure logged %d times, want exactly 1 per overflow episode", got)
	}

	var lost int
	for _, op := range drainCtrl(c) {
		if op.Op == "replay_lost" {
			lost++
		}
	}
	if lost != 1 {
		t.Errorf("replay_lost sent %d times, want exactly 1 per overflow episode", lost)
	}
}

// TestEnqueueOverflowKeepsTheNewestEvents pins which events survive: the
// buffer holds the tail of the stream, so a client that resyncs and then
// keeps reading sees the current state rather than a stale prefix.
func TestEnqueueOverflowKeepsTheNewestEvents(t *testing.T) {
	t.Parallel()
	c, _, _ := newBackpressureClient(t, 4)

	const total = 20
	for i := range total {
		c.enqueue(Event{Topic: "device.X.channels.1.data_points.LEVEL", Seq: uint64(i + 1)})
	}

	var seqs []uint64
	for {
		select {
		case ev := <-c.out:
			seqs = append(seqs, ev.Seq)
			continue
		default:
		}
		break
	}
	if len(seqs) == 0 {
		t.Fatal("buffer is empty after overflow; every event was dropped")
	}
	if last := seqs[len(seqs)-1]; last != total {
		t.Errorf("newest buffered seq = %d, want %d — the drop must take the oldest, not the newest", last, total)
	}
}

// TestEnqueueRecoveryAllowsANewEpisode pins that the once-per-episode
// suppression resets once the writer has drained the queue: a later
// overflow is a new event an operator needs to see.
func TestEnqueueRecoveryAllowsANewEpisode(t *testing.T) {
	t.Parallel()
	c, logs, _ := newBackpressureClient(t, 4)

	for i := range 12 {
		c.enqueue(Event{Topic: "t", Seq: uint64(i + 1)})
	}
	// The writer catches up.
	for {
		select {
		case <-c.out:
			continue
		default:
		}
		break
	}
	c.noteDrained()

	for i := range 12 {
		c.enqueue(Event{Topic: "t", Seq: uint64(100 + i)})
	}

	if got := countWarnings(t, logs); got != 2 {
		t.Errorf("ws.backpressure logged %d times, want 2 (one per episode)", got)
	}
}

// TestControlOverflowClosesAndDeregisters pins that the control plane
// keeps the strict policy — an ACK or auth reply must not be dropped —
// but that the doomed client leaves the hub immediately.
//
// Staying registered is what turned one overflow into a log flood: the
// publisher kept selecting a closed client as a fan-out target and every
// attempt logged again.
func TestControlOverflowClosesAndDeregisters(t *testing.T) {
	t.Parallel()
	c, logs, hub := newBackpressureClient(t, 2)

	for range 8 {
		c.enqueueCtrl(opText, []byte(`{"op":"pong"}`))
	}

	select {
	case <-c.closed:
	case <-time.After(time.Second):
		t.Fatal("control-plane overflow must still close the connection")
	}
	if got := hub.ClientCount(); got != 0 {
		t.Errorf("hub still holds %d clients; a closed client must leave the fan-out set", got)
	}
	if got := countWarnings(t, logs); got != 1 {
		t.Errorf("ws.backpressure logged %d times, want exactly 1", got)
	}
}

// TestCloseDeregistersFromHub pins the deregistration itself, on the
// ordinary close path rather than through an overflow.
func TestCloseDeregistersFromHub(t *testing.T) {
	t.Parallel()
	c, _, hub := newBackpressureClient(t, 4)
	if hub.ClientCount() != 1 {
		t.Fatalf("setup: client not registered")
	}

	c.close()

	if got := hub.ClientCount(); got != 0 {
		t.Errorf("hub holds %d clients after close, want 0", got)
	}
}

// TestPublishSkipsClosedClients pins the fan-out side of the same
// invariant: a closed client is never a publish target.
func TestPublishSkipsClosedClients(t *testing.T) {
	t.Parallel()
	c, logs, hub := newBackpressureClient(t, 1)
	c.subscribe([]string{"*"})
	c.close()

	for range 50 {
		hub.Publish(Event{Topic: "device.X.channels.1.data_points.LEVEL"})
	}

	if got := countWarnings(t, logs); got != 0 {
		t.Errorf("closed client produced %d backpressure warnings; it must not be a fan-out target at all", got)
	}
	if strings.Contains(logs.String(), "ws.backpressure") {
		t.Error("closed client still reached the backpressure path")
	}
}
