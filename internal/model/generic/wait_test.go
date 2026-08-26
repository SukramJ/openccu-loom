// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForConfirmationReturnsImmediatelyWhenIdle(t *testing.T) {
	dp := dpForCollector(t, "0001:1", "LEVEL")

	start := time.Now()
	if err := dp.WaitForConfirmation(context.Background()); err != nil {
		t.Fatalf("idle wait: %v", err)
	}
	// A correct idle wait returns in microseconds; the only way to exceed
	// a generous bound is to wrongly block. Kept loose so race-detector
	// overhead and CI scheduling jitter never flake a non-blocking return.
	if dur := time.Since(start); dur > time.Second {
		t.Fatalf("idle wait should not block, took %v", dur)
	}
}

func TestWaitForConfirmationUnblocksOnMatchingConfirm(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(2*time.Second))
	dp.OnEvent(0.0)

	if err := sendFloat(t, dp, 0.5); err != nil {
		t.Fatalf("send: %v", err)
	}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- dp.WaitForConfirmation(context.Background())
	}()

	// Give the waiter a moment to subscribe before the event
	// arrives.
	time.Sleep(20 * time.Millisecond)
	dp.OnEvent(0.5) // confirms

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("wait err: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitForConfirmation must unblock within 500ms")
	}
}

func TestWaitForConfirmationUnblocksOnRollback(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(50*time.Millisecond))
	dp.OnEvent(0.0)
	_ = sendFloat(t, dp, 0.5)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- dp.WaitForConfirmation(context.Background())
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("wait err on rollback: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitForConfirmation must unblock when timeout-rollback fires")
	}
}

func TestWaitForConfirmationContextCancellation(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.0)
	_ = sendFloat(t, dp, 0.5)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := dp.WaitForConfirmation(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx-cancel: got %v, want DeadlineExceeded", err)
	}
}

func TestWaitForConfirmationMultipleSubscribersAllSettle(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(2*time.Second))
	dp.OnEvent(0.0)
	_ = sendFloat(t, dp, 0.5)

	const N = 5
	results := make(chan error, N)
	for range N {
		go func() {
			results <- dp.WaitForConfirmation(context.Background())
		}()
	}

	time.Sleep(20 * time.Millisecond) // let subscribers attach
	dp.OnEvent(0.5)                   // confirm

	for i := range N {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("waiter %d: %v", i, err)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("waiter %d did not settle", i)
		}
	}
}

func TestWaitForConfirmationUnblocksOnMismatchClear(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(2*time.Second))
	dp.OnEvent(0.0)
	_ = sendFloat(t, dp, 0.5)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- dp.WaitForConfirmation(context.Background())
	}()

	time.Sleep(15 * time.Millisecond)
	dp.OnEvent(0.7) // mismatch → clear (CCU authoritative)

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("wait err: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("mismatch-clear must unblock waiter")
	}
}
