// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSwitchTurnOnAppliesOptimisticBeforeWireCall verifies that calling
// TurnOn on a Switch applies an optimistic update so the UI reads the
// target state immediately, before the CCU confirms the write. The test
// uses a writer that captures calls without triggering a CCU echo.
func TestSwitchTurnOnAppliesOptimisticBeforeWireCall(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0010:1", "central1", w)

	// Seed the switch with an initial FALSE so optimistic tracking has a
	// confirmed value to work from.
	s.OnState(false)

	// TurnOn — the underlying sendAndObserve applies the optimistic update
	// synchronously before the wire call returns.
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("TurnOn error: %v", err)
	}

	// After TurnOn the Switch must report on=true even before a CCU
	// confirmation arrives.
	on, _ := s.IsOn()
	if !on {
		t.Error("expected Switch to report on=true after TurnOn (optimistic update)")
	}
}

// TestSwitchTurnOffAppliesOptimisticBeforeWireCall mirrors the TurnOn
// test for the TurnOff path.
func TestSwitchTurnOffAppliesOptimisticBeforeWireCall(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU0010:2", "central1", w)

	// Seed with an initial TRUE.
	s.OnState(true)

	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("TurnOff error: %v", err)
	}

	on, _ := s.IsOn()
	if on {
		t.Error("expected Switch to report on=false after TurnOff (optimistic update)")
	}
}
