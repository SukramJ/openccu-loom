// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// extractor writes (docs/parity/matter/matter-schema-snapshot.json).
//
// The two files are kept in sync by a manual copy step (see the sync note in
// parity.go). If the master is refreshed but the embed copy is not, the parity
// tests would keep validating against a stale schema and silently pass — this
// guard fails the build the moment the copies diverge, so the drift surfaces at
// PR time rather than as a mysterious parity miss weeks later.
func TestEmbeddedSchemaMatchesMasterSnapshot(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// This file lives at internal/north/matter/parity/snapshot_identity_test.go,
	// so the repo root is four directories up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	masterPath := filepath.Join(repoRoot, "docs", "parity", "matter", "matter-schema-snapshot.json")

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
		"  cp docs/parity/matter/matter-schema-snapshot.json internal/north/matter/parity/schema.json\n"+
		"  embedded: %d bytes, sha256 %s\n"+
		"  master:   %d bytes, sha256 %s",
		len(embedded), hex.EncodeToString(embedSum[:]),
		len(master), hex.EncodeToString(masterSum[:]))
}
