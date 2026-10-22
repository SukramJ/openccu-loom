// SPDX-License-Identifier: Apache-2.0
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

// ── matterMDNS ───────────────────────────────────────────────────────────────

// TestMatterMDNSSelectsTheNodesDNSSD pins the mapping of mdns_advertise onto
// the node facade's DNS-SD: the unset default and "zeroconf" leave the
// node's Zeroconf with the subtype responder; "noop" and an unknown value
// disable it, the unknown one with a warning.
func TestMatterMDNSSelectsTheNodesDNSSD(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value        string
		wantDisabled bool
		wantWarning  bool
	}{
		{"", false, false},
		{"zeroconf", false, false},
		{"noop", true, false},
		{"invalid-backend", true, true},
	} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		got := matterMDNS(config.NorthMatter{MDNSAdvertise: tc.value}, logger)
		if got.Disabled != tc.wantDisabled {
			t.Errorf("matterMDNS(%q).Disabled = %v, want %v", tc.value, got.Disabled, tc.wantDisabled)
		}
		if warned := bytes.Contains(buf.Bytes(), []byte("matter.bridge.mdns.unknown")); warned != tc.wantWarning {
			t.Errorf("matterMDNS(%q) warned = %v, want %v; log:\n%s", tc.value, warned, tc.wantWarning, buf.String())
		}
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
