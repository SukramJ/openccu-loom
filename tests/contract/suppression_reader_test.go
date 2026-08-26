// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSuppressionTablesHaveReaders enforces the invariant: every suppression
// table declared in internal/store/visibility/ must be consumed by at least
// one reader in either the visibility package itself or in an upstream
// pipeline / adapter / discovery path.
//
// The tables are:
//
// - hiddenParameters (visibility/rules.go:176) - ignoredParameters
// (visibility/rules.go:204) - ignoredParametersEndPattern
// (visibility/rules.go:284) - ignoredParametersStartPattern
// (visibility/rules.go:289)
func TestSuppressionTablesHaveReaders(t *testing.T) {
	t.Parallel()

	repoRoot := mustRepoRoot(t)
	visibilityDir := filepath.Join(repoRoot, "internal", "store", "visibility")

	tables := []struct {
		name        string
		minReaders  int
		searchPaths []string
		// declDir is the directory the declaration site lives in.
		// Empty value defaults to the visibility/ directory (where the
		// original four core tables live).
		declDir string
	}{
		{
			name:       "hiddenParameters",
			minReaders: 2,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
				filepath.Join(repoRoot, "internal", "central", "adapter"),
			},
		},
		{
			name:       "ignoredParameters",
			minReaders: 2,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
				filepath.Join(repoRoot, "internal", "central", "adapter"),
			},
		},
		{
			name:       "ignoredParametersEndPattern",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "ignoredParametersStartPattern",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		// Additional suppression tables that the
		// pipeline relies on. Each must keep at least one reader so the
		// next refactor cannot silently orphan the table.
		{
			name:       "ignoreParametersByDevice",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "unIgnoreParametersByDevice",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "ignoreDevicesForDataPointEvents",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "relevantMasterParamsetsByChannel",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "relevantMasterParamsetsByDevice",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "store", "visibility"),
			},
		},
		{
			name:       "ImpulseEvents",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "central", "adapter"),
			},
			declDir: filepath.Join(repoRoot, "internal", "central", "adapter"),
		},
		{
			name:       "buttonActionParameters",
			minReaders: 1,
			searchPaths: []string{
				filepath.Join(repoRoot, "internal", "central", "adapter"),
			},
			declDir: filepath.Join(repoRoot, "internal", "central", "adapter"),
		},
	}

	for _, tc := range tables {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// 1. Confirm the table is declared in its decl directory
			// (defaults to visibility/ for the core tables).
			declDir := tc.declDir
			if declDir == "" {
				declDir = visibilityDir
			}
			if !containsIdentifierInDir(t, declDir, tc.name, declarationKeywords) {
				t.Fatalf("suppression table %q not declared in %s", tc.name, declDir)
			}

			// 2. Count reader occurrences (excluding the declaration
			// itself). A reader is any non-declaration reference in a
			// non-test .go file.
			readers := 0
			for _, dir := range tc.searchPaths {
				readers += countReaderRefs(t, dir, tc.name)
			}
			if readers < tc.minReaders {
				t.Errorf("suppression table %q has %d reader(s); need >= %d (M-OHNE-R regression)",
					tc.name, readers, tc.minReaders)
			}
		})
	}
}

// declarationKeywords are the Go syntax tokens that mark a top-level
// identifier as a declaration — `var`, `const`, `func`, `type`. A line
// containing the table name *plus* one of these counts as the
// declaration site, NOT a read site.
var declarationKeywords = []string{"var ", "const ", "func ", "type "}

// containsIdentifierInDir returns true when at least one .go file in
// dir (non-test) contains a declaration of ident.
func containsIdentifierInDir(t *testing.T, dir, ident string, keywords []string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // best-effort filesystem walk
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // fixed test path
		if err != nil {
			return nil //nolint:nilerr // best-effort filesystem walk; unreadable file is skipped
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			if !strings.Contains(line, ident) {
				continue
			}
			for _, kw := range keywords {
				if strings.Contains(line, kw) {
					found = true
					return nil
				}
			}
		}
		return nil
	})
	return found
}

// countReaderRefs walks dir and returns the number of .go (non-test)
// lines that reference ident WITHOUT being a declaration line.
func countReaderRefs(t *testing.T, dir, ident string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // best-effort filesystem walk
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path) //nolint:gosec // fixed test path
		if err != nil {
			return nil //nolint:nilerr // best-effort filesystem walk; unreadable file is skipped
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(line, ident) {
				continue
			}
			isDecl := false
			for _, kw := range declarationKeywords {
				if strings.Contains(line, kw+ident) {
					isDecl = true
					break
				}
			}
			if !isDecl {
				n++
			}
		}
		return nil
	})
	return n
}

// mustRepoRoot returns the repository root by walking upwards from the
// test's working directory until it finds a go.mod file.
func mustRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			t.Fatalf("no go.mod found upwards from %s", wd)
		}
	}
}
