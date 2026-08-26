// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCommandPriorityEnumOrdering verifies that the CommandPriority
// enum values satisfy CRITICAL < HIGH < LOW, matching the ordering
// contract that the CommandPriority enum expresses via its integer
// values (0, 1, 2 respectively).
//
// The invariant is also tested in the unit layer (bitmask_test.go) but
// repeating it here as an integration-tagged test catches accidental
// regressions when the integration suite runs independently.
func TestCommandPriorityEnumOrdering(t *testing.T) {
	if hmenum.CommandPriorityCritical != 0 {
		t.Fatalf("CommandPriorityCritical must be 0 (zero-value), got %d",
			hmenum.CommandPriorityCritical)
	}
	if hmenum.CommandPriorityCritical >= hmenum.CommandPriorityHigh {
		t.Fatalf("CRITICAL (%d) must be less than HIGH (%d)",
			hmenum.CommandPriorityCritical, hmenum.CommandPriorityHigh)
	}
	if hmenum.CommandPriorityHigh >= hmenum.CommandPriorityLow {
		t.Fatalf("HIGH (%d) must be less than LOW (%d)",
			hmenum.CommandPriorityHigh, hmenum.CommandPriorityLow)
	}

	// Verify sort order produces CRITICAL, HIGH, LOW.
	priorities := []hmenum.CommandPriority{
		hmenum.CommandPriorityLow,
		hmenum.CommandPriorityCritical,
		hmenum.CommandPriorityHigh,
	}
	// Simple insertion sort — stdlib sort requires a less-function or
	// slice of comparable; CommandPriority is int so direct comparison works.
	for i := 1; i < len(priorities); i++ {
		for j := i; j > 0 && priorities[j] < priorities[j-1]; j-- {
			priorities[j], priorities[j-1] = priorities[j-1], priorities[j]
		}
	}

	want := []hmenum.CommandPriority{
		hmenum.CommandPriorityCritical,
		hmenum.CommandPriorityHigh,
		hmenum.CommandPriorityLow,
	}
	for i, got := range priorities {
		if got != want[i] {
			t.Errorf("sorted[%d] = %d, want %d", i, got, want[i])
		}
	}
}
