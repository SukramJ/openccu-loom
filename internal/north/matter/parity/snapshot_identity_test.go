// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parity_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestEmbeddedSchemaMatchesMasterSnapshot asserts the go:embed source consumed
// by every matter-side parity test (internal/north/matter/parity/schema.json,
// returned by parity.SchemaJSON) is byte-identical to the master snapshot the
// extractor writes (notes/parity/matter/matter-schema-snapshot.json).
//
// The two files are kept in sync by a manual copy step (see the sync note in
// parity.go). If the master is refreshed but the embed copy is not, the parity
// tests would keep validating against a stale schema and silently pass — this
// guard fails the build the moment the copies diverge, so the drift surfaces at
// PR time rather than as a mysterious parity miss weeks later.
//
// The master is read from disk rather than embedded on purpose: embedding a
// second copy alongside schema.json would compare one copy against another and
// leave the actual master unchecked, which is exactly the drift this guard
// exists to catch. The path is therefore anchored at the module root (see
// moduleRoot) instead of being counted out in parent directories.
func TestEmbeddedSchemaMatchesMasterSnapshot(t *testing.T) {
	t.Parallel()

	masterPath := filepath.Join(moduleRoot(t), "notes", "parity", "matter", "matter-schema-snapshot.json")

	master, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatalf("read master snapshot %s: %v", masterPath, err)
	}
	embedded := parity.SchemaJSON()

	if bytes.Equal(embedded, master) {
		return
	}

	embedSum := sha256.Sum256(embedded)
	masterSum := sha256.Sum256(master)
	t.Errorf("embedded schema.json diverged from the master snapshot — run\n"+
		"  cp notes/parity/matter/matter-schema-snapshot.json internal/north/matter/parity/schema.json\n"+
		"  embedded: %d bytes, sha256 %s\n"+
		"  master:   %d bytes, sha256 %s",
		len(embedded), hex.EncodeToString(embedSum[:]),
		len(master), hex.EncodeToString(masterSum[:]))
}

// moduleRoot walks up from this source file until it finds go.mod. Counting
// parent directories instead would silently resolve to the wrong tree the day
// this package moves, and the guard would then fail on a missing file rather
// than on the divergence it is meant to report.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed: cannot locate the module root")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
