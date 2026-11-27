// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const auditPkgSuffix = "/internal/audit"

// isAuditEntry reports whether t is internal/audit.Entry.
func isAuditEntry(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "Entry" && obj.Pkg() != nil && strings.HasSuffix(obj.Pkg().Path(), auditPkgSuffix)
}

// TestAuditProducersRecordThroughRecordFrom pins that every change-log row
// outside internal/audit is written through audit.RecordFrom, and that no
// producer sets Entry.OnBehalfOf itself.
//
// RecordFrom is the one place that stamps a row with the request's actor —
// the authenticated principal and the X-On-Behalf-Of assertion. A producer
// that calls Recorder.Record directly writes a row without the assertion
// while its neighbours carry it, and nothing else would notice: the row
// looks complete. A producer that sets OnBehalfOf itself could attach an
// assertion no request made. Both are resolved with type information, so a
// Record method of an unrelated type does not count.
func TestAuditProducersRecordThroughRecordFrom(t *testing.T) {
	pkgs := loadProductionPackages(t)
	var direct, selfAsserted []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !strings.HasPrefix(p.PkgPath, "github.com/SukramJ/openccu-loom/") ||
			strings.HasSuffix(p.PkgPath, auditPkgSuffix) {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Record" || len(x.Args) != 1 {
						return true
					}
					if tv, ok := p.TypesInfo.Types[x.Args[0]]; ok && isAuditEntry(tv.Type) {
						direct = append(direct, p.Fset.Position(x.Pos()).String())
					}
				case *ast.CompositeLit:
					tv, ok := p.TypesInfo.Types[x]
					if !ok || !isAuditEntry(tv.Type) {
						return true
					}
					for _, el := range x.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "OnBehalfOf" {
								selfAsserted = append(selfAsserted, p.Fset.Position(kv.Pos()).String())
							}
						}
					}
				}
				return true
			})
		}
	})
	for _, pos := range direct {
		t.Errorf("%s: records an audit.Entry with Recorder.Record; use audit.RecordFrom(ctx, rec, entry) "+
			"so the row carries the request's actor", pos)
	}
	for _, pos := range selfAsserted {
		t.Errorf("%s: sets audit.Entry.OnBehalfOf; only audit.RecordFrom takes it, from the request", pos)
	}
}

// TestAuthNeverReadsOnBehalfOf pins that the asserted actor never reaches an
// authorization decision: internal/auth must not mention it at all. The
// header is a label an integration supplies for the change log; the daemon
// cannot check it, so a role or scope check that read it would grant on an
// unverifiable claim.
func TestAuthNeverReadsOnBehalfOf(t *testing.T) {
	pkgs := loadProductionPackages(t)
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !strings.HasSuffix(p.PkgPath, "/internal/auth") {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if x.Name == "OnBehalfOf" || x.Name == "OnBehalfOfHeader" {
						t.Errorf("%s: internal/auth refers to %s", p.Fset.Position(x.Pos()), x.Name)
					}
				case *ast.BasicLit:
					if strings.Contains(strings.ToLower(x.Value), "on-behalf-of") {
						t.Errorf("%s: internal/auth names the X-On-Behalf-Of header", p.Fset.Position(x.Pos()))
					}
				}
				return true
			})
		}
	})
}
