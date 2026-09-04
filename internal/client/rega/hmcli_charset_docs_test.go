// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestHmCliRegaDocsCarryTheLatin1Half pins the completeness of this package's
// charset instruction.
//
// ReGa string fields are percent-encoded Latin-1. A doc comment that tells a
// caller to apply url.QueryUnescape and stops there instructs half a rule:
// the unescape yields raw Latin-1 bytes, json.Marshal turns them into U+FFFD
// on the way north, and the character is then unrecoverable. Two prior
// corruptions in this tree came from exactly that half-rule, so every comment
// in this package that names url.QueryUnescape must also name the transcode
// (see the package doc comment for the full rule and the canonical decoder).
func TestHmCliRegaDocsCarryTheLatin1Half(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, group := range f.Comments {
			text := group.Text()
			if !strings.Contains(text, "QueryUnescape") {
				continue
			}
			checked++
			if !strings.Contains(text, "Latin-1") {
				t.Errorf("%s:%d names url.QueryUnescape without the Latin-1 transcode; percent-decoding alone corrupts every non-ASCII ReGa value irreversibly",
					name, fset.Position(group.Pos()).Line)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no doc comment names url.QueryUnescape — the guard would pass vacuously")
	}
}
