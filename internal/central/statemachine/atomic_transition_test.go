// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestTransitionToSetsFailureInterfaceAtomic verifies that passing
// WithFailureInterface to TransitionTo stores the interface ID inside the
// same mutex section as the state change — a subsequent State() + FailureInterfaceID()
// read must be consistent without any additional synchronisation from the caller.
func TestTransitionToSetsFailureInterfaceAtomic(t *testing.T) {
	t.Parallel()
	m := NewCentral("test", nil)

	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("boot transition to %s: %v", s, err)
		}
	}

	if err := m.TransitionTo(
		hmenum.CentralStateDegraded,
		hmenum.FailureReasonNetwork,
		WithFailureInterface("HmIP-RF"),
	); err != nil {
		t.Fatalf("transition to Degraded: %v", err)
	}

	if got := m.State(); got != hmenum.CentralStateDegraded {
		t.Fatalf("State() = %s, want DEGRADED", got)
	}
	if got := m.FailureInterfaceID(); got != "HmIP-RF" {
		t.Fatalf("FailureInterfaceID() = %q, want HmIP-RF", got)
	}
}

// TestTransitionToSetsDegradedInterfacesAtomic verifies that passing
// WithDegradedInterfaces populates the map atomically: after TransitionTo
// returns, DegradedInterfaces must contain the supplied entries without any
// preceding MarkInterfaceDegraded call.
func TestTransitionToSetsDegradedInterfacesAtomic(t *testing.T) {
	t.Parallel()
	m := NewCentral("test", nil)

	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("boot transition to %s: %v", s, err)
		}
	}

	degraded := map[string]hmenum.FailureReason{
		"HmIP-RF":   hmenum.FailureReasonNetwork,
		"BidCos-RF": hmenum.FailureReasonTimeout,
	}
	if err := m.TransitionTo(
		hmenum.CentralStateDegraded,
		hmenum.FailureReasonNetwork,
		WithDegradedInterfaces(degraded),
	); err != nil {
		t.Fatalf("transition to Degraded: %v", err)
	}

	got := m.DegradedInterfaces()
	if len(got) != 2 {
		t.Fatalf("DegradedInterfaces() len = %d, want 2; map = %v", len(got), got)
	}
	if got["HmIP-RF"] != hmenum.FailureReasonNetwork {
		t.Fatalf("HmIP-RF reason = %v, want Network", got["HmIP-RF"])
	}
	if got["BidCos-RF"] != hmenum.FailureReasonTimeout {
		t.Fatalf("BidCos-RF reason = %v, want Timeout", got["BidCos-RF"])
	}
}

// TestTransitionToRunningClearsDegradedMapAtomically ensures that a single
// TransitionTo(Running) atomically clears both the failureInterface and the
// degraded-interfaces map that were set via WithFailureInterface /
// WithDegradedInterfaces on the preceding DEGRADED transition.
func TestTransitionToRunningClearsDegradedMapAtomically(t *testing.T) {
	t.Parallel()
	m := NewCentral("test", nil)

	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("boot transition to %s: %v", s, err)
		}
	}

	if err := m.TransitionTo(
		hmenum.CentralStateDegraded,
		hmenum.FailureReasonNetwork,
		WithFailureInterface("HmIP-RF"),
		WithDegradedInterfaces(map[string]hmenum.FailureReason{"HmIP-RF": hmenum.FailureReasonNetwork}),
	); err != nil {
		t.Fatalf("transition to Degraded: %v", err)
	}

	// Recover through Recovering → Running.
	if err := m.TransitionTo(hmenum.CentralStateRecovering, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Recovering: %v", err)
	}
	if err := m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Running: %v", err)
	}

	if got := m.DegradedInterfaces(); len(got) != 0 {
		t.Fatalf("DegradedInterfaces after Running = %v, want empty", got)
	}
	if got := m.FailureInterfaceID(); got != "" {
		t.Fatalf("FailureInterfaceID after Running = %q, want empty", got)
	}
}

// TestTransitionToWithBothOptionsAtomicConsistency verifies that a caller
// providing both WithFailureInterface and WithDegradedInterfaces sees a
// consistent snapshot: State, FailureInterfaceID and DegradedInterfaces all
// reflect the single TransitionTo call without any intermediate inconsistency.
func TestTransitionToWithBothOptionsAtomicConsistency(t *testing.T) {
	t.Parallel()
	m := NewCentral("test", nil)

	// Boot to DEGRADED (RUNNING → DEGRADED is a valid transition).
	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
		hmenum.CentralStateDegraded,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("boot transition to %s: %v", s, err)
		}
	}

	// DEGRADED → FAILED is a valid transition.
	const failIface = "HmIP-Wired"
	if err := m.TransitionTo(
		hmenum.CentralStateFailed,
		hmenum.FailureReasonNetwork,
		WithFailureInterface(failIface),
		WithDegradedInterfaces(map[string]hmenum.FailureReason{
			failIface: hmenum.FailureReasonNetwork,
			"CUxD":    hmenum.FailureReasonInternal,
		}),
	); err != nil {
		t.Fatalf("transition to Failed: %v", err)
	}

	if got := m.State(); got != hmenum.CentralStateFailed {
		t.Fatalf("State() = %s, want FAILED", got)
	}
	if got := m.FailureInterfaceID(); got != failIface {
		t.Fatalf("FailureInterfaceID() = %q, want %q", got, failIface)
	}
	deg := m.DegradedInterfaces()
	if len(deg) != 2 {
		t.Fatalf("DegradedInterfaces() = %v, want 2 entries", deg)
	}
}
