// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package contract connectivity_state_contract_test.go implements
// : contract that ClientStateChangedEvent carries enough
// information to update a Connectivity tracker and that the state
// mapping (Connected→reachable, Disconnected/Failed/Stopped/Reconnecting
// →unreachable) is invariant.
//
// The actual wiring that subscribes to ClientStateChangedEvent and calls
// Connectivity.OnState lives in internal/central/adapter/device_availability.go
// (outside the file boundary for this sprint). This test locks the
// contract: the Connectivity model and the event type share a compatible
// InterfaceID surface so the adapter can bridge them without lossy
// transformation.
package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// stateToReachable maps ClientState to the connectivity reachability
// flag expected by the Connectivity model. This mirrors the switch in
// internal/central/adapter/device_availability.go and
// internal/central/adapter/health_wiring.go.
func stateToReachable(s hmenum.ClientState) (reachable, relevant bool) {
	switch s {
	case hmenum.ClientStateConnected:
		return true, true
	case hmenum.ClientStateDisconnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateFailed,
		hmenum.ClientStateStopped:
		return false, true
	default:
		return false, false // transient states: Created, Initializing, Initialized, Stopping
	}
}

// TestClientStateChangedEventToConnectivityMapping verifies that every
// non-transient ClientState produces the correct Reachable flag when
// translated to a Connectivity.OnState call. This is the core contract
// the adapter layer must honour.
func TestClientStateChangedEventToConnectivityMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state     hmenum.ClientState
		wantReach bool
		relevant  bool
	}{
		{hmenum.ClientStateConnected, true, true},
		{hmenum.ClientStateDisconnected, false, true},
		{hmenum.ClientStateReconnecting, false, true},
		{hmenum.ClientStateFailed, false, true},
		{hmenum.ClientStateStopped, false, true},
		// Transient states: not expected to trigger connectivity updates.
		{hmenum.ClientStateCreated, false, false},
		{hmenum.ClientStateInitializing, false, false},
		{hmenum.ClientStateInitialized, false, false},
		{hmenum.ClientStateStopping, false, false},
	}

	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			t.Parallel()
			// Build a minimal event as the statemachine would publish.
			e := hmevent.ClientStateChangedEvent{
				CentralName: "ccu-1",
				InterfaceID: "HmIP-RF",
				Interface:   hmenum.InterfaceHmIPRF,
				To:          tc.state,
			}

			reachable, relevant := stateToReachable(e.To)
			if relevant != tc.relevant {
				t.Fatalf("stateToReachable(%s) relevant=%v, want %v", tc.state, relevant, tc.relevant)
			}
			if !relevant {
				return // transient — no connectivity update expected
			}
			if reachable != tc.wantReach {
				t.Errorf("stateToReachable(%s)=%v, want %v", tc.state, reachable, tc.wantReach)
			}

			// Apply the state to a Connectivity tracker and assert.
			c := hub.NewConnectivity()
			c.OnStateWithInterface(e.InterfaceID, e.Interface, reachable)

			got, observed := c.Reachable(e.InterfaceID)
			if !observed {
				t.Fatalf("Connectivity must mark interface as observed after OnState")
			}
			if got != tc.wantReach {
				t.Errorf("Connectivity.Reachable(%s)=%v, want %v", tc.state, got, tc.wantReach)
			}
		})
	}
}

// TestClientStateChangedEventInterfaceFieldParity verifies that
// ClientStateChangedEvent.Interface (hmenum.Interface) and
// InterfaceReachability.Interface share the same type so the adapter
// can pass the event field directly to OnStateWithInterface.
// This is a compile-time shape contract enforced at runtime.
func TestClientStateChangedEventInterfaceFieldParity(t *testing.T) {
	t.Parallel()
	e := hmevent.ClientStateChangedEvent{
		Interface: hmenum.InterfaceHmIPRF,
	}
	ir := hub.InterfaceReachability{
		Interface: e.Interface, // must compile: same type
	}
	if ir.Interface != hmenum.InterfaceHmIPRF {
		t.Errorf("type mismatch: InterfaceReachability.Interface=%q after assignment", ir.Interface)
	}
}

// TestConnectivityOnStateWithInterfaceFromEvent demonstrates the full
// bridging pattern the adapter uses: extract InterfaceID and Interface
// from a ClientStateChangedEvent and forward to Connectivity.
func TestConnectivityOnStateWithInterfaceFromEvent(t *testing.T) {
	t.Parallel()
	c := hub.NewConnectivity()
	var gotIR hub.InterfaceReachability
	c.OnUpdate(func(ir hub.InterfaceReachability) { gotIR = ir })

	// Simulate: client connected event arrives.
	e := hmevent.ClientStateChangedEvent{
		CentralName: "ccu-1",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		To:          hmenum.ClientStateConnected,
	}
	reachable, _ := stateToReachable(e.To)
	c.OnStateWithInterface(e.InterfaceID, e.Interface, reachable)

	if gotIR.InterfaceID != "BidCos-RF" {
		t.Errorf("callback InterfaceID=%q, want BidCos-RF", gotIR.InterfaceID)
	}
	if gotIR.Interface != hmenum.InterfaceBidCosRF {
		t.Errorf("callback Interface=%q, want BidCos-RF", gotIR.Interface)
	}
	if !gotIR.Reachable {
		t.Error("callback Reachable must be true for Connected state")
	}
}
