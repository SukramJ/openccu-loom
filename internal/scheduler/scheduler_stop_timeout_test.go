// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scheduler

import (
	"context"
	"testing"
	"time"
)

// TestStopWithTimeoutCleanExit verifies that when all goroutines
// finish before the deadline StopWithTimeout returns without waiting
// the full timeout.
func TestStopWithTimeoutCleanExit(t *testing.T) {
	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "fast",
		Interval: time.Minute, // will not tick before Stop
		Run:      func(_ context.Context) error { return nil },
	})
	ctx := context.Background()
	_ = s.Start(ctx)

	start := time.Now()
	s.StopWithTimeout(5 * time.Second)
	elapsed := time.Since(start)

	// Clean exit — must return well before the 5 s deadline.
	if elapsed > 2*time.Second {
		t.Fatalf("StopWithTimeout took too long: %v", elapsed)
	}
}

// TestStopWithTimeoutDefaultsToFiveSeconds verifies that passing 0
// defaults to a 5 s window. We only verify it does not panic;
// the goroutines exit quickly so the deadline is never hit.
func TestStopWithTimeoutDefaultsToFiveSeconds(t *testing.T) {
	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "quick",
		Interval: time.Minute,
		Run:      func(_ context.Context) error { return nil },
	})
	_ = s.Start(context.Background())
	s.StopWithTimeout(0) // must not panic
}

// TestStopWithTimeoutDeadlineExceeded verifies the timeout code path.
// We use a job whose goroutine blocks after cancellation to simulate a
// slow drain, then supply a very short timeout so the warn path is hit.
func TestStopWithTimeoutDeadlineExceeded(t *testing.T) {
	s := New(nil, nil)
	blocker := make(chan struct{})

	_ = s.Add(Job{
		Name:       "slow",
		Interval:   time.Millisecond,
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			// Block until the test closes blocker.
			select {
			case <-ctx.Done():
				// Still block briefly to simulate a slow shutdown path.
				time.Sleep(200 * time.Millisecond)
			case <-blocker:
			}
			return nil
		},
	})
	_ = s.Start(context.Background())

	// Give the job a moment to start its first invocation.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	// Use a 10ms timeout — shorter than the 200ms sleep in the job's
	// cancellation handler.
	s.StopWithTimeout(10 * time.Millisecond)
	elapsed := time.Since(start)

	// The function must return at or shortly after the 10ms window.
	if elapsed > time.Second {
		t.Fatalf("StopWithTimeout blocked beyond timeout: %v", elapsed)
	}

	// Let the goroutine finish so the test does not leak.
	close(blocker)
	// Give it time to exit cleanly.
	time.Sleep(300 * time.Millisecond)
}
