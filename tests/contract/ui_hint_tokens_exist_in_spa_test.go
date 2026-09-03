// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	uiHintCatalogue   = "../../pkg/hmui/quantity.go"
	uiHintIconsTS     = "../../assets/ui/src/lib/icons.ts"
	uiHintStateColors = "../../assets/ui/src/lib/sensor-actor/state-color.ts"
)

// TestUIHintTokensExistInSPA pins the cross-language half of the
// data-point UI hint: the daemon emits `ui_hint.icon` and
// `ui_hint.state_color_rule` as opaque strings on every
// DataPointSummary, and the SPA resolves both by table lookup with a
// silent fallback — an unknown icon renders as a generic Gauge, an
// unknown rule renders in the default text colour.
//
// A hint the SPA does not carry therefore produces no error anywhere:
// the tile just looks wrong. The contract between the two catalogues
// was prose only ("Adding a rule is a single switch-case below + a row
// in the Go catalogue. Keep them in sync." — state-color.ts), which is
// a hypothesis, not a check.
func TestUIHintTokensExistInSPA(t *testing.T) {
	t.Parallel()

	icons, rules := uiHintCatalogueTokens(t)
	if len(icons) == 0 || len(rules) == 0 {
		t.Fatalf("extractor found %d icons and %d state-color rules in %s; it is no longer reading the catalogue",
			len(icons), len(rules), uiHintCatalogue)
	}

	known := uiHintSPAIconKeys(t)
	for _, icon := range icons {
		if !known[icon] {
			t.Errorf("%s emits icon %q, which is not a key in icons.ts — the SPA falls back to a generic Gauge", uiHintCatalogue, icon)
		}
	}

	cases := uiHintSPAStateColorCases(t)
	for _, rule := range rules {
		if !cases[rule] {
			t.Errorf("%s emits state_color_rule %q, which stateColorFor has no case for — the SPA renders the default colour", uiHintCatalogue, rule)
		}
	}
}

// uiHintCatalogueTokens collects every Icon and StateColorRule string
// literal in the hint catalogue, wherever it sits: the exact-match
// table, the substring table, the unit table, the enum-shape rule and
// the type fallback are all `Hint{...}` composite literals, so one AST
// pass over keyed fields covers them without assuming which table a
// row lives in.
//
// A field written in an unexpected shape — a constant reference, a
// concatenation — is reported rather than skipped, so the guard cannot
// go quiet by being fed something it did not anticipate.
func uiHintCatalogueTokens(t *testing.T) (icons, rules []string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), uiHintCatalogue, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", uiHintCatalogue, err)
	}
	uiHintAssertFieldNames(t, file)

	iconSet, ruleSet := map[string]bool{}, map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || (key.Name != "Icon" && key.Name != "StateColorRule") {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("%s: %s is not a plain string literal; teach the extractor this shape before trusting the result", uiHintCatalogue, key.Name)
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || v == "" {
			return true
		}
		if key.Name == "Icon" {
			iconSet[v] = true
		} else {
			ruleSet[v] = true
		}
		return true
	})
	return uiHintSorted(iconSet), uiHintSorted(ruleSet)
}

// uiHintAssertFieldNames fails when hmui.Hint no longer carries the two
// fields the extractor keys on. Without it a rename would empty the
// extraction and leave the guard green over an unchecked catalogue.
func uiHintAssertFieldNames(t *testing.T, file *ast.File) {
	t.Helper()
	want := map[string]bool{"Icon": false, "StateColorRule": false}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Hint" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if _, tracked := want[name.Name]; tracked {
					want[name.Name] = true
				}
			}
		}
		return false
	})
	for name, found := range want {
		if !found {
			t.Fatalf("%s: hmui.Hint has no field %q — the token extractor keys on it", uiHintCatalogue, name)
		}
	}
}

// uiHintSPAIconKeys reads the SPA icon registries as text and returns
// every mdi token used as a map key. REGISTRY is spread into
// LOOSE_REGISTRY, so both tables are resolvable by resolveIconLoose and
// both count. Tokens that only appear as a `return "mdi:…"` value are
// not keys and are deliberately not collected.
func uiHintSPAIconKeys(t *testing.T) map[string]bool {
	t.Helper()
	src := uiHintReadFile(t, uiHintIconsTS)
	if !strings.Contains(src, "...REGISTRY") {
		t.Fatalf("%s: LOOSE_REGISTRY no longer spreads REGISTRY; the key collection assumes it does", uiHintIconsTS)
	}
	keys := map[string]bool{}
	re := regexp.MustCompile(`(?m)^\s*"(mdi:[^"]+)"\s*:`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		keys[m[1]] = true
	}
	if len(keys) == 0 {
		t.Fatalf("%s: found no mdi keys; the registry shape changed", uiHintIconsTS)
	}
	return keys
}

// uiHintSPAStateColorCases returns every rule name stateColorFor
// switches on.
func uiHintSPAStateColorCases(t *testing.T) map[string]bool {
	t.Helper()
	src := uiHintReadFile(t, uiHintStateColors)
	body, _, found := strings.Cut(src, "export function lifecycleTint")
	if !found {
		t.Fatalf("%s: lifecycleTint is gone; the file no longer has the shape this guard slices on", uiHintStateColors)
	}
	cases := map[string]bool{}
	re := regexp.MustCompile(`case\s+"([^"]+)"\s*:`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		cases[m[1]] = true
	}
	if len(cases) == 0 {
		t.Fatalf("%s: stateColorFor has no string cases; the rule dispatch changed shape", uiHintStateColors)
	}
	return cases
}

func uiHintReadFile(t *testing.T, path string) string {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(buf)
}

func uiHintSorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
