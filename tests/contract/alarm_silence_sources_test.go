// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
)

// silenceVerbs maps each engine silence entry point to the argument index
// that carries the source string. The three signatures differ, and the
// index is the whole point of the table: reading the wrong argument would
// collect zone ids and actor names as if they were sources.
//
//	Silence(ctx, zoneID, by, source)
//	SilenceWithCode(ctx, zoneID, by, source, code)
//	SilenceAll(ctx, by, source)
var silenceVerbs = map[string]int{
	"Silence":         3,
	"SilenceWithCode": 3,
	"SilenceAll":      2,
}

// TestSilenceGatesAreOfferedOnlyWhereTheyBite pins the operator-facing
// half of [engine.CodePolicy.RequireSilence] to the engine's own
// behaviour.
//
// RequireSilence is a map keyed by source string. The engine accepts and
// persists an entry for any key and then ignores it for every
// pre-authenticated source — an operator session carries its own second
// factor, and a keypad or remote press is authenticated by its slot or
// binding match and carries no PIN that could be typed. A switch offered
// for one of those is stored, is inert, and tells the operator a
// protection exists that does not.
//
// The alarm policies view offered three: mqtt, keypad and remote. Two of
// them could never gate anything.
//
// This measures both halves from the sources rather than restating them:
// which source strings actually reach a silence verb (by walking the
// production tree), and which of those the engine can gate (by asking
// [engine.IsPreAuthenticatedSource]). The view's list must be exactly the
// intersection.
func TestSilenceGatesAreOfferedOnlyWhereTheyBite(t *testing.T) {
	t.Parallel()

	reachable := reachableSilenceSources(t)
	// Negative control on the scanner itself: a walk that matches nothing
	// would make every assertion below vacuously true. The daemon has more
	// than one silence surface, so anything under two means the scan broke
	// rather than that the surfaces went away.
	if len(reachable) < 2 {
		t.Fatalf("the scan found %d silence sources (%v); it is measuring nothing",
			len(reachable), reachable)
	}

	var gateable []string
	for _, src := range reachable {
		if !engine.IsPreAuthenticatedSource(src) {
			gateable = append(gateable, src)
		}
	}
	sort.Strings(gateable)

	offered := spaSilenceSources(t)
	if len(offered) == 0 {
		t.Fatal("SILENCE_SOURCES not found in the alarm policies view; the scan is measuring nothing")
	}

	if strings.Join(offered, ",") != strings.Join(gateable, ",") {
		t.Errorf("the alarm policies view offers silence gates for %v, but only %v can gate anything.\n"+
			"Sources reaching a silence verb in production: %v.\n"+
			"A gate offered for a pre-authenticated source is stored and ignored: see "+
			"engine.IsPreAuthenticatedSource and engine.CodePolicy.RequireSilence.",
			offered, gateable, reachable)
	}
}

// silenceSourcesListRE extracts the view's own list. The constant is a
// plain `as const` tuple of string literals, so the shape is stable and a
// change to it either keeps matching or fails the emptiness check above.
var silenceSourcesListRE = regexp.MustCompile(`(?s)const SILENCE_SOURCES\s*=\s*\[(.*?)\]`)

// spaSilenceSources reads the sources the alarm policies view offers.
func spaSilenceSources(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "assets", "ui", "src", "routes", "alarm", "AlarmPolicies.svelte"))
	if err != nil {
		t.Fatalf("read alarm policies view: %v", err)
	}
	m := silenceSourcesListRE.FindSubmatch(data)
	if m == nil {
		return nil
	}
	var out []string
	for _, part := range strings.Split(string(m[1]), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, strings.Trim(part, `"'`))
	}
	sort.Strings(out)
	return out
}

// reachableSilenceSources walks the production tree and returns every
// source string that reaches one of the engine's silence verbs, resolved
// through the string constants the call sites use.
func reachableSilenceSources(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	consts := map[string]string{}
	type call struct {
		expr string
		file string
	}
	var calls []call

	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				// A file this parser cannot read is a file this guard
				// cannot classify, and skipping it silently would shrink
				// the measured set without saying so.
				t.Errorf("parse %s: %v", path, perr)
				return nil
			}
			// Constants are collected everywhere, including the engine,
			// because the call sites name the engine's own CodeSource*
			// tokens. Calls are collected everywhere but the engine: its
			// forwarders (Silence -> SilenceWithCode) pass their source
			// parameter straight through, so they are not surfaces and
			// carry no source string to classify.
			collectStringConsts(f, consts)
			if strings.Contains(path, filepath.Join("internal", "alarm", "engine")) {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := ce.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				idx, ok := silenceVerbs[sel.Sel.Name]
				if !ok || idx >= len(ce.Args) {
					return true
				}
				calls = append(calls, call{expr: exprText(ce.Args[idx]), file: path})
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", dir, walkErr)
		}
	}

	seen := map[string]bool{}
	for _, c := range calls {
		src, ok := resolveSourceExpr(c.expr, consts)
		if !ok {
			// A source that is not a literal or a package-level string
			// constant cannot be classified, and guessing is what this
			// guard exists to prevent.
			t.Errorf("%s: silence source %q is neither a string literal nor a known constant; "+
				"this guard cannot classify it", c.file, c.expr)
			continue
		}
		seen[src] = true
	}
	out := make([]string, 0, len(seen))
	for src := range seen {
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

// collectStringConsts records every package-level string constant by its
// bare name and by "<pkg>.<Name>", so both spellings at a call site
// resolve.
func collectStringConsts(f *ast.File, into map[string]string) {
	pkg := f.Name.Name
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				into[name.Name] = val
				into[pkg+"."+name.Name] = val
			}
		}
	}
}

// resolveSourceExpr turns a call-site argument into the string it carries.
func resolveSourceExpr(expr string, consts map[string]string) (string, bool) {
	if strings.HasPrefix(expr, `"`) {
		val, err := strconv.Unquote(expr)
		return val, err == nil
	}
	val, ok := consts[expr]
	return val, ok
}
