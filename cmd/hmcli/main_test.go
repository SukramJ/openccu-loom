// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionPrintsBuild(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "hmcli") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestConfigValidateAccepts(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cfg.yaml")
	_ = os.WriteFile(tmp, []byte("locale: de\ncentrals:\n  - name: ccu\n    host: h\n    interfaces: [HmIP-RF]\n"), 0o600)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"config", "validate", tmp}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok:") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestConfigValidateRejectsInvalid(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cfg.yaml")
	_ = os.WriteFile(tmp, []byte("logging:\n  level: lol\n  format: text\n"), 0o600)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"config", "validate", tmp}, &stdout, &stderr); err == nil {
		t.Fatal("expected error")
	}
}

func TestUnknownSubcommandRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"nope"}, &stdout, &stderr); err == nil {
		t.Fatal("expected error")
	}
}

func TestMissingSubcommandPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "Usage") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

// ─── Subcommand routing ───────────────────────────────────────────────────────

func TestVersionFlagLongFormWorks(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(stdout.String(), "hmcli") {
		t.Fatalf("stdout does not contain 'hmcli': %q", stdout.String())
	}
}

func TestVersionFlagShortFormWorks(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-v"}, &stdout, &stderr); err != nil {
		t.Fatalf("-v: %v", err)
	}
	if !strings.Contains(stdout.String(), "hmcli") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestHelpSubcommandPrintsUsageToStdout(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Fatalf("help output missing 'Usage': %q", stdout.String())
	}
}

func TestHelpFlagLongFormPrintsUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Fatalf("--help output missing 'Usage': %q", stdout.String())
	}
}

func TestHelpFlagShortFormPrintsUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, &stdout, &stderr); err != nil {
		t.Fatalf("-h: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Fatalf("-h output missing 'Usage': %q", stdout.String())
	}
}

// ─── Error exit codes (non-zero on bad args) ──────────────────────────────────

func TestUnknownSubcommandReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"bogus-command"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown subcommand, got nil")
	}
	if !strings.Contains(err.Error(), "bogus-command") {
		t.Errorf("error should mention the bad subcommand, got: %v", err)
	}
}

func TestMissingSubcommandReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run(nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no subcommand given")
	}
}

func TestMissingSubcommandPrintsUsageToStderr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	_ = run(nil, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "Usage") {
		t.Fatalf("missing-subcommand must print usage to stderr, got: %q", stderr.String())
	}
}

func TestUnknownSubcommandPrintsUsageToStderr(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	_ = run([]string{"doesnotexist"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "Usage") {
		t.Fatalf("unknown subcommand must print usage to stderr, got: %q", stderr.String())
	}
}

// ─── config subcommand ────────────────────────────────────────────────────────

func TestConfigMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"config"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when config has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestConfigUnknownOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"config", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown config operation")
	}
}

func TestConfigValidateMissingPathReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"config", "validate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no path given to config validate")
	}
}

func TestConfigValidateNonExistentFileReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"config", "validate", "/no/such/file/config.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-existent config file")
	}
}

func TestConfigValidateOutputContainsCentralCount(t *testing.T) {
	t.Parallel()
	tmp := filepath.Join(t.TempDir(), "test.yaml")
	_ = os.WriteFile(tmp, []byte("locale: en\ncentrals:\n  - name: c1\n    host: 192.168.1.1\n    interfaces: [HmIP-RF]\n  - name: c2\n    host: 192.168.1.2\n    interfaces: [BidCos-RF]\n"), 0o600)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"config", "validate", tmp}, &stdout, &stderr); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(stdout.String(), "2") {
		t.Fatalf("expected '2' in output (2 centrals), got: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "locale=en") {
		t.Fatalf("expected locale in output, got: %q", stdout.String())
	}
}

// ─── Version output format ────────────────────────────────────────────────────

func TestVersionOutputContainsOSAndArch(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("version: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, runtime.GOOS) {
		t.Errorf("version output missing GOOS %q: %q", runtime.GOOS, out)
	}
	if !strings.Contains(out, runtime.GOARCH) {
		t.Errorf("version output missing GOARCH %q: %q", runtime.GOARCH, out)
	}
}

// ─── Help text completeness ───────────────────────────────────────────────────

func TestHelpTextMentionsAllSubcommands(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"version", "config", "validate", "cache"} {
		if !strings.Contains(out, want) {
			t.Errorf("help text missing %q: %q", want, out)
		}
	}
}
