// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestThrottlePurgeCancelsAllWaitersForAddress(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

	// Block in-flight slot.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer tt.Release()

	// Queue 3 waiters for "A:1", 1 for "B:1".
	results := make(chan error, 4)
	for range 3 {
		go func() {
			results <- tt.AcquireFor(context.Background(), hmenum.CommandPriorityHigh, "A:1")
		}()
	}
	go func() {
		results <- tt.AcquireFor(context.Background(), hmenum.CommandPriorityHigh, "B:1")
	}()

	// Wait for all to enqueue.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() == 4 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	purged := tt.Purge("A:1")
	if purged != 3 {
		t.Fatalf("expected 3 purged waiters, got %d", purged)
	}

	// Three "A:1" goroutines should return ErrSuperseded.
	supersededCount := 0
	timeout := time.After(200 * time.Millisecond)
	for range 3 {
		select {
		case err := <-results:
			if !errors.Is(err, ErrSuperseded) {
				t.Fatalf("waiter result = %v, want ErrSuperseded", err)
			}
			supersededCount++
		case <-timeout:
			t.Fatalf("not all purged waiters returned in time: got %d", supersededCount)
		}
	}
	// "B:1" waiter is still queued.
	if tt.Waiting() != 1 {
		t.Fatalf("Waiting = %d, want 1 (B:1 untouched)", tt.Waiting())
	}
}

func TestThrottlePurgeReturnsZeroForUnknownAddress(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	if got := tt.Purge("nonexistent"); got != 0 {
		t.Fatalf("Purge of unknown addr returned %d, want 0", got)
	}
	if got := tt.Purge(""); got != 0 {
		t.Fatalf("Purge of empty addr returned %d, want 0", got)
	}
}

func TestThrottlePurgePreservesPriorityOrderOfSurvivors(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)

	// Mixed waiters with various priorities & addresses.
	type entry struct {
		addr string
		prio hmenum.CommandPriority
		got  chan error
	}
	entries := []entry{
		{addr: "A:1", prio: hmenum.CommandPriorityHigh, got: make(chan error, 1)},
		{addr: "B:1", prio: hmenum.CommandPriorityCritical, got: make(chan error, 1)},
		{addr: "A:1", prio: hmenum.CommandPriorityLow, got: make(chan error, 1)},
		{addr: "B:1", prio: hmenum.CommandPriorityHigh, got: make(chan error, 1)},
	}
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(e *entry) {
			defer wg.Done()
			e.got <- tt.AcquireFor(context.Background(), e.prio, e.addr)
		}(&entries[i])
	}

	// Wait for all to enqueue.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() == 4 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if got := tt.Purge("A:1"); got != 2 {
		t.Fatalf("purged %d, want 2", got)
	}

	// A:1 goroutines must return ErrSuperseded.
	for _, e := range entries {
		if e.addr != "A:1" {
			continue
		}
		select {
		case err := <-e.got:
			if !errors.Is(err, ErrSuperseded) {
				t.Fatalf("A:1 waiter %v: got %v", e.prio, err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("A:1 waiter did not return")
		}
	}

	// Surviving B:1-Critical must run first when the slot frees up.
	tt.Release()
	for _, e := range entries {
		if e.addr != "B:1" || e.prio != hmenum.CommandPriorityCritical {
			continue
		}
		select {
		case err := <-e.got:
			if err != nil {
				t.Fatalf("critical B:1: %v", err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("critical B:1 should have been admitted first")
		}
	}
	tt.Release()
}

func TestThrottlePurgeDoesNotAffectInFlight(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	if err := tt.AcquireFor(context.Background(), hmenum.CommandPriorityHigh, "A:1"); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Purge must not touch the holder.
	if got := tt.Purge("A:1"); got != 0 {
		t.Fatalf("Purge cancelled %d, expected 0 (holder is in-flight)", got)
	}
	if tt.InFlight() != 1 {
		t.Fatalf("InFlight = %d, want 1", tt.InFlight())
	}
	tt.Release()
}
