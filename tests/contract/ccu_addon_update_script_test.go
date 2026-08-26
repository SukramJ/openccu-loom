// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// updateScriptStub is a POSIX-sh no-op replacement for a system command the
// CCU add-on's update_script shells out to. Written once per test run into a
// directory prepended to PATH so the real system utilities are never
// invoked — the script's own file operations (mkdir/cp/chmod/sync) target
// /usr/local, which is neither writable nor desirable to touch from a test.
const updateScriptStub = "#!/bin/sh\nexit 0\n"

// updateScriptUnameStub reports a CPU architecture the script's `case`
// selector recognizes (armv7l → openccu-loom.armv7), so the ARCH-detection
// step never hits the "Unsupported architecture" exit 1 branch and the
// platform-identifier branch under test ($1) is the only thing that
// determines the exit code.
const updateScriptUnameStub = "#!/bin/sh\ncase \"$1\" in\n  -m) echo armv7l ;;\n  *) echo unknown ;;\nesac\nexit 0\n"

// TestCCUAddonUpdateScriptExitCodeContract locks the exit-code contract
// RaspberryMatic/OpenCCU's /bin/install_addon relies on: 0 means "installed
// without reboot", 10 means "reboot required" and anything else means the
// platform identifier ($1) was rejected outright. This is the exact
// contract that regressed in the 0.27.1 bugfix (see CHANGELOG.md) — a
// silent flip between 0 and 10 reboots CCU3 needlessly or leaves a
// RaspberryMatic install half-started.
//
// The subprocess environment is hermetic: PATH is pointed at stub
// replacements for every external command the script invokes
// (uname/mount/lcdtool/cp/mkdir/chmod/sync) so the real filesystem under
// /usr/local is never touched, and the script's own relative source
// directories (addon/, rc.d/, www/, etc/) are seeded as empty fixtures in a
// scratch working directory.
func TestCCUAddonUpdateScriptExitCodeContract(t *testing.T) {
	t.Parallel()

	scriptPath, err := filepath.Abs("../../packaging/ccu-addon/ccu/update_script")
	if err != nil {
		t.Fatalf("resolve update_script path: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("update_script not found at %s: %v", scriptPath, err)
	}

	stubDir := writeUpdateScriptStubs(t)
	workDir := writeUpdateScriptFixtures(t)

	cases := []struct {
		// name documents the platform identifier the CCU/RaspberryMatic
		// installer passes as $1.
		name     string
		arg      string
		wantCode int
	}{
		{name: "unset platform identifier is rejected", arg: "", wantCode: 1},
		{name: "CCU2 is unsupported", arg: "CCU2", wantCode: 1},
		{name: "RaspberryMatic installs inline, no reboot", arg: "HM-RASPBERRYMATIC", wantCode: 0},
		{name: "stock CCU3 requires a reboot", arg: "CCU3", wantCode: 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cmd *exec.Cmd
			if tc.arg == "" {
				cmd = exec.Command("sh", scriptPath)
			} else {
				cmd = exec.Command("sh", scriptPath, tc.arg)
			}
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(), "PATH="+stubDir+":"+os.Getenv("PATH"))

			out, runErr := cmd.CombinedOutput()
			gotCode := exitCodeOf(t, runErr)
			if gotCode != tc.wantCode {
				t.Fatalf("update_script %q exit code = %d, want %d\noutput:\n%s", tc.arg, gotCode, tc.wantCode, out)
			}
		})
	}
}

// exitCodeOf extracts the process exit code from the error returned by
// exec.Cmd.CombinedOutput/Run — nil means exit 0, an *exec.ExitError
// carries any other code, and any other error type fails the calling test
// (a launch failure, not an exit-code mismatch).
func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running update_script: %v", err)
	}
	return exitErr.ExitCode()
}

// writeUpdateScriptStubs materializes no-op replacements for every external
// command update_script shells out to (besides POSIX-sh builtins) into a
// fresh directory and returns its path for PATH-prepending.
func writeUpdateScriptStubs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"mount", "lcdtool", "cp", "mkdir", "chmod", "sync"} {
		writeExecutable(t, filepath.Join(dir, name), updateScriptStub)
	}
	writeExecutable(t, filepath.Join(dir, "uname"), updateScriptUnameStub)
	return dir
}

// writeUpdateScriptFixtures materializes the relative source directories
// update_script's (stubbed) cp calls reference, so the working directory
// looks like the unpacked add-on tarball the CCU installer extracts before
// invoking this script.
func writeUpdateScriptFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"addon", "addon/assets", "rc.d", "www", "etc"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir fixture %s: %v", sub, err)
		}
	}
	for _, f := range []string{
		"addon/openccu-loom.armv7",
		"addon/VERSION",
		"rc.d/openccu-loom",
		"www/placeholder",
		"etc/placeholder",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fixture\n"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", f, err)
		}
	}
	return dir
}

// writeExecutable writes content to path and marks it executable, failing
// the test on any error.
func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", path, err)
	}
}
