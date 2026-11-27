// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// RestartOnRequest restarts the daemon the way an operator does: POST
// /api/v1/system/restart, after which the daemon shuts itself down, and
// the harness — standing in for the supervisor — starts the next process.
// The route is mounted only under a detected supervisor, so the harness
// must have been started with OPENCCU_LOOM_SUPERVISOR=1 in Options.Env.
// The REST client is replaced, so a session login has to be repeated.
func (h *Harness) RestartOnRequest(t *testing.T) {
	t.Helper()
	if status, body := h.nodeDo(t, http.MethodPost, "/api/v1/system/restart", nil, nil); status != http.StatusAccepted {
		t.Fatalf("POST /api/v1/system/restart: status %d, want 202 (is OPENCCU_LOOM_SUPERVISOR=1 in Options.Env?): %s", status, body)
	}
	select {
	case <-h.cmdDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not exit after the restart request")
	}
	h.relaunch(t)
}

// RestartWithBinary stops the daemon and starts bin on the same
// configuration and data directory — a software update in place. Every
// later restart runs bin too.
func (h *Harness) RestartWithBinary(t *testing.T, bin string) {
	t.Helper()
	h.stopProcess()
	h.binPath = bin
	h.relaunch(t)
}

// BuildDaemonWithVersion builds the daemon from this checkout into a test
// directory with build.Version set to version, for a test that needs a
// second release of the same code: an update is a start into a version
// other than the one that ran before.
func BuildDaemonWithVersion(t *testing.T, version string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	bin := filepath.Join(t.TempDir(), "openccu-loom")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{
		"build", "-trimpath",
		"-ldflags", "-X github.com/SukramJ/openccu-loom/internal/build.Version=" + version,
		"-o", bin, "./cmd/openccu-loom",
	}
	cmd := exec.CommandContext(t.Context(), "go", args...) //nolint:gosec // G204: building this checkout's daemon is the helper's purpose
	cmd.Dir = repoRoot
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build openccu-loom %s: %v\n%s", version, err, out)
	}
	return bin
}
