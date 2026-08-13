// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	sweep := strings.Index(src, "RunUnscopedDiscoveryCleanupOnce(")
	if sweep < 0 {
		t.Fatal("the composition root never calls RunUnscopedDiscoveryCleanupOnce — retained configs " +
			"published with an empty serial slot keep their ambiguous entity ids on the broker, and the " +
			"corrected payload creates a second entity beside each one")
	}
	snapshot := strings.Index(src, "PublishInitialSnapshot(")
	if snapshot < 0 {
		t.Fatal("the composition root never calls PublishInitialSnapshot — this pin cannot check an order " +
			"that no longer exists")
	}
	if sweep > snapshot {
		t.Errorf("RunUnscopedDiscoveryCleanupOnce is called after PublishInitialSnapshot.\n"+
			"  The sweep clears retained discovery configs; running it after the snapshot deletes the "+
			"payloads the snapshot just wrote, so the entities vanish instead of being re-announced "+
			"under their corrected ids.\n  (sweep at offset %d, snapshot at %d)", sweep, snapshot)
	}
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
