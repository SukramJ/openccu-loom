// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package optimistic

// OptimisticBurstWindow constant tests.
//
// Verifies that DefaultBurstWindow is exported with the expected value
// and that it is distinct from DefaultTimeout so callers can configure
// each independently.

import (
	"testing"
	"time"
)

// TestDefaultBurstWindowValue verifies the constant has the documented
// 500ms value, matching the typical CCU-callback round-trip budget
// Used.
func TestDefaultBurstWindowValue(t *testing.T) {
	t.Parallel()
	const want = 500 * time.Millisecond
	if DefaultBurstWindow != want {
		t.Fatalf("DefaultBurstWindow = %v, want %v", DefaultBurstWindow, want)
	}
}

// TestDefaultTimeoutIsDistinctFromBurstWindow ensures the two constants
// serve different purposes and are not accidentally equal.
func TestDefaultTimeoutIsDistinctFromBurstWindow(t *testing.T) {
	t.Parallel()
	if DefaultTimeout == DefaultBurstWindow {
		t.Fatalf("DefaultTimeout and DefaultBurstWindow must not be equal (%v)", DefaultTimeout)
	}
}

// TestDefaultBurstWindowIsPositive verifies the constant is usable as
// a timer duration (> 0).
func TestDefaultBurstWindowIsPositive(t *testing.T) {
	t.Parallel()
	if DefaultBurstWindow <= 0 {
		t.Fatalf("DefaultBurstWindow must be positive, got %v", DefaultBurstWindow)
	}
}

// TestDefaultBurstWindowSmallerThanDefaultTimeout verifies that the
// burst window is shorter than the rollback timeout — a burst window
// larger than the rollback timeout would subsume it, making the
// timeout guard unreachable.
func TestDefaultBurstWindowSmallerThanDefaultTimeout(t *testing.T) {
	t.Parallel()
	if DefaultBurstWindow >= DefaultTimeout {
		t.Fatalf("DefaultBurstWindow (%v) must be < DefaultTimeout (%v)", DefaultBurstWindow, DefaultTimeout)
	}
}
