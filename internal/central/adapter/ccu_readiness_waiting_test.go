// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestRecordCentralWaitingSurfacesWithout503 pins the readiness-gate health
// contract: a central waiting for its CCU to boot is visible in the health
// snapshot but must NOT make ServiceAvailability report Unhealthy — a
// co-booting CCU is a startup state, not a hard failure that should drain the
// instance via a /health 503.
func TestRecordCentralWaitingSurfacesWithout503(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "OttoLoom"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	recordCentralWaiting(unit)

	comp, ok := unit.Health.Get("startup.OttoLoom")
	if !ok {
		t.Fatal("expected a startup.<central> waiting component to be recorded")
	}
	if comp.Status != health.StatusDegraded {
		t.Errorf("waiting component status = %q, want degraded (RecordQuality caps it)", comp.Status)
	}
	if comp.LastSample.Note == "" {
		t.Error("waiting component should carry a human-readable note")
	}

	// The waiting state, even paired with a healthy persistence layer, must not
	// collapse service availability to Unhealthy (which maps to HTTP 503).
	snap := append(unit.Health.Snapshot(), health.Component{Name: "sqlite", Status: health.StatusHealthy})
	if got := health.ServiceAvailability(snap); got == health.StatusUnhealthy {
		t.Errorf("ServiceAvailability = %q while a central is merely waiting for its CCU; must never be 503", got)
	}
}

// TestRecordCentralWaitingNilSafe verifies the helper tolerates a nil unit /
// nil tracker (tooling / partially-built units).
func TestRecordCentralWaitingNilSafe(t *testing.T) {
	t.Parallel()
	recordCentralWaiting(nil)             // must not panic
	recordCentralWaiting(&central.Unit{}) // nil Health field — must not panic
}

// TestResolveCentralWaitingRemovesComponent verifies that after
// recordCentralWaiting has registered the transient startup component,
// resolveCentralWaiting removes it so Get returns ok==false.
func TestResolveCentralWaitingRemovesComponent(t *testing.T) {
	t.Parallel()

	unit, err := central.New(central.Config{Name: "RolfLoom"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	recordCentralWaiting(unit)

	// Precondition: component must be present before resolve.
	if _, ok := unit.Health.Get(startupHealthComponent(unit.Name())); !ok {
		t.Fatal("precondition: startup component not registered after recordCentralWaiting")
	}

	resolveCentralWaiting(unit)

	// After resolve the component must be gone.
	if _, ok := unit.Health.Get(startupHealthComponent(unit.Name())); ok {
		t.Error("startup component still present after resolveCentralWaiting; want it unregistered")
	}
}

// TestResolveCentralWaitingNilSafe verifies the helper tolerates nil
// inputs (nil unit and a unit with a nil Health field).
func TestResolveCentralWaitingNilSafe(t *testing.T) {
	t.Parallel()
	resolveCentralWaiting(nil)             // must not panic
	resolveCentralWaiting(&central.Unit{}) // nil Health field — must not panic
}

// TestStartupComponentNameIncludesCentralName verifies the naming
// convention so the record and resolve sides stay in sync.
func TestStartupComponentNameIncludesCentralName(t *testing.T) {
	t.Parallel()
	if got, want := startupHealthComponent("my-ccu"), "startup.my-ccu"; got != want {
		t.Errorf("startupHealthComponent = %q, want %q", got, want)
	}
}
