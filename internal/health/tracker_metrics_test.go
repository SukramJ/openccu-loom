// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health

import (
	"sync"
	"testing"
)

func TestMetricsHealthSummaryEmptyTrackerScoresOne(t *testing.T) {
	t.Parallel()

	tr := NewTracker()
	view := tr.MetricsHealthSummary()
	if view.OverallScore != 1.0 {
		t.Errorf("empty score=%f, want 1.0", view.OverallScore)
	}
	if view.ClientsHealthy != 0 || view.ClientsDegraded != 0 || view.ClientsFailed != 0 {
		t.Errorf("non-zero counters on empty tracker: %+v", view)
	}
}

func TestMetricsHealthSummaryClassifiesByStatus(t *testing.T) {
	t.Parallel()

	tr := NewTracker()
	tr.Record("hmip-rf", Sample{Healthy: true})
	// Two unhealthy samples to escalate degraded → unhealthy.
	tr.Record("bidcos", Sample{Healthy: true})
	tr.Record("bidcos", Sample{Healthy: false}) // degraded.
	tr.Record("cuxd", Sample{Healthy: true})
	tr.Record("cuxd", Sample{Healthy: false})
	tr.Record("cuxd", Sample{Healthy: false}) // unhealthy.

	view := tr.MetricsHealthSummary()
	if view.ClientsHealthy != 1 {
		t.Errorf("healthy=%d, want 1", view.ClientsHealthy)
	}
	if view.ClientsDegraded != 1 {
		t.Errorf("degraded=%d, want 1", view.ClientsDegraded)
	}
	if view.ClientsFailed != 1 {
		t.Errorf("failed=%d, want 1", view.ClientsFailed)
	}
	// expected: (healthy=1.0 + degraded=0.5 + failed=0.0) / 3 clients
	want := (1.0 + 0.5) / 3.0
	if view.OverallScore != want {
		t.Errorf("score=%f, want %f", view.OverallScore, want)
	}
}

func TestMetricsHealthSummaryRaceSafe(t *testing.T) {
	t.Parallel()

	tr := NewTracker()
	const goroutines = 8
	const samples = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			for j := range samples {
				tr.Record(componentName(i, j), Sample{Healthy: j%2 == 0})
			}
		}()
	}
	wg.Go(func() {
		for range samples * goroutines {
			_ = tr.MetricsHealthSummary()
		}
	})
	wg.Wait()

	// Expect no panic / data race; at least one component healthy.
	view := tr.MetricsHealthSummary()
	if view.ClientsHealthy+view.ClientsDegraded+view.ClientsFailed == 0 {
		t.Error("no components recorded — sanity check failed")
	}
}

func componentName(i, j int) string {
	switch (i + j) % 3 {
	case 0:
		return "hmip-rf"
	case 1:
		return "bidcos-rf"
	}
	return "cuxd"
}
