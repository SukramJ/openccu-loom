// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// rest_path_decode_test.go — single-decode tripwire for REST path params.
//
// The router installs decodedPathRouting as its first middleware, which
// forces chi to route on the already-decoded r.URL.Path. Every
// chi.URLParam therefore yields the final value. A handler that decodes
// it again rejects any identity containing a literal '%' and silently
// rewrites one whose component still looks like an escape — a defect
// that produced a 400 for valid security source refs and, worse, applied
// an operator's override to a different data point.
//
// The rule was documented in internal/north/rest/routing_decode.go and
// broken anyway, so it is checked here instead of trusted.

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRESTHandlersDoNotDecodePathParamsTwice fails when a REST source
// file feeds a chi.URLParam value into url.PathUnescape or
// url.QueryUnescape.
func TestRESTHandlersDoNotDecodePathParamsTwice(t *testing.T) {
	t.Parallel()
	root := filepath.Join(repoRoot(t), "internal", "north", "rest")

	var dirs []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	var offenders []string
	for _, dir := range dirs {
		fset, files := parseDir(t, dir)
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isSelector(call.Fun, "url", "PathUnescape", "QueryUnescape") {
					return true
				}
				for _, arg := range call.Args {
					inner, ok := arg.(*ast.CallExpr)
					if !ok || !isSelector(inner.Fun, "chi", "URLParam", "URLParamFromCtx") {
						continue
					}
					offenders = append(offenders,
						fset.Position(call.Pos()).String())
				}
				return true
			})
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("chi.URLParam values are already decoded; these call sites decode them a second time:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// isSelector reports whether expr is `pkg.Name(...)` for any of names.
func isSelector(expr ast.Expr, pkg string, names ...string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != pkg {
		return false
	}
	for _, n := range names {
		if sel.Sel.Name == n {
			return true
		}
	}
	return false
}
