// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestInstallerSpawnInvokesInjectedRunner(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotArgs []string
	fakeRun := func(_ context.Context, path string, args ...string) error {
		gotPath = path
		gotArgs = args
		return nil
	}

	inst := &Installer{InstallerPath: "/bin/install_addon", TarballPath: "/tmp/staged.tar.gz", Run: fakeRun}
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	if gotPath != "/bin/install_addon" {
		t.Errorf("path = %q, want /bin/install_addon", gotPath)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "/tmp/staged.tar.gz" {
		t.Errorf("args = %v, want [/tmp/staged.tar.gz]", gotArgs)
	}
}

// TestNewInstallerDefaults verifies NewInstaller wires the package
// constants and DefaultRunner. It exercises DefaultRunner's early-exit
// path (a cancelled ctx is checked before any process is spawned) so the
// test never actually execs a process.
func TestNewInstallerDefaults(t *testing.T) {
	t.Parallel()

	inst := NewInstaller()
	if inst.InstallerPath != InstallerPath {
		t.Errorf("InstallerPath = %q, want %q", inst.InstallerPath, InstallerPath)
	}
	if inst.TarballPath != DefaultStagePath {
		t.Errorf("TarballPath = %q, want %q", inst.TarballPath, DefaultStagePath)
	}
	if inst.Run == nil {
		t.Fatal("Run is nil, want DefaultRunner")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := inst.Spawn(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Spawn(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// TestDefaultRunnerSpawnsDetachedProcess exercises the real setsid +
// Release path against a trivial, near-instantaneous command so the test
// stays fast and never blocks on the child (Release means we never wait
// for it, so its exit code is deliberately not asserted).
func TestDefaultRunnerSpawnsDetachedProcess(t *testing.T) {
	t.Parallel()

	path, args := trueOrShCommand(t)
	if err := DefaultRunner(context.Background(), path, args...); err != nil {
		t.Fatalf("DefaultRunner(%q, %v) error = %v", path, args, err)
	}
}

// trueOrShCommand resolves a near-instantaneous, always-successful
// command available on the test runner: "true" if present, otherwise
// "sh -c exit 0".
func trueOrShCommand(t *testing.T) (path string, args []string) {
	t.Helper()
	if p, err := exec.LookPath("true"); err == nil {
		return p, nil
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh, []string{"-c", "exit 0"}
	}
	t.Skip("neither `true` nor `sh` found in PATH")
	return "", nil
}
