// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// combinedTypesWithoutProjection lists combined data-point types that
// deliberately have no north-bound projection.
//
// A type belongs here only when its absence from every north-bound
// surface is the intent — not when a projection has simply not been
// written yet. The map is empty today: every type carrying the
// IsCombined marker projects.
//
// Note that WeekProfile lives in this package but carries no IsCombined
// marker, so channels never surface it as a combined data point and it
// is out of this guard's scope. Schedules reach north-bound surfaces
// through the dedicated schedule-entity path instead.
var combinedTypesWithoutProjection = map[string]string{}

// TestCombinedProjectionCoversEveryCombinedType fails when a combined
// data-point type neither implements [payload.CombinedProjection] nor is
// listed as deliberately unprojected.
//
// This replaces a type-switch in the event bridge that had no default
// branch: a combined type nobody remembered to add a case for compiled,
// attached to its channel, published nothing, and looked exactly like a
// working one. The failure mode was invisible precisely because nothing
// failed.
//
// The type list is discovered by parsing the package rather than
// hand-maintained, because a hand-maintained list has the same defect as
// the switch it replaced — a new type is missing from both.
func TestCombinedProjectionCoversEveryCombinedType(t *testing.T) {
	t.Parallel()

	declared := combinedDataPointTypes(t)
	if len(declared) == 0 {
		t.Fatal("no combined data-point types discovered — the parse found nothing, " +
			"so a missing projection could not be detected either")
	}

	// Every discovered type must appear in exactly one of the two
	// buckets. The implementers map is keyed by type name so the parse
	// result and the compile-time assertions meet on the same key.
	// Constructed, not zero-valued: a kind that only a constructor sets
	// would read as empty on a bare struct and the CombinedKind check
	// below would fire on an artefact of the test rather than on the
	// production type.
	implementers := map[string]payload.CombinedProjection{
		"Timer":         combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"),
		"LevelCombined": combined.NewLevelCombined("VCU0000001:1", nil, "LEVEL", "LEVEL_2", "LEVEL_COMBINED"),
		"HSColor":       combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION"),
		"EnumSelect": combined.NewEnumSelect(combined.EnumSelectConfig{
			Address:           "VCU0000001:1",
			Kind:              "door_mode",
			CombinedParameter: "DOOR_MODE",
			StateParameter:    "DOOR_STATE",
			CommandParameter:  "DOOR_COMMAND",
			Modes:             []combined.EnumSelectMode{{State: "CLOSED", Command: "CLOSE"}},
		}),
	}

	for _, name := range declared {
		if reason, exempt := combinedTypesWithoutProjection[name]; exempt {
			if reason == "" {
				t.Errorf("%s is exempt from the projection requirement without a reason", name)
			}
			if _, alsoImplements := implementers[name]; alsoImplements {
				t.Errorf("%s is listed as deliberately unprojected but also implements the projection; "+
					"remove it from combinedTypesWithoutProjection", name)
			}
			continue
		}
		dp, known := implementers[name]
		if !known {
			t.Errorf("combined type %s has no north-bound projection.\n"+
				"Every combined data point must either implement payload.CombinedProjection "+
				"(so the event bridge publishes it) or be listed in combinedTypesWithoutProjection "+
				"with the reason it stays internal. Without one of the two it attaches to its "+
				"channel, publishes nothing, and is indistinguishable from a working data point.", name)
			continue
		}
		if kind := dp.CombinedKind(); kind == "" {
			t.Errorf("%s.CombinedKind() is empty; the kind is the retained topic segment "+
				"and an empty one silently suppresses publication", name)
		}
	}

	// The reverse direction: an entry in either bucket naming a type the
	// package no longer declares is stale, and a stale exemption is how a
	// renamed type slips back out of coverage.
	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[name] = struct{}{}
	}
	for name := range implementers {
		if _, ok := declaredSet[name]; !ok {
			t.Errorf("implementers lists %s, which the package no longer declares", name)
		}
	}
	for name := range combinedTypesWithoutProjection {
		if _, ok := declaredSet[name]; !ok {
			t.Errorf("combinedTypesWithoutProjection lists %s, which the package no longer declares", name)
		}
	}
}

// combinedDataPointTypes parses the package and returns the names of
// every exported struct that carries an IsCombined method — the marker
// [device.Channel.CombinedDataPoints] filters on, and therefore the exact
// set the event bridge walks.
func combinedDataPointTypes(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	structs := map[string]struct{}{}
	withMarker := map[string]struct{}{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					if _, isStruct := ts.Type.(*ast.StructType); isStruct {
						structs[ts.Name.Name] = struct{}{}
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name != "IsCombined" || d.Recv == nil || len(d.Recv.List) == 0 {
					continue
				}
				if recv := receiverTypeName(d.Recv.List[0].Type); recv != "" {
					withMarker[recv] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(withMarker))
	for name := range withMarker {
		if _, isStruct := structs[name]; isStruct {
			out = append(out, name)
		}
	}
	return out
}

// receiverTypeName unwraps a pointer receiver to its bare type name.
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}
