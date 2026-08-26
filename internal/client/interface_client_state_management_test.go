// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for InterfaceClient state management:
// StateMachine FailureMessage FailureReason,
// Enabled/Disable/Enable RPCServerType/RPCServerTypeForInterface,
// TransitionTo CanTransitionTo, OnSystemStatusRestored.

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newStateManagementIC(t *testing.T) *InterfaceClient {
	t.Helper()
	c, err := New(Config{
		CentralName: "central-a",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ---------------------------------------------------------------------------
// StateMachine() exposes a non-nil ClientStateMachine
// ---------------------------------------------------------------------------

func TestStateMachineNotNil(t *testing.T) {
	c := newStateManagementIC(t)
	if c.StateMachine() == nil {
		t.Fatal("StateMachine() returned nil; expected a live ClientStateMachine")
	}
}

func TestStateMachineStartsInCreated(t *testing.T) {
	c := newStateManagementIC(t)
	if got := c.StateMachine().State(); got != hmenum.ClientStateCreated {
		t.Fatalf("StateMachine().State() = %s; want %s", got, hmenum.ClientStateCreated)
	}
}

// ---------------------------------------------------------------------------
// FailureMessage / FailureReason delegates
// ---------------------------------------------------------------------------

func TestFailureMessageAndReasonDelegateToStateMachine(t *testing.T) {
	c := newStateManagementIC(t)

	// No failure yet — both should be empty / none.
	if got := c.FailureMessage(); got != "" {
		t.Errorf("FailureMessage() = %q; want empty before any failure", got)
	}
	if got := c.FailureReason(); got != hmenum.FailureReasonNone {
		t.Errorf("FailureReason() = %s; want %s", got, hmenum.FailureReasonNone)
	}

	// Force the state machine into FAILED via TransitionTo.
	wantMsg := "network timeout"
	wantReason := hmenum.FailureReasonNetwork
	if err := c.StateMachine().TransitionTo(
		hmenum.ClientStateFailed, wantMsg, true, wantReason,
	); err != nil {
		t.Fatalf("TransitionTo(FAILED) unexpected error: %v", err)
	}

	if got := c.FailureMessage(); got != wantMsg {
		t.Errorf("FailureMessage() = %q; want %q", got, wantMsg)
	}
	if got := c.FailureReason(); got != wantReason {
		t.Errorf("FailureReason() = %s; want %s", got, wantReason)
	}
}

// ---------------------------------------------------------------------------
// Enabled / Disable / Enable
// ---------------------------------------------------------------------------

func TestEnabledDefaultsTrue(t *testing.T) {
	c := newStateManagementIC(t)
	if !c.Enabled() {
		t.Error("Enabled() = false for fresh client; want true")
	}
}

func TestDisablePreventsCall(t *testing.T) {
	c := newStateManagementIC(t)
	c.Disable()

	if c.Enabled() {
		t.Error("Enabled() = true after Disable(); want false")
	}

	_, err := c.Call(context.Background(), "ping", nil, hmenum.CommandPriorityHigh, "")
	if err == nil {
		t.Error("Call() succeeded on disabled client; want error")
	}
}

func TestEnableRestoresCall(t *testing.T) {
	c := newStateManagementIC(t)
	c.Disable()
	c.Enable()

	if !c.Enabled() {
		t.Error("Enabled() = false after Enable(); want true")
	}

	_, err := c.Call(context.Background(), "ping", nil, hmenum.CommandPriorityHigh, "")
	if err != nil {
		t.Errorf("Call() after Enable() returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RPCServerType / RPCServerTypeForInterface
// ---------------------------------------------------------------------------

func TestRPCServerTypeHmIPRF(t *testing.T) {
	c, _ := New(Config{
		CentralName: "c",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if got := c.RPCServerType(); got != hmenum.RPCServerTypeXMLRPC {
		t.Errorf("RPCServerType(HmIP-RF) = %s; want %s", got, hmenum.RPCServerTypeXMLRPC)
	}
}

func TestRPCServerTypeCUxD(t *testing.T) {
	c, _ := New(Config{
		CentralName: "c",
		Interface:   hmenum.InterfaceCUxD,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if got := c.RPCServerType(); got != hmenum.RPCServerTypeBINRPC {
		t.Errorf("RPCServerType(CUxD) = %s; want %s", got, hmenum.RPCServerTypeBINRPC)
	}
}

func TestRPCServerTypeForInterfaceTable(t *testing.T) {
	cases := []struct {
		iface hmenum.Interface
		want  hmenum.RPCServerType
	}{
		{hmenum.InterfaceHmIPRF, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceBidCosRF, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceBidCosWired, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceVirtualDevices, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceCUxD, hmenum.RPCServerTypeBINRPC},
	}
	for _, tc := range cases {
		if got := RPCServerTypeForInterface(tc.iface); got != tc.want {
			t.Errorf("RPCServerTypeForInterface(%s) = %s; want %s", tc.iface, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TransitionTo / CanTransitionTo delegates
// ---------------------------------------------------------------------------

func TestTransitionToValidatesTable(t *testing.T) {
	c := newStateManagementIC(t)

	// CREATED → INITIALIZING is valid.
	if err := c.TransitionTo(hmenum.ClientStateInitializing, "startup", false, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("TransitionTo(INITIALIZING) unexpected error: %v", err)
	}

	// Should have propagated to the inline state.
	if got := c.ClientState(); got != hmenum.ClientStateInitializing {
		t.Errorf("ClientState() = %s after TransitionTo(INITIALIZING); want INITIALIZING", got)
	}

	// INITIALIZING → CONNECTED is invalid (must go via INITIALIZED).
	err := c.TransitionTo(hmenum.ClientStateConnected, "skip", false, hmenum.FailureReasonNone)
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("TransitionTo(CONNECTED from INITIALIZING) = %v; want ErrInvalidStateTransition", err)
	}
}

func TestCanTransitionToMirrorsStateMachine(t *testing.T) {
	c := newStateManagementIC(t)
	// CREATED can go to INITIALIZING.
	if !c.CanTransitionTo(hmenum.ClientStateInitializing) {
		t.Error("CanTransitionTo(INITIALIZING) from CREATED = false; want true")
	}
	// CREATED cannot go directly to CONNECTED.
	if c.CanTransitionTo(hmenum.ClientStateConnected) {
		t.Error("CanTransitionTo(CONNECTED) from CREATED = true; want false")
	}
}

// ---------------------------------------------------------------------------
// — OnSystemStatusRestored clears the PingPong cache
// ---------------------------------------------------------------------------

// TestInterfaceClientClearsPingPongOnRestore verifies that
// OnSystemStatusRestored clears the PingPong tracker.
func TestInterfaceClientClearsPingPongOnRestore(t *testing.T) {
	t.Parallel()
	c := newStateManagementIC(t)

	// Inject a ping so the tracker has state to clear.
	c.RecordPing("ping-001")

	// Simulate a connection-restored event by calling OnSystemStatusRestored.
	c.OnSystemStatusRestored()

	// After clearing, a sweep should find no pending entries.
	mismatches := c.SweepPingPong()
	for _, m := range mismatches {
		if m.ID == "ping-001" {
			t.Errorf("SweepPingPong() found ping-001 after OnSystemStatusRestored; want cache cleared")
		}
	}
}

// TestInterfaceClientClearsPingPongOnRestoreIdempotent verifies that
// calling OnSystemStatusRestored on an already-empty tracker does not panic
// or return an error.
func TestInterfaceClientClearsPingPongOnRestoreIdempotent(t *testing.T) {
	t.Parallel()
	c := newStateManagementIC(t)
	// Must not panic on an empty tracker.
	c.OnSystemStatusRestored()
	c.OnSystemStatusRestored()
}
