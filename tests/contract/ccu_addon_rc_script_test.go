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
printf '%s\n' "${OPENCCU_LOOM_BACKUP_DIR:-<unset>}" > "${LOOM_RC_TEST_STARTS}.backupdir"
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
	// cronBackupPath is the stand-in for the CCU's CronBackupPath file,
	// which names the operator's backup directory.
	cronBackupPath string
	// logFile collects every message the script hands to logger.
	logFile string
}

func strconvQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (e *rcScriptEnv) logged(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.logFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read logger output: %v", err)
	}
	return string(b)
}

// rcScriptUnitCgroup and rcScriptInstallCgroup are the cgroup v2 lines of
// the add-on's own systemd unit on openccu-lite and of the scope its
// installer runs in.
const (
	rcScriptUnitCgroup    = "0::/system.slice/addon-openccu-loom.service\n"
	rcScriptInstallCgroup = "0::/system.slice/occulite-addon-6be83965.scope\n"
)

func newRCScriptEnv(t *testing.T, withMonit bool) *rcScriptEnv {
	t.Helper()
	return newRCScriptEnvIn(t, withMonit, rcScriptUnitCgroup)
}

func newRCScriptEnvIn(t *testing.T, withMonit bool, cgroup string) *rcScriptEnv {
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
	logFile := filepath.Join(stubDir, "logger.out")
	writeExecutable(t, filepath.Join(stubDir, "logger"),
		"#!/bin/sh\n# keep the message (the last argument) for assertions\nfor a; do m=$a; done\nprintf '%s\\n' \"$m\" >> "+strconvQuote(logFile)+"\n")
	monit := filepath.Join(stubDir, "monit-absent")
	if withMonit {
		monit = filepath.Join(stubDir, "monit")
		writeExecutable(t, monit, updateScriptStub)
	}

	cronBackupPath := filepath.Join(stubDir, "CronBackupPath")
	cgroupFile := filepath.Join(stubDir, "cgroup")
	if err := os.WriteFile(cgroupFile, []byte(cgroup), 0o644); err != nil {
		t.Fatalf("write cgroup file: %v", err)
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
		"LOOM_RC_CGROUP="+cgroupFile,
		"LOOM_RC_CRON_BACKUP_PATH_FILE="+cronBackupPath,
	)
	e.cronBackupPath = cronBackupPath
	e.logFile = logFile
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

// TestCCUAddonRCScriptStartsNoLoopOutsideTheUnit pins the install path on
// openccu-lite: the installer runs the script as root in its own scope,
// where a supervising loop would outlive the install and keep starting a
// second daemon as root beside the unit's. Without monit the script starts
// nothing there; the unit is where the daemon runs. The negative control is
// the same call inside the unit, which does start it.
func TestCCUAddonRCScriptStartsNoLoopOutsideTheUnit(t *testing.T) {
	t.Parallel()

	outside := newRCScriptEnvIn(t, false, rcScriptInstallCgroup)
	outside.run(t, "init")
	if n := len(outside.waitStarts(t, 1, 3*time.Second)); n != 0 {
		t.Fatalf("init in the installer's scope started the daemon %d times, want 0", n)
	}
	if _, err := os.Stat(outside.pidfile()); !os.IsNotExist(err) {
		t.Fatalf("init in the installer's scope wrote a pidfile (stat err %v)", err)
	}
	// A start that does not happen reports nothing it would have done: the
	// backup target is resolved and logged only for a real start.
	if log := outside.logged(t); strings.Contains(log, "archives") {
		t.Fatalf("init in the installer's scope logged a backup target:\n%s", log)
	}

	inside := newRCScriptEnvIn(t, false, rcScriptUnitCgroup)
	inside.run(t, "init")
	if n := len(inside.waitStarts(t, 1, 5*time.Second)); n != 1 {
		t.Fatalf("init inside the unit started the daemon %d times, want 1", n)
	}
	if log := inside.logged(t); !strings.Contains(log, "archives") {
		t.Fatalf("init inside the unit logged no backup target:\n%s", log)
	}
	inside.run(t, "stop")
}

// TestCCUAddonRCScriptReplacesAPidfileItCannotWrite pins the second half of
// the install incident: the root-run loop left a root-owned pidfile in the
// unit's runtime directory, which the add-on's user cannot overwrite. The
// directory is the add-on's, so start replaces the file instead of silently
// failing to record the loop it just started.
func TestCCUAddonRCScriptReplacesAPidfileItCannotWrite(t *testing.T) {
	t.Parallel()
	e := newRCScriptEnv(t, false)
	if err := os.WriteFile(e.pidfile(), []byte("1\n"), 0o444); err != nil {
		t.Fatalf("write read-only pidfile: %v", err)
	}
	e.run(t, "init")
	starts := e.waitStarts(t, 1, 5*time.Second)
	if len(starts) != 1 {
		t.Fatalf("init started the daemon %d times, want 1", len(starts))
	}
	loop, ok := e.pidfilePID()
	if !ok || loop == 1 {
		t.Fatalf("pidfile still names %d (ok=%v): the read-only file was not replaced", loop, ok)
	}
	e.run(t, "stop")
	if !waitGone(starts[0], 5*time.Second) {
		t.Fatalf("stop left the daemon %d running", starts[0])
	}
}

// TestCCUAddonRCScriptPassesOnlyAWritableBackupDir pins the backup target on
// openccu-lite: the unit mounts /usr/local read-only apart from the add-on's
// own directories, so a directory the script picks there can never take an
// archive. The script passes a backup directory only when this process can
// write it, and otherwise leaves the daemon on its own default. The negative
// control is a writable directory, which is passed.
func TestCCUAddonRCScriptPassesOnlyAWritableBackupDir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only-permission directory; the check needs an unprivileged user")
	}

	backupDirSeen := func(t *testing.T, target string) string {
		t.Helper()
		e := newRCScriptEnv(t, false)
		if err := os.WriteFile(e.cronBackupPath, []byte(target+"\n"), 0o644); err != nil {
			t.Fatalf("write CronBackupPath: %v", err)
		}
		e.run(t, "init")
		if n := len(e.waitStarts(t, 1, 5*time.Second)); n != 1 {
			t.Fatalf("init started the daemon %d times, want 1", n)
		}
		b, err := os.ReadFile(e.starts + ".backupdir")
		e.run(t, "stop")
		if err != nil {
			t.Fatalf("the fake daemon did not record its backup directory: %v", err)
		}
		return strings.TrimSpace(string(b))
	}

	writable := filepath.Join(t.TempDir(), "backup")
	if got := backupDirSeen(t, writable); got != writable {
		t.Fatalf("a writable backup directory reached the daemon as %q, want %q", got, writable)
	}

	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	if got := backupDirSeen(t, filepath.Join(readOnly, "backup")); got != "<unset>" {
		t.Fatalf("a backup directory the add-on cannot create reached the daemon as %q, want it unset", got)
	}
}
