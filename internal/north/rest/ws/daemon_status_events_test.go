// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"testing"
	"time"
)

func daemonStatusFilter(topic string) bool { return topic == daemonStatusTopic }

// TestPublishDaemonShuttingDownAnnouncesOffline pins the shape a client
// reads. The point of the broadcast is that a stopping daemon is
// distinguishable from a dropped connection, so both the offline status
// and the reason have to be on the wire — a frame carrying only "offline"
// would be indistinguishable from a client-side guess.
func TestPublishDaemonShuttingDownAnnouncesOffline(t *testing.T) {
	t.Parallel()
	h := NewHub()

	h.PublishDaemonShuttingDown(context.Background(), time.Now().UTC())

	ev := pollHub(t, h, daemonStatusFilter)
	if ev.Type != broadcastDaemonStatusChanged {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastDaemonStatusChanged)
	}
	p, ok := ev.Payload.(DaemonStatusPayload)
	if !ok {
		t.Fatalf("payload type %T, want DaemonStatusPayload", ev.Payload)
	}
	if p.Status != DaemonStatusOffline {
		t.Fatalf("status = %q, want %q", p.Status, DaemonStatusOffline)
	}
	if p.Reason != "shutdown" {
		t.Fatalf("reason = %q, want \"shutdown\" — without it the client cannot tell a stopping daemon from a dropped connection", p.Reason)
	}
	if p.EventAt.IsZero() {
		t.Fatal("event_at is zero")
	}
}

// TestDaemonStatusUsesTheSameWordsAsTheMQTTBridge pins the two planes to
// one vocabulary. A client bridging both — which is what the Home
// Assistant integration does — would otherwise have to translate between
// them, and a divergence would only show up in that third codebase.
func TestDaemonStatusUsesTheSameWordsAsTheMQTTBridge(t *testing.T) {
	t.Parallel()
	// These are the literals internal/north/mqtt writes to
	// <base>/bridge/status in AnnounceOnline / AnnounceOffline, and the
	// literal cmd/openccu-loom sets as the broker's last will.
	if DaemonStatusOnline != "online" || DaemonStatusOffline != "offline" {
		t.Fatalf("status words are %q/%q, but the MQTT bridge retains \"online\"/\"offline\"",
			DaemonStatusOnline, DaemonStatusOffline)
	}
}

// TestDaemonStatusTopicIsDaemonLevel pins the topic to a shape with no
// central segment. The daemon is one process serving every configured
// CCU, so a per-central topic would announce the same stop N times and
// leave a client guessing which of them meant the process.
func TestDaemonStatusTopicIsDaemonLevel(t *testing.T) {
	t.Parallel()
	if got, want := DaemonStatusTopic(), "system.daemon_status"; got != want {
		t.Fatalf("topic = %q, want %q", got, want)
	}
}

// TestDrainPendingReturnsWhenTheContextEnds is the safety property of the
// wait: a client whose writer goroutine is wedged must not hold up a
// shutdown a supervisor is already timing.
func TestDrainPendingReturnsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	h := NewHub()
	c := &client{out: make(chan Event, 4), closed: make(chan struct{}), topics: []string{daemonStatusTopic}}
	// Fill the queue and never drain it: this is the wedged writer.
	c.out <- Event{Topic: daemonStatusTopic}
	h.register(c)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	h.drainPending(ctx)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("drainPending took %s with a wedged writer; it must return with the context", elapsed)
	}
	if h.pendingWrites() == 0 {
		t.Fatal("the queue drained after all; this case no longer exercises the wedged path")
	}
}

// TestDrainPendingReturnsOnceTheWriterTookTheEvent is the positive half:
// with a live writer the wait ends as soon as the queue empties, rather
// than always burning the whole timeout.
func TestDrainPendingReturnsOnceTheWriterTookTheEvent(t *testing.T) {
	t.Parallel()
	h := NewHub()
	c := &client{out: make(chan Event, 4), closed: make(chan struct{}), topics: []string{daemonStatusTopic}}
	h.register(c)
	h.Publish(Event{Topic: daemonStatusTopic, Type: broadcastDaemonStatusChanged})
	if h.pendingWrites() != 1 {
		t.Fatalf("pendingWrites = %d, want 1 — the event never reached the queue", h.pendingWrites())
	}
	go func() { <-c.out }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h.drainPending(ctx)

	if h.pendingWrites() != 0 {
		t.Fatalf("pendingWrites = %d after drain, want 0", h.pendingWrites())
	}
	if ctx.Err() != nil {
		t.Fatal("drainPending waited for the context instead of returning when the queue emptied")
	}
}
