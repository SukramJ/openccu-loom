// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireHubDiscoveryOnReady_TriggersOnReadyEvent proves the core fix: a
// CentralSouthboundReadyEvent (fired once the async, readiness-gated bring-up
// resolves the CCU serial) drives a hub-publisher re-Start, so the serial-gated
// hub-discovery plane — the named central device that cures the "unknown
// device" parent and the sysvar entities — is published. Without the
// subscription the boot-time (empty-serial) Start would be the last one and no
// hub discovery would ever appear.
func TestWireHubDiscoveryOnReady_TriggersOnReadyEvent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	fired := make(chan struct{}, 8)
	restart := func(context.Context) {
		count.Add(1)
		fired <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closers, trigger := wireHubDiscoveryOnReady(ctx, []*events.Bus{bus}, restart, 20*time.Millisecond, discardLogger())
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})
	if len(closers) != 1 {
		t.Fatalf("closers = %d, want 1 (one subscription)", len(closers))
	}
	if trigger == nil {
		t.Fatal("trigger = nil, want a reusable non-blocking trigger for the live-adopt hook")
	}

	events.Publish(bus, hmevent.CentralSouthboundReadyEvent{Base: hmevent.NewBase(), CentralName: "ccu-01"})

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("hub re-Start was not triggered by the ready event")
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("restart count = %d, want 1", got)
	}
}

// TestWireHubDiscoveryOnReady_CoalescesBurst asserts a staggered multi-CCU boot
// (several ready events in quick succession) collapses into a single re-Start
// rather than one per central.
func TestWireHubDiscoveryOnReady_CoalescesBurst(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	restart := func(context.Context) { count.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closers, _ := wireHubDiscoveryOnReady(ctx, []*events.Bus{bus}, restart, 60*time.Millisecond, discardLogger())
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})

	for range 5 {
		events.Publish(bus, hmevent.CentralSouthboundReadyEvent{Base: hmevent.NewBase(), CentralName: "ccu"})
	}
	time.Sleep(300 * time.Millisecond)
	if got := count.Load(); got != 1 {
		t.Fatalf("restart count = %d, want 1 (burst coalesced)", got)
	}
}

// TestWireHubDiscoveryOnReady_StopsOnCtxCancel verifies the debounce goroutine
// exits on teardown so a late ready event does not re-Start the publisher.
func TestWireHubDiscoveryOnReady_StopsOnCtxCancel(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	restart := func(context.Context) { count.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	closers, _ := wireHubDiscoveryOnReady(ctx, []*events.Bus{bus}, restart, 20*time.Millisecond, discardLogger())
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})

	cancel()
	time.Sleep(50 * time.Millisecond) // let the goroutine observe ctx.Done and exit
	events.Publish(bus, hmevent.CentralSouthboundReadyEvent{Base: hmevent.NewBase(), CentralName: "ccu-01"})
	time.Sleep(100 * time.Millisecond)
	if got := count.Load(); got != 0 {
		t.Fatalf("restart count = %d, want 0 after ctx cancel", got)
	}
}

// TestWireHubDiscoveryOnReady_NoBusesOrNilRestart pins the edge shapes: no
// buses yields no closers but still a usable trigger (a daemon can boot with
// zero centrals and adopt its first at runtime — the debounce pipeline must
// exist for the live-adopt hook), while a nil restart wires nothing at all.
func TestWireHubDiscoveryOnReady_NoBusesOrNilRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	got, trigger := wireHubDiscoveryOnReady(ctx, nil, func(context.Context) {}, time.Millisecond, discardLogger())
	if got != nil {
		t.Fatalf("expected nil closers for no buses, got %v", got)
	}
	if trigger == nil {
		t.Fatal("expected a non-nil trigger even without boot-time buses (live-adopt needs it)")
	}
	got, trigger = wireHubDiscoveryOnReady(ctx, []*events.Bus{events.NewBus()}, nil, time.Millisecond, discardLogger())
	if got != nil {
		t.Fatalf("expected nil closers for nil restart, got %v", got)
	}
	if trigger != nil {
		t.Fatal("expected nil trigger for nil restart")
	}
}
