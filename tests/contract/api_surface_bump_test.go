// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

var updateAPISurface = flag.Bool("update-api-surface", false,
	"rewrite tests/contract/testdata/api_surface.json from the current spec")

// apiSurface is the committed inventory of everything a client can depend on.
// It is deliberately coarse: names and types, no descriptions, no examples —
// a description edit must not make this guard fire.
type apiSurface struct {
	APIVersion string              `json:"api_version"`
	Operations map[string]string   `json:"operations"` // "GET /path" -> operationId
	Schemas    map[string][]string `json:"schemas"`    // schema name -> ["field:type", …] sorted
}

// valueSemanticsChanges records fields whose *meaning* changed while their
// name and type stayed the same. No schema diff can detect that — the shape is
// identical on both sides — so the only honest mechanism is a list somebody
// has to write by hand, and a review rule that says a semantics change is a
// major bump.
//
// The entry format is "<major-version-that-carried-it> <schema>.<field>: what
// changed". Entries are never deleted: the list is the answer to "why did this
// field stop meaning what my client assumed", asked by someone reading an old
// integration years later.
var valueSemanticsChanges = []string{
	"7.0.0 CaptureIndex: the diagnostics capture response became an array, having been declared an object",
	"7.1.0 DataPoint.value: unchanged, but display_value was added beside it — value stays the raw CCU wire value",
}

// TestAPISurfaceChangesCarryTheRightBump pins the two halves of this project's
// versioning policy to each other: what [handlers.APIVersion] claims about a
// change, and what the specification actually did.
//
// The policy is written on APIVersion itself — "addition of capabilities is a
// minor bump, removal or rename of an existing capability or payload field is
// a major bump" — and until this guard nothing enforced it. Two payload fields
// had already changed value semantics under a minor and a patch bump, and the
// nearest thing to a check was a test pinning the version string to itself.
//
// What this catches:
//   - a removed or renamed operation or field under anything less than a major
//     bump: a generated client stops compiling, or silently reads a zero;
//   - a retyped field under anything less than a major bump: the same, with the
//     failure moved to runtime;
//   - an added operation or field with no bump at all: a client has no way to
//     detect the capability it could now use.
//
// What it cannot catch, by construction: a field that keeps its name and type
// and changes what it *means*. That is what [valueSemanticsChanges] is for, and
// why the review rule matters more than the guard.
func TestAPISurfaceChangesCarryTheRightBump(t *testing.T) {
	spec := loadOpenAPISpec(t)
	current := buildAPISurface(spec)

	path := apiSurfacePath(t)
	if *updateAPISurface {
		writeAPISurface(t, path, current)
		t.Logf("rewrote %s at api_version %s", path, current.APIVersion)
		return
	}

	raw, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-api-surface)", path, err)
	}
	var baseline apiSurface
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var breaking, additive []string

	for key, id := range baseline.Operations {
		cur, ok := current.Operations[key]
		switch {
		case !ok:
			breaking = append(breaking, "operation removed: "+key)
		case cur != id:
			breaking = append(breaking, fmt.Sprintf("operationId renamed on %s: %q -> %q", key, id, cur))
		}
	}
	for key := range current.Operations {
		if _, ok := baseline.Operations[key]; !ok {
			additive = append(additive, "operation added: "+key)
		}
	}

	for name, oldFields := range baseline.Schemas {
		newFields, ok := current.Schemas[name]
		if !ok {
			breaking = append(breaking, "schema removed: "+name)
			continue
		}
		oldSet := map[string]string{}
		for _, f := range oldFields {
			n, typ := splitField(f)
			oldSet[n] = typ
		}
		newSet := map[string]string{}
		for _, f := range newFields {
			n, typ := splitField(f)
			newSet[n] = typ
		}
		for n, oldType := range oldSet {
			newType, ok := newSet[n]
			switch {
			case !ok:
				breaking = append(breaking, fmt.Sprintf("field removed: %s.%s", name, n))
			case newType != oldType:
				breaking = append(breaking, fmt.Sprintf("field retyped: %s.%s %s -> %s", name, n, oldType, newType))
			}
		}
		for n := range newSet {
			if _, ok := oldSet[n]; !ok {
				additive = append(additive, fmt.Sprintf("field added: %s.%s", name, n))
			}
		}
	}
	for name := range current.Schemas {
		if _, ok := baseline.Schemas[name]; !ok {
			additive = append(additive, "schema added: "+name)
		}
	}

	if len(breaking) == 0 && len(additive) == 0 {
		if baseline.APIVersion != current.APIVersion {
			t.Errorf("APIVersion moved %s -> %s with no surface change recorded.\n"+
				"If the change is a value-semantics one, add it to valueSemanticsChanges\n"+
				"and refresh the baseline; a schema diff cannot see it.",
				baseline.APIVersion, current.APIVersion)
		}
		return
	}

	oldMajor, oldMinor := majorMinor(t, baseline.APIVersion)
	newMajor, newMinor := majorMinor(t, current.APIVersion)

	sort.Strings(breaking)
	sort.Strings(additive)

	switch {
	case len(breaking) > 0 && newMajor <= oldMajor:
		t.Errorf("the specification lost or reshaped %d thing(s) while APIVersion went %s -> %s.\n"+
			"A removal, rename or retype is a MAJOR bump — a generated client either stops\n"+
			"compiling or silently reads a zero.\n\n  %s\n\n"+
			"Either restore the field additively (new name alongside the old one, old one\n"+
			"deprecated for a release), or bump the major and refresh the baseline with:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface",
			len(breaking), baseline.APIVersion, current.APIVersion, strings.Join(breaking, "\n  "))
	case len(additive) > 0 && newMajor == oldMajor && newMinor <= oldMinor:
		t.Errorf("the specification gained %d thing(s) while APIVersion stayed at %s.\n"+
			"An addition is a MINOR bump — without one a client has no way to detect the\n"+
			"capability it could now use.\n\n  %s\n\n"+
			"Bump the minor, then refresh the baseline with:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface",
			len(additive), baseline.APIVersion, strings.Join(additive, "\n  "))
	default:
		// The bump matches what the surface did. The baseline is simply stale;
		// say so rather than passing silently, so it is refreshed in the same
		// commit as the change it describes and never drifts a release behind.
		t.Errorf("the surface changed and APIVersion moved %s -> %s correctly, but the\n"+
			"committed baseline is stale. Refresh it in this same commit:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface\n\n"+
			"breaking (%d):\n  %s\nadditive (%d):\n  %s",
			baseline.APIVersion, current.APIVersion,
			len(breaking), strings.Join(breaking, "\n  "),
			len(additive), strings.Join(additive, "\n  "))
	}
}

// TestAPIVersionMatchesTheSpecDocument keeps the constant and the document
// from drifting: they are two spellings of one number, and a bump applied to
// only one of them makes every other check here meaningless.
func TestAPIVersionMatchesTheSpecDocument(t *testing.T) {
	spec := loadOpenAPISpec(t)
	if spec.Info == nil {
		t.Fatal("openapi.yaml has no info block")
	}
	if spec.Info.Version != handlers.APIVersion {
		t.Errorf("openapi.yaml info.version = %q but handlers.APIVersion = %q",
			spec.Info.Version, handlers.APIVersion)
	}
}

func buildAPISurface(spec *openapi3.T) apiSurface {
	out := apiSurface{
		APIVersion: handlers.APIVersion,
		Operations: map[string]string{},
		Schemas:    map[string][]string{},
	}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			if id == "" {
				id = "(unnamed)"
			}
			out.Operations[method+" "+path] = id
		}
	}
	for name, ref := range spec.Components.Schemas {
		if ref == nil || ref.Value == nil {
			continue
		}
		fields := make([]string, 0, len(ref.Value.Properties))
		for prop, pref := range ref.Value.Properties {
			fields = append(fields, prop+":"+schemaTypeOf(pref))
		}
		sort.Strings(fields)
		out.Schemas[name] = fields
	}
	return out
}

// schemaTypeOf reduces a property to the coarse shape a client's generated
// type depends on. A $ref is reported by target name, so re-pointing a field
// at a different schema reads as a retype — which for a client it is.
func schemaTypeOf(ref *openapi3.SchemaRef) string {
	if ref == nil {
		return "unknown"
	}
	if ref.Ref != "" {
		return "$ref:" + ref.Ref[strings.LastIndex(ref.Ref, "/")+1:]
	}
	if ref.Value == nil {
		return "unknown"
	}
	v := ref.Value
	if v.Type == nil || len(*v.Type) == 0 {
		switch {
		case len(v.OneOf) > 0:
			return "oneOf"
		case len(v.AnyOf) > 0:
			return "anyOf"
		case len(v.AllOf) > 0:
			return "allOf"
		}
		return "unknown"
	}
	types := append([]string(nil), (*v.Type)...)
	sort.Strings(types)
	base := strings.Join(types, "|")
	if base == "array" {
		return "array<" + schemaTypeOf(v.Items) + ">"
	}
	return base
}

func splitField(f string) (name, typ string) {
	i := strings.Index(f, ":")
	if i < 0 {
		return f, "unknown"
	}
	return f[:i], f[i+1:]
}

func majorMinor(t *testing.T, v string) (int, int) {
	t.Helper()
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("APIVersion %q is not semver", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("APIVersion %q has a non-numeric major: %v", v, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("APIVersion %q has a non-numeric minor: %v", v, err)
	}
	return major, minor
}

func apiSurfacePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "api_surface.json")
}

func writeAPISurface(t *testing.T, path string, s apiSurface) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	blob, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		t.Fatalf("marshal surface: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
