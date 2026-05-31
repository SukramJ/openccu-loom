// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"testing"
)

// TestSoundPlayerDataVersionTrackerInitialNonZero verifies that a freshly
// constructed SoundPlayer's DataVersion is non-zero (random seed per Matter
// §10.6.5 which reserves DataVersion=0 as "absent or invalid").
func TestSoundPlayerDataVersionTrackerInitialNonZero(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	if v := sp.Current(); v == 0 {
		t.Fatalf("DataVersionTracker.Current() = 0, want non-zero random initial value")
	}
}

// TestSoundPlayerDataVersionTrackerBump verifies that Bump() increments
// the monotonic counter.
func TestSoundPlayerDataVersionTrackerBump(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	before := sp.Current()
	sp.Bump()
	after := sp.Current()
	if after <= before {
		t.Fatalf("DataVersionTracker.Current() did not increment: before=%d after=%d", before, after)
	}
}

// TestSoundPlayerDataVersionTrackerMonotonicallyRises verifies that
// consecutive Bump() calls each produce a strictly larger value.
func TestSoundPlayerDataVersionTrackerMonotonicallyRises(t *testing.T) {
	t.Parallel()
	sp := NewSoundPlayer(SoundPlayerConfig{})
	prev := sp.Current()
	for i := range 5 {
		sp.Bump()
		next := sp.Current()
		if next <= prev {
			t.Fatalf("iteration %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
		prev = next
	}
}
