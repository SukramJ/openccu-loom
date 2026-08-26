// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the reliability composition order in InterfaceClient.Call: the
// circuit breaker is consulted BEFORE a throttle permit is taken (an OPEN
// breaker sheds load instead of queueing), and the throttle permit is acquired
// per wire-attempt and released before each retry backoff (a backing-off
// command does not hold the pool permit hostage).

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// methodAwareCaller returns setValueErr for setValue and ("ok", nil) for every
// other method, so a test can make writes fail (and back off) while reads
// succeed.
type methodAwareCaller struct {
	setValueErr error
}

func (m methodAwareCaller) Call(_ context.Context, method string, _ []any) (any, error) {
	if method == "setValue" {
		return nil, m.setValueErr
	}
	return "ok", nil
}

// TestOpenCircuitShedsWithoutAcquiringThrottle verifies that when the circuit
// breaker is OPEN, a non-critical Call returns ErrCircuitBreakerOpen
// immediately WITHOUT waiting on a fully-held throttle. Before the fix the
// throttle permit was acquired before the circuit was consulted, so a shed
// call would block behind the held permit and time out instead of failing
// fast. Critical-priority calls are the deliberate exception: they probe an
// OPEN circuit once (alarm stop path, S5) — covered by the breaker's own
// tests and the siren-safety contract suite.
func TestOpenCircuitShedsWithoutAcquiringThrottle(t *testing.T) {
	t.Parallel()

	circuit := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour, // stay OPEN for the test
	})
	circuit.RecordFailure() // trip OPEN
	if circuit.State() != hmenum.CircuitStateOpen {
		t.Fatalf("circuit state = %v, want OPEN", circuit.State())
	}

	rec := &recordingCaller{}
	readThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	ic, err := New(Config{
		CentralName:  "c",
		Interface:    hmenum.InterfaceHmIPRF,
		Caller:       rec,
		Circuit:      circuit,
		ReadThrottle: readThrottle,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Fully occupy the read pool from outside: any attempt to acquire it would
	// block until the context expires.
	if err := readThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("readThrottle.Acquire: %v", err)
	}
	defer readThrottle.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, callErr := ic.Call(ctx, "getValue", nil, hmenum.CommandPriorityHigh, "")
	elapsed := time.Since(start)

	if !errors.Is(callErr, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("Call error = %v, want ErrCircuitBreakerOpen (load shed before throttle)", callErr)
	}
	if rec.calls.Load() != 0 {
		t.Errorf("transport invoked %d times, want 0 (call must be shed)", rec.calls.Load())
	}
	if elapsed >= 200*time.Millisecond {
		t.Errorf("Call took %v — it blocked on the held throttle instead of shedding fast", elapsed)
	}
}

// TestBackingOffWriteReleasesThrottlePermit verifies that a command in retry
// backoff does NOT hold its throttle permit across the backoff sleep, so an
// independent call can proceed. Before the fix the permit was acquired once
// around the whole retry chain and held through every backoff, blocking every
// other call sharing the pool for the entire (potentially tens-of-seconds)
// backoff window.
func TestBackingOffWriteReleasesThrottlePermit(t *testing.T) {
	t.Parallel()

	fakeClock := clock.NewFake(time.Now())
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: 4,
		Initial:     1 * time.Second,
		Max:         8 * time.Second,
		Multiplier:  2,
		Jitter:      -1, // disable jitter for deterministic backoff timing
		Clock:       fakeClock,
	})
	// Single shared pool: reads and writes contend for the one permit, so a
	// write holding it across backoff would visibly block a read.
	throttle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	ic, err := New(Config{
		CentralName: "c",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      methodAwareCaller{setValueErr: errors.New("boom")},
		Retrier:     retrier,
		Throttle:    throttle,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	writeDone := make(chan struct{})
	go func() {
		_, _ = ic.Call(context.Background(), "setValue", nil, hmenum.CommandPriorityCritical, "")
		close(writeDone)
	}()

	// Wait until the write is parked in its first backoff (pending fake timer).
	deadline := time.Now().Add(2 * time.Second)
	for fakeClock.PendingCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("write never entered retry backoff")
		}
		time.Sleep(time.Millisecond)
	}

	// Key assertion: the permit is free while the write is backing off.
	if got := throttle.InFlight(); got != 0 {
		t.Fatalf("throttle InFlight during backoff = %d, want 0 (permit held across backoff)", got)
	}

	// An independent read acquires the freed permit and completes immediately.
	readCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	v, rerr := ic.Call(readCtx, "getValue", nil, hmenum.CommandPriorityCritical, "")
	if rerr != nil {
		t.Fatalf("independent read blocked/failed during write backoff: %v", rerr)
	}
	if v != "ok" {
		t.Fatalf("read value = %v, want ok", v)
	}

	// Drain the write goroutine: advance the fake clock past its remaining
	// backoffs so it exhausts and returns.
	drainDeadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-writeDone:
			if got := throttle.InFlight(); got != 0 {
				t.Errorf("throttle InFlight after write finished = %d, want 0", got)
			}
			return
		default:
			fakeClock.Advance(10 * time.Second)
			time.Sleep(2 * time.Millisecond)
			if time.Now().After(drainDeadline) {
				t.Fatal("write goroutine did not finish draining")
			}
		}
	}
}
