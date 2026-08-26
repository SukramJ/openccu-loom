// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireMatterReassembleOnReady_TriggersOnReadyEvent proves the core fix: a
// CentralSouthboundReadyEvent (fired once the CCU device load completes) drives
// a topology reassemble, so persisted exposures take effect without an operator
// toggling one. Without the subscription the boot-time (pre-device-load) empty
// topology would persist and the commissioning window would stay refused.
func TestWireMatterReassembleOnReady_TriggersOnReadyEvent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	fired := make(chan struct{}, 8)
	reassemble := func(context.Context) error {
		count.Add(1)
		fired <- struct{}{}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closers, trigger := wireMatterReassembleOnReady(ctx, []*events.Bus{bus}, reassemble, 20*time.Millisecond, discardLogger())
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
		t.Fatal("reassemble was not triggered by the ready event")
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("reassemble count = %d, want 1", got)
	}
}

// TestWireMatterReassembleOnReady_CoalescesBurst asserts a staggered multi-CCU
// boot (several ready events in quick succession) collapses into a single
// reassemble rather than one per central.
func TestWireMatterReassembleOnReady_CoalescesBurst(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	reassemble := func(context.Context) error { count.Add(1); return nil }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closers, _ := wireMatterReassembleOnReady(ctx, []*events.Bus{bus}, reassemble, 60*time.Millisecond, discardLogger())
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})

	for range 5 {
		events.Publish(bus, hmevent.CentralSouthboundReadyEvent{Base: hmevent.NewBase(), CentralName: "ccu"})
	}
	// Wait well past the debounce window for the single coalesced reassemble.
	time.Sleep(300 * time.Millisecond)
	if got := count.Load(); got != 1 {
		t.Fatalf("reassemble count = %d, want 1 (burst coalesced)", got)
	}
}

// TestWireMatterReassembleOnReady_StopsOnCtxCancel verifies the debounce
// goroutine exits on teardown so a late ready event does not reassemble.
func TestWireMatterReassembleOnReady_StopsOnCtxCancel(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	reassemble := func(context.Context) error { count.Add(1); return nil }

	ctx, cancel := context.WithCancel(context.Background())
	closers, _ := wireMatterReassembleOnReady(ctx, []*events.Bus{bus}, reassemble, 20*time.Millisecond, discardLogger())
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
		t.Fatalf("reassemble count = %d, want 0 after ctx cancel", got)
	}
}

// TestWireMatterReassembleOnReady_NoBusesOrNilReassemble pins the edge
// shapes: no buses yields no closers but still a usable trigger (a daemon can
// boot with zero centrals and adopt its first one at runtime — the debounce
// pipeline must exist for the live-adopt hook), while a nil reassemble wires
// nothing at all.
func TestWireMatterReassembleOnReady_NoBusesOrNilReassemble(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	got, trigger := wireMatterReassembleOnReady(ctx, nil, func(context.Context) error { return nil }, time.Millisecond, discardLogger())
	if got != nil {
		t.Fatalf("expected nil closers for no buses, got %v", got)
	}
	if trigger == nil {
		t.Fatal("expected a non-nil trigger even without boot-time buses (live-adopt needs it)")
	}
	got, trigger = wireMatterReassembleOnReady(ctx, []*events.Bus{events.NewBus()}, nil, time.Millisecond, discardLogger())
	if got != nil {
		t.Fatalf("expected nil closers for nil reassemble, got %v", got)
	}
	if trigger != nil {
		t.Fatal("expected nil trigger for nil reassemble")
	}
}

// TestWireMatterReassembleOnReady_TriggerDrivesDebounce verifies the returned
// trigger feeds the same debounce pipeline as a bus event — the seam the
// live-adopt hook uses for a central adopted before/without boot-time buses.
func TestWireMatterReassembleOnReady_TriggerDrivesDebounce(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	fired := make(chan struct{}, 8)
	reassemble := func(context.Context) error {
		count.Add(1)
		fired <- struct{}{}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_, trigger := wireMatterReassembleOnReady(ctx, nil, reassemble, 20*time.Millisecond, discardLogger())
	if trigger == nil {
		t.Fatal("trigger = nil")
	}

	trigger()
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("reassemble was not triggered by the returned trigger func")
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("reassemble count = %d, want 1", got)
	}
}
