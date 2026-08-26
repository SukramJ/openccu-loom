// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// TestMatterSchemaSnapshotInSync asserts the two copies of the matter.js HEAD
// schema snapshot are byte-identical: the master at
// notes/parity/matter/matter-schema-snapshot.json (re-extracted by
// `make generate-matter-schema`) and the embedded copy at
// internal/north/matter/parity/schema.json that every matter parity test runs
// against.
//
// The Makefile copies master → embedded as one step of the codegen pipeline.
// A developer who regenerates the master and then runs `make test` without the
// copy step would see all parity tests pass against a STALE embedded snapshot
// while the generated schema maps are current — a silent false-green. This
// guard closes that window (architecture analysis, Matter W1).
func TestMatterSchemaSnapshotInSync(t *testing.T) {
	root := contractRepoRoot(t)
	master := filepath.Join(root, "notes", "parity", "matter", "matter-schema-snapshot.json")
	embedded := filepath.Join(root, "internal", "north", "matter", "parity", "schema.json")

	sum := func(path string) [32]byte {
		t.Helper()
		data, err := os.ReadFile(path) //nolint:gosec // fixed in-repo path resolved from the test file location
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return sha256.Sum256(data)
	}

	if sum(master) != sum(embedded) {
		t.Fatalf("matter schema snapshots diverge:\n  %s\n  %s\n"+
			"the embedded copy is stale — run `make generate-matter-schema` to re-sync both",
			master, embedded)
	}
}
