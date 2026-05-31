// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import "testing"

// TestScoreAllOK verifies that a tracker whose every component is
// HEALTHY reports a ScoreInt of 100 and OverallStatus HEALTHY.
func TestScoreAllOK(t *testing.T) {
	h := NewTracker(WithStaleAfter(0)) // disable stale decay for determinism
	h.Record("a", Sample{Healthy: true})
	h.Record("b", Sample{Healthy: true})
	h.Record("c", Sample{Healthy: true})

	if got := h.ScoreInt(); got != 100 {
		t.Errorf("ScoreInt()=%d, want 100", got)
	}
	if got := h.OverallStatus(); got != StatusHealthy {
		t.Errorf("OverallStatus()=%s, want %s", got, StatusHealthy)
	}
}

// TestScoreOneFailed verifies that a single UNHEALTHY component
// drives ScoreInt below 100.
func TestScoreOneFailed(t *testing.T) {
	h := NewTracker(WithStaleAfter(0))
	h.Record("a", Sample{Healthy: true})
	h.Record("b", Sample{Healthy: true})
	// Two consecutive unhealthy samples escalate b to UNHEALTHY.
	h.Record("b", Sample{Healthy: false})
	h.Record("b", Sample{Healthy: false})

	score := h.ScoreInt()
	if score >= 100 {
		t.Errorf("ScoreInt()=%d, want < 100 with one failed component", score)
	}
	// a=1.0, b=0.0 → raw=0.5 → int=50
	if score != 50 {
		t.Errorf("ScoreInt()=%d, want 50 (1 healthy + 1 unhealthy)", score)
	}
}

// TestScoreAllFailed verifies that when every component is UNHEALTHY
// ScoreInt returns 0.
func TestScoreAllFailed(t *testing.T) {
	h := NewTracker(WithStaleAfter(0))
	// Drive each component to UNHEALTHY via two consecutive unhealthy samples.
	for _, name := range []string{"a", "b", "c"} {
		h.Record(name, Sample{Healthy: true})
		h.Record(name, Sample{Healthy: false})
		h.Record(name, Sample{Healthy: false})
	}

	if got := h.ScoreInt(); got != 0 {
		t.Errorf("ScoreInt()=%d, want 0 when all components UNHEALTHY", got)
	}
}

// TestOverallStatusReportsWorstCase verifies that OverallStatus returns
// the most severe status seen across the component set.
func TestOverallStatusReportsWorstCase(t *testing.T) {
	h := NewTracker(WithStaleAfter(0))

	// Single healthy component.
	h.Record("a", Sample{Healthy: true})
	if got := h.OverallStatus(); got != StatusHealthy {
		t.Errorf("OverallStatus()=%s, want %s", got, StatusHealthy)
	}

	// Add a degraded component (first failure after healthy → DEGRADED).
	h.Record("b", Sample{Healthy: true})
	h.Record("b", Sample{Healthy: false})
	if got := h.OverallStatus(); got != StatusDegraded {
		t.Errorf("OverallStatus()=%s, want %s after degraded component", got, StatusDegraded)
	}

	// Escalate b to UNHEALTHY.
	h.Record("b", Sample{Healthy: false})
	if got := h.OverallStatus(); got != StatusUnhealthy {
		t.Errorf("OverallStatus()=%s, want %s after unhealthy component", got, StatusUnhealthy)
	}
}

// TestScoreIntEmptyTracker verifies that an empty tracker returns 0.
func TestScoreIntEmptyTracker(t *testing.T) {
	h := NewTracker()
	if got := h.ScoreInt(); got != 0 {
		t.Errorf("ScoreInt()=%d on empty tracker, want 0", got)
	}
}

// TestScoreIntDegradedHalfScore verifies that a single DEGRADED
// component (with no other components) yields ScoreInt == 50.
func TestScoreIntDegradedHalfScore(t *testing.T) {
	h := NewTracker(WithStaleAfter(0))
	// healthy → first unhealthy → DEGRADED (not yet UNHEALTHY)
	h.Record("x", Sample{Healthy: true})
	h.Record("x", Sample{Healthy: false})
	c, _ := h.Get("x")
	if c.Status != StatusDegraded {
		t.Fatalf("pre-condition failed: status=%s want degraded", c.Status)
	}

	if got := h.ScoreInt(); got != 50 {
		t.Errorf("ScoreInt()=%d, want 50 for single DEGRADED component", got)
	}
}
