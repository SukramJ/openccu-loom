// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestConnectionRecoveryStopDrainsInFlightRecovery verifies that Stop
// cancels an in-flight pipeline step (via runCancel) and blocks until the
// goroutine exits (via recoveryWG.Wait), rather than returning while the
// step goroutine is still alive. goleak catches any surviving goroutine.
func TestConnectionRecoveryStopDrainsInFlightRecovery(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	// Disable the attempt cap so exhaustion does not interfere.
	c := NewConnectionRecoveryCoordinatorWithLimit("drain-central", bus, 0)

	started := make(chan struct{})     // closed when the step has entered
	cancelled := make(chan struct{})   // closed when the step observes ctx.Done
	var stepObservedCancel atomic.Bool // true if step exited via ctx.Done

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(ctx context.Context) error {
			close(started) // signal: step is executing
			select {
			case <-ctx.Done():
				stepObservedCancel.Store(true)
				close(cancelled)
				return ctx.Err()
			case <-time.After(30 * time.Second):
				// Should never reach here in tests; the test deadline
				// would fire first, but guard anyway.
				return errors.New("step timed out waiting for cancel")
			}
		},
	}}

	c.WithDefaultPipeline(pipeline)
	c.Subscribe()

	// triggerRecovery is exported for in-package callers; use it directly so
	// the pipeline runs as a managed goroutine (registered in recoveryWG).
	c.triggerRecovery("HmIP-RF")

	// Wait until the step is executing before calling Stop.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery step did not start within deadline")
	}

	// Stop must return within a generous deadline. If recoveryWG.Wait hangs,
	// the select catches it.
	stopDone := make(chan struct{})
	go func() {
		c.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Good — Stop returned promptly.
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5 s; recoveryWG.Wait likely hung")
	}

	// The step must have observed cancellation.
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("recovery step did not observe ctx.Done() cancellation")
	}

	if !stepObservedCancel.Load() {
		t.Error("step exited without observing ctx.Done; cancellation did not reach it")
	}
}

// TestConnectionRecoveryStopIdempotent verifies that Stop is safe to call
// when no recovery has ever run, and that a second Stop call is a no-op and
// does not panic. goleak will catch any goroutine that Subscribe might spawn.
func TestConnectionRecoveryStopIdempotent(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("idem-central", bus, 0)
	c.Subscribe()

	// First Stop: normal teardown.
	c.Stop()

	// Second Stop: must not panic and must return promptly.
	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop did not return within 2 s")
	}
}
