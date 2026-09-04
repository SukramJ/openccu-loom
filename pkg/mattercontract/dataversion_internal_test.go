// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercontract

// Whitebox test for DataVersionTracker that exercises the wrap-around
// branch in Bump (next==0 path). This requires direct access to the
// unexported v atomic.Uint32 field which is only possible from within
// the same package.

import (
	"math"
	"testing"
)

// TestDataVersionTracker_Bump_WrapAround verifies that Bump skips zero
// when the counter wraps from 0xFFFFFFFF. This exercises the
// `if next == 0 { next = d.v.Add(1) }` branch in Bump.
func TestDataVersionTracker_Bump_WrapAround(t *testing.T) {
	t.Parallel()

	var dv DataVersionTracker
	// Prime Current() so the once-init is done.
	dv.Current()
	// Set v to 0xFFFFFFFF so the next Add(1) wraps to 0, triggering the skip.
	dv.v.Store(math.MaxUint32)

	next := dv.Bump()
	if next == 0 {
		t.Fatal("Bump must skip zero on wrap-around (Matter §10.6.5)")
	}
	// After wrapping: Add(1) from 0xFFFFFFFF gives 0 (skipped),
	// then Add(1) again gives 1. So next must be 1.
	if next != 1 {
		t.Fatalf("Bump after wrap-around = %d, want 1 (0 skipped, then +1)", next)
	}
}
