// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestRecordUnhealthyStatesTheConditionOutright pins the entry point a caller
// needs when it is not sampling but reporting.
//
// Record applies flap-damping: the first unhealthy sample after a healthy run
// yields DEGRADED, and only a second consecutive one escalates. That is right
// for a probe whose single failure may be noise, and wrong for an event that
// states the condition — a client that failed, a breaker that opened. Callers
// worked around it by recording twice, the second time with an invented
// "(escalated)" note, which re-encoded the tracker's threshold at the call
// site and left a sample in the history that describes nothing that happened.
func TestRecordUnhealthyStatesTheConditionOutright(t *testing.T) {
	t.Parallel()

	tr := health.NewTracker()
	tr.Record("iface", health.Sample{Healthy: true, Note: "up"})

	tr.RecordUnhealthy("iface", health.Sample{Note: "client failed"})

	components := tr.Snapshot()
	var found bool
	for _, c := range components {
		if c.Name != "iface" {
			continue
		}
		found = true
		if c.Status != health.StatusUnhealthy {
			t.Errorf("status = %v, want UNHEALTHY from one report", c.Status)
		}
		if c.LastSample.Note != "client failed" {
			t.Errorf("last note = %q, want the caller's own note", c.LastSample.Note)
		}
	}
	if !found {
		t.Fatal("component missing from the snapshot")
	}
}

// TestRecordKeepsItsFlapDamping pins that the sampling entry point is
// unchanged: a probe's first failure still yields DEGRADED.
func TestRecordKeepsItsFlapDamping(t *testing.T) {
	t.Parallel()

	tr := health.NewTracker()
	tr.Record("probe", health.Sample{Healthy: true})
	tr.Record("probe", health.Sample{Healthy: false, Note: "one miss"})

	for _, c := range tr.Snapshot() {
		if c.Name == "probe" && c.Status != health.StatusDegraded {
			t.Errorf("status = %v, want DEGRADED after a single missed sample", c.Status)
		}
	}
}
