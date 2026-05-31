// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCLIVersion asserts that ./bin/hmcli prints a version envelope
// on every documented invocation form. The CLI is the operator's
// debugging surface — version reporting is the smallest invariant
// we want to keep stable.
func TestCLIVersion(t *testing.T) {
	t.Parallel()
	bin := locateHmcli(t)

	for _, args := range [][]string{
		{"version"},
		{"--version"},
		{"-v"},
	} {
		out, err := runCLI(t, bin, args...)
		if err != nil {
			t.Errorf("hmcli %s: %v\n%s", strings.Join(args, " "), err, out)
			continue
		}
		if !strings.HasPrefix(out, "hmcli ") {
			t.Errorf("hmcli %s: stdout %q does not start with %q", args, out, "hmcli ")
		}
	}
}

// TestCLIConfigValidate asserts that `hmcli config validate <path>`
// accepts a well-formed YAML and rejects malformed input. This
// covers the sub-command parser plus the daemon's config loader on
// the operator's side — the same loader runs at daemon startup.
func TestCLIConfigValidate(t *testing.T) {
	t.Parallel()
	bin := locateHmcli(t)

	good := writeTempConfig(t, `
locale: en
data_dir: /tmp/openccu-loom-test
north:
  rest:
    enabled: true
    listen: ":0"
centrals:
  - name: ccu-cli-test
    host: 127.0.0.1
    interfaces:
      - HmIP-RF
`)
	if out, err := runCLI(t, bin, "config", "validate", good); err != nil {
		t.Errorf("hmcli config validate %s: %v\n%s", good, err, out)
	}

	bad := writeTempConfig(t, "this is: not: yaml: at all\n  unbalanced indentation\n")
	out, err := runCLI(t, bin, "config", "validate", bad)
	if err == nil {
		t.Errorf("hmcli config validate %s: expected non-zero exit, got success\n%s", bad, out)
	}
}

// TestCLIHelp asserts that running with no subcommand or with
// `help` prints the usage envelope. Operators rely on this to
// discover the CLI surface at first contact.
func TestCLIHelp(t *testing.T) {
	t.Parallel()
	bin := locateHmcli(t)

	// No arguments → exits non-zero with usage on stderr.
	out, err := runCLI(t, bin)
	if err == nil {
		t.Errorf("hmcli (no args): expected non-zero exit\n%s", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("hmcli (no args): stderr missing %q\n%s", "Usage:", out)
	}

	// `help` → exits zero with usage on stdout.
	helpOut, helpErr := runCLI(t, bin, "help")
	if helpErr != nil {
		t.Errorf("hmcli help: %v\n%s", helpErr, helpOut)
	}
	if !strings.Contains(helpOut, "Usage:") {
		t.Errorf("hmcli help: stdout missing %q\n%s", "Usage:", helpOut)
	}
}

// ─────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────

// locateHmcli resolves ./bin/hmcli relative to the repo root.
// Mirrors locateDaemonBinary in tests/e2e/harness/daemon.go.
func locateHmcli(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("OPENCCU_LOOM_E2E_HMCLI"); p != "" {
		return p
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	bin := filepath.Join(repoRoot, "bin", "hmcli")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("hmcli binary not found at %s: %v\n"+
			"run `make build-all` before `make e2e`, or set OPENCCU_LOOM_E2E_HMCLI",
			bin, err)
	}
	return bin
}

// runCLI executes bin with args and returns the merged stdout +
// stderr output. Returns the underlying *exec.ExitError when the
// command exits non-zero so callers can branch on success.
func runCLI(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// writeTempConfig writes content to a fresh temp file and returns
// its path. The harness's t.TempDir reaps the file when the test
// finishes.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
