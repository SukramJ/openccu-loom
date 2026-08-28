// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cdpkind

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestKindsCoversEveryKindConstant pins [Kinds] against the constant
// block it publishes. The constants are read out of the source with
// the Go parser rather than re-listed here — a second hand-written
// list would drift in step with the first and prove nothing.
func TestKindsCoversEveryKindConstant(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "kind.go", nil, 0)
	if err != nil {
		t.Fatalf("parse kind.go: %v", err)
	}

	declared := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Kind") || name.Name == "KindUnknown" {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				declared[name.Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("parsed no Kind constants from kind.go — the guard would pass vacuously")
	}

	published := map[string]bool{}
	for _, k := range Kinds() {
		if published[k] {
			t.Errorf("Kinds() lists %q twice", k)
		}
		published[k] = true
	}

	for name, value := range declared {
		if !published[value] {
			t.Errorf("constant %s = %q is not in Kinds()", name, value)
		}
		delete(published, value)
	}
	for leftover := range published {
		t.Errorf("Kinds() lists %q, which no Kind* constant declares", leftover)
	}
}

// TestCapabilityFlagsCoversEveryHelper pins [CapabilityFlags] against
// the per-category helpers that actually build the maps. The helpers
// are called for real; nothing about their key sets is assumed here.
func TestCapabilityFlagsCoversEveryHelper(t *testing.T) {
	produced := map[string]bool{}
	for _, m := range []map[string]bool{
		lightCaps(custom.LightCapabilities{}),
		coverCaps(custom.CoverCapabilities{}),
		climateCaps(custom.ClimateCapabilities{}),
		lockCaps(custom.LockCapabilities{}),
		sirenCaps(custom.SirenCapabilities{}),
	} {
		for k := range m {
			produced[k] = true
		}
	}
	if len(produced) == 0 {
		t.Fatal("the capability helpers produced no keys — the guard would pass vacuously")
	}

	published := map[string]bool{}
	for _, f := range CapabilityFlags() {
		if published[f] {
			t.Errorf("CapabilityFlags() lists %q twice", f)
		}
		published[f] = true
	}

	var missing, extra []string
	for k := range produced {
		if !published[k] {
			missing = append(missing, k)
		}
	}
	for k := range published {
		if !produced[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("capability keys a helper emits but CapabilityFlags() omits: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("CapabilityFlags() lists keys no helper emits: %v", extra)
	}
}
