// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestStopFiresTiersInOrder verifies that the three tiers fire in the order
// Northbound → Coordinator → External, and that all hooks within every tier
// actually run.
func TestStopFiresTiersInOrder(t *testing.T) {
	c := newTestCentral(t)

	var mu sync.Mutex
	var order []StopTier

	record := func(tier StopTier) func() {
		return func() {
			mu.Lock()
			order = append(order, tier)
			mu.Unlock()
		}
	}

	c.AddStopHook(StopTierExternal, record(StopTierExternal))
	c.AddStopHook(StopTierCoordinator, record(StopTierCoordinator))
	c.AddStopHook(StopTierNorthbound, record(StopTierNorthbound))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	mu.Lock()
	got := append([]StopTier(nil), order...)
	mu.Unlock()

	if len(got) != 3 {
		t.Fatalf("expected 3 hook calls, got %d: %v", len(got), got)
	}
	if got[0] != StopTierNorthbound {
		t.Errorf("position 0: got tier %d, want StopTierNorthbound (%d)", got[0], StopTierNorthbound)
	}
	if got[1] != StopTierCoordinator {
		t.Errorf("position 1: got tier %d, want StopTierCoordinator (%d)", got[1], StopTierCoordinator)
	}
	if got[2] != StopTierExternal {
		t.Errorf("position 2: got tier %d, want StopTierExternal (%d)", got[2], StopTierExternal)
	}
}

// TestRegistrationOrderPreservedWithinTier verifies that hooks within a single
// tier run in the order they were registered.
func TestRegistrationOrderPreservedWithinTier(t *testing.T) {
	c := newTestCentral(t)

	var mu sync.Mutex
	var calls []int

	for i := range 5 {
		n := i
		c.AddStopHook(StopTierExternal, func() {
			mu.Lock()
			calls = append(calls, n)
			mu.Unlock()
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	mu.Lock()
	got := append([]int(nil), calls...)
	mu.Unlock()

	if len(got) != 5 {
		t.Fatalf("expected 5 calls, got %d", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Errorf("position %d: got %d, want %d", i, v, i)
		}
	}
}

// TestAddOnStopHookRoutesToExternalTier confirms that a hook registered via
// AddOnStopHook lands in the External tier and fires at the same point as a
// hook registered explicitly with AddStopHook(StopTierExternal, …).
func TestAddOnStopHookRoutesToExternalTier(t *testing.T) {
	c := newTestCentral(t)

	var mu sync.Mutex
	var fired []string

	c.AddOnStopHook(func() {
		mu.Lock()
		fired = append(fired, "legacy")
		mu.Unlock()
	})
	c.AddStopHook(StopTierExternal, func() {
		mu.Lock()
		fired = append(fired, "explicit")
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	mu.Lock()
	got := append([]string(nil), fired...)
	mu.Unlock()

	if len(got) != 2 {
		t.Fatalf("expected 2 hooks to fire, got %d: %v", len(got), got)
	}
	if got[0] != "legacy" || got[1] != "explicit" {
		t.Errorf("unexpected fire order: %v", got)
	}
}

// TestAddStopHookNilFnIgnored verifies that registering a nil hook does not
// panic and produces no effect when Stop is called.
func TestAddStopHookNilFnIgnored(t *testing.T) {
	c := newTestCentral(t)
	c.AddStopHook(StopTierExternal, nil)
	c.AddOnStopHook(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Must not panic.
	c.Stop()
}

// TestAddStopHookOutOfRangeTierIgnored verifies that an out-of-range tier
// value is silently ignored — no panic, no hook registered.
func TestAddStopHookOutOfRangeTierIgnored(t *testing.T) {
	c := newTestCentral(t)
	fired := false
	c.AddStopHook(stopTierCount, func() { fired = true })
	c.AddStopHook(-1, func() { fired = true })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	if fired {
		t.Fatal("out-of-range hook must not fire")
	}
}
