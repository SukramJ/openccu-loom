// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_subscribe_test.go — C-RECOV-1 / C-RECOV-6:
// event-driven recovery and subscription teardown.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// waitFor polls pred every 2 ms until it returns true or timeout elapses.
// eventWaitTimeout is the package-wide ceiling for positive waits on
// asynchronous progress (event-bus delivery, spawned recovery goroutines,
// heartbeat ticks). It is deliberately generous: waitFor and the one-shot
// channel selects return as soon as the awaited condition holds, so the
// ceiling costs nothing on the happy path — but a tight deadline turns
// scheduler starvation on a heavily loaded CI runner under -race
// instrumentation into a spurious failure. Never use it for negative
// assertions ("nothing happens within N ms"); those need short, explicit
// windows.
const eventWaitTimeout = 30 * time.Second

func waitFor(t *testing.T, pred func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// atomicPipeline returns a pipeline with a single stage that increments
// count on each execution.
func atomicPipeline(count *atomic.Int32) []Pipeline {
	return []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			count.Add(1)
			return nil
		},
	}}
}

// TestSubscribeReactsToConnectionLostEvent verifies that publishing a
// ConnectionLostEvent for the coordinator's central triggers an async
// recovery run.
func TestSubscribeReactsToConnectionLostEvent(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "HmIP-RF",
	})

	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery did not start after ConnectionLostEvent (count=%d)", count.Load())
	}
}

// TestSubscribeReactsToCircuitBreakerTripped verifies that publishing a
// CircuitBreakerTrippedEvent triggers recovery for the named interface.
func TestSubscribeReactsToCircuitBreakerTripped(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerTrippedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "BidCos-RF",
	})

	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery did not start after CircuitBreakerTrippedEvent (count=%d)", count.Load())
	}
}

// TestSubscribeReactsToCBStateChangedOpen verifies that a
// CircuitBreakerStateChangedEvent with To==Open triggers recovery.
func TestSubscribeReactsToCBStateChangedOpen(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "CUxD",
		From:        hmenum.CircuitStateClosed,
		To:          hmenum.CircuitStateOpen,
	})

	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery did not start after CB→Open (count=%d)", count.Load())
	}
}

// TestSubscribeReactsToCBStateChangedHalfOpenToClosed verifies that
// a half_open → closed transition triggers recovery. The breaker
// reset itself after a successful probe call — but the CCU forgot
// the XML-RPC callback registration during the outage. Running the
// recovery pipeline once on this transition re-issues init() so
// callback events resume.
func TestSubscribeReactsToCBStateChangedHalfOpenToClosed(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "CUxD",
		From:        hmenum.CircuitStateHalfOpen,
		To:          hmenum.CircuitStateClosed,
	})

	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery did not start after CB half_open→closed (count=%d) — bridge will not receive callback events until next outage", count.Load())
	}
}

// TestSubscribeIgnoresCBStateChangedClosed ensures that a CB transition
// to Closed does NOT trigger recovery.
func TestSubscribeIgnoresCBStateChangedClosed(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "CUxD",
		From:        hmenum.CircuitStateOpen,
		To:          hmenum.CircuitStateClosed,
	})

	time.Sleep(30 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("recovery triggered unexpectedly by CB→Closed (count=%d)", count.Load())
	}
}

// TestSubscribeIgnoresOtherCentral verifies that events for a different
// central_name are silently ignored.
func TestSubscribeIgnoresOtherCentral(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c2", // different central
		InterfaceID: "HmIP-RF",
	})

	time.Sleep(30 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("recovery triggered for wrong central (count=%d)", count.Load())
	}
}

// TestSubscribeSkipsDuplicateRecovery checks that two rapid ConnectionLost
// events for the same interface result in only ONE active recovery (the
// second is dropped by the duplicate-event guard while the first is still
// running).
func TestSubscribeSkipsDuplicateRecovery(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var starts atomic.Int32

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			starts.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return nil
		},
	}}
	c.WithDefaultPipeline(pipeline)
	c.Subscribe()
	defer c.Stop()

	// First event: starts a slow (blocked) recovery.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "HmIP-RF",
	})
	// Wait until recovery is actually inside the stage.
	select {
	case <-entered:
	case <-time.After(eventWaitTimeout):
		t.Fatal("first recovery did not start")
	}

	// Second event while first is in flight: the duplicate guard must skip it.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "HmIP-RF",
	})
	// Small pause to give any spurious goroutine a chance to attempt entry.
	time.Sleep(20 * time.Millisecond)

	// Release the first recovery.
	close(release)

	// Wait for the coordinator's active map to drain.
	if !waitFor(t, func() bool {
		return !c.MetricsInRecovery()
	}, eventWaitTimeout) {
		t.Fatal("recovery did not finish after release")
	}

	if starts.Load() > 1 {
		t.Fatalf("duplicate recovery triggered: starts=%d want 1", starts.Load())
	}
}

// TestSubscribeHeartbeatFiresRecoveryPerInterface verifies that
// HeartbeatTimerFiredEvent triggers recovery for each listed interface.
func TestSubscribeHeartbeatFiresRecoveryPerInterface(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var (
		mu        sync.Mutex
		triggered []string
	)

	events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) {
		if e.CentralName != "c1" {
			return
		}
		mu.Lock()
		triggered = append(triggered, e.InterfaceID)
		mu.Unlock()
	})

	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "c1",
		InterfaceIDs: []string{"HmIP-RF", "BidCos-RF"},
	})

	if !waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(triggered) >= 2
	}, eventWaitTimeout) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("heartbeat did not trigger recovery for both interfaces; got %v", triggered)
	}
}

// TestStopReleasesSubscriptions verifies that after Stop, publishing events
// no longer triggers recovery.
func TestStopReleasesSubscriptions(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline(atomicPipeline(&count))
	c.Subscribe()

	// Stop before publishing.
	c.Stop()

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c1",
		InterfaceID: "HmIP-RF",
	})

	time.Sleep(50 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("recovery triggered after Stop (count=%d)", count.Load())
	}
}

// TestStopIsIdempotent verifies that calling Stop twice does not panic.
func TestStopIsIdempotent(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)
	c.Subscribe()
	c.Stop()
	c.Stop() // must not panic
}

// recoverySubscribedEventTypes are the bus event types [Subscribe]
// installs a handler for.
var recoverySubscribedEventTypes = []hmevent.EventType{
	hmevent.EventTypeConnectionLost,
	hmevent.EventTypeCircuitBreakerTripped,
	hmevent.EventTypeCircuitBreakerStateChanged,
}

// TestSubscribeIsIdempotent pins that repeated Subscribe calls leave
// exactly one handler set on the bus.
//
// The south-bound wiring calls Subscribe once per interface and again on
// every bring-up generation, so a three-interface central that had been
// re-inited twice ran six handler sets and six heartbeat loops. Each
// heartbeat tick then re-armed the per-interface attempt cap once per
// loop, defeating the exhaustion brake exactly when several CCUs were
// flapping. Nothing failed visibly — recovery simply ran more often than
// its own limits allowed, and every loop lived until Unit.Stop.
func TestSubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)

	c.Subscribe()
	t.Cleanup(c.Stop)

	baseline := map[hmevent.EventType]int{}
	for _, typ := range recoverySubscribedEventTypes {
		baseline[typ] = bus.HandlerCount(typ)
		if baseline[typ] == 0 {
			t.Fatalf("no handler registered for %s; this test would pass vacuously", typ)
		}
	}

	for range 3 {
		c.Subscribe()
	}

	for _, typ := range recoverySubscribedEventTypes {
		if got := bus.HandlerCount(typ); got != baseline[typ] {
			t.Errorf("%s handlers = %d after four Subscribe calls, want %d", typ, got, baseline[typ])
		}
	}
}

// TestSubscribeAfterStopStaysStopped pins that a stopped coordinator
// cannot be re-armed by a late Subscribe from a bring-up generation that
// is already going away.
func TestSubscribeAfterStopStaysStopped(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 0)
	c.Subscribe()
	c.Stop()

	c.Subscribe()

	for _, typ := range recoverySubscribedEventTypes {
		if got := bus.HandlerCount(typ); got != 0 {
			t.Errorf("%s handlers = %d after Subscribe on a stopped coordinator, want 0", typ, got)
		}
	}
}
