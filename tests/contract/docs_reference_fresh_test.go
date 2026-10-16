// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/docsref"
)

// TestGeneratedReferenceIsFresh regenerates the reference pages under
// docs/reference/ and compares them byte for byte with the checked-in
// copies, so the published configuration, WebSocket and REST references
// cannot fall behind the config schema, assets/wsapi.json or
// assets/openapi.yaml.
func TestGeneratedReferenceIsFresh(t *testing.T) {
	root := filepath.Join("..", "..")
	pages, err := docsref.Pages(root)
	if err != nil {
		t.Fatalf("docsref.Pages: %v", err)
	}
	paths := make([]string, 0, len(pages))
	for p := range pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s: %v — run `make docs-reference`", p, err)
			continue
		}
		if !bytes.Equal(onDisk, pages[p]) {
			t.Errorf("%s is stale — run `make docs-reference` and commit the result", p)
		}
	}
}
