// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// enum_catalogue_parity_test.go — wire-contract drift detector for
// DataPointCategory and DataPointType.
//
// Both enums appear in the external wire contract (DataPointSummary.category
// and data_point_type in assets/openapi.yaml) and are exported verbatim in
// assets/schemas/enums.json. Any addition, removal, or rename is a breaking
// change for REST and WebSocket clients and must be caught before release.
//
// Strategy: parse pkg/hmenum/datapoint.go with go/ast to collect every const
// declaration whose declared type is DataPointCategory or DataPointType.
// Build a {go_name → wire_value} map from the string literals. Compare that
// map against the matching entry in assets/schemas/enums.json. Any set
// difference fails the test with a concrete diff and a regeneration hint.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// enumCatalogueEntry mirrors one element of the "values" array in
// assets/schemas/enums.json.
type enumCatalogueEntry struct {
	GoName    string `json:"go_name"`
	WireValue string `json:"wire_value"`
}

// enumCatalogueBlock mirrors one element of the top-level "enums" array.
type enumCatalogueBlock struct {
	Name   string               `json:"name"`
	Values []enumCatalogueEntry `json:"values"`
}

// enumCatalogueFile mirrors the top-level structure of assets/schemas/enums.json.
type enumCatalogueFile struct {
	Enums []enumCatalogueBlock `json:"enums"`
}

// loadEnumCatalogue reads and decodes assets/schemas/enums.json.
func loadEnumCatalogue(t *testing.T) enumCatalogueFile {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "assets", "schemas", "enums.json"))
	if err != nil {
		t.Fatalf("read assets/schemas/enums.json: %v", err)
	}
	var f enumCatalogueFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse assets/schemas/enums.json: %v", err)
	}
	return f
}

// extractEnumConstantsFromSource walks a single Go source file with go/ast
// and collects all const specs whose declared type matches targetType.
// Returns a map of {go_name → wire_value} (string literals unquoted).
func extractEnumConstantsFromSource(t *testing.T, srcPath, targetType string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, srcPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", srcPath, err)
	}

	out := make(map[string]string)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok.String() != "const" {
			continue
		}
		// currentType tracks the declared type within a const block; it
		// propagates from one spec to the next when omitted (iota pattern
		// or string-typed runs).
		var currentType string
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// A ValueSpec carries an explicit type node only when the
			// programmer wrote it; otherwise the previous type persists.
			if vs.Type != nil {
				if ident, ok := vs.Type.(*ast.Ident); ok {
					currentType = ident.Name
				} else {
					currentType = ""
				}
			}
			if currentType != targetType {
				continue
			}
			if len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind.String() != "STRING" {
				continue
			}
			goName := vs.Names[0].Name
			wireValue := strings.Trim(lit.Value, `"`)
			out[goName] = wireValue
		}
	}
	return out
}

// catalogueMapForEnum converts the values slice for a named enum block into a
// {go_name → wire_value} map, or fails the test when the enum is absent.
func catalogueMapForEnum(t *testing.T, catalogue enumCatalogueFile, enumName string) map[string]string {
	t.Helper()
	for _, block := range catalogue.Enums {
		if block.Name == enumName {
			m := make(map[string]string, len(block.Values))
			for _, v := range block.Values {
				m[v.GoName] = v.WireValue
			}
			return m
		}
	}
	t.Fatalf("assets/schemas/enums.json has no entry for enum %q — "+
		"run `go run ./script/export_schemas.go` to regenerate", enumName)
	return nil
}

// assertEnumMapParity compares the AST-extracted Go-source map against the
// JSON catalogue map and reports every difference.
func assertEnumMapParity(t *testing.T, enumName string, fromSource, fromJSON map[string]string) {
	t.Helper()
	failed := false

	// Collect all keys from both sides for a stable iteration order.
	allKeys := make(map[string]struct{}, len(fromSource)+len(fromJSON))
	for k := range fromSource {
		allKeys[k] = struct{}{}
	}
	for k := range fromJSON {
		allKeys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var extraInSource, extraInJSON, wireMismatch []string

	for _, goName := range sorted {
		srcWire, inSource := fromSource[goName]
		jsonWire, inJSON := fromJSON[goName]
		switch {
		case inSource && !inJSON:
			extraInSource = append(extraInSource, goName+"="+srcWire)
		case !inSource && inJSON:
			extraInJSON = append(extraInJSON, goName+"="+jsonWire)
		case inSource && inJSON && srcWire != jsonWire:
			wireMismatch = append(wireMismatch, goName+": source="+srcWire+" json="+jsonWire)
		}
	}

	if len(extraInSource) > 0 {
		t.Errorf("[%s] consts in pkg/hmenum/datapoint.go but MISSING from assets/schemas/enums.json "+
			"— run `go run ./script/export_schemas.go` to regenerate:\n  %s",
			enumName, strings.Join(extraInSource, "\n  "))
		failed = true
	}
	if len(extraInJSON) > 0 {
		t.Errorf("[%s] entries in assets/schemas/enums.json but NO matching const in pkg/hmenum/datapoint.go "+
			"— remove the stale entry or restore the const, then run `go run ./script/export_schemas.go`:\n  %s",
			enumName, strings.Join(extraInJSON, "\n  "))
		failed = true
	}
	if len(wireMismatch) > 0 {
		t.Errorf("[%s] wire-value mismatch between source and catalogue "+
			"— run `go run ./script/export_schemas.go` to regenerate:\n  %s",
			enumName, strings.Join(wireMismatch, "\n  "))
		failed = true
	}

	if !failed {
		t.Logf("[%s] %d consts match catalogue exactly", enumName, len(fromSource))
	}
}

// TestEnumCatalogueMatchesGoConstants asserts that the entries for
// DataPointCategory and DataPointType in assets/schemas/enums.json
// exactly match the const declarations in pkg/hmenum/datapoint.go.
// Any addition, removal, or wire-value rename is a breaking change for
// REST and WebSocket clients and must be reflected in the schema file.
//
// To fix a failure: update pkg/hmenum/datapoint.go, then regenerate
// the schema with `go run ./script/export_schemas.go`.
func TestEnumCatalogueMatchesGoConstants(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	srcPath := filepath.Join(root, "pkg", "hmenum", "datapoint.go")
	catalogue := loadEnumCatalogue(t)

	for _, tc := range []struct {
		enumName   string
		sourceType string
	}{
		{"DataPointCategory", "DataPointCategory"},
		{"DataPointType", "DataPointType"},
	} {
		t.Run(tc.enumName, func(t *testing.T) {
			t.Parallel()
			fromSource := extractEnumConstantsFromSource(t, srcPath, tc.sourceType)
			if len(fromSource) == 0 {
				t.Fatalf("no %s consts found in %s — source path may have moved", tc.sourceType, srcPath)
			}
			fromJSON := catalogueMapForEnum(t, catalogue, tc.enumName)
			assertEnumMapParity(t, tc.enumName, fromSource, fromJSON)
		})
	}
}
