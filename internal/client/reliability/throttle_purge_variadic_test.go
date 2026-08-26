// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// waitForWaiters polls until tt.Waiting() reaches wantN or deadline passes.
func waitForWaiters(t *testing.T, tt *CommandThrottle, wantN int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() >= wantN {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d waiters; got %d", wantN, tt.Waiting())
}

// TestPurgeVariadicSingleAddress verifies that Purge(addr) cancels all waiters
// for a single address — backward-compatible with the pre-variadic form.
func TestPurgeVariadicSingleAddress(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()

	ctx := context.Background()
	// Acquire the only permit so the next waiter queues up.
	if err := tt.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- tt.AcquireFor(ctx, hmenum.CommandPriorityHigh, "addr1")
	}()
	waitForWaiters(t, tt, 1)

	n := tt.Purge("addr1")
	if n != 1 {
		t.Errorf("Purge returned %d, want 1", n)
	}
	select {
	case gotErr := <-result:
		if !errors.Is(gotErr, ErrSuperseded) {
			t.Errorf("waiter got %v, want ErrSuperseded", gotErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waiter did not return after Purge")
	}
}

// TestPurgeVariadicMultipleAddresses verifies that passing two addresses
// cancels waiters for both in a single call.
func TestPurgeVariadicMultipleAddresses(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()

	ctx := context.Background()
	// Hold the only permit.
	if err := tt.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	results := make([]chan error, 2)
	for i, addr := range []string{"addr-a", "addr-b"} {
		results[i] = make(chan error, 1)

		ch := results[i]
		go func() {
			ch <- tt.AcquireFor(ctx, hmenum.CommandPriorityHigh, addr)
		}()
	}
	waitForWaiters(t, tt, 2)

	n := tt.Purge("addr-a", "addr-b")
	if n != 2 {
		t.Errorf("Purge returned %d, want 2", n)
	}
	timeout := time.After(500 * time.Millisecond)
	for i, ch := range results {
		select {
		case err := <-ch:
			if !errors.Is(err, ErrSuperseded) {
				t.Errorf("waiter[%d] got %v, want ErrSuperseded", i, err)
			}
		case <-timeout:
			t.Fatalf("waiter[%d] did not return after Purge", i)
		}
	}
}

// TestPurgeVariadicNoArgs verifies that Purge() with no arguments is a no-op.
func TestPurgeVariadicNoArgs(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()
	n := tt.Purge()
	if n != 0 {
		t.Errorf("Purge() returned %d, want 0", n)
	}
}

// TestPurgeVariadicEmptyStringsNoOp verifies that Purge("", "") skips empty strings.
func TestPurgeVariadicEmptyStringsNoOp(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()
	n := tt.Purge("", "")
	if n != 0 {
		t.Errorf("Purge with empty strings returned %d, want 0", n)
	}
}

// TestPurgeVariadicOnlyPurgesMatchingAddress verifies that Purge("a") only
// cancels waiters for "a" and leaves waiters for other addresses in the queue.
func TestPurgeVariadicOnlyPurgesMatchingAddress(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer tt.Close()

	ctx := context.Background()
	if err := tt.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	resultA := make(chan error, 1)
	resultB := make(chan error, 1)
	go func() { resultA <- tt.AcquireFor(ctx, hmenum.CommandPriorityHigh, "addr-a") }()
	go func() { resultB <- tt.AcquireFor(ctx, hmenum.CommandPriorityHigh, "addr-b") }()
	waitForWaiters(t, tt, 2)

	// Purge only addr-a; addr-b must survive.
	n := tt.Purge("addr-a")
	if n != 1 {
		t.Errorf("Purge(addr-a) returned %d, want 1", n)
	}
	select {
	case err := <-resultA:
		if !errors.Is(err, ErrSuperseded) {
			t.Errorf("addr-a: got %v, want ErrSuperseded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("addr-a waiter did not return")
	}
	if tt.Waiting() != 1 {
		t.Errorf("Waiting=%d, want 1 (addr-b still queued)", tt.Waiting())
	}
}
