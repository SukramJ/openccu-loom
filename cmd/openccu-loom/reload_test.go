// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestHotReloadHandlerLogsAppliedAndRestartRequired verifies that the
// reload handler correctly classifies a mixed diff: a logging-level
// change is hot-applied (info log), a `north.rest.listen` change is
// flagged as restart-required (warn log).
func TestHotReloadHandlerLogsAppliedAndRestartRequired(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	prev := config.Default()
	next := config.Default()
	next.Logging.Level = "debug"      // hot-reloadable
	next.North.REST.Listen = ":18080" // restart-required

	if err := hotReloadHandler(logger, nil)(prev, next); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "daemon.reload.logging") {
		t.Errorf("expected daemon.reload.logging entry, got:\n%s", out)
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
	case <-time.After(20 * time.Second):
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
	case <-time.After(20 * time.Second):
		t.Fatal("daemonServeWithReload did not return in time")
	}
}

// TestHotReloadHandler_AllRestartRequiredFields exercises every
// restart-required field to confirm none causes a panic and all are
// detected and logged.
func TestHotReloadHandler_AllRestartRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	prev := config.Default()
	next := config.Default()

	// data_dir
	next.DataDir = "/tmp/changed"
	// locale
	next.Locale = "de"
	// callback (change port so struct comparison finds a diff)
	next.Callback.Port = 9999
	// centrals count
	next.Centrals = append(next.Centrals, config.CentralConfig{Name: "extra", Host: "10.0.0.99"})

	if err := hotReloadHandler(logger, nil)(prev, next); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	out := buf.String()
	for _, field := range []string{"data_dir", "locale", "callback", "centrals.count"} {
		if !strings.Contains(out, field) {
			t.Errorf("expected restart_required for field %q, got:\n%s", field, out)
		}
	}
}
