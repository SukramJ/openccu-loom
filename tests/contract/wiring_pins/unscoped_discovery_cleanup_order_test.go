// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUnscopedDiscoveryCleanupRunsBeforeTheInitialSnapshot pins the one
// property that makes the sweep work at all: it clears retained
// discovery configs whose entity id carries an empty CCU-serial slot,
// and the snapshot is what re-announces those entities under a corrected
// id.
//
// Run it after the snapshot and it deletes the payload just written —
// the entity disappears until the next boot, and the boot after that
// deletes it again. Run it before, and the sequence reads: clear the
// ambiguous identity, announce the correct one. The consumer forgets the
// old entity because an empty retained payload is what removes it, and
// picks up the new one from the announcement that follows.
//
// The order is invisible in the type system and cheap to lose in a
// refactor that groups "all the sweeps" together — the other two
// deliberately run after the snapshot, because they compare against what
// it declared.
func TestUnscopedDiscoveryCleanupRunsBeforeTheInitialSnapshot(t *testing.T) {
	t.Parallel()

	src := readDaemonSouthbound(t)
	if !strings.Contains(src, "RunUnscopedDiscoveryCleanupOnce(") {
		t.Fatal("the composition root never calls RunUnscopedDiscoveryCleanupOnce — retained configs " +
			"published with an empty serial slot keep their ambiguous entity ids on the broker, and the " +
			"corrected payload creates a second entity beside each one")
	}
	// The scrub lives in bootRetainCleanups.run; the boot path invokes it
	// through d.bootCleanups.run. The order that matters is that invocation
	// against the snapshot call on the same path.
	sweep := strings.Index(src, "d.bootCleanups.run(")
	if sweep < 0 {
		t.Fatal("the boot path never invokes d.bootCleanups.run — the unscoped discovery scrub " +
			"no longer runs before the initial snapshot on the boot path")
	}
	snapshot := strings.Index(src, "PublishInitialSnapshot(")
	if snapshot < 0 {
		t.Fatal("the composition root never calls PublishInitialSnapshot — this pin cannot check an order " +
			"that no longer exists")
	}
	if sweep > snapshot {
		t.Errorf("d.bootCleanups.run is called after PublishInitialSnapshot.\n"+
			"  The scrub clears retained discovery configs; running it after the snapshot deletes the "+
			"payloads the snapshot just wrote, so the entities vanish instead of being re-announced "+
			"under their corrected ids.\n  (scrub at offset %d, snapshot at %d)", sweep, snapshot)
	}

	// The late-broker path mirrors the same pair inside one OnConnect hook;
	// keep the scrub ahead of the snapshot there too.
	rootSrc := readDaemonRoot(t)
	hookSweep := strings.Index(rootSrc, "bootCleanups.run(")
	hookSnapshot := strings.Index(rootSrc, "bridge.PublishInitialSnapshot(")
	if hookSweep < 0 || hookSnapshot < 0 {
		t.Fatal("daemon.go no longer wires bootCleanups.run and bridge.PublishInitialSnapshot on " +
			"broker (re)connect — a daemon that boots beside a down broker never scrubs the " +
			"ambiguous retained ids")
	}
	if hookSweep > hookSnapshot {
		t.Errorf("on the broker-connect hook, bootCleanups.run is called after "+
			"bridge.PublishInitialSnapshot (scrub at offset %d, snapshot at %d)", hookSweep, hookSnapshot)
	}
}

// readDaemonRoot returns the daemon composition-root source (daemon.go).
func readDaemonRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "cmd", "openccu-loom", "daemon.go"))
	if err != nil {
		t.Fatalf("read daemon.go: %v", err)
	}
	return string(b)
}

// readDaemonSouthbound returns the southbound composition-root source.
func readDaemonSouthbound(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	path := filepath.Join(root, "cmd", "openccu-loom", "daemon_southbound.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}
