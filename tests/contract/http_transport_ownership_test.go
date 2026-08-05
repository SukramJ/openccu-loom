// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// http_transport_ownership_test.go enforces that every http.Client the
// daemon builds owns its transport.
//
// Leaving http.Client.Transport nil falls back to the process-wide
// http.DefaultTransport, which couples otherwise unrelated callers
// through a single connection pool: the CCU readiness probe, SSDP
// discovery, firmware downloads and the JSON-RPC client would all share
// one. Whatever closes idle connections on that transport then tears
// down requests the others have in flight — surfacing as
// `transport connection broken: http: CloseIdleConnections called`.
//
// The test suite is where this bites hardest and most visibly:
// httptest.Server.Close calls CloseIdleConnections on
// http.DefaultTransport unconditionally (the stdlib documents it as a
// courtesy to users of the default transport), so in a package with
// parallel tests one server shutting down breaks a request another test
// is issuing. That is a defect in the code under test, not test noise —
// the coupling is equally present in production, it simply has no
// equally reliable trigger there.
//
// This guard exists because the insight was already available and did
// not spread: cmd/hmcli/client.go had cloned the transport for exactly
// this reason, with a comment naming the flaky parallel test that
// exposed it, while eight other call sites kept the default. A rule
// without a guard decays back to a comment.

package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// httpClientOwnershipRoots are the trees searched. Test files are
// excluded during the walk: a test that deliberately constructs a bare
// client to exercise a nil-transport path is not the defect this guards.
var httpClientOwnershipRoots = []string{"internal", "cmd", "pkg"}

// httpClientOwnershipExempt lists files allowed to build an http.Client
// without an explicit Transport, with the reason. Keep it empty unless
// there is a genuine case; an entry here is a claim someone verified.
var httpClientOwnershipExempt = map[string]string{
	// internal/httpx is the helper that supplies the transport; the
	// client it returns carries one by construction.
	"internal/httpx/transport.go": "constructs the owned transport itself",
}

// TestEveryHTTPClientOwnsItsTransport fails on any composite literal of
// type http.Client that omits the Transport field.
func TestEveryHTTPClientOwnsItsTransport(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRootForHTTPGuard(t)
	var offenders []string

	for _, root := range httpClientOwnershipRoots {
		walkRoot := filepath.Join(repoRoot, root)
		if _, err := os.Stat(walkRoot); err != nil {
			continue
		}
		err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "spa_dist" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			if _, ok := httpClientOwnershipExempt[rel]; ok {
				return nil
			}
			offenders = append(offenders, bareHTTPClientLiterals(t, path, rel)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", walkRoot, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("%d http.Client literal(s) without an explicit Transport:\n\n%s\n\n"+
			"A nil Transport uses the process-wide http.DefaultTransport, which couples\n"+
			"independent callers through one connection pool — closing idle connections on\n"+
			"it breaks requests other callers have in flight.\n"+
			"Use httpx.NewClient(timeout), or set Transport: httpx.NewTransport() when the\n"+
			"transport needs customising.",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// bareHTTPClientLiterals returns one entry per `http.Client{…}` literal
// in path that has no Transport field.
func bareHTTPClientLiterals(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Client" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Transport" {
				return true
			}
		}
		found = append(found, fmt.Sprintf("  %s:%d", rel, fset.Position(lit.Pos()).Line))
		return true
	})
	return found
}

// findRepoRootForHTTPGuard walks up from the test's directory to the
// module root.
func findRepoRootForHTTPGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}
