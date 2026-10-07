// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// maintenanceRecorder records what the maintenance topics did.
type maintenanceRecorder struct {
	mu       sync.Mutex
	levels   []slog.Level
	restarts atomic.Int32
}

func (r *maintenanceRecorder) setLogLevel(l slog.Level) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.levels = append(r.levels, l)
}

func (r *maintenanceRecorder) gotLevels() []slog.Level {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]slog.Level(nil), r.levels...)
}

// buildMaintenancePlane builds the command plane the way the daemon does —
// through makeMQTTSubscriberBuilder — over a bridge whose maintenance hooks
// record their effect.
func buildMaintenancePlane(t *testing.T, supervised bool) (*mqtt.NoopClient, *maintenanceRecorder) {
	t.Helper()
	ctx := context.Background()
	reg := central.NewRegistry()
	rec := &maintenanceRecorder{}
	noop := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
		Maintenance: mqtt.MaintenanceConfig{
			SetLogLevel: rec.setLogLevel,
			Supervised:  func() bool { return supervised },
			Shutdown:    func() { rec.restarts.Add(1) },
		},
	}, noop)
	build := makeMQTTSubscriberBuilder(ctx, reg, clientpkg.NewValueWriter(), nil, nil, nil, nil, nil, supervisorLogger())
	teardown, err := build(ctx, noop, bridge)
	if err != nil {
		t.Fatalf("subscriber builder: %v", err)
	}
	t.Cleanup(func() {
		if teardown != nil {
			teardown()
		}
	})
	return noop, rec
}

const maintenanceFilter = "openccu-loom/maintenance/set/#"

// TestMQTTSubscriberBuilderRoutesTheMaintenanceLogLevel pins the maintenance
// routes through the composition root, not the setter: the daemon's
// subscriber builder must register `<base>/maintenance/set/#` on the command
// plane, and a `loglevel` published there must reach the bridge's
// SetLogLevel hook (ADR 0083, spec §7).
//
// Falsifiability: drop `WithMaintenance(bridge.Instance())` from
// makeMQTTSubscriberBuilder and the delivery finds no subscription.
func TestMQTTSubscriberBuilderRoutesTheMaintenanceLogLevel(t *testing.T) {
	t.Parallel()
	noop, rec := buildMaintenancePlane(t, false)

	if !noop.DeliverInbound(maintenanceFilter, "openccu-loom/maintenance/set/loglevel", []byte("debug")) {
		t.Fatal("the daemon does not subscribe to its own maintenance topics")
	}
	waitFor(t, func() bool { return len(rec.gotLevels()) > 0 }, "the log-level command never reached the hook")
	if got := rec.gotLevels(); len(got) != 1 || got[0] != slog.LevelDebug {
		t.Fatalf("log levels set = %v, want [DEBUG]", got)
	}
}

// TestMQTTMaintenanceRestartFollowsTheSupervisorPredicate pins both halves
// of the restart gate through the composition root: unsupervised, the
// restart is refused and the shutdown never runs; supervised, an EMPTY
// payload fires it — the management tool the convention exists for
// publishes the restart empty, the one exception to "empty `set` payloads
// are ignored".
func TestMQTTMaintenanceRestartFollowsTheSupervisorPredicate(t *testing.T) {
	t.Parallel()
	const topic = "openccu-loom/maintenance/set/restart"

	t.Run("unsupervised", func(t *testing.T) {
		t.Parallel()
		noop, rec := buildMaintenancePlane(t, false)
		if !noop.DeliverInbound(maintenanceFilter, topic, nil) {
			t.Fatal("the daemon does not subscribe to its own maintenance topics")
		}
		time.Sleep(200 * time.Millisecond)
		if n := rec.restarts.Load(); n != 0 {
			t.Fatalf("an unsupervised daemon ran its shutdown %d time(s): a restart without a supervisor is a stop", n)
		}
	})
	t.Run("supervised, empty payload", func(t *testing.T) {
		t.Parallel()
		noop, rec := buildMaintenancePlane(t, true)
		if !noop.DeliverInbound(maintenanceFilter, topic, nil) {
			t.Fatal("the daemon does not subscribe to its own maintenance topics")
		}
		waitFor(t, func() bool { return rec.restarts.Load() > 0 }, "a supervised restart with an empty payload did nothing")
	})
}

// TestMQTTMaintenanceHooksReachTheBridgeConfig pins the other half of the
// wiring — what [mqttSupervisor.SetMaintenance] hands to buildMQTT — against
// the daemon's own collaborators: the hooks built from the log-level
// registry move its root level, and the operator's settings decide whether
// the topics exist and how often the statistics go out.
func TestMQTTMaintenanceHooksReachTheBridgeConfig(t *testing.T) {
	t.Parallel()
	levels := hmlog.NewLevelRegistry(slog.LevelInfo)
	hooks := newMQTTMaintenanceHooks(levels, audit.NewBuffer(4))

	cfg := hooks.config(config.NorthMQTTMaintenance{})
	if cfg.Disabled {
		t.Error("maintenance disabled by default; the convention recommends it on")
	}
	if cfg.StatsInterval != 60*time.Second {
		t.Errorf("stats interval = %v, want the 60 s default", cfg.StatsInterval)
	}
	if cfg.SetLogLevel == nil || cfg.Supervised == nil || cfg.Shutdown == nil {
		t.Fatal("a hook is missing from the bridge's maintenance config")
	}
	cfg.SetLogLevel(slog.LevelDebug)
	if got := levels.Default(); got != slog.LevelDebug {
		t.Errorf("root log level = %v after maintenance/set/loglevel debug, want DEBUG", got)
	}

	off, zero := false, 0
	cfg = hooks.config(config.NorthMQTTMaintenance{Enabled: &off, StatsIntervalSeconds: &zero})
	if !cfg.Disabled {
		t.Error("north.mqtt.maintenance.enabled: false did not disable the topics")
	}
	if cfg.StatsInterval != hapublisher.StatsOff {
		t.Errorf("stats_interval_seconds: 0 = %v, want StatsOff (0 means off, not the default)", cfg.StatsInterval)
	}

	// And the composition root carries the config into the bridge it builds.
	full := config.Default()
	full.North.MQTT.Enabled = true
	full.North.MQTT.Maintenance = config.NorthMQTTMaintenance{Enabled: &off}
	stack := buildMQTT(full, slog.Default(), nil, nil, nil, hooks)
	if stack == nil {
		t.Fatal("expected a stack with MQTT enabled")
	}
	if stack.wiring.Bridge().Instance().Maintenance() {
		t.Error("buildMQTT ignored north.mqtt.maintenance.enabled: false")
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
