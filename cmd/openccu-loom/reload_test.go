// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestHotReloadHandlerLogsAppliedAndRestartRequired verifies that the
// reload handler correctly classifies a mixed diff: an MQTT broker change is
// hot-applied (the supervisor rebuilds the stack), a `north.rest.listen`
// change is flagged as restart-required (warn log).
func TestHotReloadHandlerLogsAppliedAndRestartRequired(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := context.Background()
	sup := newMQTTSupervisor(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), health.NewTracker(), nil)
	startCfg := config.Default()
	startCfg.North.MQTT.Enabled = true
	if err := sup.Start(ctx, startCfg); err != nil {
		t.Fatalf("start mqtt supervisor: %v", err)
	}
	t.Cleanup(func() { sup.Shutdown(ctx) })
	deps := newReloadDeps()
	deps.SetMQTTSupervisor(sup)

	prev := config.Default()
	prev.North.MQTT.Enabled = true
	next := config.Default()
	next.North.MQTT.Enabled = true
	next.North.MQTT.TopicBase = "newbase" // hot-reloadable
	next.North.REST.Listen = ":18080"     // restart-required

	if err := hotReloadHandler(logger, deps)(prev, next); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "daemon.reload.mqtt_swapped") {
		t.Errorf("expected daemon.reload.mqtt_swapped entry, got:\n%s", out)
	}
	if !strings.Contains(out, "daemon.reload.restart_required") ||
		!strings.Contains(out, "field=north.rest.listen") {
		t.Errorf("expected restart_required for north.rest.listen, got:\n%s", out)
	}
	if !strings.Contains(out, "hot_reloaded_fields=1") {
		t.Errorf("expected hot_reloaded_fields=1, got:\n%s", out)
	}
	if !strings.Contains(out, "restart_required_fields=1") {
		t.Errorf("expected restart_required_fields=1, got:\n%s", out)
	}
}

// TestHotReloadHandlerIdempotent verifies that an unchanged diff
// emits zero applied / zero restart-required.
func TestHotReloadHandlerIdempotent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Default()

	if err := hotReloadHandler(logger, nil)(cfg, cfg); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "hot_reloaded_fields=0") ||
		!strings.Contains(out, "restart_required_fields=0") {
		t.Errorf("expected zero counts on idempotent diff, got:\n%s", out)
	}
	if strings.Contains(out, "daemon.reload.restart_required") {
		t.Errorf("idempotent diff must not log restart_required, got:\n%s", out)
	}
}

// TestHotReloadHandlerNilDiffNoOp verifies that a nil prev or next
// short-circuits cleanly without panicking — defensive against the
// watcher firing before the initial-config pointer is stored.
func TestHotReloadHandlerNilDiffNoOp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if err := hotReloadHandler(logger, nil)(nil, config.Default()); err != nil {
		t.Errorf("nil prev: %v", err)
	}
	if err := hotReloadHandler(logger, nil)(config.Default(), nil); err != nil {
		t.Errorf("nil next: %v", err)
	}
}

// TestDaemonServeWithReload_EmptyConfigPath_CallsDaemonServe verifies
// that an empty configPath bypasses the watcher and delegates directly
// to daemonServe (which returns nil on clean cancellation).
func TestDaemonServeWithReload_EmptyConfigPath_CallsDaemonServe(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	// The REST listener starts regardless of Enabled (ADR 0044 folds the
	// bootstrap surface into it), so pin an ephemeral port — the fixed
	// default makes parallel daemon tests race for :8119.
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(false)
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServeWithReload(ctx, cfg, "", &bytes.Buffer{}, &bytes.Buffer{}) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServeWithReload (empty path): %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("daemonServeWithReload did not return in time")
	}
}

// TestDaemonServeWithReload_WithConfigPath_StartsWatcher verifies that
// a non-empty configPath starts the file watcher and the daemon still
// shuts down cleanly when the context is cancelled.
func TestDaemonServeWithReload_WithConfigPath_StartsWatcher(t *testing.T) {
	t.Parallel()

	// Write a minimal valid config file that daemonServe can load.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "openccu-loom.yaml")
	if err := os.WriteFile(cfgPath, []byte("data_dir: "+dir+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	// The REST listener starts regardless of Enabled (ADR 0044 folds the
	// bootstrap surface into it), so pin an ephemeral port — the fixed
	// default makes parallel daemon tests race for :8119.
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(false)
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.DataDir = dir

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServeWithReload(ctx, cfg, cfgPath, &bytes.Buffer{}, &bytes.Buffer{}) }()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServeWithReload (with path): %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("daemonServeWithReload did not return in time")
	}
}

// TestConfigFileWatcherReportsRestartRequiredEditsFromTheRuleTable drives the
// production pair — [config.NewWatcher] plus [hotReloadHandler], exactly as
// daemonServeWithReload assembles them — over a real edit of a real config
// file, and asserts the operator is told the truth about it.
//
// The edit switches the Matter bridge on and the Basic-auth gate off. Both
// are boot-wired, both are in the restart rule table, and neither is applied
// by the reload. Before the punch-list was derived from that table, the only
// records were `restart_required_fields=0` and `config.watch.reloaded` — an
// affirmative "your edit is live" for two settings that existed nowhere but
// in the file.
func TestConfigFileWatcherReportsRestartRequiredEditsFromTheRuleTable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "openccu-loom.yaml")
	const before = "data_dir: " + "%s" + `
north:
  matter:
    enabled: false
  rest:
    auth:
      basic_enabled: true
`
	if err := os.WriteFile(cfgPath, fmt.Appendf(nil, before, dir), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf lockedBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	w, _, err := config.NewWatcher(cfgPath,
		config.WithLogger(logger),
		config.WithInterval(time.Second),
		config.WithHandler(hotReloadHandler(logger, nil)))
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	const after = "data_dir: " + "%s" + `
north:
  matter:
    enabled: true
  rest:
    auth:
      basic_enabled: false
`
	if err := os.WriteFile(cfgPath, fmt.Appendf(nil, after, dir), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out = buf.String()
		if strings.Contains(out, "daemon.reload.applied") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out, "daemon.reload.applied") {
		t.Fatalf("the watcher never reported the edit; log:\n%s", out)
	}
	for _, field := range []string{"north.matter.enabled", "north.rest.auth.basic_enabled"} {
		if !strings.Contains(out, "field="+field) {
			t.Errorf("no daemon.reload.restart_required for %s — the edit is reported as reloaded "+
				"while the setting stays at its boot value; log:\n%s", field, out)
		}
	}
	if !strings.Contains(out, "restart_required_fields=2") {
		t.Errorf("want restart_required_fields=2 in the summary; log:\n%s", out)
	}
}

// lockedBuffer is a [bytes.Buffer] safe for the watcher goroutine writing
// while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestHotReloadHandler_AllRestartRequiredFields exercises every
// restart-required field to confirm none causes a panic and all are
// detected and logged.
func TestHotReloadHandler_AllRestartRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	prev := config.Default()
	prev.Centrals = []config.CentralConfig{{Name: "ccu1", Host: "10.0.0.1"}}
	next := config.Default()
	next.Centrals = []config.CentralConfig{{Name: "ccu1", Host: "10.0.0.1"}}

	// data_dir
	next.DataDir = "/tmp/changed"
	// locale
	next.Locale = "de"
	// callback (change port so struct comparison finds a diff)
	next.Callback.Port = 9999
	// centrals: modify a central present in both prev and next in place
	// (add/remove of a central is a live operation and is intentionally
	// NOT restart-required — see TestHotReloadHandler_CentralsAdded).
	next.Centrals[0].Host = "10.0.0.2"

	if err := hotReloadHandler(logger, nil)(prev, next); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	out := buf.String()
	for _, field := range []string{"data_dir", "locale", "callback", "centrals"} {
		if !strings.Contains(out, field) {
			t.Errorf("expected restart_required for field %q, got:\n%s", field, out)
		}
	}
}
