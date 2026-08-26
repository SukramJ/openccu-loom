// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func TestRecordSecretHealthPlaintextDegradesNot503(t *testing.T) {
	tracker := health.NewTracker()
	reg := metrics.NewRegistry()
	recordSecretHealth(tracker, reg, false)

	comp, ok := tracker.Get(secretsHealthComponent)
	if !ok {
		t.Fatal("config.secrets component was not recorded")
	}
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("component status=%q, want unhealthy", comp.Status)
	}
	// Crucial: the plaintext condition must NOT make /health return 503.
	// config.secrets is non-critical, so service availability collapses to
	// Degraded — keeping liveness probes green while still flagging the issue.
	if got := health.ServiceAvailability(tracker.Snapshot()); got != health.StatusDegraded {
		t.Errorf("ServiceAvailability=%q, want degraded (not unhealthy/503)", got)
	}
	if g := reg.Gauge(secretsPlaintextMetric, ""); g.Value() != 1 {
		t.Errorf("gauge=%v, want 1 (plaintext)", g.Value())
	}
}

func TestRecordSecretHealthEncrypted(t *testing.T) {
	tracker := health.NewTracker()
	reg := metrics.NewRegistry()
	recordSecretHealth(tracker, reg, true)

	comp, ok := tracker.Get(secretsHealthComponent)
	if !ok || comp.Status != health.StatusHealthy {
		t.Errorf("component=%+v ok=%v, want healthy", comp, ok)
	}
	if g := reg.Gauge(secretsPlaintextMetric, ""); g.Value() != 0 {
		t.Errorf("gauge=%v, want 0 (encrypted)", g.Value())
	}
}

func TestRecordSecretHealthNilGuards(t *testing.T) {
	recordSecretHealth(nil, nil, false) // must not panic
}
