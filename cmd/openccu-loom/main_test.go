// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersionSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(version) returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "openccu-loom") {
		t.Fatalf("version output missing product name: %q", stdout.String())
	}
}

func TestRunRunFlagVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(run --version) returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "openccu-loom") {
		t.Fatalf("run --version output missing product name: %q", stdout.String())
	}
}

func TestRunUnknownSubcommandErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"bogus"}, &stdout, &stderr); err == nil {
		t.Fatal("expected error for unknown subcommand, got nil")
	}
}
