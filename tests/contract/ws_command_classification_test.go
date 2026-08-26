// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// ws_command_classification_test.go — reverse role-guard contract test.
//
// internal/north/rest/ws/commands.go maintains two disjoint tables:
// writeCommandRoles (state-changing commands, gated to a minimum role) and
// readOnlyCommands (commands that intentionally need no more than an
// authenticated identity). A command absent from both tables silently
// falls through the Dispatch role gate and becomes callable by any
// viewer — this test catches that omission at build time instead of
// letting it ship as a privilege-escalation bug.
//
// Strategy mirrors wsapi_schema_test.go's extractRegisteredWSCommands:
// walk the command source files with go/ast to get the ground-truth set
// of registered command names, then walk commands.go to extract the key
// sets of both classification maps. No ws package import and no DI
// scaffolding are needed for either.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// extractMapStringKeys parses the Go source file at path and returns the
// string keys of the package-level map literal assigned to varName, e.g.
//
//	var writeCommandRoles = map[string]auth.Role{
//	    "backup.trigger": auth.RoleAdmin,
//	    ...
//	}
//
// Fails the test if varName is not found or is not a composite literal
// keyed by string literals.
func extractMapStringKeys(t *testing.T, path, varName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filepath.Base(path), err)
	}

	var keys []string
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != varName {
			return true
		}
		if len(spec.Values) != 1 {
			return true
		}
		lit, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		found = true
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			keyLit, ok := kv.Key.(*ast.BasicLit)
			if !ok || keyLit.Kind.String() != "STRING" {
				continue
			}
			keys = append(keys, strings.Trim(keyLit.Value, `"`))
		}
		return true
	})

	if !found {
		t.Fatalf("var %s not found as a map composite literal in %s", varName, filepath.Base(path))
	}
	sort.Strings(keys)
	return keys
}

// TestEveryRegisteredWSCommandIsClassified asserts that every command
// name registered via router.Register(...) in the WS command source
// files appears in exactly one of writeCommandRoles or readOnlyCommands
// (internal/north/rest/ws/commands.go). A command in neither set would
// bypass Dispatch's role gate and be callable by an unprivileged viewer;
// a command in both sets is a contradictory (and therefore suspicious)
// classification.
func TestEveryRegisteredWSCommandIsClassified(t *testing.T) {
	t.Parallel()

	registered := extractRegisteredWSCommands(t)

	root := repoRoot(t)
	commandsGo := filepath.Join(root, "internal", "north", "rest", "ws", "commands.go")
	writeCommands := extractMapStringKeys(t, commandsGo, "writeCommandRoles")
	readOnly := extractMapStringKeys(t, commandsGo, "readOnlyCommands")

	writeSet := make(map[string]bool, len(writeCommands))
	for _, c := range writeCommands {
		writeSet[c] = true
	}
	readSet := make(map[string]bool, len(readOnly))
	for _, c := range readOnly {
		readSet[c] = true
	}

	var unclassified []string
	var doubleClassified []string
	for _, cmd := range registered {
		inWrite, inRead := writeSet[cmd], readSet[cmd]
		switch {
		case !inWrite && !inRead:
			unclassified = append(unclassified, cmd)
		case inWrite && inRead:
			doubleClassified = append(doubleClassified, cmd)
		}
	}

	if len(unclassified) > 0 {
		t.Errorf("commands registered but classified as neither read-only nor "+
			"write in internal/north/rest/ws/commands.go (add each to writeCommandRoles "+
			"or readOnlyCommands):\n  %s", strings.Join(unclassified, "\n  "))
	}
	if len(doubleClassified) > 0 {
		t.Errorf("commands present in both writeCommandRoles and readOnlyCommands "+
			"(remove from one):\n  %s", strings.Join(doubleClassified, "\n  "))
	}

	// Symmetric direction: every classified command must actually be
	// registered, so a stale rename doesn't leave a dead entry that masks
	// the fact that the real (renamed) command is unclassified.
	registeredSet := make(map[string]bool, len(registered))
	for _, c := range registered {
		registeredSet[c] = true
	}
	var deadWrite []string
	for _, c := range writeCommands {
		if !registeredSet[c] {
			deadWrite = append(deadWrite, c)
		}
	}
	var deadReadOnly []string
	for _, c := range readOnly {
		if !registeredSet[c] {
			deadReadOnly = append(deadReadOnly, c)
		}
	}
	if len(deadWrite) > 0 {
		t.Errorf("writeCommandRoles entries with no registered handler (dead entry or typo):\n  %s",
			strings.Join(deadWrite, "\n  "))
	}
	if len(deadReadOnly) > 0 {
		t.Errorf("readOnlyCommands entries with no registered handler (dead entry or typo):\n  %s",
			strings.Join(deadReadOnly, "\n  "))
	}
}
