// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// stopOrderProbe is a north-bound Service that records the hub's event
// count at the moment it is stopped. It is the whole point of the pin: the
// announcement has to be on the hub *before* the servers carrying it go
// away, and a test that only checked "the event exists afterwards" would
// pass with the order reversed.
type stopOrderProbe struct {
	hub            *ws.Hub
	stopped        atomic.Bool
	eventsAtStop   atomic.Int64
	stopCalledOnce atomic.Int64
}

func (p *stopOrderProbe) Name() string { return "stop-order-probe" }

func (p *stopOrderProbe) Start(context.Context) error { return nil }

func (p *stopOrderProbe) Stop(context.Context) error {
	p.stopCalledOnce.Add(1)
	p.eventsAtStop.Store(int64(len(p.hub.Replay(0, func(topic string) bool {
		return topic == ws.DaemonStatusTopic()
	}).Events)))
	p.stopped.Store(true)
	return nil
}

// TestAwaitShutdownAnnouncesTheDaemonStopBeforeTheServersGoAway pins the
// production shutdown path: the composition root's own awaitShutdown, the
// real ws.Hub and the real north-bound registry.
//
// The ordering is the load-bearing part. A WebSocket client has no third
// party holding a last will the way an MQTT client does, so the only chance
// to tell it "this is a stop, not your network" is while the connection is
// still up. Announcing after the servers stop would compile, pass a
// "the event was published" assertion, and reach nobody.
func TestAwaitShutdownAnnouncesTheDaemonStopBeforeTheServersGoAway(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	probe := &stopOrderProbe{hub: hub}
	reg := northbridge.NewRegistry(slog.Default())
	reg.Register(probe)
	if err := reg.StartAll(t.Context()); err != nil {
		t.Fatalf("start north bridges: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		awaitShutdown(ctx, slog.Default(), matterWiring{}, reg, hub)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("awaitShutdown did not return")
	}

	if !probe.stopped.Load() {
		t.Fatal("the north-bound registry was never stopped; the pin did not exercise the shutdown path")
	}
	if got := probe.eventsAtStop.Load(); got != 1 {
		t.Fatalf("daemon-status events on the hub when the servers stopped = %d, want 1 — "+
			"the announcement must be published before the surface carrying it is torn down", got)
	}

	res := hub.Replay(0, func(topic string) bool { return topic == ws.DaemonStatusTopic() })
	if len(res.Events) != 1 {
		t.Fatalf("daemon-status events = %d, want exactly 1", len(res.Events))
	}
	payload, ok := res.Events[0].Payload.(ws.DaemonStatusPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.DaemonStatusPayload", res.Events[0].Payload)
	}
	if payload.Status != ws.DaemonStatusOffline {
		t.Fatalf("status = %q, want %q", payload.Status, ws.DaemonStatusOffline)
	}
}

// TestAwaitShutdownWithoutAWebSocketHubStillStops covers the deployments
// that wire no hub: the announcement is a courtesy, never a precondition
// for stopping.
func TestAwaitShutdownWithoutAWebSocketHubStillStops(t *testing.T) {
	t.Parallel()
	probe := &stopOrderProbe{hub: ws.NewHub()}
	reg := northbridge.NewRegistry(slog.Default())
	reg.Register(probe)
	if err := reg.StartAll(t.Context()); err != nil {
		t.Fatalf("start north bridges: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		awaitShutdown(ctx, slog.Default(), matterWiring{}, reg, nil)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("awaitShutdown did not return without a hub")
	}
	if !probe.stopped.Load() {
		t.Fatal("the north-bound registry was not stopped")
	}
}
