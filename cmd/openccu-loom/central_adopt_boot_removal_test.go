// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestRemoveCentralTearsDownABootTimeCentral pins the teardown path for a
// central this orchestrator never adopted.
//
// `handles` is written only by adoptCentral, so every central the boot path
// registered — the normal case once the daemon has been restarted after
// onboarding — took the not-live branch. Both REST mutators tolerate that
// sentinel, so `DELETE /api/v1/admin/centrals/{name}` answered 204 and dropped
// the persisted row while the CCU stayed completely live: registry entry,
// bring-up goroutines, callback routes, MQTT/WS publishing, scheduler jobs. A
// second DELETE then answered 404 and only a restart made the deletion real.
//
// The assertion is on the effect — the unit is gone from the shared registry
// every north-bound adapter reads — not on which branch ran.
func TestRemoveCentralTearsDownABootTimeCentral(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const name = "boot-central"
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name:       name,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}}}

	reg := central.NewRegistry()
	// Mirror the boot path: the unit is constructed and registered by
	// central.Bootstrap, never by the orchestrator.
	unit, err := central.New(central.Config{Name: name, Logger: discardTestLogger()})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	if err := unit.Start(ctx); err != nil {
		t.Fatalf("unit.Start: %v", err)
	}

	orch := buildLiveTestOrchestrator(ctx, t, reg, cfg)
	// The boot path runs this for every registered central; its on-stop hook
	// is what removes the unit from the shared registry.
	wireCentralNorthbound(orch.sbDeps, unit)

	if err := orch.removeCentral(ctx, name); err != nil {
		t.Fatalf("removeCentral for a boot-time central: %v", err)
	}
	if _, live := reg.Get(name); live {
		t.Error("central is still registered after removeCentral — the CCU stays live behind a successful DELETE")
	}

	// A name that is live nowhere is still the sentinel: the REST decorator
	// relies on it to distinguish "nothing to tear down" from a real failure.
	err = orch.removeCentral(ctx, "never-existed")
	if !errors.Is(err, errCentralNotLive) {
		t.Errorf("removeCentral for an unknown name = %v, want errCentralNotLive", err)
	}
}
