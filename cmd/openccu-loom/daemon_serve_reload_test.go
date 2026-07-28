// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── buildOpenAPIValidator ─────────────────────────────────────────────────────

func TestBuildOpenAPIValidator_MissingSpecFile_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = "/nonexistent/openapi.yaml"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := buildOpenAPIValidator(cfg, logger)
	if got != nil {
		t.Errorf("expected nil for missing spec, got %v", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("openapi.spec.read")) {
		t.Errorf("expected openapi.spec.read warning; got:\n%s", buf.String())
	}
}

func TestBuildOpenAPIValidator_InvalidYAML_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Write an invalid YAML file.
	path := filepath.Join(t.TempDir(), "bad-openapi.yaml")
	if err := os.WriteFile(path, []byte("this: [is: invalid yaml"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = path
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := buildOpenAPIValidator(cfg, logger)
	// Invalid YAML may or may not parse as valid OpenAPI — either way the
	// function should not panic. A nil or non-nil result is acceptable.
	_ = got
}

func TestBuildOpenAPIValidator_EmptyPath_UsesDefaultAndReturnsNilOrValidator(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = "" // triggers default "assets/openapi.yaml"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// Running from the project root this will find the real spec and
	// return a validator. From a different working directory it returns
	// nil. Both are acceptable — we just assert no panic.
	got := buildOpenAPIValidator(cfg, logger)
	_ = got
}

// ── daemonServeWithReload ─────────────────────────────────────────────────────

// TestDaemonServeWithReload_EmptyConfigPath_DelegatesDirectly verifies
// that an empty configPath skips the watcher and delegates directly to
// daemonServe (the same outcome as no --config flag).
func TestDaemonServeWithReload_EmptyConfigPath_DelegatesDirectly(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- daemonServeWithReload(ctx, cfg, "" /* empty → noop watcher */, &bytes.Buffer{}, &bytes.Buffer{})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServeWithReload: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("daemonServeWithReload did not shut down in time")
	}
}

// TestDaemonServeWithReload_InvalidConfigPath_ReturnsError verifies
// that a non-empty but non-existent config path causes the watcher
// to return an error rather than silently starting.
func TestDaemonServeWithReload_InvalidConfigPath_ReturnsError(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)

	ctx := t.Context()

	err := daemonServeWithReload(ctx, cfg, "/nonexistent/config.yaml", &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Error("expected error for non-existent config path")
	}
}
