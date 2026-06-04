// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_heartbeat_loop_test.go — tests for the internal
// heartbeat-loop producer. The loop scans for exhausted interfaces and emits
// HeartbeatTimerFiredEvent so the subscriber in Subscribe can re-open one
// recovery attempt per tick. These tests exercise the goroutine launched by
// Subscribe, not the subscriber itself (which is covered by
// connection_recovery_concurrent_cancel_test.go and
// connection_recovery_backoff_statemachine_test.go).

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestHeartbeatLoopEmitsEventForExhaustedInterface verifies that after
// Subscribe is called, the internal heartbeat-loop goroutine fires a
// HeartbeatTimerFiredEvent that includes an exhausted interface. A compressed
// interval is used so the test runs in milliseconds, not seconds.
func TestHeartbeatLoopEmitsEventForExhaustedInterface(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	const maxAttempts = 2
	c := NewConnectionRecoveryCoordinatorWithLimit("hbl-central", bus, maxAttempts)

	// Use a short interval so we do not wait 60 s in the test.
	const testInterval = 20 * time.Millisecond
	c.WithHeartbeatInterval(testInterval)

	// Exhaust the lane by driving the attempt counter to maxAttempts without going
	// through triggerRecovery (which would also start goroutines). We use
	// bumpAttempt directly — it is the same path runInternal takes.
	for range maxAttempts {
		c.bumpAttempt("HmIP-RF")
	}
	// Ensure state entry exists so fireHeartbeatIfExhausted sees the interface.
	c.mu.Lock()
	c.ensureStateLocked("HmIP-RF")
	c.mu.Unlock()

	// Subscribe also starts the heartbeat-loop goroutine.
	c.Subscribe()
	defer c.Stop()

	// Watch for HeartbeatTimerFiredEvent containing "HmIP-RF".
	var fired atomic.Bool
	events.Subscribe(bus, func(e hmevent.HeartbeatTimerFiredEvent) {
		if e.CentralName != "hbl-central" {
			return
		}
		if slices.Contains(e.InterfaceIDs, "HmIP-RF") {
			fired.Store(true)
			return
		}
	})

	// Wait up to 500 ms for the heartbeat tick to fire (interval = 20 ms,
	// so we expect it within the first couple of ticks).
	if !waitFor(t, fired.Load, 500*time.Millisecond) {
		t.Fatal("heartbeat-loop did not emit HeartbeatTimerFiredEvent for exhausted interface within 500 ms")
	}
}

// TestHeartbeatLoopSilentWhenNoExhaustedInterfaces verifies that the loop
// does NOT emit HeartbeatTimerFiredEvent when no interface has hit its cap.
func TestHeartbeatLoopSilentWhenNoExhaustedInterfaces(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hbl-quiet", bus, 5)
	c.WithHeartbeatInterval(20 * time.Millisecond)

	// No attempts at all — nothing is exhausted.
	c.Subscribe()
	defer c.Stop()

	var count atomic.Int32
	events.Subscribe(bus, func(e hmevent.HeartbeatTimerFiredEvent) {
		if e.CentralName == "hbl-quiet" {
			count.Add(1)
		}
	})

	// Let several ticks pass; expect zero events.
	time.Sleep(80 * time.Millisecond)
	if n := count.Load(); n != 0 {
		t.Errorf("got %d HeartbeatTimerFiredEvent(s), want 0 when no interface is exhausted", n)
	}
}

// TestHeartbeatLoopSilentWhenCapDisabled verifies that when maxAttempts == 0
// (cap disabled) the loop never emits HeartbeatTimerFiredEvent regardless of
// attempt count, because fireHeartbeatIfExhausted is a no-op without a cap.
func TestHeartbeatLoopSilentWhenCapDisabled(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hbl-nocap", bus, 0) // cap disabled
	c.WithHeartbeatInterval(20 * time.Millisecond)

	// Even with a non-zero attempt count, no heartbeat event should fire.
	c.bumpAttempt("HmIP-RF")
	c.mu.Lock()
	c.ensureStateLocked("HmIP-RF")
	c.mu.Unlock()

	c.Subscribe()
	defer c.Stop()

	var count atomic.Int32
	events.Subscribe(bus, func(e hmevent.HeartbeatTimerFiredEvent) {
		if e.CentralName == "hbl-nocap" {
			count.Add(1)
		}
	})

	time.Sleep(80 * time.Millisecond)
	if n := count.Load(); n != 0 {
		t.Errorf("got %d HeartbeatTimerFiredEvent(s), want 0 when cap is disabled", n)
	}
}

// TestHeartbeatLoopStopsAfterStop verifies that the heartbeat-loop goroutine
// stops emitting events after Stop is called.
func TestHeartbeatLoopStopsAfterStop(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	const maxAttempts = 1
	c := NewConnectionRecoveryCoordinatorWithLimit("hbl-stop", bus, maxAttempts)
	c.WithHeartbeatInterval(20 * time.Millisecond)

	c.bumpAttempt("CUxD")
	c.mu.Lock()
	c.ensureStateLocked("CUxD")
	c.mu.Unlock()

	c.Subscribe()

	// Wait for at least one heartbeat event to confirm the loop is running.
	var count atomic.Int32
	events.Subscribe(bus, func(e hmevent.HeartbeatTimerFiredEvent) {
		if e.CentralName == "hbl-stop" {
			count.Add(1)
		}
	})
	if !waitFor(t, func() bool { return count.Load() >= 1 }, 500*time.Millisecond) {
		t.Fatal("loop did not fire at least one event before Stop")
	}

	// Stop the coordinator and record how many events fired before stop.
	c.Stop()
	snapshot := count.Load()

	// Wait another couple of intervals; no new events should arrive.
	time.Sleep(80 * time.Millisecond)
	if after := count.Load(); after != snapshot {
		t.Errorf("got %d extra HeartbeatTimerFiredEvent(s) after Stop, want 0", after-snapshot)
	}
}
