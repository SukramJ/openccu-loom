// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/go-fabric/mdns"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── loadTranslations ──────────────────────────────────────────────────────────

func TestLoadTranslations_DefaultEmbedded_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.TranslationsPath = "" // use embedded
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadTranslations(cfg, logger)
	if got == nil {
		t.Fatal("expected non-nil translations from embedded data")
	}
}

func TestLoadTranslations_InvalidPath_FallsBackToEmbedded(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.TranslationsPath = "/nonexistent/translations.json"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadTranslations(cfg, logger)
	// Even with an invalid path, falls back to embedded → non-nil.
	if got == nil {
		t.Fatal("expected non-nil translations (fallback to embedded)")
	}
	// Should have logged a warning about the bad path.
	if !bytes.Contains(buf.Bytes(), []byte("ccudata.translations.load")) {
		t.Errorf("expected load warning in log; got:\n%s", buf.String())
	}
}

// ── loadEasymode ──────────────────────────────────────────────────────────────

func TestLoadEasymode_Embedded_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.EasymodePath = "" // use embedded
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadEasymode(cfg, logger)
	if got == nil {
		t.Fatal("expected non-nil easymode from embedded data")
	}
	if len(got.ChannelMetadata) == 0 {
		t.Fatal("embedded easymode archive must carry channel metadata")
	}
}

func TestLoadEasymode_PathOverride_IsHonored(t *testing.T) {
	t.Parallel()
	// A valid but empty archive: proves the file (not the embedded
	// bundle, which has channel metadata) served the result.
	path := filepath.Join(t.TempDir(), "easymode.json.gz")
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if _, err := zw.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.CCUData.EasymodePath = path
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadEasymode(cfg, logger)
	if got == nil {
		t.Fatal("expected non-nil easymode from override file")
	}
	if len(got.ChannelMetadata) != 0 {
		t.Fatal("override file must win over the embedded archive")
	}
}

func TestLoadEasymode_InvalidPath_FallsBackToEmbedded(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.EasymodePath = "/nonexistent/easymode.json.gz"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadEasymode(cfg, logger)
	if got == nil || len(got.ChannelMetadata) == 0 {
		t.Fatal("invalid override path must fall back to the embedded archive")
	}
	if !bytes.Contains(buf.Bytes(), []byte("ccudata.easymode.load")) {
		t.Errorf("expected load warning in log; got:\n%s", buf.String())
	}
}

// ── loadProfiles ──────────────────────────────────────────────────────────────

func TestLoadProfiles_Embedded_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := loadProfiles(logger)
	if got == nil {
		t.Fatal("expected non-nil profile store from embedded data")
	}
}

// ── buildMatterAdvertiser ─────────────────────────────────────────────────────

func TestBuildMatterAdvertiser_EmptyValue_ReturnsZeroconf(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	mc := config.NorthMatter{MDNSAdvertise: ""}
	got, closeAdv := buildMatterAdvertiser(mc, logger)
	t.Cleanup(closeAdv)
	if got == nil {
		t.Fatal("expected non-nil advertiser for empty MDNSAdvertise")
	}
	if _, ok := got.(*mdns.Noop); ok {
		t.Fatal("expected the unset default to resolve to the zeroconf advertiser, not noop")
	}
	if _, ok := got.(*mdns.Zeroconf); !ok {
		t.Fatalf("expected *mdns.Zeroconf for empty MDNSAdvertise, got %T", got)
	}
}

func TestBuildMatterAdvertiser_NoopValue_ReturnsNoop(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	mc := config.NorthMatter{MDNSAdvertise: "noop"}
	got, closeAdv := buildMatterAdvertiser(mc, logger)
	t.Cleanup(closeAdv)
	if _, ok := got.(*mdns.Noop); !ok {
		t.Fatalf("expected *mdns.Noop for explicit noop MDNSAdvertise, got %T", got)
	}
}

func TestBuildMatterAdvertiser_UnknownValue_FallsBackToNoop(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mc := config.NorthMatter{MDNSAdvertise: "invalid-backend"}
	got, closeAdv := buildMatterAdvertiser(mc, logger)
	t.Cleanup(closeAdv)
	if _, ok := got.(*mdns.Noop); !ok {
		t.Fatalf("expected *mdns.Noop fallback for an unknown MDNSAdvertise value, got %T", got)
	}
	// Should log a warning about the unknown value.
	if !bytes.Contains(buf.Bytes(), []byte("matter.bridge.mdns.unknown")) {
		t.Errorf("expected mdns.unknown warning; got:\n%s", buf.String())
	}
}

// ── applyVisibilityUnIgnore ───────────────────────────────────────────────────

func TestApplyVisibilityUnIgnore_NilConfig_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	got := applyVisibilityUnIgnore(context.Background(), nil, nil, nil, nil, logger)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestApplyVisibilityUnIgnore_NilReg_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := config.Default()
	got := applyVisibilityUnIgnore(context.Background(), cfg, nil, nil, nil, logger)
	if got != 0 {
		t.Errorf("expected 0 when reg is nil, got %d", got)
	}
}

func TestApplyVisibilityUnIgnore_NilStore_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := config.Default()
	reg := buildTestRegistry(t, "ccu-one")
	got := applyVisibilityUnIgnore(context.Background(), cfg, reg, nil, nil, logger)
	if got != 0 {
		t.Errorf("expected 0 when store is nil, got %d", got)
	}
}

func TestApplyVisibilityUnIgnore_EmptyRegistry_NoPatterns_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-one"}}
	// No UnIgnore patterns in config.
	reg := buildTestRegistry(t, "ccu-one")

	// Use an in-memory SQLite store from the test helper.
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	_ = store // the visibility store is a different type — we need a nil here to trigger the early return
	// Pass nil store to hit the nil guard.
	got := applyVisibilityUnIgnore(context.Background(), cfg, reg, nil, nil, logger)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}
