// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var updateCatalogue = flag.Bool("update-catalogue", false,
	"regenerate tests/contract/README.md from the guard functions found on disk")

// catalogueGuard is one row of the tests/contract guard catalogue: an
// exported TestXxx function found by parsing the contract test sources.
type catalogueGuard struct {
	Name string // e.g. TestCUxDUsesBINRPCBackend
	File string // path relative to tests/contract, e.g. backend_capabilities_test.go
	Doc  string // first sentence of the doc comment, or "" if none
}

// discoverCatalogueGuards walks tests/contract (including the
// wire_snapshots and wiring_pins subpackages) with go/parser and collects
// every exported func TestXxx(t *testing.T) declaration. It parses source
// text directly rather than using go/packages, so a TestXxx-shaped
// identifier that only appears inside a string literal or a comment is
// never mistaken for a guard.
//
// Parsing is build-tag agnostic: files gated behind a build tag (the
// wire_snapshots generator/reference-compare tests) are parsed and
// catalogued the same as untagged files, because the catalogue's job is
// to list every guard that exists in source, not only the ones the
// default build compiles.
func discoverCatalogueGuards(t *testing.T, root string) []catalogueGuard {
	t.Helper()

	var guards []catalogueGuard
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		for _, decl := range src.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			name := fd.Name.Name
			if !strings.HasPrefix(name, "Test") || !ast.IsExported(name) {
				continue
			}
			// A Go test function has exactly one parameter, *testing.T.
			if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
				continue
			}

			doc := ""
			if fd.Doc != nil {
				doc = firstSentence(fd.Doc.Text())
			}

			guards = append(guards, catalogueGuard{Name: name, File: rel, Doc: doc})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("discovering contract guards: %v", err)
	}

	sort.Slice(guards, func(i, j int) bool {
		if guards[i].File != guards[j].File {
			return guards[i].File < guards[j].File
		}
		return guards[i].Name < guards[j].Name
	})
	return guards
}

// firstSentence normalizes a Go doc comment to its first complete
// sentence, collapsed onto a single line.
func firstSentence(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	doc = strings.Join(strings.Fields(doc), " ")
	if idx := strings.Index(doc, ". "); idx >= 0 {
		return doc[:idx+1]
	}
	if strings.HasSuffix(doc, ".") {
		return doc
	}
	return doc + "."
}

const catalogueHeader = `# tests/contract Guard Catalogue

This table is generated, not hand-maintained: one row per exported
` + "`Test*`" + ` function found under ` + "`tests/contract/`" + ` (including the
` + "`wire_snapshots/`" + ` and ` + "`wiring_pins/`" + ` subpackages), sorted by file
then by guard name. The "Holds" column is the first complete sentence of
the guard's Go doc comment, normalized to one line — it is not written by
hand, so a missing doc comment surfaces here as a gap rather than being
papered over with an invented description.

Regenerate after adding, renaming, or removing a guard:

` + "```sh" + `
GOMAXPROCS=2 go test -p 2 -run TestContractCatalogueIsComplete ./tests/contract/ -update-catalogue
` + "```" + `

` + "`TestContractCatalogueIsComplete`" + ` (` + "`tests/contract/catalogue_test.go`" + `) fails the
build when this file drifts from the guard functions actually present on
disk, in either direction.

`

// renderCatalogueMarkdown renders the guard table plus the "no doc
// comment" count line that precedes it.
func renderCatalogueMarkdown(guards []catalogueGuard) string {
	noDoc := 0
	for _, g := range guards {
		if g.Doc == "" {
			noDoc++
		}
	}

	var b strings.Builder
	b.WriteString(catalogueHeader)
	fmt.Fprintf(&b, "Guards without a doc comment: %d of %d.\n\n", noDoc, len(guards))
	b.WriteString("| Guard | File | Holds |\n")
	b.WriteString("|---|---|---|\n")
	for _, g := range guards {
		holds := g.Doc
		if holds == "" {
			holds = "— (no doc comment)"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", mdEscape(g.Name), mdEscape(g.File), mdEscape(holds))
	}
	return b.String()
}

// mdEscape neutralizes Markdown table- and link-breaking sequences a Go
// identifier or doc sentence can contain: a literal pipe (breaks the table
// column split), and a "](" sequence (a doc comment quoting Go syntax like
// "[text](path.md)" would otherwise read as a real Markdown link, which the
// repository's link checker then resolves against the filesystem).
//
// The separator is a plain space rather than a zero-width one: an invisible
// character in a generated file survives review unnoticed, breaks a grep for
// the text it sits inside, and shows up as unexplainable diff noise. A visible
// space costs one character of fidelity in a catalogue cell and nothing else.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "](", "] (")
	return s
}

// catalogueRowPattern matches one guard table row of tests/contract/README.md,
// capturing the guard name from the first column.
var catalogueRowPattern = regexp.MustCompile(`^\|\s*(Test[A-Za-z0-9_]*)\s*\|`)

// readCatalogueGuardNames parses the guard names listed in
// tests/contract/README.md's table, independent of column formatting.
func readCatalogueGuardNames(t *testing.T, readmePath string) map[string]bool {
	t.Helper()

	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading %s: %v", readmePath, err)
	}

	names := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		m := catalogueRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names[m[1]] = true
	}
	return names
}

// TestContractCatalogueIsComplete holds that tests/contract/README.md
// lists exactly the exported TestXxx functions that exist under
// tests/contract/ — no guard undocumented in the catalogue, no catalogue
// entry pointing at a guard that no longer exists.
//
// Without this test the catalogue silently rots the moment a guard is
// added, renamed, or removed: nothing else in the build reads
// tests/contract/README.md, so a stale table would sit there
// indefinitely looking authoritative. Run with -update-catalogue to
// regenerate the file after a deliberate change to the guard set.
func TestContractCatalogueIsComplete(t *testing.T) {
	guards := discoverCatalogueGuards(t, ".")
	readmePath := "README.md"

	if *updateCatalogue {
		if err := os.WriteFile(readmePath, []byte(renderCatalogueMarkdown(guards)), 0o644); err != nil {
			t.Fatalf("writing %s: %v", readmePath, err)
		}
		return
	}

	catalogued := readCatalogueGuardNames(t, readmePath)

	found := map[string]bool{}
	for _, g := range guards {
		found[g.Name] = true
	}

	var missingFromCatalogue []string
	for name := range found {
		if !catalogued[name] {
			missingFromCatalogue = append(missingFromCatalogue, name)
		}
	}
	sort.Strings(missingFromCatalogue)

	var staleInCatalogue []string
	for name := range catalogued {
		if !found[name] {
			staleInCatalogue = append(staleInCatalogue, name)
		}
	}
	sort.Strings(staleInCatalogue)

	if len(missingFromCatalogue) > 0 || len(staleInCatalogue) > 0 {
		var b strings.Builder
		b.WriteString("tests/contract/README.md is out of date with the guard functions on disk.\n")
		if len(missingFromCatalogue) > 0 {
			fmt.Fprintf(&b, "Guards missing from the catalogue (%d):\n", len(missingFromCatalogue))
			for _, name := range missingFromCatalogue {
				fmt.Fprintf(&b, "  + %s\n", name)
			}
		}
		if len(staleInCatalogue) > 0 {
			fmt.Fprintf(&b, "Catalogue entries with no matching guard (%d):\n", len(staleInCatalogue))
			for _, name := range staleInCatalogue {
				fmt.Fprintf(&b, "  - %s\n", name)
			}
		}
		b.WriteString("Regenerate with: GOMAXPROCS=2 go test -p 2 -run TestContractCatalogueIsComplete ./tests/contract/ -update-catalogue\n")
		t.Fatal(b.String())
	}
}
