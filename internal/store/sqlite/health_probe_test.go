// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// withShrunkLatencyBudgets temporarily lowers the healthy/degraded latency
// budgets to near-zero so a genuinely fast local `SELECT 1` round-trip still
// crosses them deterministically, without depending on the database
// actually being slow (which would make the test flaky or need a multi-
// hundred-millisecond sleep). Restores the real budgets on cleanup.
func withShrunkLatencyBudgets(t *testing.T, healthy, degraded bool) {
	t.Helper()
	origHealthy, origDegraded := healthyLatencyBudget, degradedLatencyBudget
	t.Cleanup(func() {
		healthyLatencyBudget, degradedLatencyBudget = origHealthy, origDegraded
	})
	if healthy {
		healthyLatencyBudget = 0
	}
	if degraded {
		degradedLatencyBudget = 0
	}
}

// TestProbeOnce_HealthyReportsHealthy pins the ordinary case: a fast probe
// against a working database reports Healthy through the strict Record
// path, and the tracker resolves the component to StatusHealthy.
func TestProbeOnce_HealthyReportsHealthy(t *testing.T) {
	openMu.Lock()
	db, err := Open(context.Background(), FileDSN(filepath.Join(t.TempDir(), "health_ok.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tracker := health.NewTracker()
	probeOnce(context.Background(), db, tracker)

	comp, ok := tracker.Get(StoreComponentName)
	if !ok {
		t.Fatal("no sqlite component recorded")
	}
	if comp.Status != health.StatusHealthy {
		t.Errorf("status = %v, want Healthy", comp.Status)
	}
}

// TestProbeOnce_ElevatedLatencyStaysDegradedAcrossRepeatedProbes is the
// regression guard for the audit finding: two consecutive probes that only
// exceed the HEALTHY (not the DEGRADED) budget must never escalate the
// critical "sqlite" component past DEGRADED. Before the fix, probeOnce used
// the strict Record path for this case, whose flap-damp rule turns a second
// consecutive unhealthy sample into UNHEALTHY — tripping ServiceAvailability
// to 503 on a database that never failed a single query.
func TestProbeOnce_ElevatedLatencyStaysDegradedAcrossRepeatedProbes(t *testing.T) {
	withShrunkLatencyBudgets(t, true, false) // healthy budget only: elevated-but-not-slow branch
	openMu.Lock()
	db, err := Open(context.Background(), FileDSN(filepath.Join(t.TempDir(), "health_elevated.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tracker := health.NewTracker()

	for i := range 3 {
		probeOnce(context.Background(), db, tracker)
		comp, ok := tracker.Get(StoreComponentName)
		if !ok {
			t.Fatalf("probe %d: no sqlite component recorded", i)
		}
		if comp.Status != health.StatusDegraded {
			t.Errorf("probe %d: status = %v, want Degraded (never Unhealthy for elevated-latency-only samples)", i, comp.Status)
		}
		if comp.LastSample.Healthy {
			t.Errorf("probe %d: sample reported Healthy=true for an elevated round-trip", i)
		}
	}

	// The daemon-facing signal must follow: a critical component pinned at
	// DEGRADED must not drag ServiceAvailability down to Unhealthy/503.
	if got := health.ServiceAvailability([]health.Component{mustGet(t, tracker, StoreComponentName)}); got == health.StatusUnhealthy {
		t.Errorf("ServiceAvailability = Unhealthy for a degraded-only sqlite component, want Degraded or better")
	}
}

// TestProbeOnce_SlowLatencyStaysDegradedAcrossRepeatedProbes mirrors the
// above for the "slow probe" branch (elapsed > degradedLatencyBudget).
func TestProbeOnce_SlowLatencyStaysDegradedAcrossRepeatedProbes(t *testing.T) {
	withShrunkLatencyBudgets(t, true, true)
	openMu.Lock()
	db, err := Open(context.Background(), FileDSN(filepath.Join(t.TempDir(), "health_slow.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tracker := health.NewTracker()

	for i := range 3 {
		probeOnce(context.Background(), db, tracker)
		comp, ok := tracker.Get(StoreComponentName)
		if !ok {
			t.Fatalf("probe %d: no sqlite component recorded", i)
		}
		if comp.Status != health.StatusDegraded {
			t.Errorf("probe %d: status = %v, want Degraded", i, comp.Status)
		}
	}
}

// TestProbeOnce_QueryErrorEscalatesToUnhealthy pins the other half: a
// genuine query failure (not merely elevated latency) must still reach
// UNHEALTHY through the strict Record path's flap-damp escalation — the fix
// narrows what counts as a failure, it does not weaken the failure path.
func TestProbeOnce_QueryErrorEscalatesToUnhealthy(t *testing.T) {
	openMu.Lock()
	db, err := Open(context.Background(), FileDSN(filepath.Join(t.TempDir(), "health_err.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Closing the pool makes every subsequent query fail immediately with
	// sql.ErrConnDone, giving a deterministic, fast query error.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tracker := health.NewTracker()
	probeOnce(context.Background(), db, tracker)

	comp, ok := tracker.Get(StoreComponentName)
	if !ok {
		t.Fatal("no sqlite component recorded")
	}
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("status = %v, want Unhealthy after a genuine query error", comp.Status)
	}
}

func mustGet(t *testing.T, tracker *health.Tracker, name string) health.Component {
	t.Helper()
	comp, ok := tracker.Get(name)
	if !ok {
		t.Fatalf("component %q not recorded", name)
	}
	return comp
}
