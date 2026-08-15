// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newNopCaller returns a CallerFunc that always succeeds with nil.
func newNopCaller() client.CallerFunc {
	return client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, nil
	})
}

// registerConnectedClient registers a new InterfaceClient in CONNECTED state.
func registerConnectedClient(t *testing.T, c *Unit, ifaceID string, iface hmenum.Interface) *coordinators.ClientEntry {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName:  c.cfg.Name,
		Interface:    iface,
		Caller:       newNopCaller(),
		Capabilities: backends.Capabilities{},
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ic.SetState(hmenum.ClientStateConnected)
	entry := &coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   iface,
		Client:      ic,
	}
	if err := c.Clients.Register(entry); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	return entry
}

// registerDisconnectedClient registers a new InterfaceClient in DISCONNECTED
// state. Connected() returns false for this entry.
func registerDisconnectedClient(t *testing.T, c *Unit, ifaceID string, iface hmenum.Interface) *coordinators.ClientEntry {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName:  c.cfg.Name,
		Interface:    iface,
		Caller:       newNopCaller(),
		Capabilities: backends.Capabilities{},
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ic.SetState(hmenum.ClientStateDisconnected)
	entry := &coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   iface,
		Client:      ic,
	}
	if err := c.Clients.Register(entry); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	return entry
}

// drainSystemStatusEvents subscribes to the bus and returns a drain function
// that unsubscribes and returns all collected events.
func drainSystemStatusEvents(c *Unit) func() []hmevent.SystemStatusChangedEvent {
	var received []hmevent.SystemStatusChangedEvent
	unsub := events.Subscribe(c.EventBus, func(e hmevent.SystemStatusChangedEvent) {
		received = append(received, e)
	})
	return func() []hmevent.SystemStatusChangedEvent {
		unsub()
		return received
	}
}

// mustNew creates a Unit for testing and fails the test on error.
func mustNew(t *testing.T, name string) *Unit {
	t.Helper()
	c, err := New(Config{Name: name})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// mustStarted starts the Unit and registers a cleanup that stops it.
func mustStarted(t *testing.T, c *Unit) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		c.Stop()
	})
	return cancel
}

// TestEvaluateCentralState_AllConnected verifies RUNNING is set and an event
// is emitted when every registered client is CONNECTED.
func TestEvaluateCentralState_AllConnected(t *testing.T) {
	c := mustNew(t, "eval-all-connected")
	_ = mustStarted(t, c)

	// Place machine in DEGRADED so the RUNNING transition is valid.
	_ = c.StateMachine.ForceTransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)

	registerConnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)

	drain := drainSystemStatusEvents(c)
	c.EvaluateCentralState("test", false)
	evts := drain()

	if got := c.StateMachine.State(); got != hmenum.CentralStateRunning {
		t.Fatalf("state = %s; want RUNNING", got)
	}
	if len(evts) == 0 {
		t.Fatal("expected at least one SystemStatusChangedEvent")
	}
	last := evts[len(evts)-1]
	if last.CentralState != hmenum.CentralStateRunning {
		t.Errorf("event.CentralState = %s; want RUNNING", last.CentralState)
	}
	if !last.Healthy {
		t.Error("event.Healthy = false; want true when all connected")
	}
}

// TestEvaluateCentralState_Degraded verifies DEGRADED is set when at least
// one client is connected but not all.
func TestEvaluateCentralState_Degraded(t *testing.T) {
	c := mustNew(t, "eval-degraded")
	_ = mustStarted(t, c)

	registerConnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)
	registerDisconnectedClient(t, c, "ccu-BidCos-RF", hmenum.InterfaceBidCosRF)

	drain := drainSystemStatusEvents(c)
	c.EvaluateCentralState("test", false)
	evts := drain()

	if got := c.StateMachine.State(); got != hmenum.CentralStateDegraded {
		t.Fatalf("state = %s; want DEGRADED", got)
	}
	if len(evts) == 0 {
		t.Fatal("expected SystemStatusChangedEvent for DEGRADED")
	}
	last := evts[len(evts)-1]
	if last.CentralState != hmenum.CentralStateDegraded {
		t.Errorf("event.CentralState = %s; want DEGRADED", last.CentralState)
	}
	if !last.Healthy {
		t.Error("event.Healthy = false; want true with at least one connected client")
	}
	if len(last.DegradedInterfaces) == 0 {
		t.Error("DegradedInterfaces must be non-empty in DEGRADED state")
	}
}

// TestEvaluateCentralState_Failed verifies FAILED is set and event is emitted
// when no clients are CONNECTED. The state machine requires passing through
// DEGRADED before FAILED (RUNNING → FAILED is not a valid direct transition).
func TestEvaluateCentralState_Failed(t *testing.T) {
	c := mustNew(t, "eval-failed")
	_ = mustStarted(t, c)

	// Place machine in DEGRADED so the FAILED transition is valid.
	_ = c.StateMachine.ForceTransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)

	registerDisconnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)

	drain := drainSystemStatusEvents(c)
	c.EvaluateCentralState("test", false)
	evts := drain()

	if got := c.StateMachine.State(); got != hmenum.CentralStateFailed {
		t.Fatalf("state = %s; want FAILED", got)
	}
	if len(evts) == 0 {
		t.Fatal("expected SystemStatusChangedEvent for FAILED")
	}
	if evts[len(evts)-1].Healthy {
		t.Error("event.Healthy = true; want false when no clients connected")
	}
}

// TestEvaluateCentralState_SameStateNoEvent verifies no event is emitted when
// fromStart=false and the connectivity state is unchanged.
func TestEvaluateCentralState_SameStateNoEvent(t *testing.T) {
	c := mustNew(t, "eval-no-change")
	_ = mustStarted(t, c)

	registerConnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)
	// First call brings state to RUNNING.
	c.EvaluateCentralState("init", false)

	drain := drainSystemStatusEvents(c)
	// Second call: same connectivity, same state — must be a no-op.
	c.EvaluateCentralState("noop", false)
	evts := drain()

	if len(evts) != 0 {
		t.Errorf("expected 0 events for unchanged state; got %d", len(evts))
	}
}

// TestEvaluateCentralState_FromStartAlwaysEmits verifies that fromStart=true
// always emits an event regardless of whether the state changed.
func TestEvaluateCentralState_FromStartAlwaysEmits(t *testing.T) {
	c := mustNew(t, "eval-from-start")
	_ = mustStarted(t, c)

	registerConnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)
	// Bring state to RUNNING first.
	c.EvaluateCentralState("init", false)

	drain := drainSystemStatusEvents(c)
	// fromStart=true must fire even though nothing changed.
	c.EvaluateCentralState("start", true)
	evts := drain()

	if len(evts) == 0 {
		t.Fatal("fromStart=true must always emit a SystemStatusChangedEvent")
	}
}

// TestEvaluateCentralState_InRecoveryBlocksRunning verifies RUNNING is
// suppressed while at least one interface is in active recovery.
func TestEvaluateCentralState_InRecoveryBlocksRunning(t *testing.T) {
	c := mustNew(t, "eval-in-recovery")
	_ = mustStarted(t, c)

	// Place machine in DEGRADED so RUNNING would otherwise be the target.
	_ = c.StateMachine.ForceTransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)

	// Simulate active recovery without a real pipeline.
	c.Recovery.SetActiveForTest("ccu-HmIP-RF")

	registerConnectedClient(t, c, "ccu-HmIP-RF", hmenum.InterfaceHmIPRF)

	stateBefore := c.StateMachine.State()
	c.EvaluateCentralState("test", false)

	if got := c.StateMachine.State(); got != stateBefore {
		t.Errorf("state changed to %s; RUNNING must be suppressed during active recovery", got)
	}
}

// TestEvaluateCentralState_EventMatchesStateMachineOnRejectedTransition pins
// the payload of the very first system-status event a central emits.
//
// Start() leaves the machine in RUNNING while the south-bound bring-up has not
// registered a single InterfaceClient yet, so the computed target is FAILED —
// which RUNNING has no edge to. The transition is rejected and the machine
// stays RUNNING; publishing the computed state anyway told every north-bound
// consumer the central had failed while the machine, /health and the SPA badge
// all said RUNNING.
func TestEvaluateCentralState_EventMatchesStateMachineOnRejectedTransition(t *testing.T) {
	c := mustNew(t, "eval-rejected-transition")

	drain := drainSystemStatusEvents(c)
	_ = mustStarted(t, c)
	evts := drain()

	if len(evts) == 0 {
		t.Fatal("Start must emit an initial SystemStatusChangedEvent")
	}
	if got := c.StateMachine.State(); got != hmenum.CentralStateRunning {
		t.Fatalf("state machine = %s; want RUNNING (RUNNING -> FAILED is not a valid edge)", got)
	}
	for i, e := range evts {
		if e.CentralState != hmenum.CentralStateRunning {
			t.Errorf("event[%d].CentralState = %s; want RUNNING to match the state machine", i, e.CentralState)
		}
	}
}

// TestEvaluateCentralState_NilGuard verifies EvaluateCentralState is a no-op
// when Clients or Health are nil.
func TestEvaluateCentralState_NilGuard(t *testing.T) {
	c := &Unit{}
	// Must not panic.
	c.EvaluateCentralState("nil-guard", false)
}
