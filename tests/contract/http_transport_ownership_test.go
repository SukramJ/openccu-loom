// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//
// It also rejects http.DefaultClient and the http.Get/Post/Head/PostForm
// convenience wrappers that dispatch through it. They are the same
// defect written shorter — the shared transport plus no request deadline
// at all — and while the guard only matched composite literals, four
// call sites (the add-on update checker and downloader, the OIDC
// discovery/JWKS/exchange fallbacks) carried it unnoticed.

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
//
// It is empty, and that is the interesting part. Its one entry exempted
// internal/httpx/transport.go on the grounds that the helper "constructs the
// owned transport itself" — which is true, and is exactly why the exemption
// was unnecessary: NewClient sets Transport explicitly, so the file satisfies
// the guard on its own terms. Exempting it only removed the guard from the
// one file that defines the pattern everything else is measured against.
var httpClientOwnershipExempt = map[string]string{}

// TestEveryHTTPClientOwnsItsTransport fails on any composite literal of
// type http.Client that omits the Transport field, on any reference to
// http.DefaultClient, and on the http package's request helpers that
// dispatch through it.
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
			offenders = append(offenders, unownedHTTPClientUses(t, path, rel)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", walkRoot, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("%d HTTP client use(s) on the process-wide default transport:\n\n%s\n\n"+
			"A nil Transport uses the process-wide http.DefaultTransport, which couples\n"+
			"independent callers through one connection pool — closing idle connections on\n"+
			"it breaks requests other callers have in flight. http.DefaultClient (and the\n"+
			"http.Get/Post/Head/PostForm helpers that dispatch through it) additionally\n"+
			"carry no request deadline, so a server that accepts the connection and never\n"+
			"answers blocks the caller for the daemon's remaining uptime.\n"+
			"Use httpx.NewClient(timeout), or set Transport: httpx.NewTransport() when the\n"+
			"transport needs customising.",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// defaultDispatchHelpers are the http package functions that issue a
// request through http.DefaultClient, and therefore inherit both its
// shared transport and its absent deadline.
var defaultDispatchHelpers = map[string]bool{
	"Get": true, "Post": true, "Head": true, "PostForm": true,
}

// unownedHTTPClientUses returns one entry per `http.Client{…}` literal in
// path that has no Transport field, per reference to http.DefaultClient,
// and per call to one of [defaultDispatchHelpers].
func unownedHTTPClientUses(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var found []string
	report := func(n ast.Node, what string) {
		found = append(found, fmt.Sprintf("  %s:%d (%s)", rel, fset.Position(n.Pos()).Line, what))
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if !isHTTPSelector(node.Type, "Client") {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Transport" {
					return true
				}
			}
			report(node, "http.Client literal without a Transport")
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || !defaultDispatchHelpers[sel.Sel.Name] {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" {
				report(node, "http."+sel.Sel.Name+" dispatches through http.DefaultClient")
			}
		case *ast.SelectorExpr:
			if isHTTPSelector(node, "DefaultClient") {
				report(node, "http.DefaultClient")
			}
		}
		return true
	})
	return found
}

// isHTTPSelector reports whether expr is the selector `http.<name>`.
func isHTTPSelector(expr ast.Expr, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "http"
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
