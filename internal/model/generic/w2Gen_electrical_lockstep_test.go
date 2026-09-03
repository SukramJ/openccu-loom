// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// TestW2GenElectricalGroupCoversEveryElectricalParameter is the guard
// [electricalParameterSlot]'s doc comment names.
//
// The coupling it pins: the Matter assembler defers every data point that
// [matterMeasurementForParameter] classifies MatterMeasurementPower or
// MatterMeasurementEnergy into the electrical group, and
// [NewElectricalGroup] then drops any member [electricalParameterSlot]
// rejects. A parameter present on one side and absent on the other is
// therefore collected and silently discarded — and when it was the channel's
// only electrical member, the channel gets no endpoint at all. Nothing about
// that failure is visible at runtime, which is why it needs a guard.
//
// The two member sets are read out of the source rather than restated here.
// Restating them would make the guard agree with whichever list it was
// written from and stop measuring the other one.
func TestW2GenElectricalGroupCoversEveryElectricalParameter(t *testing.T) {
	t.Parallel()

	matterSide := w2GenSwitchCaseIdents(t, "matter.go", "matterMeasurementForParameter",
		func(ret *ast.ReturnStmt) bool {
			if len(ret.Results) != 1 {
				return false
			}
			sel, ok := ret.Results[0].(*ast.SelectorExpr)
			return ok && (sel.Sel.Name == "MatterMeasurementPower" || sel.Sel.Name == "MatterMeasurementEnergy")
		})

	groupSide := w2GenSwitchCaseIdents(t, "electrical.go", "electricalParameterSlot",
		func(ret *ast.ReturnStmt) bool {
			if len(ret.Results) != 2 {
				return false
			}
			ok, isIdent := ret.Results[1].(*ast.Ident)
			return isIdent && ok.Name == "true"
		})

	if len(matterSide) == 0 || len(groupSide) == 0 {
		t.Fatalf("extraction produced an empty set (matter=%v group=%v); the guard would pass "+
			"vacuously — check whether either function was renamed or restructured", matterSide, groupSide)
	}

	if strings.Join(matterSide, ",") != strings.Join(groupSide, ",") {
		t.Errorf("electrical membership has drifted:\n"+
			"  matterMeasurementForParameter Power/Energy arms: %v\n"+
			"  electricalParameterSlot accepted parameters:     %v\n"+
			"a parameter on the Matter side only is collected into the group and dropped; "+
			"a parameter on the group side only can never reach it", matterSide, groupSide)
	}
}

// w2GenSwitchCaseIdents returns, sorted, the rendered case expressions of every
// clause in fn's switch statement whose body returns something wantReturn
// accepts. Rendering is by identifier text (`hmenum.ParameterPower`), which is
// what "the same parameter on both sides" means for two switches in one
// package.
func w2GenSwitchCaseIdents(t *testing.T, file, fn string, wantReturn func(*ast.ReturnStmt) bool) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var decl *ast.FuncDecl
	for _, d := range parsed.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == fn && f.Recv == nil {
			decl = f
			break
		}
	}
	if decl == nil {
		t.Fatalf("%s: no top-level func %s — the guard's subject was renamed or moved", file, fn)
	}

	seen := map[string]bool{}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		matched := false
		for _, stmt := range clause.Body {
			if ret, isRet := stmt.(*ast.ReturnStmt); isRet && wantReturn(ret) {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
		for _, expr := range clause.List {
			seen[w2GenRenderExpr(expr)] = true
		}
		return true
	})

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// w2GenRenderExpr renders a case expression as `pkg.Ident` or `Ident`.
func w2GenRenderExpr(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return w2GenRenderExpr(v.X) + "." + v.Sel.Name
	case *ast.Ident:
		return v.Name
	default:
		return "<unrenderable>"
	}
}
