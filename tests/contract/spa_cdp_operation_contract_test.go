// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSPACDPOperationsMatchDispatcher pins the SPA's CDP widget payloads
// against the accepted (operation, param-key) surface of the custom-DP
// dispatcher (internal/central/adapter/custom_dp_dispatcher.go). Each widget
// calls `invoke("<op>", { <keys> })`, which POSTs to
// `/devices/.../cdps/{name}/{operation}` and is routed by
// CustomDPDispatcher.dispatch to one or more `dispatch*` functions depending
// on the concrete custom-DP type the widget's `cdp.kind` can represent.
//
// A widget file that sends an operation name or a param key the dispatcher
// does not read is a silent no-op or a 4xx at best — this test turns that
// class of drift into a build failure by extracting every literal
// invoke(...) call from the widget source and checking it against a
// whitelist built by parsing the dispatcher's own switch statements
// (see [dispatcherOperations]), not by restating them.
func TestSPACDPOperationsMatchDispatcher(t *testing.T) {
	dir := filepath.Join("..", "..", "assets", "ui", "src", "lib", "cdp", "widgets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read widgets dir: %v", err)
	}

	dispatchOps := dispatcherOperations(t)
	acceptedOperations := make(map[string]widgetContract, len(widgetDispatchFuncs))
	for widget, funcs := range widgetDispatchFuncs {
		contract := widgetContract{}
		for _, fn := range funcs {
			ops, ok := dispatchOps[fn]
			if !ok {
				t.Fatalf("widgetDispatchFuncs references %q for %s, but no such dispatch* function "+
					"was found in custom_dp_dispatcher.go — update the mapping", fn, widget)
			}
			for op, keys := range ops {
				merged := contract[op]
				if merged == nil {
					merged = opKeys{}
					contract[op] = merged
				}
				for k := range keys {
					merged[k] = struct{}{}
				}
			}
		}
		acceptedOperations[widget] = contract
	}

	// Every acceptedOperations entry must correspond to a real widget file —
	// a stale entry (renamed/removed widget) would otherwise go unnoticed.
	onDisk := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".svelte") {
			onDisk[e.Name()] = true
		}
	}
	for name := range acceptedOperations {
		if !onDisk[name] {
			t.Errorf("widgetDispatchFuncs has an entry for %q but no such widget file exists under %s", name, dir)
		}
	}

	var callSites, keyChecks, widgetsChecked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".svelte") {
			continue
		}
		name := e.Name()

		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		calls := extractInvokeCalls(string(raw))

		if reason, skip := skippedWidgetFiles[name]; skip {
			if len(calls) > 0 {
				t.Errorf("%s: documented skip (%s) is stale — %d invoke(...) call site(s) were "+
					"found by the extractor; add this widget to acceptedOperations instead of skipping it",
					name, reason, len(calls))
			}
			continue
		}

		contract, ok := acceptedOperations[name]
		if !ok {
			t.Errorf("%s has no entry in acceptedOperations and is not in skippedWidgetFiles — "+
				"add its dispatcher-derived op/key contract or an explicit, documented skip", name)
			continue
		}
		widgetsChecked++

		for _, call := range calls {
			callSites++
			accepted, ok := contract[call.op]
			if !ok {
				t.Errorf("%s: invoke(%q, ...) — operation %q has no matching case in the "+
					"corresponding dispatch* function in custom_dp_dispatcher.go", name, call.op, call.op)
				continue
			}
			for _, k := range call.keys {
				keyChecks++
				if _, ok := accepted[k]; !ok {
					t.Errorf("%s: invoke(%q, { %s: ... }) — param key %q is not read by the "+
						"dispatcher for operation %q", name, call.op, k, k, call.op)
				}
			}
		}
	}

	if callSites == 0 {
		t.Fatal("no invoke(...) call sites were extracted from any CDP widget — the extraction regexes are broken")
	}
	t.Logf("validated %d invoke(...) call site(s) (%d param-key check(s)) across %d widget file(s)",
		callSites, keyChecks, widgetsChecked)
}

// opKeys is the set of param keys the dispatcher accepts for one operation.
type opKeys map[string]struct{}

// widgetContract maps operation name -> accepted param keys for one widget
// file. It is a superset contract: a key must be valid for at least one of
// the custom-DP kinds the widget can render (several widgets render more
// than one `dispatch*` target depending on `cdp.kind`, e.g. LightTile spans
// plain Light through RGBWLight). Missing-required-key is out of scope —
// this only catches operation names and param keys the dispatcher does not
// recognise at all.
type widgetContract map[string]opKeys

// widgetDispatchFuncs maps each CDP widget file to the dispatch* function(s)
// in custom_dp_dispatcher.go it can route through, depending on the
// concrete custom-DP kind the widget renders (`cdp.kind` /
// `cdp.capabilities`). This mapping is architectural (which model kinds a
// widget can represent) and is not restated from the dispatcher's op/key
// content — that content is parsed out of the dispatcher source itself by
// [dispatcherOperations].
var widgetDispatchFuncs = map[string][]string{
	"SwitchTile.svelte": {"dispatchSwitch"},
	// ValveTile.svelte renders both valve kinds via `cdp.kind === "valve_modulating"`.
	"ValveTile.svelte": {"dispatchIrrigation", "dispatchModulatingValve"},
	// LightTile.svelte renders any Light subtype depending on
	// cdp.capabilities / cdp.kind, and every subtype falls back to
	// dispatchLight for the operations it does not override.
	// dispatchDRGDaliLight is excluded: it has no `switch op` of its own
	// (it delegates to dispatchColorTempLight and special-cases
	// "set_effect" via an if-statement), and its set_effect/label surface
	// is already covered by dispatchEffectLight / dispatchRGBWLight below.
	"LightTile.svelte": {
		"dispatchLight", "dispatchColorLight", "dispatchColorTempLight",
		"dispatchFixedColorLight", "dispatchSoundPlayerLED",
		"dispatchEffectLight", "dispatchRGBWLight",
	},
	"ClimateTile.svelte": {"dispatchClimate"},
	// CoverTile.svelte renders `cover`, `cover_blind`, and `cover_garage`
	// from the same file.
	"CoverTile.svelte": {"dispatchCover", "dispatchGarage", "dispatchBlind"},
	"LockTile.svelte":  {"dispatchLock"},
	"SirenTile.svelte": {"dispatchSiren"},
}

// dispatcherOperations parses internal/central/adapter/custom_dp_dispatcher.go
// and, for every `func (d *CustomDPDispatcher) dispatchXxx(...)` method,
// returns the operation names its `switch op { case "...": }` accepts and,
// per operation, the param keys its case body reads — via direct
// `p["key"]` / `params["key"]` indexing, `paramXxx(p, "key", ...)` helper
// calls, and one level of resolution through local helper functions
// (onTimeParam / requireOnTime / awayDuration) that read params
// themselves. This is the dispatcher's actual accepted surface, read from
// its source rather than retyped by hand — a case removed from the switch,
// or a key no longer read, changes what this returns.
func dispatcherOperations(t *testing.T) map[string]widgetContract {
	t.Helper()

	path := filepath.Join("..", "..", "internal", "central", "adapter", "custom_dp_dispatcher.go")
	src, err := os.ReadFile(path) //nolint:gosec // contract test, fixed relative path
	if err != nil {
		t.Fatalf("read dispatcher source: %v", err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse dispatcher source: %v", err)
	}

	// Pass 1: every top-level function's raw source text, keyed by name —
	// used both to find dispatch* switches and to resolve helper calls
	// like requireOnTime(p) back to the params[...] keys they read.
	funcSrc := make(map[string]string)
	funcDecl := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcSrc[fn.Name.Name] = string(src[fn.Pos()-1 : fn.End()-1])
		funcDecl[fn.Name.Name] = fn
	}
	if len(funcDecl) == 0 {
		t.Fatal("no function declarations found in dispatcher source — parser broken or file moved")
	}

	// Pass 2: direct params[...] / paramXxx(p, "key") keys per function,
	// resolved one level through same-file helper calls (requireOnTime ->
	// onTimeParam, etc.) so a case that only calls a shared extractor still
	// gets credited with the keys that extractor reads.
	directKeys := make(map[string][]string, len(funcSrc))
	for name, body := range funcSrc {
		directKeys[name] = extractParamKeys(body)
	}
	resolvedKeys := make(map[string]map[string]struct{}, len(funcSrc))
	for name := range funcSrc {
		resolvedKeys[name] = keysOf(directKeys[name])
	}
	for name, body := range funcSrc {
		for calleeName, calleeKeys := range directKeys {
			if calleeName == name || len(calleeKeys) == 0 {
				continue
			}
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(calleeName) + `\(\s*(?:params|p)\s*[,)]`).MatchString(body) {
				for _, k := range calleeKeys {
					resolvedKeys[name][k] = struct{}{}
				}
			}
		}
	}

	out := make(map[string]widgetContract, len(funcDecl))
	for name, fn := range funcDecl {
		if !strings.HasPrefix(name, "dispatch") {
			continue
		}
		sw := findOpSwitch(fn)
		if sw == nil {
			continue
		}
		contract := widgetContract{}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok || cc.List == nil {
				continue // default:
			}
			caseStart := cc.Pos()
			caseEnd := cc.End()
			caseSrc := string(src[caseStart-1 : caseEnd-1])
			keys := keysOf(extractParamKeys(caseSrc))
			for calleeName := range directKeys {
				if regexp.MustCompile(`\b` + regexp.QuoteMeta(calleeName) + `\(\s*(?:params|p)\s*[,)]`).MatchString(caseSrc) {
					for k := range resolvedKeys[calleeName] {
						keys[k] = struct{}{}
					}
				}
			}
			for _, expr := range cc.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				op, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				contract[op] = keys
			}
		}
		out[name] = contract
	}
	return out
}

// findOpSwitch returns the switch statement inside fn whose tag is the
// bare identifier "op" — every dispatch* function's operation switch uses
// that name — or nil if fn has none.
func findOpSwitch(fn *ast.FuncDecl) *ast.SwitchStmt {
	var found *ast.SwitchStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		if id, ok := sw.Tag.(*ast.Ident); ok && id.Name == "op" {
			found = sw
			return false
		}
		return true
	})
	return found
}

// paramKeyRe matches the ways dispatcher code reads one named key out of
// its params map: direct indexing (`p["key"]` / `params["key"]`) and the
// paramFloat/paramString/paramStringOptional/paramInt32/paramTime helper
// calls, whose second argument is always the literal key.
var paramKeyRe = regexp.MustCompile(
	`(?:params|p)\[\s*"([A-Za-z0-9_]+)"\s*\]` +
		`|\bparam(?:Float|String|StringOptional|Int32|Time|Bool)\(\s*(?:params|p)\s*,\s*"([A-Za-z0-9_]+)"`,
)

// extractParamKeys returns every param key literal referenced in src.
func extractParamKeys(src string) []string {
	matches := paramKeyRe.FindAllStringSubmatch(src, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if m[1] != "" {
			out = append(out, m[1])
		} else if m[2] != "" {
			out = append(out, m[2])
		}
	}
	return out
}

func keysOf(names []string) opKeys {
	m := make(opKeys, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// skippedWidgetFiles documents CDP widget files intentionally excluded from
// extraction, with the structural reason the invoke(...) regex extractor
// below cannot parse them. A file listed here is verified to yield zero
// extracted call sites — see TestSPACDPOperationsMatchDispatcher — so the
// skip cannot silently mask a real, checkable call.
var skippedWidgetFiles = map[string]string{
	"TextDisplayTile.svelte": "calls api.invokeCustomDataPoint(address, cdp.name, \"write\", params) " +
		"directly with a params object built by mutation (const params = { id, text }; " +
		"then conditional params.icon = ...) rather than an invoke(op, { <literal> }) call, " +
		"so there is no inline object-literal body for the extractor to parse",
}

// opCall is one extracted invoke(...) call site: the operation name and the
// top-level keys of its param object literal (nil for a no-param call).
type opCall struct {
	op   string
	keys []string
}

// Regexes matching the three invoke(...) call shapes used by the CDP
// widgets. All three require an operation string literal immediately after
// "invoke(" (optionally behind a ternary condition), so none of them can
// match the local helper's own declaration line
// (`async function invoke(op: string, params: ... = {}) {`), whose first
// token after "invoke(" is the bare identifier "op", not a quote or a "?".
var (
	// invoke(cond ? "opA" : "opB") — used for `invoke(next ? "turn_on" :
	// "turn_off")`-style ternary calls; both branches take no params.
	ternaryInvokeRe = regexp.MustCompile(`invoke\(\s*[A-Za-z_]\w*\s*\?\s*"([a-z_]+)"\s*:\s*"([a-z_]+)"\s*\)`)
	// invoke("op", { ...body... }) — the object body has no nested braces
	// in the current widget source, so a non-greedy match up to the first
	// "}" is safe.
	literalObjInvokeRe = regexp.MustCompile(`invoke\(\s*"([a-z_]+)"\s*,\s*\{([^}]*)\}\s*\)`)
	// invoke("op") — no-param call.
	plainInvokeRe = regexp.MustCompile(`invoke\(\s*"([a-z_]+)"\s*\)`)
)

// extractInvokeCalls scans src (one widget file's raw text) for every
// invoke(...) call site and returns the operation + param keys for each.
func extractInvokeCalls(src string) []opCall {
	ternaryMatches := ternaryInvokeRe.FindAllStringSubmatch(src, -1)
	literalMatches := literalObjInvokeRe.FindAllStringSubmatch(src, -1)
	plainMatches := plainInvokeRe.FindAllStringSubmatch(src, -1)

	calls := make([]opCall, 0, 2*len(ternaryMatches)+len(literalMatches)+len(plainMatches))
	for _, m := range ternaryMatches {
		calls = append(calls, opCall{op: m[1]}, opCall{op: m[2]})
	}
	for _, m := range literalMatches {
		calls = append(calls, opCall{op: m[1], keys: extractObjectKeys(m[2])})
	}
	for _, m := range plainMatches {
		calls = append(calls, opCall{op: m[1]})
	}
	return calls
}

// extractObjectKeys splits a flat JS/TS object-literal body on commas and
// returns the identifier before an optional ":" for each non-empty segment
// — covering both `key: value` pairs and shorthand `{ key }` properties.
// Values are never inspected.
func extractObjectKeys(body string) []string {
	var out []string
	for _, seg := range strings.Split(body, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		name := seg
		if idx := strings.Index(seg, ":"); idx >= 0 {
			name = seg[:idx]
		}
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
