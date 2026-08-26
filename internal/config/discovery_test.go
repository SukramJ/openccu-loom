// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

func TestConfigSearchPaths_Order(t *testing.T) {
	t.Setenv(config.ConfigEnvVar, "/tmp/explicit.yaml")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got := config.SearchPaths()
	want := []string{
		"/tmp/explicit.yaml",
		"config.yaml",
		// SearchPaths builds the XDG entry with filepath.Join, so it
		// carries the OS-native separator (backslashes on Windows).
		// FromSlash mirrors that without feeding Join a separator-laden
		// first segment (which gocritic's filepathJoin flags).
		filepath.FromSlash("/xdg/openccu-loom/config.yaml"),
		"/etc/openccu-loom/config.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestConfigSearchPaths_NoEnvOverride(t *testing.T) {
	t.Setenv(config.ConfigEnvVar, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got := config.SearchPaths()
	if len(got) == 0 || got[0] != "config.yaml" {
		t.Fatalf("without %s the first candidate must be ./config.yaml, got %v", config.ConfigEnvVar, got)
	}
	for _, p := range got {
		if p == "" {
			t.Errorf("empty candidate path in %v", got)
		}
	}
}

func TestDiscoverConfigPath_EnvOverrideWins(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yaml")
	if err := os.WriteFile(explicit, []byte("data_dir: /data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.ConfigEnvVar, explicit)

	if got := config.DiscoverConfigPath(); got != explicit {
		t.Errorf("DiscoverConfigPath() = %q, want %q", got, explicit)
	}
}

func TestDiscoverConfigPath_CwdFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigEnvVar, "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))
	t.Chdir(dir)
	if err := os.WriteFile("config.yaml", []byte("data_dir: /data\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := config.DiscoverConfigPath(); got != "config.yaml" {
		t.Errorf("DiscoverConfigPath() = %q, want %q", got, "config.yaml")
	}
}

func TestDiscoverConfigPath_NoneFound(t *testing.T) {
	if _, err := os.Stat("/etc/openccu-loom/config.yaml"); err == nil {
		t.Skip("a system-wide /etc/openccu-loom/config.yaml exists; cannot assert the empty case")
	}
	dir := t.TempDir()
	t.Setenv(config.ConfigEnvVar, filepath.Join(dir, "does-not-exist.yaml"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))
	t.Chdir(dir) // no config.yaml here

	if got := config.DiscoverConfigPath(); got != "" {
		t.Errorf("DiscoverConfigPath() = %q, want \"\" (no file should be found)", got)
	}
}

// A directory named config.yaml must not be mistaken for a config file.
func TestDiscoverConfigPath_IgnoresDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigEnvVar, "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))
	t.Chdir(dir)
	if err := os.Mkdir("config.yaml", 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/etc/openccu-loom/config.yaml"); err == nil {
		t.Skip("system-wide config present")
	}
	if got := config.DiscoverConfigPath(); got != "" {
		t.Errorf("DiscoverConfigPath() = %q, want \"\" (a directory is not a config file)", got)
	}
}
