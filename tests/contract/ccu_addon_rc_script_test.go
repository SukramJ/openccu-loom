// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rcScriptFakeDaemon stands in for the add-on binary: it appends its PID to
// the file named by LOOM_RC_TEST_STARTS on every start and exits cleanly on
// TERM, the way the real daemon does after a restart from the SPA.
const rcScriptFakeDaemon = `#!/bin/sh
echo $$ >> "${LOOM_RC_TEST_STARTS}"
trap 'exit 0' TERM
while :; do sleep 0.1; done
`

// rcScriptEnv is one hermetic installation of the add-on's rc.d script: the
// fake daemon in its own add-on directory, a runtime directory for the
// pidfile, and PATH stubs for the CCU commands the script calls.
type rcScriptEnv struct {
	script string
	runDir string
	starts string
	env    []string
}

func newRCScriptEnv(t *testing.T, withMonit bool) *rcScriptEnv {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the rc.d script reads /proc; it only runs on Linux")
	}
	script, err := filepath.Abs("../../packaging/ccu-addon/ccu/rc.d/openccu-loom")
	if err != nil {
		t.Fatalf("resolve rc.d script path: %v", err)
	}
	addonDir := filepath.Join(t.TempDir(), "openccu-loom")
	if err := os.MkdirAll(addonDir, 0o755); err != nil {
		t.Fatalf("mkdir add-on dir: %v", err)
	}
	writeExecutable(t, filepath.Join(addonDir, "openccu-loom"), rcScriptFakeDaemon)

	stubDir := t.TempDir()
	writeExecutable(t, filepath.Join(stubDir, "logger"), updateScriptStub)
	monit := filepath.Join(stubDir, "monit-absent")
	if withMonit {
		monit = filepath.Join(stubDir, "monit")
		writeExecutable(t, monit, updateScriptStub)
	}

	e := &rcScriptEnv{
		script: script,
		runDir: t.TempDir(),
		starts: filepath.Join(t.TempDir(), "starts"),
	}
	e.env = append(
		os.Environ(),
		"PATH="+stubDir+":"+os.Getenv("PATH"),
		"LOOM_RC_ADDON_DIR="+addonDir,
		"LOOM_RC_RUN_DIR="+e.runDir,
		"LOOM_RC_MONIT="+monit,
		"LOOM_RC_TEST_STARTS="+e.starts,
	)
	t.Cleanup(func() {
		// Whatever a failed assertion left running must not outlive the test.
		for _, pid := range e.startedPIDs(t) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if pid, ok := e.pidfilePID(); ok {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return e
}

func (e *rcScriptEnv) run(t *testing.T, action string) {
	t.Helper()
	cmd := exec.Command("sh", e.script, action)
	cmd.Env = e.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rc.d %s: %v\n%s", action, err, out)
	}
}

func (e *rcScriptEnv) pidfile() string { return filepath.Join(e.runDir, "openccu-loom.pid") }

func (e *rcScriptEnv) pidfilePID() (int, bool) {
	b, err := os.ReadFile(e.pidfile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil
}

func (e *rcScriptEnv) startedPIDs(t *testing.T) []int {
	t.Helper()
	b, err := os.ReadFile(e.starts)
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("starts file holds %q", f)
		}
		pids = append(pids, pid)
	}
	return pids
}

// waitStarts waits until the fake daemon has been started n times, or the
// deadline passes; it returns the PIDs seen so far.
func (e *rcScriptEnv) waitStarts(t *testing.T, n int, within time.Duration) []int {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		pids := e.startedPIDs(t)
		if len(pids) >= n || time.Now().After(deadline) {
			return pids
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// runningNotZombie reports whether pid runs and has not exited. A killed
// child of the test stays a zombie until it is reaped, and kill -0 still
// reports a zombie alive; its state in /proc/<pid>/stat is Z.
func runningNotZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] != 'Z'
}

func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for alive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// TestCCUAddonRCScriptRestartsTheDaemonWithoutMonit pins what the SPA's
// "Restart" relies on where no monit runs (openccu-lite): the daemon
// SIGTERMs itself and the rc.d script must start it again. Before, the
// oneshot unit stayed "active" with the daemon gone until an operator
// started the service by hand. The negative control is the monit branch
// of the same script, which leaves the restart to monit.
func TestCCUAddonRCScriptRestartsTheDaemonWithoutMonit(t *testing.T) {
	t.Parallel()

	t.Run("without monit the script starts the daemon again", func(t *testing.T) {
		t.Parallel()
		e := newRCScriptEnv(t, false)
		e.run(t, "init")
		first := e.waitStarts(t, 1, 5*time.Second)
		if len(first) != 1 {
			t.Fatalf("init started the daemon %d times, want 1", len(first))
		}
		if err := syscall.Kill(first[0], syscall.SIGTERM); err != nil {
			t.Fatalf("signal daemon: %v", err)
		}
		again := e.waitStarts(t, 2, 10*time.Second)
		if len(again) != 2 {
			t.Fatalf("after the daemon's own TERM it was started %d times in total, want 2", len(again))
		}

		loop, ok := e.pidfilePID()
		if !ok {
			t.Fatal("no pidfile while the daemon runs")
		}
		e.run(t, "stop")
		if !waitGone(again[1], 5*time.Second) {
			t.Fatalf("stop left the daemon %d running", again[1])
		}
		if !waitGone(loop, 5*time.Second) {
			t.Fatalf("stop left the supervising loop %d running", loop)
		}
		if _, err := os.Stat(e.pidfile()); !os.IsNotExist(err) {
			t.Fatalf("stop left the pidfile behind (stat err %v)", err)
		}
		if n := len(e.waitStarts(t, 3, 3*time.Second)); n != 2 {
			t.Fatalf("the daemon was started again after stop (%d starts)", n)
		}
	})

	t.Run("with monit the restart is left to monit", func(t *testing.T) {
		t.Parallel()
		if _, err := exec.LookPath("start-stop-daemon"); err != nil {
			t.Skip("start-stop-daemon not installed")
		}
		e := newRCScriptEnv(t, true)
		e.run(t, "init")
		first := e.waitStarts(t, 1, 5*time.Second)
		if len(first) != 1 {
			t.Fatalf("init started the daemon %d times, want 1", len(first))
		}
		if pid, ok := e.pidfilePID(); !ok || pid != first[0] {
			t.Fatalf("pidfile names %d (ok=%v), want the daemon %d", pid, ok, first[0])
		}
		if err := syscall.Kill(first[0], syscall.SIGTERM); err != nil {
			t.Fatalf("signal daemon: %v", err)
		}
		if n := len(e.waitStarts(t, 2, 4*time.Second)); n != 1 {
			t.Fatalf("the script restarted the daemon itself although monit supervises it (%d starts)", n)
		}
	})
}

// TestCCUAddonRCScriptStopIgnoresAStalePidfile pins the failure seen on
// openccu-lite: the pidfile was left by a root-run install and named a PID
// that no longer belonged to the daemon. stop must not signal a process
// that is not the add-on's, and start must not take the stale file for a
// running daemon.
func TestCCUAddonRCScriptStopIgnoresAStalePidfile(t *testing.T) {
	t.Parallel()
	e := newRCScriptEnv(t, false)

	bystander := exec.Command("sleep", "60")
	if err := bystander.Start(); err != nil {
		t.Fatalf("start bystander: %v", err)
	}
	t.Cleanup(func() {
		_ = bystander.Process.Kill()
		_ = bystander.Wait()
	})
	if err := os.WriteFile(e.pidfile(), []byte(strconv.Itoa(bystander.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatalf("write stale pidfile: %v", err)
	}

	e.run(t, "stop")
	time.Sleep(200 * time.Millisecond)
	if !runningNotZombie(bystander.Process.Pid) {
		t.Fatal("stop signalled the unrelated process the stale pidfile named")
	}

	if err := os.WriteFile(e.pidfile(), []byte(strconv.Itoa(bystander.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite stale pidfile: %v", err)
	}
	e.run(t, "init")
	if n := len(e.waitStarts(t, 1, 5*time.Second)); n != 1 {
		t.Fatalf("init did not start the daemon over a stale pidfile (%d starts)", n)
	}
	e.run(t, "stop")
}
