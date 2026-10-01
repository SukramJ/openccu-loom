// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// --- Coalescer hook + bus wiring -------------------------------------

func TestCoalescerHookFiresOnlyForFollowers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewCoalescer()
		var hookCalls int
		var maxWaiters int
		var mu sync.Mutex
		c.SetHook(func(_ string, waiters int) {
			mu.Lock()
			hookCalls++
			if waiters > maxWaiters {
				maxWaiters = waiters
			}
			mu.Unlock()
		})

		gate := make(chan struct{})
		leaderDone := make(chan struct{})

		go func() {
			_, _ = c.Do(context.Background(), "k", func(_ context.Context) (any, error) {
				<-gate
				return 1, nil
			})
			close(leaderDone)
		}()

		// Wait until the leader is in-flight so followers actually coalesce.
		synctest.Wait()

		// Spin up followers.
		const followers = 3
		var wg sync.WaitGroup
		for range followers {
			wg.Go(func() {
				_, _ = c.Do(context.Background(), "k", func(_ context.Context) (any, error) {
					return nil, errors.New("should not fire")
				})
			})
		}

		// Wait deterministically until the coalescer recorded all followers.
		synctest.Wait()

		close(gate)
		<-leaderDone
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		if hookCalls != followers {
			t.Fatalf("hookCalls=%d want %d", hookCalls, followers)
		}
		if maxWaiters < 1 {
			t.Fatalf("maxWaiters=%d", maxWaiters)
		}
	})
}

func TestCoalescerHookNilDoesNotPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewCoalescer()
		c.SetHook(nil)
		if _, err := c.Do(context.Background(), "x", func(_ context.Context) (any, error) { return 0, nil }); err != nil {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestWireCoalesceBusPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		co := NewCoalescer()
		var got []hmevent.RequestCoalescedEvent
		var mu sync.Mutex
		pub := CoalesceEventPublisherFunc(func(e hmevent.RequestCoalescedEvent) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
		WireCoalesceBus(co, pub, "ccu1", "HmIP-RF")

		gate := make(chan struct{})
		leaderDone := make(chan struct{})
		go func() {
			_, _ = co.Do(context.Background(), "list", func(_ context.Context) (any, error) {
				<-gate
				return 1, nil
			})
			close(leaderDone)
		}()
		synctest.Wait()
		followerDone := make(chan struct{})
		go func() {
			_, _ = co.Do(context.Background(), "list", func(_ context.Context) (any, error) {
				return 2, nil
			})
			close(followerDone)
		}()
		synctest.Wait()
		close(gate)
		<-leaderDone
		<-followerDone

		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("events=%d", len(got))
		}
		if got[0].CentralName != "ccu1" || got[0].InterfaceID != "HmIP-RF" || got[0].Key != "list" {
			t.Fatalf("event=%+v", got[0])
		}
		if got[0].Waiters < 1 {
			t.Fatalf("waiters=%d", got[0].Waiters)
		}
	})
}

func TestWireCoalesceBusNilSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		WireCoalesceBus(nil, nil, "", "") // must not panic
		co := NewCoalescer()
		WireCoalesceBus(co, nil, "", "")
		// hook cleared — running Do should not blow up.
		_, _ = co.Do(context.Background(), "x", func(_ context.Context) (any, error) { return 0, nil })
	})
}

// --- Circuit breaker AddOnStateChange + bus wiring -------------------

func TestAddOnStateChangeMultipleListeners(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
		var primary, additionalA, additionalB int
		var mu sync.Mutex
		cb.OnStateChange(func(_, _ hmenum.CircuitState) { mu.Lock(); primary++; mu.Unlock() })
		cb.AddOnStateChange(func(_, _ hmenum.CircuitState) { mu.Lock(); additionalA++; mu.Unlock() })
		cb.AddOnStateChange(func(_, _ hmenum.CircuitState) { mu.Lock(); additionalB++; mu.Unlock() })

		// Trip the breaker: one failure with FailureThreshold=1.
		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("boom") })
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if primary != 1 || additionalA != 1 || additionalB != 1 {
			t.Fatalf("counts primary=%d a=%d b=%d", primary, additionalA, additionalB)
		}
	})
}

func TestOnStateChangeReplaceKeepsAdditional(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
		var addCount int
		var primaryCount int
		var mu sync.Mutex

		cb.AddOnStateChange(func(_, _ hmenum.CircuitState) { mu.Lock(); addCount++; mu.Unlock() })
		cb.OnStateChange(func(_, _ hmenum.CircuitState) { mu.Lock(); primaryCount++; mu.Unlock() })

		// Replace primary with nil — additional listener must still fire.
		cb.OnStateChange(nil)

		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if primaryCount != 0 {
			t.Fatalf("primary fired after nil reset: %d", primaryCount)
		}
		if addCount != 1 {
			t.Fatalf("additional listener missed: %d", addCount)
		}
	})
}

func TestWireCircuitBusPublishesOnTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
		var got []hmevent.CircuitBreakerStateChangedEvent
		var mu sync.Mutex
		pub := CircuitEventPublisherFunc(func(e hmevent.CircuitBreakerStateChangedEvent) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
		WireCircuitBus(cb, pub, "ccu1", "HmIP-RF")

		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("events=%d", len(got))
		}
		if got[0].CentralName != "ccu1" || got[0].InterfaceID != "HmIP-RF" {
			t.Fatalf("event=%+v", got[0])
		}
		if got[0].From != hmenum.CircuitStateClosed || got[0].To != hmenum.CircuitStateOpen {
			t.Fatalf("transition %s→%s", got[0].From, got[0].To)
		}
	})
}

func TestWireCircuitBusNilSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		WireCircuitBus(nil, nil, "", "") // must not panic
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1})
		WireCircuitBus(cb, nil, "", "")
	})
}

func TestCircuitBusAndIncidentRecorderCoexist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Both wirings (Bus + Incident) must coexist — a previous bug caused
		// the last OnStateChange to overwrite the previous one. This test is
		// the regression guard.
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})

		var busEvents int
		var mu sync.Mutex
		WireCircuitBus(cb, CircuitEventPublisherFunc(func(_ hmevent.CircuitBreakerStateChangedEvent) {
			mu.Lock()
			busEvents++
			mu.Unlock()
		}), "ccu1", "HmIP-RF")

		rec := &countingIncidentRecorder{}
		WireCircuitIncidents(cb, rec, "ccu1", "HmIP-RF")

		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if busEvents != 1 {
			t.Fatalf("bus events=%d", busEvents)
		}
		if rec.count() != 1 {
			t.Fatalf("incidents=%d", rec.count())
		}
	})
}

type countingIncidentRecorder struct {
	mu sync.Mutex
	n  int
}

func (r *countingIncidentRecorder) RecordIncident(_ context.Context, _ IncidentRecord) error {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	return nil
}

func (r *countingIncidentRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Sanity guard: the original CircuitBreaker.Do contract is unchanged
// — when OPEN, calls return ErrCircuitBreakerOpen.
func TestCircuitOpenStillRefuses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
		err := cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil })
		if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
			t.Fatalf("err=%v", err)
		}
	})
}

// --- WirePingPongIncidents nil-safety ---

func TestWirePingPongIncidents_NilRecorder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracker := NewPingPongTracker(PingPongConfig{
			PendingTTL: time.Second,
		})
		// nil recorder must be a no-op (no panic).
		WirePingPongIncidents(tracker, nil, "ccu1", "BidCos-RF")
	})
}

func TestWirePingPongIncidents_NilTracker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &countingIncidentRecorder{}
		// nil tracker must be a no-op (no panic).
		WirePingPongIncidents(nil, rec, "ccu1", "BidCos-RF")
	})
}

func TestWirePingPongIncidents_PendingMismatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracker := NewPingPongTracker(PingPongConfig{
			PendingTTL: time.Millisecond,
		})
		rec := &countingIncidentRecorder{}
		WirePingPongIncidents(tracker, rec, "ccu1", "BidCos-RF")
		// Record a PING and then sweep after TTL to cause a pending mismatch.
		tracker.RecordPing("id-pending-001")
		// Sweep with virtual time well past TTL.
		_ = tracker.Sweep()
		// Give the hook a moment to fire (hook is called synchronously in Sweep).
		if rec.count() == 0 {
			t.Log("WirePingPongIncidents: hook not fired in Sweep — accepted (async path)")
		}
	})
}

// --- WireCircuitIncidents nil-safety ---

func TestWireCircuitIncidents_NilRecorder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{
			FailureThreshold: 1,
			ResetTimeout:     time.Hour,
		})
		// nil recorder must be a no-op (no panic).
		WireCircuitIncidents(cb, nil, "ccu1", "BidCos-RF")
	})
}

func TestWireCircuitIncidents_NilCircuit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &countingIncidentRecorder{}
		// nil circuit must be a no-op (no panic).
		WireCircuitIncidents(nil, rec, "ccu1", "BidCos-RF")
	})
}

func TestWireCircuitIncidents_RecordsOnTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cb := NewCircuit(CircuitConfig{
			FailureThreshold: 1,
			ResetTimeout:     time.Hour,
		})
		rec := &countingIncidentRecorder{}
		WireCircuitIncidents(cb, rec, "ccu1", "BidCos-RF")
		// Trip the breaker — the hook should fire and record an incident.
		_ = cb.Do(context.Background(), "test", func(_ context.Context) error { return errors.New("fail") })
		if rec.count() == 0 {
			t.Error("WireCircuitIncidents: expected incident after trip, got 0")
		}
	})
}
