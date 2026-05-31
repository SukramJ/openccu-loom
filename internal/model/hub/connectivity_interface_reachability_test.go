// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for InterfaceReachability field semantics and the Connectivity
// state-machine contract that maps ClientState transitions to reachable/unreachable.
package hub

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── InterfaceReachability.Interface field ───────────────────────────

// TestInterfaceReachabilityInterfaceFieldDefault verifies that a zero-value
// InterfaceReachability has Interface == "" (zero value of hmenum.Interface).
func TestInterfaceReachabilityInterfaceFieldDefault(t *testing.T) {
	t.Parallel()
	var ir InterfaceReachability
	if ir.Interface != "" {
		t.Errorf("zero-value Interface=%q, want empty", ir.Interface)
	}
}

// TestOnStateWithInterfaceSetsEnumField verifies that OnStateWithInterface
// stores the typed interface enum in the callback's InterfaceReachability.
func TestOnStateWithInterfaceSetsEnumField(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	var got InterfaceReachability
	c.OnUpdate(func(ir InterfaceReachability) { got = ir })

	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, true)

	if got.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q, want HmIP-RF", got.InterfaceID)
	}
	if got.Interface != hmenum.InterfaceHmIPRF {
		t.Errorf("Interface=%q, want HmIP-RF", got.Interface)
	}
	if !got.Reachable {
		t.Error("Reachable must be true")
	}
}

// TestOnStateFallbackCast verifies that OnState (which has no explicit enum
// argument) sets Interface to hmenum.Interface(InterfaceID), effectively
// a type-cast of the string ID.
func TestOnStateFallbackCast(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	var got InterfaceReachability
	c.OnUpdate(func(ir InterfaceReachability) { got = ir })

	c.OnState("BidCos-RF", true)

	if got.Interface != hmenum.Interface("BidCos-RF") {
		t.Errorf("OnState fallback Interface=%q, want BidCos-RF", got.Interface)
	}
}

// TestListPopulatesInterfaceField verifies that List() returns entries
// with Interface populated from InterfaceID.
func TestListPopulatesInterfaceField(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	c.OnState("HmIP-RF", true)
	c.OnState("CUxD", false)

	list := c.List()
	if len(list) != 2 {
		t.Fatalf("List() len=%d, want 2", len(list))
	}
	for _, ir := range list {
		if ir.Interface != hmenum.Interface(ir.InterfaceID) {
			t.Errorf("List() Interface=%q != Interface(%q)=%q",
				ir.Interface, ir.InterfaceID, hmenum.Interface(ir.InterfaceID))
		}
	}
}

// TestResolvedInterfacePrefersDeclaredEnum verifies that ResolvedInterface
// returns the explicitly declared Interface when it is non-empty.
func TestResolvedInterfacePrefersDeclaredEnum(t *testing.T) {
	t.Parallel()
	ir := InterfaceReachability{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Reachable:   true,
	}
	if ir.ResolvedInterface() != hmenum.InterfaceHmIPRF {
		t.Errorf("ResolvedInterface()=%q, want HmIP-RF", ir.ResolvedInterface())
	}
}

// TestResolvedInterfaceFallsBackToIDWhenEnumEmpty verifies that
// ResolvedInterface falls back to parsing InterfaceID when Interface is "".
func TestResolvedInterfaceFallsBackToIDWhenEnumEmpty(t *testing.T) {
	t.Parallel()
	ir := InterfaceReachability{
		InterfaceID: "CUxD",
		Interface:   "",
	}
	want := hmenum.Interface("CUxD")
	if got := ir.ResolvedInterface(); got != want {
		t.Errorf("ResolvedInterface() fallback=%q, want %q", got, want)
	}
}

// ─── Contract — ClientStateChanged → Connectivity state ──────────────

// TestConnectivityConnectedTransitionReachable verifies the invariant that
// a ClientState = Connected maps to Connectivity.Reachable == true.
// This is the contract the adapter layer honours: when a client reaches
// ClientStateConnected the interface is marked reachable.
//
// The wiring (ClientStateChangedEvent → Connectivity.OnState) lives in
// internal/central/adapter/ (outside the file boundary). This test
// exercises the Connectivity model layer directly, asserting the state
// machine expected by the adapter.
func TestConnectivityConnectedTransitionReachable(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()

	// Simulate the adapter calling OnState(Connected → true).
	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, true)

	reachable, observed := c.Reachable("HmIP-RF")
	if !observed {
		t.Fatal("interface must be observed after OnState")
	}
	if !reachable {
		t.Error("interface must be reachable after Connected transition")
	}
}

// TestConnectivityDisconnectedTransitionUnreachable verifies the invariant
// that a ClientState = Disconnected/Failed maps to Connectivity.Reachable
// == false.
func TestConnectivityDisconnectedTransitionUnreachable(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()

	// First connect, then disconnect (mirrors real client lifecycle).
	c.OnStateWithInterface("BidCos-RF", hmenum.InterfaceBidCosRF, true)
	c.OnStateWithInterface("BidCos-RF", hmenum.InterfaceBidCosRF, false)

	reachable, observed := c.Reachable("BidCos-RF")
	if !observed {
		t.Fatal("interface must remain observed after disconnect")
	}
	if reachable {
		t.Error("interface must be unreachable after Disconnected/Failed transition")
	}
}

// TestConnectivityCallbackFiredOnTransition verifies that a registered
// OnUpdate callback fires exactly once when the state flips (the
// adapter attaches callbacks to propagate connectivity to north-bound).
func TestConnectivityCallbackFiredOnTransition(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	var events []InterfaceReachability
	c.OnUpdate(func(ir InterfaceReachability) { events = append(events, ir) })

	// Connected
	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, true)
	// Repeat — must NOT fire (no state change).
	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, true)
	// Disconnected — fires.
	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, false)

	if len(events) != 2 {
		t.Fatalf("callback fired %d times, want 2 (connect + disconnect)", len(events))
	}
	if events[0].Interface != hmenum.InterfaceHmIPRF {
		t.Errorf("callback[0].Interface=%q, want HmIP-RF", events[0].Interface)
	}
	if !events[0].Reachable {
		t.Error("callback[0] must be reachable (connected)")
	}
	if events[1].Reachable {
		t.Error("callback[1] must be unreachable (disconnected)")
	}
}

// TestConnectivityAllReachableContract verifies the AllReachable()
// aggregate which the adapter uses for system-health roll-ups.
func TestConnectivityAllReachableContract(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()

	// No observations yet.
	allOK, obs := c.AllReachable()
	if obs {
		t.Error("AllReachable() observed must be false before any state recorded")
	}
	if allOK {
		t.Error("AllReachable() must be false with no observations")
	}

	// One interface connected.
	c.OnStateWithInterface("HmIP-RF", hmenum.InterfaceHmIPRF, true)
	allOK, obs = c.AllReachable()
	if !obs {
		t.Error("AllReachable() observed must be true after first state")
	}
	if !allOK {
		t.Error("AllReachable() must be true when only interface is reachable")
	}

	// Second interface disconnected.
	c.OnStateWithInterface("CUxD", hmenum.InterfaceCUxD, false)
	allOK, _ = c.AllReachable()
	if allOK {
		t.Error("AllReachable() must be false when one interface is unreachable")
	}
}
