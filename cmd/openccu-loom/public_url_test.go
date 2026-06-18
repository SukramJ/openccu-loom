// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestWriteConfigUIHint_Writes verifies that a non-empty URL is written
// to <dataDir>/public_url with a trailing newline.
func TestWriteConfigUIHint_Writes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logger := discardLogger()

	writeConfigUIHint(tmp, "https://loom.example.de/app/", logger)

	got, err := os.ReadFile(filepath.Join(tmp, publicURLHintFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	const want = "https://loom.example.de/app/\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", string(got), want)
	}
}

// TestWriteConfigUIHint_Overwrites verifies that writing a second URL
// replaces the first.
func TestWriteConfigUIHint_Overwrites(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logger := discardLogger()

	writeConfigUIHint(tmp, "https://old.example.de", logger)
	writeConfigUIHint(tmp, "https://new.example.de", logger)

	got, err := os.ReadFile(filepath.Join(tmp, publicURLHintFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	const want = "https://new.example.de\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", string(got), want)
	}
}

// TestWriteConfigUIHint_Removes verifies that an empty URL removes the
// hint file that was previously written.
func TestWriteConfigUIHint_Removes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logger := discardLogger()

	writeConfigUIHint(tmp, "https://loom.example.de", logger)
	writeConfigUIHint(tmp, "", logger)

	_, err := os.Stat(filepath.Join(tmp, publicURLHintFile))
	if !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, got Stat err: %v", err)
	}
}

// TestWriteConfigUIHint_RemovesNonExistent verifies that removing a
// hint file that does not exist is a no-op and does not panic.
func TestWriteConfigUIHint_RemovesNonExistent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logger := discardLogger()

	// Must not panic; hint file was never written.
	writeConfigUIHint(tmp, "", logger)

	_, err := os.Stat(filepath.Join(tmp, publicURLHintFile))
	if !os.IsNotExist(err) {
		t.Errorf("expected file to be absent, got Stat err: %v", err)
	}
}
