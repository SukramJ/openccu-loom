// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestEveryCapabilityTokenIsEmittedAndDocumented pins the three sets that
// make up the capability surface against each other: the tokens the
// handlers package declares, the tokens capabilities() can actually
// append, and the tokens assets/openapi.yaml tells a client to expect.
//
// A token declared and never appended is a constant no client will ever
// see. A token appended and never documented is a value that reaches every
// client and appears in no spec, so a generated client's own comments say
// the set is smaller than it is. Both failures are silent: the endpoint
// answers 200 either way and no test changes colour.
//
// Round 7 found four surfaces with no token at all — the MQTT raw plane,
// the inbound webhook endpoints, diagrams, and the persistence-backed admin
// routes. A client could not discover any of them, and the SPA had started
// gating its diagram panel on history.v1 as a stand-in, which breaks the
// moment an operator turns recording off while keeping their diagrams.
func TestEveryCapabilityTokenIsEmittedAndDocumented(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	infoGo := filepath.Join(root, "internal", "north", "rest", "handlers", "info.go")

	declared, emitted := capabilityTokens(t, infoGo)
	if len(declared) < 10 {
		t.Fatalf("read only %d capability constants from info.go — the parse is wrong, "+
			"and a guard that sees too few tokens passes by measuring nothing", len(declared))
	}

	spec := loadOpenAPISpec(t)
	documented := documentedCapabilityTokens(t, spec)
	if len(documented) < 5 {
		t.Fatalf("found only %d capability tokens in the openapi description — the "+
			"extraction is wrong", len(documented))
	}

	var neverEmitted, undocumented []string
	for name, value := range declared {
		if !emitted[name] {
			neverEmitted = append(neverEmitted, name+" = "+value)
			continue
		}
		if !documented[value] {
			undocumented = append(undocumented, value)
		}
	}
	sort.Strings(neverEmitted)
	sort.Strings(undocumented)

	if len(neverEmitted) > 0 {
		t.Errorf("%d capability constant(s) are declared and never appended by capabilities():\n  %s\n\n"+
			"A token no code path can emit is a constant, not a capability — no client will "+
			"ever see it.",
			len(neverEmitted), strings.Join(neverEmitted, "\n  "))
	}
	if len(undocumented) > 0 {
		t.Errorf("%d capability token(s) reach clients and appear in no openapi description:\n  %s\n\n"+
			"Add them to the Info.capabilities description in assets/openapi.yaml. A client "+
			"reading the spec has no other way to learn a token exists, and the generated "+
			"clients carry that description verbatim.",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
}

// TestCapabilityDetectorsReportConfigurationNotLiveness pins the decision
// that a token means "the daemon is configured for this", not "this is
// working right now".
//
// The two questions have different answers and different homes: a broker
// that is briefly unreachable is not a missing capability, and a token that
// came and went with connectivity would make every client re-derive its
// feature set on each poll. Liveness is /health's job, and after round 7
// the components are there for it.
//
// Enforced structurally, because prose alone did not hold: the detector
// carries plain bool fields captured once at construction, so a runtime
// probe cannot be smuggled into a getter without changing that shape.
func TestCapabilityDetectorsReportConfigurationNotLiveness(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	path := filepath.Join(root, "cmd", "openccu-loom", "daemon_north.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var fields, getters int
	var probing []string
	ast.Inspect(file, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == "runtimeCapabilityDetector" {
			st, isStruct := ts.Type.(*ast.StructType)
			if !isStruct {
				t.Fatal("runtimeCapabilityDetector is not a struct — the pin cannot measure it")
			}
			for _, f := range st.Fields.List {
				id, isIdent := f.Type.(*ast.Ident)
				if !isIdent || id.Name != "bool" {
					t.Errorf("runtimeCapabilityDetector field %v is not a bool. A capability "+
						"token reports configuration captured once; anything richer is a "+
						"runtime probe, and liveness belongs on /health.", f.Names)
					continue
				}
				fields += len(f.Names)
			}
			return false
		}
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "Has") {
			return true
		}
		if !strings.Contains(typeNameOfReceiver(fn), "runtimeCapabilityDetector") {
			return true
		}
		getters++
		// A getter is allowed to return a field, or a conjunction of
		// fields (HasMCPWrite is `r.mcp && r.mcpWrite`). Anything that
		// calls out is a probe.
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if _, isCall := inner.(*ast.CallExpr); isCall {
				probing = append(probing, fn.Name.Name)
			}
			return true
		})
		return false
	})

	if fields < 10 || getters < 10 {
		t.Fatalf("read %d fields and %d getters — the parse is wrong, and a pin that "+
			"measures neither passes by measuring nothing", fields, getters)
	}
	if len(probing) > 0 {
		sort.Strings(probing)
		t.Errorf("%d capability getter(s) call out instead of reporting a captured bool:\n  %s\n\n"+
			"That turns the token into a liveness probe, which is a different question with a "+
			"different answer — and one clients already read this field for. Report liveness "+
			"through a /health component instead.",
			len(probing), strings.Join(probing, "\n  "))
	}
}

func typeNameOfReceiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// capabilityTokens returns the Capability* constants info.go declares
// (name → value) and the set of those names capabilities() appends.
func capabilityTokens(t *testing.T, path string) (declared map[string]string, emitted map[string]bool) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	declared = map[string]string{}
	emitted = map[string]bool{}

	ast.Inspect(file, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Capability") || i >= len(vs.Values) {
					continue
				}
				if lit, isLit := vs.Values[i].(*ast.BasicLit); isLit {
					declared[name.Name] = strings.Trim(lit.Value, `"`)
				}
			}
			return true
		}
		// Two emission shapes: the always-on tokens sit in the initial
		// []string literal, the conditional ones arrive through append.
		// Reading only the appends reported the three always-on tokens as
		// unreachable — the loudest possible false positive, since they are
		// the ones every client sees.
		if cl, ok := n.(*ast.CompositeLit); ok {
			if arr, isArr := cl.Type.(*ast.ArrayType); isArr {
				if id, isIdent := arr.Elt.(*ast.Ident); isIdent && id.Name == "string" {
					for _, el := range cl.Elts {
						if id, isIdent := el.(*ast.Ident); isIdent {
							emitted[id.Name] = true
						}
					}
				}
			}
			return true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, isIdent := call.Fun.(*ast.Ident); !isIdent || id.Name != "append" {
			return true
		}
		for _, arg := range call.Args[1:] {
			if id, isIdent := arg.(*ast.Ident); isIdent {
				emitted[id.Name] = true
			}
		}
		return true
	})
	return declared, emitted
}

// documentedCapabilityTokens returns every token named in the
// Info.capabilities description or its examples.
func documentedCapabilityTokens(t *testing.T, spec *openapi3.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	info := spec.Components.Schemas["Info"]
	if info == nil || info.Value == nil {
		t.Fatal("openapi.yaml has no Info schema — the pin cannot measure the documented set")
	}
	caps := info.Value.Properties["capabilities"]
	if caps == nil || caps.Value == nil {
		t.Fatal("Info has no capabilities property")
	}
	for _, tok := range backtickedTokens(caps.Value.Description) {
		out[tok] = true
	}
	if caps.Value.Items != nil && caps.Value.Items.Value != nil {
		for _, ex := range caps.Value.Items.Value.Examples {
			if s, ok := ex.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// backtickedTokens pulls `token.v1`-shaped words out of a description.
func backtickedTokens(desc string) []string {
	var out []string
	for i := 0; i < len(desc); i++ {
		if desc[i] != '`' {
			continue
		}
		end := strings.IndexByte(desc[i+1:], '`')
		if end < 0 {
			break
		}
		word := desc[i+1 : i+1+end]
		i += end + 1
		if strings.Contains(word, ".") || strings.Contains(word, "_") {
			out = append(out, word)
		}
	}
	return out
}
