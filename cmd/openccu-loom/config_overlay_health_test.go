// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
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

// TestRecordSQLiteOpenFailureHealthTripsCriticalUnavailable is the
// regression guard for a failed shared-DB open leaving the `sqlite`
// component unregistered rather than unhealthy: openLoomDB returning a nil
// db must trip health.isCriticalComponent's 503 rule, not silently vanish
// from the snapshot the way it did when nothing recorded a verdict at all.
func TestRecordSQLiteOpenFailureHealthTripsCriticalUnavailable(t *testing.T) {
	tracker := health.NewTracker()
	recordSQLiteOpenFailureHealth(tracker, errors.New("sqlite: ping: unable to open database file"))

	comp, ok := tracker.Get(sqlitestore.StoreComponentName)
	if !ok {
		t.Fatal("sqlite component was not recorded")
	}
	if comp.Status != health.StatusUnhealthy {
		t.Errorf("component status=%q, want unhealthy", comp.Status)
	}
	if !strings.Contains(comp.LastSample.Note, "unable to open database file") {
		t.Errorf("note=%q, want it to carry the open error", comp.LastSample.Note)
	}
	if got := health.ServiceAvailability(tracker.Snapshot()); got != health.StatusUnhealthy {
		t.Errorf("ServiceAvailability=%q, want unhealthy — sqlite is a critical component (HTTP 503)", got)
	}
}

// TestRecordSQLiteOpenFailureHealthStaysUnhealthyPastStaleWindow proves the
// verdict is Sticky: a failed boot open is never retried, so nothing
// re-records the component, and it must not decay to StatusUnknown once
// health.DefaultStaleAfter elapses — trading a wrong "healthy" for an
// equally wrong "unknown" is not the fix.
func TestRecordSQLiteOpenFailureHealthStaysUnhealthyPastStaleWindow(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	tracker := health.NewTracker(health.WithClock(clk), health.WithStaleAfter(90*time.Second))
	recordSQLiteOpenFailureHealth(tracker, errors.New("disk full"))

	clk.Advance(91 * time.Second)

	comp, ok := tracker.Get(sqlitestore.StoreComponentName)
	if !ok || comp.Status != health.StatusUnhealthy {
		t.Errorf("component=%+v ok=%v, want unhealthy (not decayed to unknown)", comp, ok)
	}
}

func TestRecordSQLiteOpenFailureHealthNilTracker(t *testing.T) {
	recordSQLiteOpenFailureHealth(nil, errors.New("boom")) // must not panic
}
