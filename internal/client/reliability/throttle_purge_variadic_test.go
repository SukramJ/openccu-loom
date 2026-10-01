// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// waitForWaiters lets every goroutine in the bubble park and then asserts
// that at least wantN callers are queued in the throttle. It must be called
// inside a synctest bubble.
func waitForWaiters(t *testing.T, tt *CommandThrottle, wantN int) {
	t.Helper()
	synctest.Wait()
	if got := tt.Waiting(); got < wantN {
		t.Fatalf("expected at least %d waiters; got %d", wantN, got)
	}
}

// TestPurgeVariadicSingleAddress verifies that Purge(addr) cancels all waiters
// for a single address — backward-compatible with the pre-variadic form.
func TestPurgeVariadicSingleAddress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		synctest.Wait()
		select {
		case gotErr := <-result:
			if !errors.Is(gotErr, ErrSuperseded) {
				t.Errorf("waiter got %v, want ErrSuperseded", gotErr)
			}
		default:
			t.Fatal("waiter did not return after Purge")
		}
	})
}

// TestPurgeVariadicMultipleAddresses verifies that passing two addresses
// cancels waiters for both in a single call.
func TestPurgeVariadicMultipleAddresses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		for i, ch := range results {
			synctest.Wait()
			select {
			case err := <-ch:
				if !errors.Is(err, ErrSuperseded) {
					t.Errorf("waiter[%d] got %v, want ErrSuperseded", i, err)
				}
			default:
				t.Fatalf("waiter[%d] did not return after Purge", i)
			}
		}
	})
}

// TestPurgeVariadicNoArgs verifies that Purge() with no arguments is a no-op.
func TestPurgeVariadicNoArgs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
		defer tt.Close()
		n := tt.Purge()
		if n != 0 {
			t.Errorf("Purge() returned %d, want 0", n)
		}
	})
}

// TestPurgeVariadicEmptyStringsNoOp verifies that Purge("", "") skips empty strings.
func TestPurgeVariadicEmptyStringsNoOp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
		defer tt.Close()
		n := tt.Purge("", "")
		if n != 0 {
			t.Errorf("Purge with empty strings returned %d, want 0", n)
		}
	})
}

// TestPurgeVariadicOnlyPurgesMatchingAddress verifies that Purge("a") only
// cancels waiters for "a" and leaves waiters for other addresses in the queue.
func TestPurgeVariadicOnlyPurgesMatchingAddress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		synctest.Wait()
		select {
		case err := <-resultA:
			if !errors.Is(err, ErrSuperseded) {
				t.Errorf("addr-a: got %v, want ErrSuperseded", err)
			}
		default:
			t.Fatal("addr-a waiter did not return")
		}
		if tt.Waiting() != 1 {
			t.Errorf("Waiting=%d, want 1 (addr-b still queued)", tt.Waiting())
		}
	})
}
