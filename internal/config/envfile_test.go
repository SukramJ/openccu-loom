// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFile_MissingIsNoError(t *testing.T) {
	if err := LoadEnvFile(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("missing file should be tolerated, got %v", err)
	}
}

func TestLoadEnvFile_EmptyPathIsNoOp(t *testing.T) {
	if err := LoadEnvFile(""); err != nil {
		t.Fatalf("empty path should be tolerated, got %v", err)
	}
}

func TestParseEnvFile_AllFormsRoundTrip(t *testing.T) {
	body := strings.NewReader(`
# leading comment
PLAIN=value
QUOTED="quoted value"
SQUOTED='single quoted'
ESCAPED="line\nbreak\twith\\backslash"
WITH_HASH="value # with hash"
INLINE=hello # trailing comment
EMPTY=
export EXPORTED=ok
  WHITESPACE   =   trimmed
`)
	captured := map[string]string{}
	get := func(string) string { return "" }
	set := func(k, v string) error { captured[k] = v; return nil }
	if err := parseEnvFile(body, "test", get, set); err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	cases := map[string]string{
		"PLAIN":      "value",
		"QUOTED":     "quoted value",
		"SQUOTED":    "single quoted",
		"ESCAPED":    "line\nbreak\\twith\\backslash",
		"WITH_HASH":  "value # with hash",
		"INLINE":     "hello",
		"EMPTY":      "",
		"EXPORTED":   "ok",
		"WHITESPACE": "trimmed",
	}
	for k, want := range cases {
		if got := captured[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestParseEnvFile_ExistingEnvWins(t *testing.T) {
	body := strings.NewReader("SECRET=from-file")
	get := func(k string) string {
		if k == "SECRET" {
			return "from-process"
		}
		return ""
	}
	written := false
	set := func(string, string) error { written = true; return nil }
	if err := parseEnvFile(body, "t", get, set); err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("env-file should not overwrite a process-env value")
	}
}

func TestParseEnvFile_MalformedLineErrors(t *testing.T) {
	body := strings.NewReader("OK=fine\nNOT_KV_LINE\n")
	err := parseEnvFile(body, "t", func(string) string { return "" }, func(string, string) error { return nil })
	if !errors.Is(err, ErrEnvFileSyntax) {
		t.Fatalf("expected ErrEnvFileSyntax, got %v", err)
	}
}

func TestLoadEnvFile_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := "OPENCCU_LOOM_TEST_KEY=hello\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Ensure the var is clean before the load.
	t.Setenv("OPENCCU_LOOM_TEST_KEY", "")
	_ = os.Unsetenv("OPENCCU_LOOM_TEST_KEY")
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OPENCCU_LOOM_TEST_KEY"); got != "hello" {
		t.Errorf("env var not populated: got %q", got)
	}
}

func TestBootstrapEnvFileDefault(t *testing.T) {
	b := DefaultBootstrap()
	if b.EnvFile != DefaultEnvFile {
		t.Errorf("default EnvFile = %q, want %q", b.EnvFile, DefaultEnvFile)
	}
	if !b.EnvFileEnabled() {
		t.Error("default should be enabled")
	}
}

func TestBootstrapEnvFileDisableSentinels(t *testing.T) {
	for _, sentinel := range []string{"-", "/dev/null"} {
		b := &BootstrapConfig{EnvFile: sentinel}
		if b.EnvFileEnabled() {
			t.Errorf("%q should disable env-file loading", sentinel)
		}
	}
}
