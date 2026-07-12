// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_serialization_test.go — regression coverage for the
// per-interface serialization loop in runInternal: concurrent Run
// invocations for the SAME interfaceID must never execute their pipeline
// stages at the same time, and the cleanup defer must only release the
// caller's own registration.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRunSerializesConcurrentInvocationsPerInterface fires N concurrent Run
// calls at the same interfaceID. Each pipeline stage increments a shared
// in-flight counter on entry, records the maximum concurrency ever observed
// via a CompareAndSwap loop, sleeps briefly to widen any race window, then
// decrements on exit.
//
// Before the runInternal fix, a single close() of the per-interface done
// channel released every goroutine blocked on it in one shot; without a
// re-check loop, more than one waiter could register itself as the active
// runner and execute its pipeline stage concurrently with another. The
// fixed runInternal re-checks c.active[interfaceID] after every wake, so
// exactly one goroutine wins each iteration — the observed max concurrency
// must be exactly 1.
func TestRunSerializesConcurrentInvocationsPerInterface(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("serialize-same-iface", events.NewBus(), 0)

	const n = 8
	var inFlight int32
	var maxObserved int32

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				prev := atomic.LoadInt32(&maxObserved)
				if cur <= prev {
					break
				}
				if atomic.CompareAndSwapInt32(&maxObserved, prev, cur) {
					break
				}
			}
			// Widen the race window: if the fix regressed, this gives a
			// second concurrent waiter ample time to also enter the stage
			// before we decrement.
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return nil
		},
	}}

	var (
		wg        sync.WaitGroup
		completed int32
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if c.Run(context.Background(), "HmIP-RF", pipeline) == hmenum.RecoveryResultSuccess {
				atomic.AddInt32(&completed, 1)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("goroutines did not finish in time — possible goroutine leak or deadlock")
	}

	if got := atomic.LoadInt32(&maxObserved); got != 1 {
		t.Errorf("max observed pipeline concurrency = %d, want 1 (concurrent Run calls for the same interface must be serialized)", got)
	}
	if got := atomic.LoadInt32(&completed); got != n {
		t.Errorf("completed invocations = %d, want %d (all Run calls must return Success)", got, n)
	}

	// The cleanup defer's ownership check must have released the slot after
	// the last runner finished — a stale registration would wedge future
	// recoveries for this interface.
	if c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecoveryFor must be false after all serialized runs finished")
	}
}
