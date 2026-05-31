// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cluster_test

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
)

// TestDataVersionTracker_StartNonZero verifies that a freshly-initialised
// DataVersionTracker returns a non-zero value (Matter §10.6.5 reserves 0
// as "absent or invalid") and that two fresh trackers carry *distinct*
// initial values — mirroring matter.js's random per-cluster init that
// Apple Home's MTRDevice cache treats as a legitimate version signal.
func TestDataVersionTracker_StartNonZero(t *testing.T) {
	t.Parallel()

	var dv cluster.DataVersionTracker
	got := dv.Current()
	if got == 0 {
		t.Errorf("DataVersionTracker.Current: initial = 0, want non-zero (Matter §10.6.5)")
	}
}

// TestDataVersionTracker_DistinctInitialAcrossInstances rolls 100 fresh
// trackers and verifies the initial DataVersions are not all identical
// (would indicate the random init regressed to a constant). Probability
// of false positive < 2^-32 even with a uniform RNG.
func TestDataVersionTracker_DistinctInitialAcrossInstances(t *testing.T) {
	t.Parallel()

	seen := make(map[uint32]struct{}, 100)
	for i := 0; i < 100; i++ {
		var dv cluster.DataVersionTracker
		seen[dv.Current()] = struct{}{}
	}
	if len(seen) < 90 {
		t.Errorf("DataVersionTracker.Current: only %d distinct values in 100 fresh trackers — random init regressed?", len(seen))
	}
}

// TestDataVersionTracker_BumpIsMonotonic verifies three sequential bumps
// return a strictly-increasing sequence and Current() tracks the last
// bump. Exact values are not asserted because the initial value is
// random.
func TestDataVersionTracker_BumpIsMonotonic(t *testing.T) {
	t.Parallel()

	var dv cluster.DataVersionTracker
	start := dv.Current()

	v1 := dv.Bump()
	if v1 != start+1 {
		t.Errorf("first Bump = %d, want %d (start+1)", v1, start+1)
	}
	v2 := dv.Bump()
	if v2 != start+2 {
		t.Errorf("second Bump = %d, want %d", v2, start+2)
	}
	v3 := dv.Bump()
	if v3 != start+3 {
		t.Errorf("third Bump = %d, want %d", v3, start+3)
	}
	if got := dv.Current(); got != start+3 {
		t.Errorf("Current after 3 bumps = %d, want %d", got, start+3)
	}
}

// TestDataVersionTracker_ConcurrentBumps verifies that 1000 concurrent
// goroutines each calling Bump() once advance Current() by exactly 1000
// from its initial value. This is the primary concurrency-safety test
// for the atomic implementation.
func TestDataVersionTracker_ConcurrentBumps(t *testing.T) {
	t.Parallel()

	const n = 1000
	var dv cluster.DataVersionTracker
	start := dv.Current()
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			dv.Bump()
		}()
	}
	wg.Wait()

	got := dv.Current()
	want := start + n
	if got != want {
		t.Errorf("Current after %d concurrent bumps = %d, want %d (delta %d)", n, got, want, got-start)
	}
}
