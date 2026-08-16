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

// TestRemovingABootCentralDetachesTheDomainsWithoutReAttaching is the second
// half of the boot-removal gap.
//
// The per-central domain hooks are attach/detach pairs, and only the adopt
// path kept their unwires: a boot-configured CCU deleted (or disabled) at
// runtime reached none of them. The subscriptions themselves ride the unit's
// EventBus and Unit.Stop drops those, but the detach halves carry state that
// lives outside the unit — the Security & Safety aggregate and its fault
// ledger above all — so the removed CCU kept reporting its hazard classes as
// active on REST, MQTT and the SPA, and its open faults survived every
// restart.
//
// Reaching those detaches by re-running the attach and releasing it in the
// same breath is not equivalent, which is why the domains are the real ones
// here: an attach is not inert. The alarm service reconciles every zone of
// every central, adopting or stopping sirens on the CCUs that stay; the Matter
// hook kicks a topology reassemble; the security domain rebuilds its whole
// index. None of that may happen because a different CCU is being deleted, so
// the assertion is two-sided — every detach ran once, and no attach ran at
// all.
func TestRemovingABootCentralDetachesTheDomainsWithoutReAttaching(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const (
		name  = "boot-central-hooks"
		other = "stays-live"
	)
	cfg := config.Default()
	cfg.Alarm.Enabled = new(true)
	cfg.Centrals = []config.CentralConfig{{
		Name:       name,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}}

	reg := central.NewRegistry()
	unit := startRegisteredTestCentral(ctx, t, reg, name)
	// A second CCU that outlives the removal: the attach side effects the
	// removal must not trigger are the ones that reach across centrals.
	startRegisteredTestCentral(ctx, t, reg, other)
	// The Matter hook's reassemble kick is gated on the unit's readiness
	// latch, so an unready unit would hide the very side effect under test.
	unit.MarkSouthboundReady()

	orch := buildLiveTestOrchestrator(ctx, t, reg, cfg)
	wireCentralNorthbound(orch.sbDeps, unit)

	// The real domains, built by the composition root's own wiring helpers
	// and started the way the daemon starts them.
	db := openMigratedTestDB(t, "boot_removal_domains.db")
	logger := discardTestLogger()
	alarmSvc := wireAlarmService(cfg, reg, db, nil, nil, logger)
	if alarmSvc == nil {
		t.Fatal("wireAlarmService returned nil")
	}
	if err := alarmSvc.Start(ctx); err != nil {
		t.Fatalf("alarm service Start: %v", err)
	}
	t.Cleanup(func() { _ = alarmSvc.Stop(ctx) })
	securitySvc := wireSecurityService(cfg, reg, db, alarmSvc, nil, logger)
	if securitySvc == nil {
		t.Fatal("wireSecurityService returned nil")
	}
	if err := securitySvc.Start(ctx); err != nil {
		t.Fatalf("security service Start: %v", err)
	}
	t.Cleanup(func() { _ = securitySvc.Stop(ctx) })

	attached, detached := map[string]int{}, map[string]int{}
	observe := func(domain string, h perCentralHook) perCentralHook {
		return perCentralHook{
			attach: func(u *central.Unit) func() {
				attached[domain]++
				return h.attach(u)
			},
			detach: func(n string) {
				detached[domain]++
				h.detach(n)
			},
		}
	}
	orch.setAlarmCentralHook(observe("alarm", alarmCentralHook(alarmSvc)))
	orch.setSecurityCentralHook(observe("security", securityCentralHook(securitySvc)))

	// The Matter hook has no name-keyed detach, so its side effect is
	// observed where production would feel it: the debounced reassemble.
	reassembles := 0
	orch.setMatterCentralHook(newMatterCentralHook(func() { reassembles++ }, nil))
	orch.setEventSourceCentralHook(func(u *central.Unit) func() {
		attached["event-source"]++
		return nil
	})

	if err := orch.removeCentral(ctx, name); err != nil {
		t.Fatalf("removeCentral for a boot-time central: %v", err)
	}

	for _, domain := range []string{"alarm", "security"} {
		if detached[domain] != 1 {
			t.Errorf("%s detach ran %d times for a removed boot-time central, want 1", domain, detached[domain])
		}
	}
	for domain, n := range attached {
		t.Errorf("%s attach ran %d times while removing a central; a removal must run no domain attach — "+
			"the alarm reconcile reaches every zone of %q, and the Matter reassemble reaches the whole topology", domain, n, other)
	}
	if reassembles != 0 {
		t.Errorf("the Matter topology was reassembled %d times while removing a central, want 0", reassembles)
	}
}

// startRegisteredTestCentral mirrors the boot path for one central: the unit
// is constructed and registered by central.Bootstrap, never by the
// orchestrator.
func startRegisteredTestCentral(ctx context.Context, t *testing.T, reg *central.Registry, name string) *central.Unit {
	t.Helper()
	unit, err := central.New(central.Config{Name: name, Logger: discardTestLogger()})
	if err != nil {
		t.Fatalf("central.New(%s): %v", name, err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register(%s): %v", name, err)
	}
	if err := unit.Start(ctx); err != nil {
		t.Fatalf("unit.Start(%s): %v", name, err)
	}
	return unit
}
