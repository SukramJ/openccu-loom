// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var updateOpenAPIInlineEnums = flag.Bool("update-openapi-inline-enums", false,
	"rewrite tests/contract/testdata/openapi_inline_enums.json from the current spec "+
		"(refuses when the spec holds an inline enum the old baseline does not)")

// inlineEnum is one anonymous enum in assets/openapi.yaml: where it sits and
// the property (or parameter) name a generator derives its type name from.
type inlineEnum struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// minPlausibleInlineEnums is the floor below which the walk is assumed to be
// broken rather than the spec to have been cleaned up. The document held
// roughly 140 inline enums when this guard was written; a walk that finds a
// fraction of that compares a near-empty list and passes by measuring
// nothing. Lower it deliberately if the spec really sheds that many.
const minPlausibleInlineEnums = 100

// TestOpenAPIInlineEnumsAreFrozen pins every anonymous enum in
// assets/openapi.yaml to its place in the document, and refuses new ones.
//
// Clients are generated from this document, and a generator has to invent a
// type name for an enum that is not a named schema. It takes the property's
// name and numbers duplicates by their position in the document: `Kind`,
// `Kind1`, `Kind2`, … So an inline enum is not a local decision. Adding one
// early in the document renumbers every later enum with the same property
// name, and hand-written client code that imports `Kind2` silently binds to a
// different set of values while still compiling. That is how
// `Info.deployment.kind`, written inline, turned the client's `Kind2` from
// the event kind (initial/change/refresh) into the pairing kind — a change
// that was harmless on the wire and broke the client's regeneration.
//
// The guard therefore has two answers. A new inline enum is refused outright:
// declare it under components.schemas and reference it with $ref, which gives
// it a stable name and shifts nobody. A removed or reordered one is accepted
// only deliberately, through -update-openapi-inline-enums, because it renames
// types in every generated client.
//
// What counts as inline: every `enum` keyword except the one directly on a
// top-level component schema (components.schemas.<Name>.enum), which is the
// named form. Enums under `items`, `additionalProperties`, `allOf`/`oneOf`/
// `anyOf` members, parameter schemas (including components.parameters) and
// nested properties are all anonymous to a generator and all count.
func TestOpenAPIInlineEnumsAreFrozen(t *testing.T) {
	t.Parallel()

	current := collectInlineEnums(t)
	if len(current) < minPlausibleInlineEnums {
		t.Fatalf("the walk found only %d inline enums in assets/openapi.yaml — it is broken, "+
			"and a guard that sees too few enums passes by measuring nothing", len(current))
	}

	path := openAPIInlineEnumsPath(t)
	baseline, haveBaseline := readInlineEnumBaseline(t, path)

	inBaseline := map[string]bool{}
	for _, e := range baseline {
		inBaseline[e.Path] = true
	}
	inCurrent := map[string]bool{}
	for _, e := range current {
		inCurrent[e.Path] = true
	}

	var added, removed []inlineEnum
	for _, e := range current {
		if !inBaseline[e.Path] {
			added = append(added, e)
		}
	}
	for _, e := range baseline {
		if !inCurrent[e.Path] {
			removed = append(removed, e)
		}
	}

	if *updateOpenAPIInlineEnums {
		// A missing baseline is the bootstrap case and has nothing to protect.
		if haveBaseline && len(added) > 0 {
			t.Fatalf("refusing to rewrite %s: the spec holds %d inline enum(s) the baseline "+
				"does not.\n\n%s\n\nA new enum is named, never accepted inline: declare it under "+
				"components.schemas and reference it with $ref.",
				path, len(added), describeNewInlineEnums(added, current))
		}
		writeInlineEnumBaseline(t, path, current)
		t.Logf("rewrote %s with %d inline enums", path, len(current))
		return
	}
	if !haveBaseline {
		t.Fatalf("%s is missing; generate it with:\n  %s", path, inlineEnumUpdateCommand)
	}

	if len(added) > 0 {
		t.Errorf("assets/openapi.yaml gained %d NEW inline enum(s):\n\n%s\n\n"+
			"Name it — declare it under components.schemas and reference it with $ref. "+
			"Generated clients name an inline enum after its property and number duplicates by "+
			"document position, so an inline enum renames every later enum of the same name in "+
			"every generated client. The update flag will not accept a new inline enum.",
			len(added), describeNewInlineEnums(added, current))
		return
	}

	if slices.Equal(baseline, current) {
		return
	}

	// Only removals or a changed order remain. Report the property names whose
	// sequence moved: those are the generated names that shift.
	shifted := shiftedNameGroups(baseline, current)
	var b strings.Builder
	for _, e := range removed {
		fmt.Fprintf(&b, "  removed: %s (name %q)\n", e.Path, e.Name)
	}
	if len(shifted) == 0 {
		b.WriteString("  order changed, but no two enums of the same name swapped places\n")
	}
	for _, name := range shifted {
		fmt.Fprintf(&b, "  enums named %q, in document order:\n    before: %s\n    now:    %s\n",
			name, strings.Join(pathsNamed(baseline, name), "\n            "),
			strings.Join(pathsNamed(current, name), "\n            "))
	}
	t.Errorf("the inline enums of assets/openapi.yaml were removed or reordered:\n\n%s\n"+
		"Generated clients number same-named inline enums by document position, so this renames "+
		"enum types in every generated client (`Kind2` becomes a different set of values). If that "+
		"is intended, name the affected enums instead where you can, and accept the rest "+
		"deliberately with:\n  %s",
		b.String(), inlineEnumUpdateCommand)
}

const inlineEnumUpdateCommand = "GOMAXPROCS=2 go test -p 2 -run TestOpenAPIInlineEnumsAreFrozen ./tests/contract/ -update-openapi-inline-enums"

// describeNewInlineEnums lists each new enum together with the other inline
// enums sharing its name: those are the ones whose generated name it shifts.
func describeNewInlineEnums(added, current []inlineEnum) string {
	var b strings.Builder
	for _, e := range added {
		fmt.Fprintf(&b, "  NEW inline enum: %s (name %q)\n", e.Path, e.Name)
		same := pathsNamed(current, e.Name)
		at := slices.Index(same, e.Path)
		earlier, later := same[:at], same[at+1:]
		if len(earlier) == 0 && len(later) == 0 {
			b.WriteString("    no other inline enum shares that name\n")
			continue
		}
		if len(later) > 0 {
			fmt.Fprintf(&b, "    later inline enums named %q — renumbered in every generated client:\n      %s\n",
				e.Name, strings.Join(later, "\n      "))
		}
		if len(earlier) > 0 {
			fmt.Fprintf(&b, "    earlier inline enums named %q — keep their names, but this one takes the next number:\n      %s\n",
				e.Name, strings.Join(earlier, "\n      "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func pathsNamed(list []inlineEnum, name string) []string {
	var out []string
	for _, e := range list {
		if e.Name == name {
			out = append(out, e.Path)
		}
	}
	return out
}

// shiftedNameGroups returns, in first-seen order, every name whose ordered
// list of enum paths differs between the two lists.
func shiftedNameGroups(baseline, current []inlineEnum) []string {
	var names []string
	seen := map[string]bool{}
	for _, list := range [][]inlineEnum{baseline, current} {
		for _, e := range list {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			if !slices.Equal(pathsNamed(baseline, e.Name), pathsNamed(current, e.Name)) {
				names = append(names, e.Name)
			}
		}
	}
	return names
}

// collectInlineEnums walks assets/openapi.yaml with the YAML node API, which
// keeps mapping keys in document order — a decoded map would lose exactly the
// order this guard exists to pin.
func collectInlineEnums(t *testing.T) []inlineEnum {
	t.Helper()

	specPath := filepath.Join(repoRoot(t), "assets", "openapi.yaml")
	raw, err := os.ReadFile(specPath) //nolint:gosec // fixed repo-relative asset
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		t.Fatalf("%s is not a YAML document", specPath)
	}

	var out []inlineEnum
	walkInlineEnums(doc.Content[0], nil, "", false, &out)
	return out
}

// valueKeywords hold instance data, not schemas: a key called `enum` inside
// an example is a value, not a constraint.
var valueKeywords = map[string]bool{
	"example": true, "examples": true, "default": true, "const": true,
}

// walkInlineEnums descends one node. underProperties is true when node's keys
// are property names rather than keywords (the value of `properties`), so a
// property that happens to be called `enum` is not read as the keyword.
func walkInlineEnums(node *yaml.Node, path []string, name string, underProperties bool, out *[]inlineEnum) {
	switch node.Kind {
	case yaml.DocumentNode, yaml.ScalarNode:
		return
	case yaml.AliasNode:
		// An alias expands to its anchor wherever it is used, and a generator
		// sees the expansion, so its enums count at the alias's position.
		walkInlineEnums(node.Alias, path, name, underProperties, out)
	case yaml.SequenceNode:
		for i, child := range node.Content {
			walkInlineEnums(child, append(slices.Clip(path), strconv.Itoa(i)), name, false, out)
		}
	case yaml.MappingNode:
		// A parameter object names the enum its schema carries.
		if !underProperties {
			if pname := mappingScalar(node, "name"); pname != "" && mappingScalar(node, "in") != "" {
				name = pname
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i].Value, node.Content[i+1]
			childPath := append(slices.Clip(path), key)
			switch {
			case underProperties:
				walkInlineEnums(val, childPath, key, false, out)
			case key == "enum" && val.Kind == yaml.SequenceNode:
				if !isTopLevelComponentSchema(path) {
					*out = append(*out, inlineEnum{Path: strings.Join(path, "/"), Name: name})
				}
			case valueKeywords[key] || strings.HasPrefix(key, "x-"):
				continue
			case key == "properties":
				walkInlineEnums(val, childPath, name, true, out)
			case len(path) > 0 && path[len(path)-1] == "headers":
				// A response header's schema is named after the header.
				walkInlineEnums(val, childPath, key, false, out)
			case len(path) == 2 && path[0] == "components" && path[1] == "schemas":
				// A component schema's own enum (and any enum in its allOf
				// members) is named after the component.
				walkInlineEnums(val, childPath, key, false, out)
			default:
				walkInlineEnums(val, childPath, name, false, out)
			}
		}
	}
}

func isTopLevelComponentSchema(path []string) bool {
	return len(path) == 3 && path[0] == "components" && path[1] == "schemas"
}

func mappingScalar(node *yaml.Node, key string) string {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key && node.Content[i+1].Kind == yaml.ScalarNode {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func openAPIInlineEnumsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "openapi_inline_enums.json")
}

func readInlineEnumBaseline(t *testing.T, path string) ([]inlineEnum, bool) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var list []inlineEnum
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return list, true
}

func writeInlineEnumBaseline(t *testing.T, path string, list []inlineEnum) {
	t.Helper()
	blob, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		t.Fatalf("marshal inline enums: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
