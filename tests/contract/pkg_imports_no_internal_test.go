// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPkgDoesNotImportInternal pins what `pkg/` is for.
//
// The promise is written on pkg/hmapi: the package sits under pkg/ so an
// external program embedding openccu-loom as a library can import it
// without pulling in the whole internal/ tree. Go enforces that promise —
// but only against importers OUTSIDE the module, and every importer in
// this repository is inside it. So `go build ./...` is green while a
// package under pkg/ quietly becomes unimportable from anywhere else, and
// nothing in the repo can tell.
//
// That is exactly what had happened: pkg/hmlog imported internal/reqctx
// in its factory and its operation logger, so any external program that
// imported pkg/hmlog failed to compile. The package moved to
// pkg/hmreqctx.
//
// There is deliberately no exception list. One entry for hmlog would have
// made this guard blind to the single case it exists for, and an
// exception here is not a ratchet — it is the defect.
//
// A note for whoever re-proves the bite: re-adding the ORIGINAL import
// ("internal/reqctx") proves nothing, because that package no longer
// exists — pkg/hmlog then fails to compile, this whole test binary fails
// at setup, and the guard never runs. It looks like a pass. Use an
// internal package that still exists, e.g. a blank import of
// internal/build.
func TestPkgDoesNotImportInternal(t *testing.T) {
	t.Parallel()

	const internalPath = "openccu-loom/internal/"
	root := filepath.Join(repoRoot(t), "pkg")
	fset := token.NewFileSet()
	scanned := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		// Test files are excluded: they are never compiled into an
		// importing program, so an internal import there costs an external
		// consumer nothing. pkg/hmlog's own tests use hmreqctx and several
		// spell internal symbol names in string literals.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Errorf("parse %s: %v", path, perr)
			return nil
		}
		scanned++
		rel, _ := filepath.Rel(repoRoot(t), path)
		for _, spec := range f.Imports {
			imported, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil {
				continue
			}
			if strings.Contains(imported, internalPath) {
				t.Errorf("%s imports %q.\n"+
					"A package under pkg/ must be importable from outside this module, and Go "+
					"refuses an internal/ path to an external importer. Move the dependency "+
					"under pkg/, or invert it so the caller injects what this package needs.",
					rel, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg/: %v", err)
	}
	// Negative control on the walk itself: a filter that matched nothing
	// would make every assertion above vacuous.
	if scanned == 0 {
		t.Fatal("no non-test Go file was scanned under pkg/; the walk is measuring nothing")
	}
}
