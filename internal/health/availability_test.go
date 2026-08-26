// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestIsAvailable covers the four required verdicts.
func TestIsAvailable(t *testing.T) {
	t.Run("empty tracker returns false", func(t *testing.T) {
		tr := health.NewTracker()
		if tr.IsAvailable() {
			t.Error("empty tracker: IsAvailable() = true, want false")
		}
	})

	t.Run("two healthy components returns true", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("b", health.Sample{Healthy: true})
		if !tr.IsAvailable() {
			t.Errorf("two healthy: IsAvailable() = false, want true (Overall=%s)", tr.Overall())
		}
	})

	t.Run("one degraded component returns false", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("b", health.Sample{Healthy: true})
		// One failure after healthy → DEGRADED.
		tr.Record("b", health.Sample{Healthy: false})
		if tr.IsAvailable() {
			t.Errorf("degraded component: IsAvailable() = true, want false (Overall=%s)", tr.Overall())
		}
	})

	t.Run("one unhealthy component returns false", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("b", health.Sample{Healthy: true})
		// Two consecutive failures → UNHEALTHY.
		tr.Record("b", health.Sample{Healthy: false})
		tr.Record("b", health.Sample{Healthy: false})
		if tr.IsAvailable() {
			t.Errorf("unhealthy component: IsAvailable() = true, want false (Overall=%s)", tr.Overall())
		}
	})
}

// TestIsDegraded covers the four required verdicts.
func TestIsDegraded(t *testing.T) {
	t.Run("empty tracker returns false", func(t *testing.T) {
		tr := health.NewTracker()
		if tr.IsDegraded() {
			t.Error("empty tracker: IsDegraded() = true, want false")
		}
	})

	t.Run("all healthy returns false", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		if tr.IsDegraded() {
			t.Errorf("healthy tracker: IsDegraded() = true, want false (Overall=%s)", tr.Overall())
		}
	})

	t.Run("one degraded component returns true", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("a", health.Sample{Healthy: false})
		if !tr.IsDegraded() {
			t.Errorf("degraded component: IsDegraded() = false, want true (Overall=%s)", tr.Overall())
		}
	})

	t.Run("unhealthy component returns false (unhealthy takes precedence)", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		// Two failures → UNHEALTHY, not DEGRADED.
		tr.Record("a", health.Sample{Healthy: false})
		tr.Record("a", health.Sample{Healthy: false})
		if tr.IsDegraded() {
			t.Errorf("unhealthy component: IsDegraded() = true, want false (Overall=%s)", tr.Overall())
		}
	})
}

// TestIsFailed covers the three required verdicts.
func TestIsFailed(t *testing.T) {
	t.Run("empty tracker returns false", func(t *testing.T) {
		tr := health.NewTracker()
		if tr.IsFailed() {
			t.Error("empty tracker: IsFailed() = true, want false")
		}
	})

	t.Run("one unhealthy component returns true", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("a", health.Sample{Healthy: false})
		tr.Record("a", health.Sample{Healthy: false})
		if !tr.IsFailed() {
			t.Errorf("unhealthy component: IsFailed() = false, want true (Overall=%s)", tr.Overall())
		}
	})

	t.Run("only degraded returns false", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("a", health.Sample{Healthy: true})
		tr.Record("a", health.Sample{Healthy: false})
		if tr.IsFailed() {
			t.Errorf("degraded component: IsFailed() = true, want false (Overall=%s)", tr.Overall())
		}
	})
}

// TestCanReceiveEvents_UnknownComponent verifies that an unregistered
// component always returns false.
func TestCanReceiveEvents_UnknownComponent(t *testing.T) {
	tr := health.NewTracker()
	if tr.CanReceiveEvents("nonexistent", time.Minute) {
		t.Error("unknown component: CanReceiveEvents() = true, want false")
	}
}

// TestCanReceiveEvents_RecentEvent verifies that a sample recorded within
// the freshness window is detected.
func TestCanReceiveEvents_RecentEvent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	tr.RecordEventReceived("HmIP-RF")

	if !tr.CanReceiveEvents("HmIP-RF", time.Minute) {
		t.Error("recent event: CanReceiveEvents() = false, want true")
	}
}

// TestCanReceiveEvents_StaleEvent verifies that a sample older than the
// freshness window is rejected.
func TestCanReceiveEvents_StaleEvent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	tr.RecordEventReceived("HmIP-RF")
	fc.Set(t0.Add(10 * time.Minute))

	if tr.CanReceiveEvents("HmIP-RF", 5*time.Minute) {
		t.Error("stale event (10 min ago, freshness 5 min): CanReceiveEvents() = true, want false")
	}
}

// TestCanReceiveEvents_FreshnessZeroUsesDefault verifies that freshness <= 0
// falls back to DefaultEventFreshness (5 minutes).
func TestCanReceiveEvents_FreshnessZeroUsesDefault(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	tr.RecordEventReceived("iface")

	// At t0+10min: older than DefaultEventFreshness (5min) → false.
	fc.Set(t0.Add(10 * time.Minute))
	if tr.CanReceiveEvents("iface", 0) {
		t.Error("t0+10min, freshness=0: CanReceiveEvents() = true, want false (default 5min)")
	}

	// New tracker at a fresh t0; advance only 2 min → within default 5 min → true.
	t1 := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	fc2 := clock.NewFake(t1)
	tr2 := health.NewTracker(health.WithClock(fc2), health.WithStaleAfter(0))
	tr2.RecordEventReceived("iface")
	fc2.Set(t1.Add(2 * time.Minute))
	if !tr2.CanReceiveEvents("iface", 0) {
		t.Error("t1+2min, freshness=0: CanReceiveEvents() = false, want true (default 5min)")
	}
}

// TestCanReceiveEvents_HealthySampleAfterUnhealthy verifies that a
// RecordEventReceived after an unhealthy sample still returns true —
// the most-recent event-received sample is what matters for freshness.
func TestCanReceiveEvents_HealthySampleAfterUnhealthy(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clock.NewFake(t0)
	tr := health.NewTracker(health.WithClock(fc), health.WithStaleAfter(0))

	// First record an unhealthy sample with a different note.
	tr.Record("conn", health.Sample{Healthy: false, Note: "timeout"})
	// Then record a fresh event-received.
	tr.RecordEventReceived("conn")

	if !tr.CanReceiveEvents("conn", time.Minute) {
		t.Error("event-received after unhealthy: CanReceiveEvents() = false, want true")
	}
}

// TestDefaultHistorySize asserts the constant is 200.
func TestDefaultHistorySize(t *testing.T) {
	const want = 200
	if health.DefaultHistorySize != want {
		t.Errorf("DefaultHistorySize = %d, want %d", health.DefaultHistorySize, want)
	}
}
