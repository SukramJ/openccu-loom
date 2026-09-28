// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sbom

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"testing"
)

// TestEmbeddedArchiveIsValidWhenPresent validates the archive `make sbom`
// dropped into gen/ — a release build embeds exactly these bytes, so a
// malformed merge fails here rather than as a broken Licenses page. A tree
// without the archive (every dev checkout) skips: absence is a documented
// state the REST handler answers with 404, not a defect.
func TestEmbeddedArchiveIsValidWhenPresent(t *testing.T) {
	b, ok := Embedded{}.SBOMArchive()
	if !ok {
		t.Skip("no gen/sbom.cdx.json.gz in this tree — run `make sbom` to produce one")
	}
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("archive is not gzip: %v", err)
	}
	var doc struct {
		BomFormat  string           `json:"bomFormat"`
		Components []map[string]any `json:"components"`
	}
	if err := json.NewDecoder(gz).Decode(&doc); err != nil {
		t.Fatalf("archive is not JSON: %v", err)
	}
	if doc.BomFormat != "CycloneDX" {
		t.Fatalf("bomFormat = %q, want CycloneDX", doc.BomFormat)
	}
	if len(doc.Components) == 0 {
		t.Fatal("archive carries no components — the merge produced an empty document")
	}
}
