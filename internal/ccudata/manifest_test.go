// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	_ "embed"
)

//go:embed embedded/MANIFEST.json
var manifestJSON []byte

// manifestFile is one entry in MANIFEST.json.
type manifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// manifest is the top-level shape of MANIFEST.json.
type manifest struct {
	SnapshotDate string         `json:"snapshot_date"`
	Files        []manifestFile `json:"files"`
}

// TestManifestHashesMatchEmbedded reads MANIFEST.json, opens each
// referenced file from the embedded FS, and verifies that the SHA-256
// hash and byte-size recorded in the manifest match the actual
// embedded content. A mismatch means the archive was refreshed without
// updating the manifest (or vice versa) — the test blocks the commit.
func TestManifestHashesMatchEmbedded(t *testing.T) {
	t.Parallel()

	var m manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("parse MANIFEST.json: %v", err)
	}
	if len(m.Files) == 0 {
		t.Fatal("MANIFEST.json contains no file entries")
	}

	for _, entry := range m.Files {
		// capture
		t.Run(entry.Path, func(t *testing.T) {
			t.Parallel()
			embedPath := "embedded/" + entry.Path

			f, err := embedded.Open(embedPath)
			if err != nil {
				t.Fatalf("open embedded file %q: %v", embedPath, err)
			}
			defer func() { _ = f.Close() }()

			h := sha256.New()
			n, err := io.Copy(h, f)
			if err != nil {
				t.Fatalf("read embedded file %q: %v", embedPath, err)
			}

			gotHash := hex.EncodeToString(h.Sum(nil))
			gotSize := n

			if gotHash != entry.SHA256 {
				t.Errorf("SHA-256 mismatch for %q:\n  manifest: %s\n  embedded: %s\n"+
					"  Hint: run 'sha256sum internal/ccudata/embedded/%s' and update MANIFEST.json",
					entry.Path, entry.SHA256, gotHash, entry.Path)
			}
			if gotSize != entry.Size {
				t.Errorf("size mismatch for %q: manifest=%d embedded=%d",
					entry.Path, entry.Size, gotSize)
			}
		})
	}
}

// TestManifestWellFormed validates structural invariants of
// MANIFEST.json itself — non-empty snapshot_date, no duplicate paths,
// no empty hashes — so a malformed manifest is caught at test time
// before the drift script runs.
func TestManifestWellFormed(t *testing.T) {
	t.Parallel()

	var m manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("parse MANIFEST.json: %v", err)
	}

	if m.SnapshotDate == "" {
		t.Error("MANIFEST.json: snapshot_date must not be empty")
	}
	if len(m.Files) == 0 {
		t.Error("MANIFEST.json: files array must not be empty")
	}

	seen := make(map[string]bool, len(m.Files))
	for i, f := range m.Files {
		label := fmt.Sprintf("files[%d]", i)
		if f.Path == "" {
			t.Errorf("%s: path must not be empty", label)
		}
		if f.SHA256 == "" {
			t.Errorf("%s (%s): sha256 must not be empty", label, f.Path)
		}
		if len(f.SHA256) != 64 {
			t.Errorf("%s (%s): sha256 must be 64 hex chars, got %d", label, f.Path, len(f.SHA256))
		}
		if f.Size <= 0 {
			t.Errorf("%s (%s): size must be > 0", label, f.Path)
		}
		if seen[f.Path] {
			t.Errorf("duplicate path in MANIFEST.json: %s", f.Path)
		}
		seen[f.Path] = true
	}
}
