// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// TestEveryReasonHasAPrecedenceEntry pins that a reason [Classify] can
// match is one [reasonPrecedence] can order.
//
// Classify collects matches into a set and then emits them by walking
// reasonPrecedence. A reason missing from that slice is therefore matched
// and then silently dropped: Classify returns it never, ClassifyPrimary
// degrades to ReasonUnknown, and the operator is told the classifier does
// not know why a parameter is hidden when in fact it does. Nothing fails —
// the only check that notices is an integration subtest behind
// `-tags=integration`, so a unit run stays green the whole time.
//
// The constants are read from the source rather than listed here, because
// a hand-kept second list is the thing this test exists to prevent.
func TestEveryReasonHasAPrecedenceEntry(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "reason.go", nil, 0)
	if err != nil {
		t.Fatalf("parse reason.go: %v", err)
	}

	var declared []HiddenReason
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 {
			return true
		}
		id, ok := vs.Type.(*ast.Ident)
		if !ok || id.Name != "HiddenReason" {
			return true
		}
		name := vs.Names[0].Name
		// ReasonUnknown is the fallback Classify returns when nothing
		// matched, not a rule it can match, so it has no place in an
		// ordering of explanations.
		if name == "ReasonUnknown" {
			return true
		}
		lit, ok := vs.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		declared = append(declared, HiddenReason(lit.Value[1:len(lit.Value)-1]))
		return true
	})

	if len(declared) == 0 {
		t.Fatal("parsed no HiddenReason constants — the guard is measuring nothing")
	}
	for _, r := range declared {
		if !slices.Contains(reasonPrecedence, r) {
			t.Errorf("reason %q is declared but absent from reasonPrecedence: Classify would match "+
				"it and then drop it, and ClassifyPrimary would report the parameter as hidden for "+
				"an unknown reason", r)
		}
	}
	for _, r := range reasonPrecedence {
		if !slices.Contains(declared, r) {
			t.Errorf("reasonPrecedence orders %q, which no HiddenReason constant declares", r)
		}
	}
}
