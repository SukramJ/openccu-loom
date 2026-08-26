// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for InterfaceClient reconnect-attempt tracking,
// state-changed bus integration, and callback-alive logic.

package client

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func newMinimalIC(t *testing.T) *InterfaceClient {
	t.Helper()
	ic, err := New(Config{
		CentralName: "test",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ic
}

// ---------------------------------------------------------------------------
// — ReconnectAttempts counter
// ---------------------------------------------------------------------------

func TestReconnectAttemptsInitiallyZero(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	if got := ic.ReconnectAttempts(); got != 0 {
		t.Errorf("ReconnectAttempts() = %d; want 0", got)
	}
}

func TestSetReconnectAttempts(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	ic.SetReconnectAttempts(3)
	if got := ic.ReconnectAttempts(); got != 3 {
		t.Errorf("ReconnectAttempts() = %d; want 3", got)
	}
}

func TestIncrementReconnectAttempts(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	n := ic.IncrementReconnectAttempts()
	if n != 1 {
		t.Errorf("IncrementReconnectAttempts() = %d; want 1", n)
	}
	n = ic.IncrementReconnectAttempts()
	if n != 2 {
		t.Errorf("IncrementReconnectAttempts() = %d; want 2", n)
	}
	ic.SetReconnectAttempts(0)
	if got := ic.ReconnectAttempts(); got != 0 {
		t.Errorf("after reset: ReconnectAttempts() = %d; want 0", got)
	}
}

// ---------------------------------------------------------------------------
// — NotifyCallback called on CONNECTED state transition
// ---------------------------------------------------------------------------

func TestSetStateChangedBusCallsNotifyCallbackOnConnected(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	bus := events.NewBus()

	// Pre-condition: no callback has been recorded yet.
	if !ic.LastCallbackAt().IsZero() {
		t.Fatal("LastCallbackAt must be zero before any callback")
	}

	ic.SetStateChangedBus(bus, "")

	// Force transition to CONNECTED via force=true.
	err := ic.TransitionTo(hmenum.ClientStateConnected, "test", true, hmenum.FailureReasonNone)
	if err != nil {
		t.Fatalf("TransitionTo CONNECTED: %v", err)
	}

	// The CONNECTED transition must have called NotifyCallback, so the
	// timestamp is now set and IsCallbackAlive returns true.
	if ic.LastCallbackAt().IsZero() {
		t.Error("LastCallbackAt must be non-zero after CONNECTED transition via SetStateChangedBus")
	}
	if !ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive should be true after CONNECTED transition via SetStateChangedBus")
	}
}

// ---------------------------------------------------------------------------
// — SetStateChangedBus emits ClientStateChangedEvent on bus
// ---------------------------------------------------------------------------

func TestSetStateChangedBusEmitsEvent(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	bus := events.NewBus()

	var received []hmevent.ClientStateChangedEvent
	unsub := events.Subscribe(bus, func(ev hmevent.ClientStateChangedEvent) {
		received = append(received, ev)
	})
	defer unsub()

	ic.SetStateChangedBus(bus, "")

	// Advance to INITIALIZING.
	_ = ic.TransitionTo(hmenum.ClientStateInitializing, "go", true, hmenum.FailureReasonNone)

	// Give the bus a tick to deliver.
	time.Sleep(5 * time.Millisecond)

	if len(received) == 0 {
		t.Fatal("no ClientStateChangedEvent received after state transition")
	}
	ev := received[0]
	if ev.From != hmenum.ClientStateCreated {
		t.Errorf("event.From = %s; want CREATED", ev.From)
	}
	if ev.To != hmenum.ClientStateInitializing {
		t.Errorf("event.To = %s; want INITIALIZING", ev.To)
	}
	if ev.CentralName != "test" {
		t.Errorf("event.CentralName = %q; want %q", ev.CentralName, "test")
	}
}

// TestSetStateChangedBusUsesProvidedWireID locks the fix for the
// "central stuck DEGRADED after connect" bug: the published event must
// carry the supplied wire ID (e.g. "OttoGo-HmIP-RF") as InterfaceID so
// the health tracker / device-availability records land on the same
// component key the client coordinator uses — not the bare interface
// name.
func TestSetStateChangedBusUsesProvidedWireID(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	bus := events.NewBus()

	var received []hmevent.ClientStateChangedEvent
	unsub := events.Subscribe(bus, func(ev hmevent.ClientStateChangedEvent) {
		received = append(received, ev)
	})
	defer unsub()

	ic.SetStateChangedBus(bus, "test-HmIP-RF")
	_ = ic.TransitionTo(hmenum.ClientStateInitializing, "go", true, hmenum.FailureReasonNone)
	time.Sleep(5 * time.Millisecond)

	if len(received) == 0 {
		t.Fatal("no ClientStateChangedEvent received after state transition")
	}
	if got := received[0].InterfaceID; got != "test-HmIP-RF" {
		t.Errorf("event.InterfaceID = %q; want the supplied wire ID %q", got, "test-HmIP-RF")
	}
}

func TestSetStateChangedBusNilRemovesPublisher(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	bus := events.NewBus()

	fired := false
	unsub := events.Subscribe(bus, func(_ hmevent.ClientStateChangedEvent) {
		fired = true
	})
	defer unsub()

	ic.SetStateChangedBus(bus, "")
	ic.SetStateChangedBus(nil, "") // remove

	_ = ic.TransitionTo(hmenum.ClientStateInitializing, "", true, hmenum.FailureReasonNone)
	time.Sleep(5 * time.Millisecond)

	if fired {
		t.Error("bus event fired after SetStateChangedBus(nil)")
	}
}

// ---------------------------------------------------------------------------
// — IsCallbackAlive freshness logic
// ---------------------------------------------------------------------------

// TestIsCallbackAliveBeforeAnyCallback verifies that a freshly constructed
// client reports IsCallbackAlive() == true even before any event has arrived.
// The post-init guard treats a zero timestamp as "not yet observed" rather
// than "stale", so scheduler ticks during init do not falsely trigger
// a connection-lost cycle.
func TestIsCallbackAliveBeforeAnyCallback(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	// No callback has been recorded yet — timestamp is zero.
	// The post-init guard must return true, not false.
	if !ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive() = false before any NotifyCallback call; want true (post-init guard)")
	}
}

func TestIsCallbackAliveRecentCallback(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)
	ic.NotifyCallback()
	if !ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive() = false immediately after NotifyCallback; want true")
	}
}

// ---------------------------------------------------------------------------
// — IsCallbackAlive: freshness and state-gate logic
// ---------------------------------------------------------------------------

// TestIsCallbackAliveStaleLogicVerified covers the three observable outcomes
// of IsCallbackAlive: zero timestamp (post-init guard → true), after a
// callback (fresh → true), and failed/reconnecting state (→ false).
func TestIsCallbackAliveStaleLogicVerified(t *testing.T) {
	t.Parallel()
	ic := newMinimalIC(t)

	// Zero timestamp — post-init guard keeps the client alive.
	if !ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive() = false with zero lastCallbackAt; want true (post-init guard)")
	}

	// After NotifyCallback the timestamp is fresh — still alive.
	ic.NotifyCallback()
	if !ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive() = false right after NotifyCallback; want true")
	}

	// FAILED state overrides the freshness window — reports false.
	err := ic.TransitionTo(hmenum.ClientStateFailed, "test", true, hmenum.FailureReasonNone)
	if err != nil {
		t.Fatalf("TransitionTo FAILED: %v", err)
	}
	if ic.IsCallbackAlive() {
		t.Error("IsCallbackAlive() = true in FAILED state; want false")
	}
}
