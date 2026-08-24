// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/security"
)

// TestSeamEffect_SecurityIndexRefresh_RebuildsOnAlarmConfigChange asserts
// what the security.index_refresh seam's Why claims: that an alarm-config
// write rebuilds the hazard-classification index.
//
// The manifest already knows the seam is declared, and the Attach guard
// knows it wraps work. Neither says the work has the effect the Why names,
// and a hook installed on the wrong object, or an alarm Reload that
// returned before reaching it, would satisfy both.
//
// The index is driven to a known-bad state first. That is the whole
// design: a test that only checked "healthy after Reload" would pass on a
// service whose index was never touched, because a fresh aggregate starts
// out healthy. Degrading it first is what makes the recovery attributable.
func TestSeamEffect_SecurityIndexRefresh_RebuildsOnAlarmConfigChange(t *testing.T) {
	alarmSvc, securitySvc := seamEffectSecurityStack(t, true)

	degradeSecurityIndex(t, securitySvc)

	if err := alarmSvc.Reload(context.Background()); err != nil {
		t.Fatalf("alarm reload: %v", err)
	}
	if !securitySvc.Snapshot().IndexHealthy {
		t.Error("an alarm-config reload left the security index degraded — the " +
			"config-changed hook did not reach security.RebuildIndex, so a sensor the " +
			"operator just re-assigned keeps its old hazard class until the daemon restarts")
	}
}

// TestSeamEffect_SecurityIndexRefresh_IsAttributableToTheSeam is the
// negative control, and it is the half that makes the test above mean
// anything: with the hook not installed, the same sequence must leave the
// index degraded.
//
// Without it, "healthy after Reload" is a claim about a service that
// happens to be healthy, not about a seam that ran.
func TestSeamEffect_SecurityIndexRefresh_IsAttributableToTheSeam(t *testing.T) {
	alarmSvc, securitySvc := seamEffectSecurityStack(t, false)

	degradeSecurityIndex(t, securitySvc)

	if err := alarmSvc.Reload(context.Background()); err != nil {
		t.Fatalf("alarm reload: %v", err)
	}
	if securitySvc.Snapshot().IndexHealthy {
		t.Error("the index recovered without the seam being wired — something other than " +
			"the config-changed hook rebuilds it, so the test above proves nothing about " +
			"this seam")
	}
}

// seamEffectSecurityStack builds both services through the production
// wiring functions and optionally installs the seam's hook through the
// production one as well.
func seamEffectSecurityStack(t *testing.T, wireHook bool) (alarmSvc *alarm.Service, securitySvc *security.Service) {
	t.Helper()

	db := openMigratedTestDB(t, "seam_effect_security.db")
	cfg := config.Default()
	reg := central.NewRegistry()
	logger := discardTestLogger()

	alarmSvc = wireAlarmService(cfg, reg, db, nil, nil, logger)
	if alarmSvc == nil {
		t.Fatal("wireAlarmService returned nil — there would be no config-change to react to")
	}
	securitySvc = wireSecurityService(cfg, reg, db, alarmSvc, nil, nil, logger)
	if securitySvc == nil {
		t.Fatal("wireSecurityService returned nil — there would be no index to rebuild")
	}
	if wireHook {
		wireSecurityIndexRefreshHook(alarmSvc, securitySvc, logger)
	}
	// Reload runs the engine reload before it reaches the hook, and an
	// unstarted engine makes it return early — so a test that skipped this
	// would report the seam as broken for a reason that has nothing to do
	// with it. The daemon starts the service through the bridge registry.
	if err := alarmSvc.Start(context.Background()); err != nil {
		t.Fatalf("alarm start: %v", err)
	}
	t.Cleanup(func() { _ = alarmSvc.Stop(context.Background()) })
	return alarmSvc, securitySvc
}

// degradeSecurityIndex drives the index into its unavailable state by
// rebuilding under a cancelled context, so a later successful rebuild is
// observable as a transition rather than as a value that was already
// there.
func degradeSecurityIndex(t *testing.T, svc *security.Service) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.RebuildIndex(ctx); err == nil {
		t.Fatal("a rebuild under a cancelled context succeeded — the degraded state this " +
			"test depends on is unreachable, so neither assertion below would discriminate")
	}
	if svc.Snapshot().IndexHealthy {
		t.Fatal("the index still reports healthy after a failed rebuild — the signal this " +
			"test reads does not track what it is supposed to")
	}
}
