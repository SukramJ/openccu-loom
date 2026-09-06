// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// compositionRootSeam names a dependency struct an adapter declares and
// the composition root fills, with the fields the daemon deliberately
// leaves at their zero value.
type compositionRootSeam struct {
	pkg       string // the identifier as it is spelled at the fill site, so an import alias counts
	typeName  string
	declDir   string
	fillFile  string
	minDecl   int // a parse that reads fewer fields than this is broken, not lenient
	notFilled map[string]string
}

// compositionRootSeams is the covered set.
//
// Every entry's gaps were measured against its consumer one at a time.
// The map is not a pattern fill: an earlier ledger in this package grouped
// seventy-five fields under one reason and thirteen of them were something
// else entirely, so a reason here claims only what was read.
//
// Known limitation, and it is the reason four candidate findings were
// wrong before this guard existed: the measurement reads the composite
// literal only. A field wired after construction — `deps.Foo = bar`, or a
// `SetFoo(...)` call on the built object — reads as absent here. Both are
// legitimate shapes: cachereset_wiring.go assigns three fields behind
// typed nil-checks, and daemon_matter.go installs three Opcreds callbacks
// through setters. So a seam whose composition root uses either shape is
// left out rather than covered with entries that would have to excuse the
// guard's own blind spot.
//
// Seven further seams are therefore not covered yet: ws.DefaultCommandsConfig,
// ws.ExtendedCommandsConfig, ws.MissingCommandsConfig, mqtt.BridgeConfig,
// core.OpcredsConfig, handlers.AuthDeps and cachereset.Deps. Between them
// they carry twenty-one unfilled fields — most deliberately optional, one
// (ws.ExtendedCommandsConfig) registering explicit stub handlers that
// answer with an error a caller can see. Covering them needs each of those
// twenty-one read individually, plus post-construction wiring taught to
// the parse.
var compositionRootSeams = []compositionRootSeam{
	{
		pkg: "rest", typeName: "Deps",
		declDir:  "internal/north/rest",
		fillFile: "cmd/openccu-loom/daemon_rest_mount.go",
		minDecl:  100,
		notFilled: map[string]string{
			"WriteTimeout": "NewRouter substitutes 30s for a zero value (internal/north/rest/router.go, at the top of NewRouter) and no config key exposes it, so the daemon has nothing to pass — leaving it unset selects the default rather than dropping a collaborator",
		},
	},
	{
		pkg: "mcp", typeName: "Deps",
		declDir:  "internal/north/mcp",
		fillFile: "cmd/openccu-loom/daemon_rest_mount.go",
		minDecl:  20,
	},
	{
		pkg: "adapter", typeName: "WireDeps",
		declDir:  "internal/central/adapter",
		fillFile: "cmd/openccu-loom/daemon_southbound.go",
		minDecl:  12,
	},
	// The matterbridge.Config seam is not listed: the struct is declared in
	// github.com/SukramJ/go-fabric/bridge, and this guard reads the
	// declaration out of a repo-relative directory. It cannot see a
	// declaration that lives in another module, and a seam entry that
	// silently reads nothing is worse than no entry at all.
	{
		// The bridge consumes an assembled topology and no longer holds
		// the knobs that govern assembly; the daemon builds the assembler
		// and fills these. The seam moved with them — the never-filled
		// IncludeMeasurements this guard was written for now lives here,
		// and dropping the entry would have retired the coverage along
		// with the field's old home.
		pkg: "matteradapter", typeName: "Config",
		declDir:  "internal/north/matteradapter",
		fillFile: "cmd/openccu-loom/daemon_matter.go",
		minDecl:  8,
		notFilled: map[string]string{
			"NameResolver": "nil selects the model-backed resolver the assembler builds from the walked devices (internal/north/matteradapter/assembler.go), which is the daemon's own naming path — the field exists for an owner substituting a different model",
		},
	},
	{
		pkg: "handlers", typeName: "SetupService",
		declDir:  "internal/north/rest/handlers",
		fillFile: "cmd/openccu-loom/daemon_rest_mount.go",
		minDecl:  4,
	},
}

// TestCompositionRootHandsOverEveryDeclaredField pins that for each covered
// seam, the set the struct declares and the set the composition root fills
// are the same set.
//
// It exists because the per-field wiring pins cannot cover this. A pin
// names one field, so it guards only a field somebody already thought
// about — and the failure that actually happens is a *new* field added to
// the struct whose author fills it in the tests and forgets the daemon. No
// pin exists for it, because the pin would have to be written by the same
// person in the same change that forgot it.
//
// Both halves of that were live when the guard was written. Seven rest.Deps
// fields were nillable with the whole contract and cmd suite staying green.
// And bridge.Config.IncludeMeasurements had never been filled at all: the
// eligibility surface reported eight derived sensor types as mappable, an
// operator could allowlist them through the SPA, and the assembler dropped
// every one at assembler.go:353 — while the field's own comment claimed a
// config flag that did not exist. The assembler was tested both ways, which
// is exactly the bracketing test CLAUDE.md warns about: it set the flag
// itself, so it proved the assembler could honour the value and never that
// the daemon supplied one.
//
// The route or feature comes up either way. That is the point: nothing
// fails at boot, so the operator sees a capability that quietly does
// nothing rather than a daemon that refuses to start.
func TestCompositionRootHandsOverEveryDeclaredField(t *testing.T) {
	root := repoRoot(t)

	for _, seam := range compositionRootSeams {
		t.Run(seam.pkg+"."+seam.typeName, func(t *testing.T) {
			declared := declaredStructFields(t, filepath.Join(root, seam.declDir), seam.typeName)
			if len(declared) < seam.minDecl {
				t.Fatalf("read only %d fields from %s.%s in %s, expected at least %d — "+
					"the parse is wrong, and a guard that reads too few fields passes by measuring nothing",
					len(declared), seam.pkg, seam.typeName, seam.declDir, seam.minDecl)
			}

			filled, nilled := filledLiteralFields(t, filepath.Join(root, seam.fillFile), seam.pkg, seam.typeName)
			if len(filled) == 0 {
				t.Fatalf("found no %s.%s literal in %s — the guard cannot measure a handover it cannot find",
					seam.pkg, seam.typeName, seam.fillFile)
			}

			var missing, explicitNil, stale []string

			for _, field := range declared {
				switch {
				case nilled[field]:
					explicitNil = append(explicitNil, field)
				case filled[field]:
				default:
					if _, known := seam.notFilled[field]; !known {
						missing = append(missing, field)
					}
				}
			}

			declaredSet := map[string]bool{}
			for _, f := range declared {
				declaredSet[f] = true
			}
			for field := range seam.notFilled {
				if !declaredSet[field] {
					stale = append(stale, field)
				}
			}

			sort.Strings(missing)
			sort.Strings(explicitNil)
			sort.Strings(stale)

			if len(missing) > 0 {
				t.Errorf("%d %s.%s field(s) are declared but never handed over in %s:\n  %s\n\n"+
					"The feature still comes up, so nothing fails at boot and no existing test changes "+
					"colour — the capability just behaves as if it were switched off. Either fill the "+
					"field in the composition root, or add it to this seam's notFilled map with the "+
					"reason its absence is a choice. If it is wired after construction instead, this "+
					"guard cannot see that: say so in the reason.",
					len(missing), seam.pkg, seam.typeName, seam.fillFile, strings.Join(missing, "\n  "))
			}
			if len(explicitNil) > 0 {
				t.Errorf("%d %s.%s field(s) are spelled out as nil in %s:\n  %s\n\n"+
					"Naming a field and handing over nothing reads like wiring to every reader "+
					"and to every pin that only checks the field is mentioned.",
					len(explicitNil), seam.pkg, seam.typeName, seam.fillFile, strings.Join(explicitNil, "\n  "))
			}
			if len(stale) > 0 {
				t.Errorf("%d notFilled entr(ies) for %s.%s name a field it no longer declares:\n  %s\n\n"+
					"A ledger entry for a field that does not exist excuses nothing and hides "+
					"how much of the struct is really covered.",
					len(stale), seam.pkg, seam.typeName, strings.Join(stale, "\n  "))
			}
		})
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
