// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build ignore

// export_schemas — emit machine-readable enum + type schemas for
// external-language consumers (Python, TypeScript, Rust, …). Run
// from the repo root:
//
//	go run ./script/export_schemas.go
//
// Writes two files into assets/schemas/:
//
//   - enums.json — every named-string enum from pkg/hmenum (and its
//     selected siblings) with the canonical {go_name: wire_value}
//     mapping. Codegen tools turn each entry into the equivalent
//     enum in the target language without touching Go AST.
//   - types.json — JSON-Schema Draft-7 stubs for the small set of
//     value carriers callers need (DataPointKey, ParamValue,
//     ValueKind). Currently hand-curated; the script normalises the
//     shape so a future packing-tool can refresh it deterministically.
//
// The output files are checked into the repo so release artefacts
// always carry the latest contract. ADR 0020 designates these as the
// canonical codegen surface for non-Go clients.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// extractEnums walks every non-test .go file in dir, collects every
// `type X string` declaration, and accumulates the const values whose
// declared type is one of those named types. Returns a stable
// {type-name: {go-const-name: wire-value}} map.
func extractEnums(dir string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	stringTypes := map[string]bool{}

	files := map[string]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		files[path] = f
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Pass 1: collect named string types.
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				id, ok := ts.Type.(*ast.Ident)
				if !ok {
					continue
				}
				if id.Name == "string" {
					stringTypes[ts.Name.Name] = true
					out[ts.Name.Name] = map[string]string{}
				}
			}
		}
	}

	// Pass 2: collect const values whose declared type matches.
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var declaredType string
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if vs.Type != nil {
					if id, ok := vs.Type.(*ast.Ident); ok {
						declaredType = id.Name
					}
				}
				if !stringTypes[declaredType] {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value := strings.Trim(lit.Value, `"`)
					out[declaredType][name.Name] = value
				}
			}
		}
	}

	// Drop named string types that have no consts (helper types
	// like aliases that only appear on the wire indirectly).
	for name, vals := range out {
		if len(vals) == 0 {
			delete(out, name)
		}
	}
	return out, nil
}

// curatedTypes returns the JSON-Schema Draft-7 entries for the small
// set of cross-cutting value carriers external clients need. Hand-
// curated because Go's struct shape (esp. the typed-union ParamValue)
// doesn't reflect cleanly into a generic schema without lossy
// projection.
func curatedTypes() map[string]any {
	return map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"definitions": map[string]any{
			"ValueKind": map[string]any{
				"type":        "string",
				"description": "Tag of the active member of a ParamValue.",
				"enum":        []string{"none", "bool", "int", "float", "string", "list"},
			},
			"ParamValue": map[string]any{
				"type":        "object",
				"description": "Typed parameter value. Exactly one of bool/int/float/string/list is meaningful per Kind.",
				"required":    []string{"kind"},
				"properties": map[string]any{
					"kind":   map[string]any{"$ref": "#/definitions/ValueKind"},
					"bool":   map[string]any{"type": "boolean"},
					"int":    map[string]any{"type": "integer"},
					"float":  map[string]any{"type": "number"},
					"string": map[string]any{"type": "string"},
					"list": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
			},
			"DataPointKey": map[string]any{
				"type":        "object",
				"description": "Composite key identifying one wire data point.",
				"required":    []string{"interface", "device_address", "channel_no", "paramset_key", "parameter"},
				// The three vocabularies are enums, and enums live in
				// enums.json — a different document with a different shape,
				// which no `$ref` from here can reach. Until this was
				// noticed the fields referenced #/definitions/Interface,
				// ParamsetKey and Parameter, none of which this document
				// defines: every one of them dangled, and a strict
				// JSON-Schema consumer would have failed to resolve the
				// type. There is no such consumer today, which is exactly
				// why nothing said so.
				//
				// They are strings on the wire; the description names where
				// the vocabulary is published.
				"properties": map[string]any{
					"interface": map[string]any{
						"type":        "string",
						"description": "Wire interface id. Vocabulary: the Interface enum in enums.json.",
					},
					"device_address": map[string]any{"type": "string"},
					"channel_no":     map[string]any{"type": "integer", "minimum": 0},
					"paramset_key": map[string]any{
						"type":        "string",
						"description": "Paramset selector. Vocabulary: the ParamsetKey enum in enums.json.",
					},
					"parameter": map[string]any{
						"type":        "string",
						"description": "Wire parameter name. Vocabulary: the Parameter enum in enums.json.",
					},
				},
			},
		},
	}
}

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	enums, err := extractEnums(filepath.Join(repoRoot, "pkg", "hmenum"))
	if err != nil {
		log.Fatalf("extract enums: %v", err)
	}

	outDir := filepath.Join(repoRoot, "assets", "schemas")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", outDir, err)
	}

	// Sort top-level keys for deterministic output.
	enumNames := make([]string, 0, len(enums))
	for k := range enums {
		enumNames = append(enumNames, k)
	}
	sort.Strings(enumNames)

	// Re-key into a sorted structure so the JSON output is stable
	// across runs (map iteration order is random in Go).
	stableEnums := make([]any, 0, len(enumNames))
	for _, name := range enumNames {
		vals := enums[name]
		constNames := make([]string, 0, len(vals))
		for k := range vals {
			constNames = append(constNames, k)
		}
		sort.Strings(constNames)
		sortedVals := make([]any, 0, len(constNames))
		for _, cn := range constNames {
			sortedVals = append(sortedVals, map[string]string{
				"go_name":    cn,
				"wire_value": vals[cn],
			})
		}
		stableEnums = append(stableEnums, map[string]any{
			"name":   name,
			"values": sortedVals,
		})
	}

	// Hub-level pseudo-addresses fill the address slot of the routing key for
	// entities with no real CCU device address (hub singletons, install-mode,
	// programs, sysvars). Exporting them here lets a wire client read them from
	// the daemon contract instead of the reference stack's constants.
	pseudoAddresses := []any{
		map[string]string{"go_name": "HubAddress", "address": routingkey.HubAddress},
		map[string]string{"go_name": "InstallModeAddress", "address": routingkey.InstallModeAddress},
		map[string]string{"go_name": "ProgramAddress", "address": routingkey.ProgramAddress},
		map[string]string{"go_name": "SysvarAddress", "address": routingkey.SysvarAddress},
	}

	enumsDoc := map[string]any{
		"$schema":          "https://openccu-loom.dev/schemas/enums-v1.json",
		"description":      "Machine-readable enum catalogue from pkg/hmenum. Each entry maps a Go const name to the wire-string value the CCU emits. Generated by script/export_schemas.go — do not edit by hand.",
		"enums":            stableEnums,
		"pseudo_addresses": pseudoAddresses,
	}

	if err := writeJSON(filepath.Join(outDir, "enums.json"), enumsDoc); err != nil {
		log.Fatalf("write enums.json: %v", err)
	}

	if err := writeJSON(filepath.Join(outDir, "types.json"), curatedTypes()); err != nil {
		log.Fatalf("write types.json: %v", err)
	}

	fmt.Printf("wrote %d enum types to %s\n", len(stableEnums), filepath.Join(outDir, "enums.json"))
	fmt.Printf("wrote curated type schemas to %s\n", filepath.Join(outDir, "types.json"))
}

func writeJSON(path string, doc any) error {
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	return os.WriteFile(path, buf, 0o644)
}
