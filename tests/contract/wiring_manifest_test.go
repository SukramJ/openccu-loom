// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// onRegisterDeclaringFile is the one file allowed to call the raw
// [central.Registry.OnRegister]: the registry's own, where
// OnRegisterDeclared delegates to it.
const onRegisterDeclaringFile = "internal/central/central_registry.go"

// TestEveryRegistryObserverDeclaresItsSeam is the guard that makes ADR
// 0065's wiring manifest a check rather than documentation.
//
// The manifest's whole claim is that a seam with no entry was not wired.
// That claim holds only while every registration goes through
// OnRegisterDeclared: one raw OnRegister call wires correctly, declares
// nothing, and silently makes "not in the manifest" mean "either not
// wired, or wired the old way" — which is no answer at all.
//
// The per-central observer is the seam class this adoption covers, and
// it is the one CLAUDE.md's second wiring rule names: walking the
// registry once is walking it at boot, so every cross-central
// collaborator arrives here. It is also where the class of defect that
// motivated the ADR keeps landing — a whole eviction subsystem shipped
// with a store method, an overlay method, unit tests and a doc comment
// naming its trigger, and no line anywhere calling it.
//
// Scope note: this scans production Go under cmd/ and internal/. Test
// files are exempt because a test that wires its own registry observer
// is describing a scenario, not composing a daemon.
func TestEveryRegistryObserverDeclaresItsSeam(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	fset := token.NewFileSet()

	var offenders []string
	scanned := 0

	for _, dir := range []string{"cmd", "internal"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if rel == onRegisterDeclaringFile {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			file, parseErr := parser.ParseFile(fset, path, src, 0)
			if parseErr != nil {
				return parseErr
			}
			scanned++

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "OnRegister" {
					return true
				}
				offenders = append(offenders, rel+":"+
					strconv.Itoa(fset.Position(call.Pos()).Line))
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", dir, walkErr)
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no production files — the guard is measuring nothing")
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("%d registry observer(s) attach without declaring the seam:\n  %s\n\n"+
			"Use reg.OnRegisterDeclared(wiring.Seam{Name, Collaborator, Phase, Why}, observe) "+
			"instead of reg.OnRegister. A seam that declares nothing can only be looked for by "+
			"name, and a name match is not a check that the collaborator arrived (ADR 0065).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestDeclaredSeamNamesAreDistinctAndScoped pins the shape of the seam
// names the manifest reports, because they are the identifiers guards
// and the diagnostics surface address a seam by — they outlive the Go
// function that declares them, so a rename must not silently become a
// new seam.
func TestDeclaredSeamNamesAreDistinctAndScoped(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	fset := token.NewFileSet()
	seen := map[string]string{}

	for _, dir := range []string{"cmd", "internal"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)

			ast.Inspect(file, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok || exprTypeName(cl.Type) != "wiring.Seam" {
					return true
				}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Name" {
						continue
					}
					lit, ok := kv.Value.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					name, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						continue
					}
					where := rel + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line)
					if prev, dup := seen[name]; dup {
						t.Errorf("seam name %q declared twice (%s and %s) — the manifest can no longer say which of them is missing",
							name, prev, where)
					}
					seen[name] = where
					if !strings.Contains(name, ".") {
						t.Errorf("seam name %q at %s is not `<subsystem>.<what>`", name, where)
					}
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", dir, walkErr)
		}
	}

	if len(seen) == 0 {
		t.Fatal("no wiring.Seam literals found — the guard is measuring nothing")
	}
}
