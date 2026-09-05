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

// TestMatterSchemaSnapshotInSync asserts that this repo's pinned copy of the
// matter.js HEAD schema snapshot — notes/parity/matter/matter-schema-snapshot.json
// — is byte-identical to the copy the Matter stack embeds and every Matter
// parity test reads (go-fabric's parity.SchemaJSON()).
//
// Why a second copy exists at all. The extractor and the generator live in
// go-fabric now, beside the schema package they feed; nothing in this repo
// produces the snapshot any more. The copy that stayed is a lockfile, not a
// source: `go.mod` pins a module version, which says nothing about which
// matter.js commit that module was extracted from, and a schema change is
// exactly the kind of thing that rides in unnoticed on a routine dependency
// bump. With the pin checked in, such a bump turns red here and the new bytes
// have to be landed deliberately, as a diff someone reads.
//
// The cost of the choice, stated plainly: the pin is refreshed by copying from
// the module under test (`make sync-matter-schema`), so this guard cannot tell
// a reviewed bump from a mechanical one — it can only make the bump visible.
// The alternative, dropping the copy and reading parity.SchemaJSON() here,
// removes a file that can go stale but leaves the comparison asserting the
// embed equals itself, and no schema change in a dependency bump would ever
// surface in this repo's own diff. Visible-and-mechanical beats invisible.
//
// The pinned file is also what tests/chiptool/wire_truth_test.go reads, so the
// chip-tool wire guards and the module agree on one set of revisions.
//
// A failure means the module shipped a different extract than the one pinned
// here. Run `make sync-matter-schema` and review what moved — do not silence
// this by hand-editing either JSON.
func TestMatterSchemaSnapshotInSync(t *testing.T) {
	root := contractRepoRoot(t)
	pinned := filepath.Join(root, "notes", "parity", "matter", "matter-schema-snapshot.json")

	data, err := os.ReadFile(pinned) //nolint:gosec // fixed in-repo path resolved from the test file location
	if err != nil {
		t.Fatalf("read %s: %v", pinned, err)
	}

	pinnedSum := sha256.Sum256(data)
	embeddedSum := sha256.Sum256(matterparity.SchemaJSON())

	if pinnedSum != embeddedSum {
		t.Fatalf("matter schema snapshots diverge:\n"+
			"  pinned   %s (sha256 %x)\n"+
			"  embedded github.com/SukramJ/go-fabric/parity.SchemaJSON() (sha256 %x)\n"+
			"the module ships a different extract than the one pinned here — run "+
			"`make sync-matter-schema` and review the resulting diff",
			pinned, pinnedSum, embeddedSum)
	}
}
