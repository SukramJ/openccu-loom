// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// safetyActivationFoldedSymbols are the private helpers the alarm and
// security packages each used to carry their own copy of. The rule they
// implemented now lives once, in internal/model/safety.
//
// This is a cheap anti-regression on the names, not a semantic guard: a
// divergent re-implementation under a different name still passes here.
// TestAlarmAndSecurityAgreeOnEnumActivation is the guard that measures
// the behaviour; this one only makes the obvious relapse loud.
var safetyActivationFoldedSymbols = map[string]string{
	"resolveActive":    "the activation rule is safety.ActiveFromRaw",
	"activeFromRaw":    "the activation rule is safety.ActiveFromRaw",
	"normalizeActive":  "the default rule is inside safety.ActiveFromRaw",
	"paramValueActive": "the default rule is inside safety.ActiveFromRaw",
	"rawIndex":         "index narrowing belongs to safety.ActiveFromRaw",
	"rawInt":           "index narrowing belongs to safety.ActiveFromRaw",
	"containsString":   "label membership belongs to safety.ActiveFromRaw",
}

// safetyActivationPackages are the two consumers that must read the rule
// rather than own it.
var safetyActivationPackages = []string{
	filepath.Join("..", "..", "internal", "alarm"),
	filepath.Join("..", "..", "internal", "security"),
}

// TestActivationRuleHasOneHome fails when either consumer package
// re-declares one of the folded activation helpers.
//
// Two copies of "does this wire value count as an activation" is how the
// alarm engine and the security plane came to disagree about an
// out-of-range enumeration index in the first place: each copy looked
// correct beside its own callers, and nothing compared them.
//
// Only production files are scanned. A test file is free to define a
// local helper of any name — it decides nothing about what a running
// daemon does.
func TestActivationRuleHasOneHome(t *testing.T) {
	t.Parallel()

	for _, dir := range safetyActivationPackages {
		fset := token.NewFileSet()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if why, banned := safetyActivationFoldedSymbols[fn.Name.Name]; banned {
					t.Errorf("%s declares %s again: %s (see internal/model/safety/activation.go)",
						filepath.ToSlash(path), fn.Name.Name, why)
				}
			}
		}
	}
}
