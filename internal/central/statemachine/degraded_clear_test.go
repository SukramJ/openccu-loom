// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDegradedClearedOnRunningTransition verifies that transitioning to
// Running from Degraded clears the degraded-interface set so consumers
// see a consistent state after recovery.
func TestDegradedClearedOnRunningTransition(t *testing.T) {
	t.Parallel()
	m := NewCentral("main", nil)

	// Boot to Running.
	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}

	// Mark an interface as degraded and move to DEGRADED.
	m.MarkInterfaceDegraded("HmIP-RF", hmenum.FailureReasonNetwork)
	if err := m.TransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNetwork); err != nil {
		t.Fatalf("transition to Degraded: %v", err)
	}
	if len(m.DegradedInterfaces()) == 0 {
		t.Fatal("degraded map empty immediately after MarkInterfaceDegraded; want non-empty")
	}

	// Recover back to Running — degraded set must be cleared.
	if err := m.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Running: %v", err)
	}
	if got := m.DegradedInterfaces(); len(got) != 0 {
		t.Fatalf("DegradedInterfaces after Running transition = %v, want empty", got)
	}
	if m.FailureInterfaceID() != "" {
		t.Fatalf("FailureInterfaceID after Running transition = %q, want empty", m.FailureInterfaceID())
	}
}

// TestDegradedNotClearedOnNonRunningTransition verifies that transitioning
// to Recovering (not Running) does NOT clear the degraded-interface set —
// the set is only authoritative-cleared by a full Running transition.
func TestDegradedNotClearedOnNonRunningTransition(t *testing.T) {
	t.Parallel()
	m := NewCentral("main", nil)

	for _, s := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
		hmenum.CentralStateDegraded,
	} {
		if err := m.TransitionTo(s, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
	m.MarkInterfaceDegraded("BidCos-RF", hmenum.FailureReasonNetwork)

	// Transition to Recovering — degraded set should still be intact.
	if err := m.TransitionTo(hmenum.CentralStateRecovering, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Recovering: %v", err)
	}
	if len(m.DegradedInterfaces()) == 0 {
		t.Fatal("degraded map cleared on Recovering transition; expected it to persist until Running")
	}
}
