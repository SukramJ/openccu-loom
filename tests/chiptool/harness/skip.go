// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// ChipToolBinaryEnv is the env var that pins a non-PATH `chip-tool`
// location. Snap-installed `chip-tool` lives under
// `/snap/bin/chip-tool` which is on PATH for interactive shells but
// often not for CI runners; setting this var lets a runner point at
// the binary directly.
const ChipToolBinaryEnv = "OPENCCU_LOOM_CHIPTOOL_BIN"

// DaemonBinaryEnv overrides the location of the openccu-loom daemon
// binary the harness spawns. Default: `./bin/openccu-loom` relative
// to the repo root.
const DaemonBinaryEnv = "OPENCCU_LOOM_CHIPTOOL_DAEMON"

// RequireChipTool skips the test when chip-tool is not reachable.
// Returns the resolved absolute path on success so tests can pass
// it into [Run].
func RequireChipTool(t *testing.T) string {
	t.Helper()
	if p := os.Getenv(ChipToolBinaryEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("chip-tool: %s=%q not usable: %v", ChipToolBinaryEnv, p, err)
		}
		return p
	}
	p, err := exec.LookPath("chip-tool")
	if err != nil {
		t.Skipf("chip-tool not in PATH; install the snap (`snap install chip-tool`) or set %s", ChipToolBinaryEnv)
	}
	return p
}

// RequireDaemonBinary resolves the openccu-loom daemon binary the
// harness execs. It does NOT build automatically — the user runs
// `make build` (or `make chiptool-test` which builds first). A
// missing binary fails the test loudly with a clear next step so a
// developer never sees an obscure exec error.
func RequireDaemonBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv(DaemonBinaryEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s=%q not usable: %v", DaemonBinaryEnv, p, err)
		}
		return p
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	bin := filepath.Join(repoRoot, "bin", "openccu-loom")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("openccu-loom binary not found at %s: %v\n"+
			"run `make build` first (or `make chiptool-test`, which builds before testing); "+
			"alternatively set %s to a pre-built daemon binary",
			bin, err, DaemonBinaryEnv)
	}
	return bin
}
