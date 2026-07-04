// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoSlogAnySecretStruct guards the redaction invariant documented on
// hmlog.RedactingHandler: redaction is key-based and shallow, so a whole
// secret-bearing struct passed via slog.Any("cfg", cfg) reaches the
// underlying handler verbatim and leaks every secret field it carries.
// Production code must instead expose the individual, named fields through
// slog.Group / slog.Attr so each sensitive key is matched and masked.
//
// The guard flags any production slog.Any(key, value) call whose VALUE
// argument is (or dereferences to) an identifier that names a config /
// secret / credential struct — never the key, which is already handled by
// the RedactingHandler. A benign field access such as cfg.Version is left
// alone; only the whole struct (cfg, config, authConfig, x.Secret, creds,
// …) trips the rule.
func TestNoSlogAnySecretStruct(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")
	roots := []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "pkg"),
		filepath.Join(repoRoot, "cmd"),
	}

	fset := token.NewFileSet()
	var violations []string

	for _, root := range roots {
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip vendored, generated, and sibling-worktree trees.
				base := d.Name()
				if base == "node_modules" || base == "spa_dist" || base == ".claude" || base == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parse %s: %v", path, perr)
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isSlogAny(call) || len(call.Args) < 2 {
					return true
				}
				if name, ok := secretBearingName(call.Args[1]); ok {
					pos := fset.Position(call.Pos())
					violations = append(violations,
						filepath.Base(path)+":"+strconv.Itoa(pos.Line)+": slog.Any value "+name+
							" looks like a secret-bearing struct — expose named fields via slog.Group/slog.Attr instead")
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", root, walkErr)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("slog.Any secret-struct guard failed:\n  %s", strings.Join(violations, "\n  "))
	}
}

// isSlogAny reports whether call is a `slog.Any(...)` invocation.
func isSlogAny(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Any" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "slog"
}

// secretBearingName inspects a slog.Any value argument and returns the
// offending name when it (or its final selector) denotes a whole config /
// secret / credential struct. A plain field access like cfg.Version is not
// flagged — only expressions whose leaf identifier is itself a secret
// struct.
func secretBearingName(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.Ident:
		if looksLikeSecretStruct(v.Name) {
			return v.Name, true
		}
	case *ast.SelectorExpr:
		if looksLikeSecretStruct(v.Sel.Name) {
			return v.Sel.Name, true
		}
	case *ast.UnaryExpr: // &cfg
		return secretBearingName(v.X)
	case *ast.StarExpr: // *cfg
		return secretBearingName(v.X)
	}
	return "", false
}

// looksLikeSecretStruct classifies an identifier name as denoting a whole
// secret-bearing struct. It matches the common names (cfg, config, creds)
// and any name whose lowercase form ends in a config/secret/credential
// token, so authConfig, oidcSecret, and userCredentials are all caught
// while benign field names (Version, Host, Value) are not.
func looksLikeSecretStruct(name string) bool {
	lc := strings.ToLower(name)
	switch lc {
	case "cfg", "config", "creds", "secret", "secrets", "credential", "credentials":
		return true
	}
	for _, suffix := range []string{"config", "secret", "secrets", "credential", "credentials", "password", "passwd"} {
		if strings.HasSuffix(lc, suffix) {
			return true
		}
	}
	return false
}
