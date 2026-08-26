// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const minimalYAML = `
locale: en
data_dir: ./var
centrals:
  - name: ccu-test
    host: 127.0.0.1
    interfaces:
      - HmIP-RF
`

const minimalYAMLDifferent = `
locale: de
data_dir: ./var
centrals:
  - name: ccu-test
    host: 127.0.0.1
    interfaces:
      - HmIP-RF
`

// TestWatcherDetectsChange verifies that mutating the underlying
// file triggers exactly one reload + handler invocation, and that
// [Watcher.Current] surfaces the new config.
func TestWatcherDetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var reloads atomic.Int32
	var lastLocale atomic.Value
	w, cfg, err := NewWatcher(
		path,
		WithInterval(50*time.Millisecond),
		WithHandler(func(_, next *Config) error {
			reloads.Add(1)
			lastLocale.Store(next.Locale)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if cfg.Locale != "en" {
		t.Fatalf("initial Locale=%q want en", cfg.Locale)
	}

	ctx := t.Context()
	go func() { _ = w.Run(ctx) }()

	// Mutate the file. We must change mtime explicitly because
	// rapid same-second writes can leave it unchanged on some
	// filesystems.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte(minimalYAMLDifferent), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reloads.Load() >= 1 && w.Current().Locale == "de" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reloads.Load() < 1 {
		t.Fatal("handler never fired")
	}
	if got := w.Current().Locale; got != "de" {
		t.Errorf("Current().Locale=%q want de", got)
	}
	if v, _ := lastLocale.Load().(string); v != "de" {
		t.Errorf("handler saw Locale=%q want de", v)
	}
}

// TestWatcherKeepsPreviousOnError verifies that when the new config
// fails to parse, the watcher keeps the old config in [Current] and
// does NOT call the handler.
func TestWatcherKeepsPreviousOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var reloads atomic.Int32
	w, cfg, err := NewWatcher(
		path,
		WithInterval(50*time.Millisecond),
		WithHandler(func(*Config, *Config) error {
			reloads.Add(1)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if cfg.Locale != "en" {
		t.Fatalf("seed Locale=%q want en", cfg.Locale)
	}
	ctx := t.Context()
	go func() { _ = w.Run(ctx) }()

	// Corrupt the file.
	if err := os.WriteFile(path, []byte(":\n  this is: not [valid yaml"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	// Wait long enough for the watcher to have stat'd at least once.
	time.Sleep(300 * time.Millisecond)
	if reloads.Load() != 0 {
		t.Errorf("handler fired on bad config (reloads=%d)", reloads.Load())
	}
	if got := w.Current().Locale; got != "en" {
		t.Errorf("Current().Locale=%q want en (must keep previous on parse error)", got)
	}
}
