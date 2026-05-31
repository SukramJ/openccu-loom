// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package diagnostics_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/diagnostics"
)

func TestLoadStartupCapture_EmptyDataDir_ReturnsZero(t *testing.T) {
	t.Parallel()
	cfg, err := diagnostics.LoadStartupCapture("")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg.Enabled {
		t.Fatalf("empty dataDir must yield disabled config, got %+v", cfg)
	}
}

func TestLoadStartupCapture_MissingFile_ReturnsZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := diagnostics.LoadStartupCapture(dir)
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if cfg.Enabled || cfg.DurationS != 0 {
		t.Fatalf("expected zero config, got %+v", cfg)
	}
}

func TestLoadStartupCapture_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := diagnostics.StartupCaptureConfig{Enabled: true, DurationS: 60, Anonymise: true}
	raw, _ := json.Marshal(want)
	if err := os.WriteFile(filepath.Join(dir, diagnostics.StartupCaptureFileName), raw, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := diagnostics.LoadStartupCapture(dir)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestLoadStartupCapture_MalformedJSON_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, diagnostics.StartupCaptureFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := diagnostics.LoadStartupCapture(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadStartupCapture_ReadError_ReturnsError(t *testing.T) {
	t.Parallel()
	// Make the file unreadable by pointing at a directory of that name.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, diagnostics.StartupCaptureFileName), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := diagnostics.LoadStartupCapture(dir); err == nil {
		t.Fatal("expected read error on directory-as-file")
	}
}

func TestSaveStartupCapture_EmptyDataDir_ReturnsError(t *testing.T) {
	t.Parallel()
	if err := diagnostics.SaveStartupCapture("", diagnostics.StartupCaptureConfig{}); err == nil {
		t.Fatal("expected error on empty dataDir")
	}
}

func TestSaveStartupCapture_HappyPath_RoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := diagnostics.StartupCaptureConfig{Enabled: true, DurationS: 120, Anonymise: false}
	if err := diagnostics.SaveStartupCapture(dir, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := diagnostics.LoadStartupCapture(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	// Confirm the on-disk permissions (0600 was chosen deliberately).
	// Windows does not honour Unix permission bits — os.Stat reports a
	// synthetic mode (0666/0444), so the bit-for-bit check is Unix-only.
	st, err := os.Stat(filepath.Join(dir, diagnostics.StartupCaptureFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("file perm = %o, want 0600", perm)
		}
	}
}

func TestSaveStartupCapture_MkdirError_ReturnsError(t *testing.T) {
	t.Parallel()
	// Create a file at the target path so MkdirAll cannot
	// promote it to a directory.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("plain file"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := diagnostics.SaveStartupCapture(blocked, diagnostics.StartupCaptureConfig{}); err == nil {
		t.Fatal("expected mkdir error when dataDir collides with a file")
	}
}
