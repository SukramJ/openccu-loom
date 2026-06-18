// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartInterfaceRetry_SucceedsAfterTransientFailures is the regression
// tripwire for the background interface device-load retry: an attempt that
// fails twice then succeeds must cause startInterfaceRetry to complete and
// drain its WaitGroup without a manual cancel.
func TestStartInterfaceRetry_SucceedsAfterTransientFailures(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	attempt := func(_ context.Context) error {
		if calls.Add(1) < 3 {
			return errors.New("backend not ready")
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := startInterfaceRetry(ctx, "OttoLoom", "OttoLoom-HmIP-RF",
		[]time.Duration{time.Millisecond}, attempt, nil)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitGroup did not drain — background retry goroutine leaked")
	}

	if got := calls.Load(); got < 3 {
		t.Fatalf("attempt called %d times, want >= 3 (two failures then success)", got)
	}
}

// TestStartInterfaceRetry_StopsOnContextCancel verifies that cancelling the
// context during a long backoff window unblocks the background goroutine
// promptly so no goroutine outlives the caller's lifecycle.
func TestStartInterfaceRetry_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	attempt := func(_ context.Context) error {
		return errors.New("never recovers")
	}

	// Long backoff so the goroutine is parked waiting when we cancel.
	wg := startInterfaceRetry(ctx, "OttoLoom", "OttoLoom-HmIP-RF",
		[]time.Duration{10 * time.Second}, attempt, nil)

	// Let the first attempt run and the goroutine enter the backoff wait.
	time.Sleep(10 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitGroup did not drain within 500 ms after context cancel — goroutine leak")
	}
}
