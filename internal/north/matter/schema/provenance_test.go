// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schema_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
)

// TestMatterSchemaSnapshotHashMatchesEmbedded recomputes the SHA-256 of
// notes/parity/matter/matter-schema-snapshot.json at test time and asserts
// it equals schema.SchemaSnapshotSHA256 — the constant emitted by the
// generator. A divergence means either:
//   - a generated constant was hand-edited without re-running the generator,
//   - the snapshot was updated without regenerating the Go schema files.
//
// In both cases the fix is to run `make generate-matter-schema`.
func TestMatterSchemaSnapshotHashMatchesEmbedded(t *testing.T) {
	t.Parallel()

	// Resolve the repo root relative to this test file's location.
	// This file lives at internal/north/matter/schema/provenance_test.go,
	// so the repo root is four directories up.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	snapshotPath := filepath.Join(repoRoot, "notes", "parity", "matter", "matter-schema-snapshot.json")

	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", snapshotPath, err)
	}

	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])

	if got != schema.SchemaSnapshotSHA256 {
		t.Errorf("snapshot SHA-256 mismatch:\n  on-disk:   %s\n  generated: %s\n\nRun `make generate-matter-schema` to resync.",
			got, schema.SchemaSnapshotSHA256)
	}
}
