// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// jobNamesOf returns the sorted names of every job currently registered on
// unit's scheduler. Sorting makes the returned slice safe to compare with
// reflect.DeepEqual / slices.Equal regardless of registration order.
func jobNamesOf(u *central.Unit) []string {
	jobs := u.Scheduler.Jobs()
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		names = append(names, j.Name)
	}
	sort.Strings(names)
	return names
}

// jobIntervalOf returns the configured Interval of the named job on unit's
// scheduler, or -1 when the job is not registered.
func jobIntervalOf(u *central.Unit, name string) time.Duration {
	for _, j := range u.Scheduler.Jobs() {
		if j.Name == name {
			return j.Interval
		}
	}
	return -1
}

// TestRegisterStandardJobsForRegistersUnconditionalJobs verifies that
// registerStandardJobsFor, called for a freshly constructed [*central.Unit],
// registers the unconditional jobs plus every hub-refresh job wired from
// [central.Unit.Hub] / [central.Unit.Events] / [central.Unit.HubModel] — all
// of which [central.New] always populates. Firmware-poll jobs are excluded
// because registerStandardJobsFor never wires those [central.StandardJobs]
// slots (they remain nil, so [central.RegisterStandardJobs] skips them).
func TestRegisterStandardJobsForRegistersUnconditionalJobs(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	u, err := central.New(central.Config{Name: "jobs-unit-a"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	registerStandardJobsFor(u, &config.Config{}, logger)

	wantPresent := []string{
		"central.health_heartbeat",
		"central.check_connection",
		"hub.connectivity_refresh",
		"hub.metrics_refresh",
		"hub.last_event_age_refresh",
		"hub.program_refresh",
		"hub.sysvar_refresh",
		"hub.inbox_refresh",
		"hub.service_messages_refresh",
		"hub.alarm_messages_refresh",
		"hub.system_update_refresh",
		"hub.install_mode_refresh",
		"central.refresh_client_data",
		"central.reconcile",
	}
	got := jobNamesOf(u)
	for _, name := range wantPresent {
		if !slices.Contains(got, name) {
			t.Errorf("registered jobs = %v, want %q present", got, name)
		}
	}

	// Firmware-poll jobs are never wired by registerStandardJobsFor, so they
	// must be absent — asserting this locks in the extraction's exact
	// behavior instead of just a loose subset check.
	for _, name := range []string{"central.firmware_check", "central.firmware_delivery_check", "central.firmware_updating_check"} {
		if slices.Contains(got, name) {
			t.Errorf("registered jobs = %v, want %q absent (never wired by registerStandardJobsFor)", got, name)
		}
	}
}

// TestRegisterStandardJobsForAppliesCheckConnectionOverride verifies that a
// per-central cfg.Centrals[].CheckConnectionInterval override for the unit's
// own name is applied to the registered central.check_connection job, and
// that a unit whose name has no matching cfg.Centrals entry keeps the
// compiled-in default.
func TestRegisterStandardJobsForAppliesCheckConnectionOverride(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	const overrideName = "jobs-unit-override"
	const plainName = "jobs-unit-plain"
	const overrideInterval = 7 * time.Second

	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: overrideName, CheckConnectionInterval: overrideInterval},
		},
	}

	uOverride, err := central.New(central.Config{Name: overrideName})
	if err != nil {
		t.Fatalf("central.New(override): %v", err)
	}
	registerStandardJobsFor(uOverride, cfg, logger)
	if got := jobIntervalOf(uOverride, "central.check_connection"); got != overrideInterval {
		t.Errorf("central.check_connection interval = %v, want override %v", got, overrideInterval)
	}

	uPlain, err := central.New(central.Config{Name: plainName})
	if err != nil {
		t.Fatalf("central.New(plain): %v", err)
	}
	// plainName has no matching cfg.Centrals entry, so the job must fall
	// back to the compiled-in default rather than picking up overrideName's
	// override.
	registerStandardJobsFor(uPlain, cfg, logger)

	uPlainNoCfg, err := central.New(central.Config{Name: plainName + "-nocfg"})
	if err != nil {
		t.Fatalf("central.New(plain-nocfg): %v", err)
	}
	registerStandardJobsFor(uPlainNoCfg, &config.Config{}, logger)
	defaultInterval := jobIntervalOf(uPlainNoCfg, "central.check_connection")
	if got := jobIntervalOf(uPlain, "central.check_connection"); got != defaultInterval {
		t.Errorf("check_connection interval for unit without matching cfg.Centrals entry = %v, want compiled-in default %v", got, defaultInterval)
	}
	if defaultInterval == overrideInterval {
		t.Fatalf("test setup invalid: compiled-in default %v collides with override %v", defaultInterval, overrideInterval)
	}
}

// TestRegisterStandardJobsForAppliesSysvarScanIntervalOverride verifies that
// a per-central cfg.Centrals[].Behavior.SysvarScanInterval override for the
// unit's own name is applied to the registered hub.sysvar_refresh job.
func TestRegisterStandardJobsForAppliesSysvarScanIntervalOverride(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	const name = "jobs-unit-sysvar-override"
	const overrideInterval = 42 * time.Second

	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: name, Behavior: config.CentralBehavior{SysvarScanInterval: overrideInterval}},
		},
	}

	u, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	registerStandardJobsFor(u, cfg, logger)

	if got := jobIntervalOf(u, "hub.sysvar_refresh"); got != overrideInterval {
		t.Errorf("hub.sysvar_refresh interval = %v, want override %v", got, overrideInterval)
	}

	// A unit with no matching cfg.Centrals entry keeps the compiled-in
	// default, proving the override is scoped to the matching central only.
	uOther, err := central.New(central.Config{Name: name + "-other"})
	if err != nil {
		t.Fatalf("central.New(other): %v", err)
	}
	registerStandardJobsFor(uOther, cfg, logger)
	if got := jobIntervalOf(uOther, "hub.sysvar_refresh"); got == overrideInterval {
		t.Errorf("hub.sysvar_refresh interval for unrelated unit = %v, want compiled-in default, not the override leaking across centrals", got)
	}
}

// TestRegisterStandardJobsMatchesPerUnitExtraction is the extraction's
// core invariant: registerStandardJobs (looping registerStandardJobsFor
// across every unit in a [*central.Registry]) must register exactly the
// same set of job names, per unit, as calling registerStandardJobsFor
// directly on an equivalent freshly constructed unit. This is what proves
// the registerStandardJobsFor factor-out (docs/plans/L-live-ccu-adopt.md
// PR3, needed so the live-adopt orchestrator can register jobs for a single
// runtime-added central) did not change registerStandardJobs' aggregate
// behavior across a multi-CCU registry.
func TestRegisterStandardJobsMatchesPerUnitExtraction(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: "jobs-agg-a", CheckConnectionInterval: 11 * time.Second},
		},
	}

	reg := central.NewRegistry()
	for _, name := range []string{"jobs-agg-a", "jobs-agg-b"} {
		u, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		if err := reg.Register(u); err != nil {
			t.Fatalf("reg.Register(%s): %v", name, err)
		}
	}

	registerStandardJobs(reg, cfg, logger)

	for _, name := range []string{"jobs-agg-a", "jobs-agg-b"} {
		u, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s) manual: %v", name, err)
		}
		registerStandardJobsFor(u, cfg, logger)

		regUnit, ok := reg.Get(name)
		if !ok {
			t.Fatalf("registry missing unit %q", name)
		}
		gotViaLoop := jobNamesOf(regUnit)
		gotManual := jobNamesOf(u)
		if !slices.Equal(gotViaLoop, gotManual) {
			t.Errorf("job names for %q: via registerStandardJobs loop = %v, via direct registerStandardJobsFor = %v", name, gotViaLoop, gotManual)
		}
	}
}
