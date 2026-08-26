// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLogLevel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want slog.Level
	}{
		{"debug", "debug", slog.LevelDebug},
		{"warn", "warn", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"info maps explicitly", "info", slog.LevelInfo},
		{"empty defaults to info", "", slog.LevelInfo},
		{"unknown value defaults to info", "trace", slog.LevelInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logLevel(tc.in); got != tc.want {
				t.Errorf("logLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunMissingOptionsFile exercises run() end to end for the one path
// that needs no listener at all: LoadOptions fails before the http.Server
// is ever built. run() registers its flags on the package-level flag.
// CommandLine, so the test swaps in a fresh FlagSet (and restores it
// afterward) to keep this test safe to run alongside others in the
// package without a "flag redefined" panic.
func TestRunMissingOptionsFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")

	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"openccu-loom-remote", "-options", missing}

	if got := run(); got != 1 {
		t.Errorf("run() = %d, want 1 for a missing options file", got)
	}
}
