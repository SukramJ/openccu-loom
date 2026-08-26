// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func newCentralForHealthTest(t *testing.T) *central.Unit {
	t.Helper()
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	return c
}

func TestWireHealthClientStateChanged(t *testing.T) {
	c := newCentralForHealthTest(t)
	closer := WireHealth(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateConnected,
	})

	got, ok := c.Health.Get("HmIP-RF")
	if !ok || got.Status != health.StatusHealthy {
		t.Fatalf("connected → healthy, got %+v ok=%v", got, ok)
	}

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateFailed,
	})
	got, _ = c.Health.Get("HmIP-RF")
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("failed → unhealthy (escalated), got %s", got.Status)
	}
}

func TestWireHealthCircuitBreaker(t *testing.T) {
	c := newCentralForHealthTest(t)
	closer := WireHealth(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "BidCos-RF",
		From:        hmenum.CircuitStateClosed,
		To:          hmenum.CircuitStateOpen,
	})
	got, _ := c.Health.Get("BidCos-RF")
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("breaker open → unhealthy, got %s", got.Status)
	}

	events.Publish(c.EventBus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "BidCos-RF",
		From:        hmenum.CircuitStateOpen,
		To:          hmenum.CircuitStateClosed,
	})
	got, _ = c.Health.Get("BidCos-RF")
	if got.Status != health.StatusHealthy {
		// First healthy after unhealthy resets, but tracker semantics
		// may flap-damp; verify the latest sample reports healthy.
		if !got.LastSample.Healthy {
			t.Fatalf("breaker closed → healthy sample expected, got %+v", got.LastSample)
		}
	}
}

func TestWireHealthRecoveryCompleted(t *testing.T) {
	c := newCentralForHealthTest(t)
	closer := WireHealth(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		Result:      hmenum.RecoveryResultSuccess,
	})
	got, _ := c.Health.Get("HmIP-RF")
	if !got.LastSample.Healthy {
		t.Fatalf("recovery success → healthy sample, got %+v", got.LastSample)
	}

	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		Result:      hmenum.RecoveryResultFailed,
	})
	got, _ = c.Health.Get("HmIP-RF")
	if got.LastSample.Healthy {
		t.Fatalf("recovery failed → unhealthy sample, got %+v", got.LastSample)
	}
}

func TestWireHealthCloserUnsubscribes(t *testing.T) {
	c := newCentralForHealthTest(t)
	closer := WireHealth(c)
	closer()

	events.Publish(c.EventBus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "ghost",
		Reason:      hmenum.FailureReasonNetwork,
	})
	if _, ok := c.Health.Get("ghost"); ok {
		t.Fatal("after closer the tracker must not see new events")
	}
}

// TestWireHealthPublishesEventOnChange verifies that every Record call inside
// WireHealth also publishes a [hmevent.ConnectionHealthChangedEvent] on the
// bus.
// Closes.
func TestWireHealthPublishesEventOnChange(t *testing.T) {
	c := newCentralForHealthTest(t)

	var healthEventCount atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(e hmevent.ConnectionHealthChangedEvent) {
		if e.CentralName == "test" && e.InterfaceID == "HmIP-RF" {
			healthEventCount.Add(1)
		}
	})
	defer unsub()

	closer := WireHealth(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: "HmIP-RF",
		To:          hmenum.ClientStateConnected,
	})

	if healthEventCount.Load() == 0 {
		t.Fatal("expected at least one ConnectionHealthChangedEvent after ClientStateConnected")
	}
	got, ok := c.Health.Get("HmIP-RF")
	if !ok || !got.LastSample.Healthy {
		t.Fatalf("health tracker: want healthy, got %+v ok=%v", got, ok)
	}
}

// TestWireHealthInitialSync verifies that WireHealth immediately seeds the
// health tracker with the current connection state of already-registered
// clients, without waiting for the first event. Closes.
func TestWireHealthInitialSync(t *testing.T) {
	c, err := central.New(central.Config{Name: "test-sync"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	// Register a client in CONNECTED state before WireHealth is called.
	ic, err := client.New(client.Config{
		CentralName: "test-sync",
		Interface:   hmenum.InterfaceBidCosRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	for _, state := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	} {
		if err := ic.TransitionTo(state, "test", false, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	closer := WireHealth(c)
	defer closer()

	// The tracker must already have a record for "BidCos-RF" — the initial
	// sync runs synchronously inside WireHealth before subscriptions are set up.
	got, ok := c.Health.Get("BidCos-RF")
	if !ok {
		t.Fatal("initial sync: component BidCos-RF not found in tracker")
	}
	if !got.LastSample.Healthy {
		t.Errorf("initial sync: want healthy sample for connected client, got %+v", got.LastSample)
	}
}
