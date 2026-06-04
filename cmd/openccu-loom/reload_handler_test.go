// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// makeLogger returns a *slog.Logger that writes text records into buf.
func makeLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestHotReloadHandler_NilInputs verifies that nil prev or next config
// causes the handler to return nil without panicking.
func TestHotReloadHandler_NilInputs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	if err := h(nil, config.Default()); err != nil {
		t.Errorf("nil prev: unexpected error: %v", err)
	}
	if err := h(config.Default(), nil); err != nil {
		t.Errorf("nil next: unexpected error: %v", err)
	}
	if err := h(nil, nil); err != nil {
		t.Errorf("both nil: unexpected error: %v", err)
	}
}

// TestHotReloadHandler_NoDiff_NothingLogged verifies that when the
// two configs are identical snapshots the handler logs "hot_reloaded_fields=0"
// and "restart_required_fields=0".
func TestHotReloadHandler_NoDiff_NothingLogged(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "hot_reloaded_fields=0") {
		t.Errorf("expected hot_reloaded_fields=0 in log; got:\n%s", out)
	}
	if !strings.Contains(out, "restart_required_fields=0") {
		t.Errorf("expected restart_required_fields=0 in log; got:\n%s", out)
	}
}

// TestHotReloadHandler_LoggingLevelChange verifies that a logging-level
// diff is counted as a hot-reloadable field.
func TestHotReloadHandler_LoggingLevelChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.Logging.Level = "debug"
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "daemon.reload.logging") {
		t.Errorf("expected daemon.reload.logging in log; got:\n%s", out)
	}
	if !strings.Contains(out, "hot_reloaded_fields=1") {
		t.Errorf("expected hot_reloaded_fields=1; got:\n%s", out)
	}
}

// TestHotReloadHandler_DataDirChange verifies that a data_dir change is
// classified as restart-required.
func TestHotReloadHandler_DataDirChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.DataDir = "/new/data/dir"
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "data_dir") {
		t.Errorf("expected data_dir restart_required log; got:\n%s", out)
	}
	if !strings.Contains(out, "restart_required_fields=1") {
		t.Errorf("expected restart_required_fields=1; got:\n%s", out)
	}
}

// TestHotReloadHandler_LocaleChange is a restart-required field.
func TestHotReloadHandler_LocaleChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.Locale = "de"
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "locale") {
		t.Errorf("expected locale restart_required log; got:\n%s", out)
	}
}

// TestHotReloadHandler_RESTListenChange is restart-required.
func TestHotReloadHandler_RESTListenChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.North.REST.Listen = ":9999"
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "north.rest.listen") {
		t.Errorf("expected north.rest.listen restart_required log; got:\n%s", out)
	}
}

// TestHotReloadHandler_UIListenChange is restart-required.
func TestHotReloadHandler_UIListenChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.North.UI.Listen = ":8888"
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "north.ui.listen") {
		t.Errorf("expected north.ui.listen restart_required log; got:\n%s", out)
	}
}

// TestHotReloadHandler_CentralsCountChange is restart-required.
func TestHotReloadHandler_CentralsCountChange(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.Centrals = append(next.Centrals, config.CentralConfig{Name: "extra"})
	if err := h(prev, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "centrals.count") {
		t.Errorf("expected centrals.count restart_required log; got:\n%s", out)
	}
}

// TestHotReloadHandler_AlwaysReturnsNilError verifies the handler never
// returns a non-nil error (watcher must always adopt the new snapshot).
func TestHotReloadHandler_AlwaysReturnsNilError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), nil)

	prev := config.Default()
	next := config.Default()
	next.DataDir = "/totally/different"
	next.Locale = "de"
	next.North.REST.Listen = ":7777"

	if err := h(prev, next); err != nil {
		t.Errorf("hotReloadHandler must always return nil, got %v", err)
	}
}

// TestHotReloadHandler_MQTT_NoSupervisorBound_LogsDeferred verifies
// that an MQTT structural diff with no supervisor installed logs
// mqtt_deferred and returns nil — the watcher must not roll back.
func TestHotReloadHandler_MQTT_NoSupervisorBound_LogsDeferred(t *testing.T) {
	t.Parallel()
	// nil deps path: reloadDeps.MQTTSupervisor() handles nil receiver.
	t.Run("nil_deps", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := hotReloadHandler(makeLogger(&buf), nil)

		prev := config.Default()
		next := config.Default()
		next.North.MQTT.TopicBase = "new-base"

		if err := h(prev, next); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "mqtt_deferred") {
			t.Errorf("expected mqtt_deferred in log; got:\n%s", out)
		}
	})

	// Non-nil deps but no supervisor set yet.
	t.Run("deps_no_supervisor", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		deps := newReloadDeps()
		h := hotReloadHandler(makeLogger(&buf), deps)

		prev := config.Default()
		next := config.Default()
		next.North.MQTT.TopicBase = "new-base"

		if err := h(prev, next); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "mqtt_deferred") {
			t.Errorf("expected mqtt_deferred in log; got:\n%s", out)
		}
	})
}

// TestHotReloadHandler_MQTT_StructuralDiff_TriggersSwap verifies that
// when a supervisor is bound and the MQTT config changes, Swap is called,
// the handler returns nil, and the log records mqtt_swapped plus
// hot_reloaded_fields=1.
func TestHotReloadHandler_MQTT_StructuralDiff_TriggersSwap(t *testing.T) {
	ctx := context.Background()

	sup := newMQTTSupervisor(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), health.NewTracker())
	startCfg := config.Default()
	startCfg.North.MQTT.Enabled = true
	startCfg.North.MQTT.BrokerURL = ""
	startCfg.North.MQTT.TopicBase = "oldbase"
	if err := sup.Start(ctx, startCfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { sup.Shutdown(ctx) })

	bridgeBefore := sup.CurrentBridge()

	deps := newReloadDeps()
	deps.SetMQTTSupervisor(sup)

	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), deps)

	prev := config.Default()
	prev.North.MQTT.Enabled = true
	prev.North.MQTT.BrokerURL = ""
	prev.North.MQTT.TopicBase = "oldbase"

	next := config.Default()
	next.North.MQTT.Enabled = true
	next.North.MQTT.BrokerURL = ""
	next.North.MQTT.TopicBase = "newbase"

	if err := h(prev, next); err != nil {
		t.Fatalf("expected nil error from handler, got %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "mqtt_swapped") {
		t.Errorf("expected mqtt_swapped in log; got:\n%s", out)
	}
	if !strings.Contains(out, "hot_reloaded_fields=1") {
		t.Errorf("expected hot_reloaded_fields=1 in log; got:\n%s", out)
	}

	// Confirm Swap ran: the active bridge must be a new object.
	bridgeAfter := sup.CurrentBridge()
	if bridgeAfter == nil {
		t.Fatal("CurrentBridge() is nil after Swap — stack torn down unexpectedly")
	}
	if bridgeAfter == bridgeBefore {
		t.Fatal("CurrentBridge() pointer unchanged after Swap — Swap did not rebuild the stack")
	}
}

// TestHotReloadHandler_MQTT_NoDiff_NoSwap verifies that when the MQTT
// config is identical in prev and next no swap-related log lines appear.
func TestHotReloadHandler_MQTT_NoDiff_NoSwap(t *testing.T) {
	ctx := context.Background()

	sup := newMQTTSupervisor(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), health.NewTracker())
	startCfg := config.Default()
	startCfg.North.MQTT.Enabled = true
	startCfg.North.MQTT.BrokerURL = ""
	if err := sup.Start(ctx, startCfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { sup.Shutdown(ctx) })

	deps := newReloadDeps()
	deps.SetMQTTSupervisor(sup)

	var buf bytes.Buffer
	h := hotReloadHandler(makeLogger(&buf), deps)

	// Both sides identical: no MQTT diff should be detected.
	prev := config.Default()
	prev.North.MQTT.Enabled = true
	prev.North.MQTT.BrokerURL = ""
	next := config.Default()
	next.North.MQTT.Enabled = true
	next.North.MQTT.BrokerURL = ""

	if err := h(prev, next); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "mqtt_swapped") {
		t.Errorf("unexpected mqtt_swapped in log; got:\n%s", out)
	}
	if strings.Contains(out, "mqtt_deferred") {
		t.Errorf("unexpected mqtt_deferred in log; got:\n%s", out)
	}
}

// TestMQTTDiffersStructurally is a table test covering every field of
// NorthMQTT: equal values must return false; any single-field change
// must return true.
func TestMQTTDiffersStructurally(t *testing.T) {
	t.Parallel()

	base := config.NorthMQTT{
		Enabled:           true,
		BrokerURL:         "tcp://broker:1883",
		ClientID:          "client-a",
		Username:          "user",
		Password:          "pass",
		TopicBase:         "home",
		RawEnabled:        true,
		DiscoveryEnabled:  true,
		PayloadFormat:     "json",
		SubDevicesEnabled: true,
	}

	cases := []struct {
		name string
		a, b config.NorthMQTT
		want bool
	}{
		{
			name: "equal",
			a:    base,
			b:    base,
			want: false,
		},
		{
			name: "Enabled",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.Enabled = false; return c }(),
			want: true,
		},
		{
			name: "BrokerURL",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.BrokerURL = "tcp://other:1883"; return c }(),
			want: true,
		},
		{
			name: "ClientID",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.ClientID = "client-b"; return c }(),
			want: true,
		},
		{
			name: "Username",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.Username = "other"; return c }(),
			want: true,
		},
		{
			name: "Password",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.Password = "secret2"; return c }(),
			want: true,
		},
		{
			name: "TopicBase",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.TopicBase = "other"; return c }(),
			want: true,
		},
		{
			name: "RawEnabled",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.RawEnabled = false; return c }(),
			want: true,
		},
		{
			name: "DiscoveryEnabled",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.DiscoveryEnabled = false; return c }(),
			want: true,
		},
		{
			name: "PayloadFormat",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.PayloadFormat = "bare"; return c }(),
			want: true,
		},
		{
			name: "SubDevicesEnabled",
			a:    base,
			b:    func() config.NorthMQTT { c := base; c.SubDevicesEnabled = false; return c }(),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mqttDiffersStructurally(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("mqttDiffersStructurally(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
