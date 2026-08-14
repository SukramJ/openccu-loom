// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// replay_done.
func TestClientReplayFromCurrentCursorSignalsDone(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	for range 3 {
		hub.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	c := newPipeClient(t, hub)

	c.replayFrom(3)

	ops := drainCtrlOps(c)
	if len(ops) != 1 || !strings.Contains(ops[0], `"replay_done"`) {
		t.Fatalf("control frames = %v, want a single replay_done", ops)
	}
}
