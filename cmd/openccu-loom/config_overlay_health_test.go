// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func TestRecordConfigOverlayHealthFailureDegradesNot503(t *testing.T) {
	tracker := health.NewTracker()
	reg := metrics.NewRegistry()
	recordConfigOverlayHealth(tracker, reg, errors.New("configstore: layer north.matter: open secrets"))

	comp, ok := tracker.Get(configOverlayHealthComponent)
	if !ok {
		t.Fatal("config.overlay component was not recorded")
	}
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("component status=%q, want unhealthy", comp.Status)
	}
	// The daemon still serves on the config-file tier, so an unapplied
	// overlay must not collapse liveness to 503.
	if got := health.ServiceAvailability(tracker.Snapshot()); got != health.StatusDegraded {
		t.Errorf("ServiceAvailability=%q, want degraded", got)
	}
	if g := reg.Gauge(configOverlayFailedMetric, ""); g.Value() != 1 {
		t.Errorf("gauge=%v, want 1 (overlay failed)", g.Value())
	}
}

func TestRecordConfigOverlayHealthApplied(t *testing.T) {
	tracker := health.NewTracker()
	reg := metrics.NewRegistry()
	recordConfigOverlayHealth(tracker, reg, nil)

	comp, ok := tracker.Get(configOverlayHealthComponent)
	if !ok || comp.Status != health.StatusHealthy {
		t.Errorf("component=%+v ok=%v, want healthy", comp, ok)
	}
	if g := reg.Gauge(configOverlayFailedMetric, ""); g.Value() != 0 {
		t.Errorf("gauge=%v, want 0 (overlay applied)", g.Value())
	}
}

func TestRecordConfigOverlayHealthNilGuards(t *testing.T) {
	recordConfigOverlayHealth(nil, nil, errors.New("boom")) // must not panic
}
