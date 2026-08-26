// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// newPipeClient builds a client bound to hub over an in-memory pipe, so
// a control-frame assertion can read c.ctrl directly without a real
// WebSocket handshake.
func newPipeClient(t *testing.T, hub *Hub) *client {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	return &client{
		conn:   serverConn,
		br:     bufio.NewReader(serverConn),
		bw:     bufio.NewWriter(serverConn),
		hub:    hub,
		logger: slog.Default(),
		out:    make(chan Event, 8),
		ctrl:   make(chan wireMsg, 8),
		closed: make(chan struct{}),
	}
}

// drainCtrlOps collects the op names queued on the client's control
// channel.
func drainCtrlOps(c *client) []string {
	var ops []string
	for {
		select {
		case msg := <-c.ctrl:
			ops = append(ops, string(msg.payload))
			continue
		default:
		}
		return ops
	}
}

// drainOutEvents collects the events queued on the client's domain
// channel, in FIFO order.
func drainOutEvents(c *client) []Event {
	var evs []Event
	for {
		select {
		case ev := <-c.out:
			evs = append(evs, ev)
			continue
		default:
		}
		return evs
	}
}

// TestClientReplayFromStaleCursorSignalsLost pins the layer the
// restart-cursor gap actually becomes observable at. A client resuming
// with a pre-restart cursor used to receive `replay_done` carrying its
// own cursor back — so it concluded it had missed nothing, never issued
// the documented /snapshot resync, and kept rendering pre-restart state.
func TestClientReplayFromStaleCursorSignalsLost(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	for range 3 {
		hub.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	c := newPipeClient(t, hub)

	c.replayFrom(48211)

	ops := drainCtrlOps(c)
	if len(ops) != 1 {
		t.Fatalf("control frames = %d (%v), want exactly 1", len(ops), ops)
	}
	if !strings.Contains(ops[0], `"replay_lost"`) {
		t.Fatalf("frame = %s, want replay_lost", ops[0])
	}
	if strings.Contains(ops[0], "48211") {
		t.Fatalf("frame = %s, must not hand the client its own stale cursor back", ops[0])
	}
}

// TestClientReplayFromCurrentCursorSignalsDone keeps the caught-up path
// intact: a cursor the hub can place still acknowledges with
// replay_done. The ack now travels through c.out (see
// [client.replayFrom]'s doc comment) rather than c.ctrl, so this checks
// the domain queue is empty of it and the control queue carries nothing
// at all — c.ctrl is not writePump, so nothing here decodes the marker
// into an actual {op:"replay_done"} frame; that happens in
// TestWritePumpOrdersReplayDoneAfterReplayedEvents below.
func TestClientReplayFromCurrentCursorSignalsDone(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	for range 3 {
		hub.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	c := newPipeClient(t, hub)

	c.replayFrom(3)

	if ops := drainCtrlOps(c); len(ops) != 0 {
		t.Fatalf("control frames = %v, want none — replay_done travels through c.out now", ops)
	}
	evs := drainOutEvents(c)
	if len(evs) == 0 {
		t.Fatal("expected the replay_done marker on c.out, got nothing")
	}
	last := evs[len(evs)-1]
	if last.Kind != kindReplayDoneMarker {
		t.Fatalf("last queued event kind = %q, want the replay_done marker as the final item", last.Kind)
	}
}

// TestWritePumpOrdersReplayDoneAfterReplayedEvents is the regression
// replay_done must reach the wire after
// every replayed event it acknowledges, never interleaved ahead of
// them. Runs replayFrom against a real client + writePump (not the raw
// channel inspection the tests above use) so the assertion covers the
// actual wire order, not just which queue the marker landed on. Before
// the fix, Go's select choosing uniformly between c.out and c.ctrl let
// replay_done (queued alone on c.ctrl) reach the wire after only a
// handful of the n buffered domain events; this asserts the exact
// count is n, not "eventually all of them arrive".
func TestWritePumpOrdersReplayDoneAfterReplayedEvents(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	const n = 200
	for range n {
		hub.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	c := newClient(serverConn, bufio.NewReader(serverConn), bufio.NewWriter(serverConn), hub, slog.Default())
	c.subscribe([]string{"*"})

	writeDone := make(chan struct{})
	go func() { defer close(writeDone); c.writePump() }()
	t.Cleanup(func() {
		c.close()
		<-writeDone
	})

	c.replayFrom(0)

	br := bufio.NewReader(clientConn)
	eventsBeforeAck := 0
	for {
		payload := readServerText(t, br)
		if strings.Contains(string(payload), `"replay_done"`) {
			break
		}
		eventsBeforeAck++
		if eventsBeforeAck > n {
			t.Fatalf("read %d frames without seeing replay_done (wanted exactly %d)", eventsBeforeAck, n)
		}
	}
	if eventsBeforeAck != n {
		t.Fatalf("replay_done arrived after %d events, want exactly %d — it must follow every replayed event, not overtake them", eventsBeforeAck, n)
	}
}
