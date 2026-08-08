// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build ignore

// whitelist_audit: Iterates all loom:reachable:reason="..." annotations in the
// repository and classifies each annotated item as either PRODUCTIVE (has at
// least one production call site outside test files — cross-file, or in the
// same file outside the symbol's own declaration) or MASKED (zero production
// callers — the annotation hides genuine dead code).
//
// Output: notes/parity/loom-reachable-audit.md
//
// Run: go run ./script/reachability/whitelist_audit.go
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// annotatedItem holds a single loom:reachable annotation found in the repo.
type annotatedItem struct {
	File       string // repo-root-relative file path
	Line       int
	Identifier string
	Reason     string
}

// classifiedItem is an annotatedItem with its classification result.
type classifiedItem struct {
	annotatedItem
	ProductionCallers []string // files outside definition + test files that mention the identifier
	Status            string   // "PRODUCTIVE" or "MASKED"
}

func main() {
	repoRoot, _ := os.Getwd()

	items, err := collectAnnotations(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect annotations: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "found %d annotated items\n", len(items))

	results := make([]classifiedItem, 0, len(items))
	for i, item := range items {
		if i%10 == 0 {
			fmt.Fprintf(os.Stderr, "classifying %d/%d\n", i, len(items))
		}
		callers := findProductionCallers(repoRoot, item)
		status := "PRODUCTIVE"
		if len(callers) == 0 {
			status = "MASKED"
		}
		results = append(results, classifiedItem{
			annotatedItem:     item,
			ProductionCallers: callers,
			Status:            status,
		})
	}

	// Sort: MASKED first, then PRODUCTIVE, then by file path.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Status != results[j].Status {
			return results[i].Status < results[j].Status // "MASKED" < "PRODUCTIVE"
		}
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})

	outPath := filepath.Join(repoRoot, "notes", "parity", "loom-reachable-audit.md")
	if err := writeReport(outPath, results); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(1)
	}

	var masked, productive int
	for _, r := range results {
		if r.Status == "MASKED" {
			masked++
		} else {
			productive++
		}
	}
	fmt.Printf("Whitelist audit complete:\n")
	fmt.Printf("  Total annotated: %d\n", len(results))
	fmt.Printf("  PRODUCTIVE:      %d\n", productive)
	fmt.Printf("  MASKED:          %d\n", masked)
	fmt.Printf("Output: %s\n", outPath)
}

// collectAnnotations walks all Go source files in the repo and extracts every
// loom:reachable:reason="..." annotation, pairing it with the declared identifier
// that follows it.
func collectAnnotations(repoRoot string) ([]annotatedItem, error) {
	var items []annotatedItem

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Skip directories that do not contain production annotation candidates.
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "spa_dist" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil
		}

		relFile := strings.TrimPrefix(path, repoRoot+"/")

		for _, decl := range f.Decls {
			reason, ok := extractWhitelistComment(f, fset, decl)
			if !ok {
				continue
			}
			ident := declIdentifier(decl)
			if ident == "" {
				continue
			}
			pos := fset.Position(decl.Pos())
			items = append(items, annotatedItem{
				File:       relFile,
				Line:       pos.Line,
				Identifier: ident,
				Reason:     reason,
			})
		}
		return nil
	})
	return items, err
}

// declIdentifier returns the primary exported identifier of a declaration.
func declIdentifier(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Name != nil && ast.IsExported(d.Name.Name) {
			return d.Name.Name
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if ast.IsExported(s.Name.Name) {
					return s.Name.Name
				}
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if ast.IsExported(n.Name) {
						return n.Name
					}
				}
			}
		}
	}
	return ""
}

// extractWhitelistComment looks for a `// loom:reachable:reason="..."` comment
// immediately before the declaration (within 3 lines to handle doc-comment gaps).
func extractWhitelistComment(f *ast.File, fset *token.FileSet, decl ast.Decl) (string, bool) {
	declLine := fset.Position(decl.Pos()).Line
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			commentLine := fset.Position(c.Pos()).Line
			// Allow comment up to 3 lines before the declaration to handle
			// multi-line doc comment blocks.
			if commentLine >= declLine-4 && commentLine < declLine {
				text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if strings.HasPrefix(text, "loom:reachable:reason=") {
					reason := strings.TrimPrefix(text, "loom:reachable:reason=")
					reason = strings.Trim(reason, `"`)
					return reason, true
				}
			}
		}
	}
	return "", false
}

// findProductionCallers runs a word-boundary grep for the identifier across
// cmd/, internal/, and pkg/, excluding the definition file and all test files,
// then additionally scans the definition file itself for call sites outside
// the symbol's own declaration (a production function in the same file is a
// production caller too). Returns the list of matching file paths
// (repo-root-relative); a same-file hit is marked "(same file)".
func findProductionCallers(repoRoot string, item annotatedItem) []string {
	cmd := exec.Command(
		"grep",
		"-rln",
		"--include=*.go",
		"--exclude=*_test.go",
		"-w",
		item.Identifier,
		"cmd/", "internal/", "pkg/",
	)
	cmd.Dir = repoRoot
	out, _ := cmd.Output()

	declFile := filepath.Clean(item.File)
	var callers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		clean := filepath.Clean(line)
		if clean == declFile {
			continue
		}
		callers = append(callers, line)
	}
	if hasSameFileProductionCaller(repoRoot, item) {
		callers = append(callers, item.File+" (same file)")
	}
	return callers
}

// hasSameFileProductionCaller reports whether the definition file itself
// references the identifier outside the annotated symbol's own declaration.
// Comment mentions do not count (only real AST identifiers), and test files
// never count as production call sites.
func hasSameFileProductionCaller(repoRoot string, item annotatedItem) bool {
	if strings.HasSuffix(item.File, "_test.go") {
		return false
	}
	path := filepath.Join(repoRoot, item.File)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return false
	}
	for _, decl := range f.Decls {
		// Skip the annotated declaration itself — a recursive call or the
		// defining identifier must not count as an external call site.
		if fset.Position(decl.Pos()).Line == item.Line {
			continue
		}
		found := false
		ast.Inspect(decl, func(n ast.Node) bool {
			if found {
				return false
			}
			if id, ok := n.(*ast.Ident); ok && id.Name == item.Identifier {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// writeReport writes the audit results to a Markdown file.
func writeReport(path string, results []classifiedItem) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	var masked, productive []classifiedItem
	for _, r := range results {
		if r.Status == "MASKED" {
			masked = append(masked, r)
		} else {
			productive = append(productive, r)
		}
	}

	fmt.Fprintf(f, "# loom:reachable Whitelist Audit\n\n")
	fmt.Fprintf(f, "Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "Total annotated items: %d — PRODUCTIVE: %d — MASKED: %d\n\n",
		len(results), len(productive), len(masked))

	fmt.Fprintf(f, "## MASKED — Annotation hides genuine dead code (%d items)\n\n", len(masked))
	fmt.Fprintf(f, "These items have zero production call sites (cross-file, or same-file\n")
	fmt.Fprintf(f, "outside the symbol's own declaration).\n")
	fmt.Fprintf(f, "**Action required:** either wire them into a real production call site, or\n")
	fmt.Fprintf(f, "document them as intentional dead code in `notes/parity/by_design.md`.\n")
	fmt.Fprintf(f, "A `loom:reachable` annotation alone is not sufficient justification.\n\n")

	if len(masked) == 0 {
		fmt.Fprintf(f, "_None — all annotated items have production callers._\n\n")
	} else {
		fmt.Fprintf(f, "| Identifier | File | Line | Reason |\n")
		fmt.Fprintf(f, "|---|---|---|---|\n")
		for _, r := range masked {
			fmt.Fprintf(f, "| `%s` | `%s` | %d | %s |\n",
				r.Identifier, r.File, r.Line, r.Reason)
		}
		fmt.Fprintf(f, "\n")
	}

	fmt.Fprintf(f, "## PRODUCTIVE — Has production callers (%d items)\n\n", len(productive))
	fmt.Fprintf(f, "These items are genuinely reachable from production code.\n\n")

	if len(productive) == 0 {
		fmt.Fprintf(f, "_None._\n\n")
	} else {
		fmt.Fprintf(f, "| Identifier | File | Line | Callers | Reason |\n")
		fmt.Fprintf(f, "|---|---|---|---|---|\n")
		for _, r := range productive {
			callerStr := strings.Join(r.ProductionCallers, ", ")
			if len(r.ProductionCallers) > 3 {
				callerStr = fmt.Sprintf("%s (and %d more)", strings.Join(r.ProductionCallers[:3], ", "), len(r.ProductionCallers)-3)
			}
			fmt.Fprintf(f, "| `%s` | `%s` | %d | %s | %s |\n",
				r.Identifier, r.File, r.Line, callerStr, r.Reason)
		}
		fmt.Fprintf(f, "\n")
	}

	return nil
}
