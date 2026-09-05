// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	matterparity "github.com/SukramJ/go-fabric/parity"
)

// TestMatterSchemaSnapshotInSync asserts that the master copy of the matter.js
// HEAD schema snapshot in this repo — notes/parity/matter/matter-schema-snapshot.json,
// re-extracted by `make generate-matter-schema` — is byte-identical to the copy
// the Matter stack embeds and every Matter parity test actually reads
// (go-fabric's parity.SchemaJSON()).
//
// Both halves are still live here, they just no longer sit in one module. The
// master is read by tests/chiptool/wire_truth_test.go and by
// script/generate_matter_schema.go; the embedded copy is what
// internal/model/custom/*/parity_matterjs_test.go compares device profiles
// against. Keeping them in step is now a cross-repo copy behind a module
// version pin, which is strictly easier to forget than the single `cp` it used
// to be: regenerate the master here, publish the Matter stack from an older
// extract, and every parity test passes against a snapshot that is not the one
// this repo believes it pinned.
//
// A failure means the two extracts disagree. Re-extract, copy into the Matter
// stack, publish it, and bump the dependency — do not silence this by editing
// either JSON by hand.
func TestMatterSchemaSnapshotInSync(t *testing.T) {
	root := contractRepoRoot(t)
	master := filepath.Join(root, "notes", "parity", "matter", "matter-schema-snapshot.json")

	data, err := os.ReadFile(master) //nolint:gosec // fixed in-repo path resolved from the test file location
	if err != nil {
		t.Fatalf("read %s: %v", master, err)
	}

	masterSum := sha256.Sum256(data)
	embeddedSum := sha256.Sum256(matterparity.SchemaJSON())

	if masterSum != embeddedSum {
		t.Fatalf("matter schema snapshots diverge:\n"+
			"  master   %s (sha256 %x)\n"+
			"  embedded github.com/SukramJ/go-fabric/parity.SchemaJSON() (sha256 %x)\n"+
			"one of the two extracts is stale — re-extract the master with "+
			"`make generate-matter-schema`, copy it into the Matter stack's parity "+
			"package, and bump the dependency",
			master, masterSum, embeddedSum)
	}
}
