// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// routerDepsNotHandedOver records every rest.Deps field the daemon's own
// literal does not fill, with the reason.
//
// It is deliberately not the same map as routerDepsLeftNil, which is about
// the *test helper* fullyWiredRouterDeps: a field the helper omits makes
// one test measure less than it claims, while a field the daemon omits
// makes the running product do less than its API describes. Sharing a map
// would let the weaker reason answer for the stronger question.
var routerDepsNotHandedOver = map[string]string{
	"WriteTimeout": "NewRouter substitutes 30s when the field is zero (internal/north/rest/router.go:595), and no config key exposes it, so the daemon has nothing to pass — leaving it unset selects the default rather than dropping a collaborator",
}

// TestDaemonHandsOverEveryRouterDependency pins that the set rest.Deps
// declares and the set the composition root fills are the same set.
//
// It exists because the per-field wiring pins cannot cover this. A pin
// names one field, so it can only guard a field somebody already thought
// about — and the failure that actually happens is a *new* field added to
// rest.Deps whose author fills it in the tests and forgets the daemon. No
// pin exists for it, because the pin would have to be written by the same
// person in the same change that forgot it. Seven fields were reachable
// this way when the guard was written: nilling Discovery or ValuesCache in
// the composition root left the whole contract and cmd suite green.
//
// The route mounts either way — that is the point. The handler decides
// what an absent facade means, so the endpoint keeps answering, and the
// operator sees a feature that quietly does nothing instead of a daemon
// that refuses to start.
func TestDaemonHandsOverEveryRouterDependency(t *testing.T) {
	const (
		declSite = "internal/north/rest"
		fillSite = "cmd/openccu-loom/daemon_rest_mount.go"
	)

	root := repoRoot(t)
	declared := declaredStructFields(t, filepath.Join(root, declSite), "Deps")
	if len(declared) < 100 {
		t.Fatalf("read only %d fields from rest.Deps in %s — the parse is wrong, "+
			"and a guard that reads too few fields passes by measuring nothing",
			len(declared), declSite)
	}

	filled, nilled := filledLiteralFields(t, filepath.Join(root, fillSite), "rest", "Deps")
	if len(filled) == 0 {
		t.Fatalf("found no rest.Deps literal in %s — the guard cannot measure a handover it cannot find", fillSite)
	}

	var missing, explicitNil, stale []string

	for _, field := range declared {
		if nilled[field] {
			explicitNil = append(explicitNil, field)
			continue
		}
		if filled[field] {
			continue
		}
		if _, known := routerDepsNotHandedOver[field]; known {
			continue
		}
		missing = append(missing, field)
	}

	declaredSet := map[string]bool{}
	for _, f := range declared {
		declaredSet[f] = true
	}
	for field := range routerDepsNotHandedOver {
		if !declaredSet[field] {
			stale = append(stale, field)
		}
	}

	sort.Strings(missing)
	sort.Strings(explicitNil)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("%d rest.Deps field(s) are declared but never handed over in %s:\n  %s\n\n"+
			"The route still mounts, so nothing fails at boot and no existing test changes "+
			"colour — the capability just answers as if it were switched off. Either fill "+
			"the field in the composition root, or add it to routerDepsNotHandedOver with "+
			"the reason its absence is a choice.",
			len(missing), fillSite, strings.Join(missing, "\n  "))
	}
	if len(explicitNil) > 0 {
		t.Errorf("%d rest.Deps field(s) are spelled out as nil in %s:\n  %s\n\n"+
			"Naming a field and handing over nothing reads like wiring to every reader "+
			"and to every pin that only checks the field is mentioned.",
			len(explicitNil), fillSite, strings.Join(explicitNil, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%d routerDepsNotHandedOver entr(ies) name a field rest.Deps no longer declares:\n  %s\n\n"+
			"A ledger entry for a field that does not exist excuses nothing and hides "+
			"how much of the struct is really covered.",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// declaredStructFields returns every field name the named struct declares
// in the given package directory, flattening grouped declarations
// (`A, B SomeType`) and skipping embedded fields, which have no name to
// hand over.
func declaredStructFields(t *testing.T, dir, typeName string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var fields []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				for _, nm := range f.Names {
					if nm.IsExported() {
						fields = append(fields, nm.Name)
					}
				}
			}
			return false
		})
	}
	sort.Strings(fields)
	return fields
}

// filledLiteralFields returns the fields a `<pkg>.<typeName>{...}` literal
// in the given file names, and separately those it names with a literal
// nil.
func filledLiteralFields(t *testing.T, file, pkg, typeName string) (filled, nilled map[string]bool) {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	filled = map[string]bool{}
	nilled = map[string]bool{}

	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := cl.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != typeName {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != pkg {
			return true
		}
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			filled[key.Name] = true
			if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "nil" {
				nilled[key.Name] = true
			}
		}
		return true
	})
	return filled, nilled
}
