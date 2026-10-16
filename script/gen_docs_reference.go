// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build ignore

// gen_docs_reference — write the generated reference pages under
// docs/reference/ from the config schema, assets/wsapi.json and
// assets/openapi.yaml. Run from the repo root:
//
//	go run ./script/gen_docs_reference.go
//
// TestGeneratedReferenceIsFresh fails while the checked-in pages differ
// from what this script writes.
package main

import (
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/SukramJ/openccu-loom/script/docsref"
)

func main() {
	pages, err := docsref.Pages(".")
	if err != nil {
		log.Fatal(err)
	}
	paths := make([]string, 0, len(pages))
	for p := range pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		dst := filepath.FromSlash(p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(dst, pages[p], 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", p)
	}
}
