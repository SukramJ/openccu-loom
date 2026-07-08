// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
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
// dispatcher-derived whitelist.
func TestSPACDPOperationsMatchDispatcher(t *testing.T) {
	dir := filepath.Join("..", "..", "assets", "ui", "src", "lib", "cdp", "widgets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read widgets dir: %v", err)
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
			t.Errorf("acceptedOperations has an entry for %q but no such widget file exists under %s", name, dir)
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

// keySet builds an [opKeys] from a literal list of accepted key names.
func keySet(names ...string) opKeys {
	m := make(opKeys, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// widgetContract maps operation name -> accepted param keys for one widget
// file. It is a superset contract: a key must be valid for at least one of
// the custom-DP kinds the widget can render (several widgets render more
// than one `dispatch*` target depending on `cdp.kind`, e.g. LightTile spans
// plain Light through RGBWLight). Missing-required-key is out of scope —
// this only catches operation names and param keys the dispatcher does not
// recognise at all.
type widgetContract map[string]opKeys

// acceptedOperations mirrors the accepted (operation, key) surface of
// internal/central/adapter/custom_dp_dispatcher.go, one entry per CDP widget
// file under assets/ui/src/lib/cdp/widgets/. Keep it in sync with the
// dispatch* functions named in the comment on each entry.
var acceptedOperations = map[string]widgetContract{
	// dispatchSwitch (switchdev.Switch).
	"SwitchTile.svelte": {
		"turn_on":     keySet(),
		"turn_off":    keySet(),
		"toggle":      keySet(),
		"turn_on_for": keySet("seconds", "duration"),
		"set_on_time": keySet("seconds", "duration"),
	},
	// dispatchIrrigation (valve.Irrigation) union dispatchModulatingValve
	// (valve.Modulating) — ValveTile.svelte renders both kinds via
	// `cdp.kind === "valve_modulating"`.
	"ValveTile.svelte": {
		"open":        keySet("seconds", "duration"),
		"close":       keySet(),
		"set_on_time": keySet("seconds", "duration"),
		"set_level":   keySet("level"),
	},
	// dispatchLight (light.Light) union dispatchColorLight,
	// dispatchColorTempLight, dispatchFixedColorLight, dispatchEffectLight,
	// dispatchRGBWLight — LightTile.svelte renders any of these depending on
	// cdp.capabilities / cdp.kind, and every subtype falls back to
	// dispatchLight for the operations it does not override.
	"LightTile.svelte": {
		"turn_on":               keySet(),
		"turn_off":              keySet(),
		"set_brightness":        keySet("brightness"),
		"set_level":             keySet("state", "brightness", "level"),
		"set_on_time":           keySet("seconds", "duration"),
		"set_color":             keySet("hue", "saturation", "slot"),
		"set_color_temperature": keySet("kelvin"),
		"set_effect":            keySet("label", "index"),
	},
	// dispatchClimate (climate.Climate).
	"ClimateTile.svelte": {
		"set_temperature":         keySet("temperature"),
		"enable_boost":            keySet(),
		"disable_boost":           keySet(),
		"set_mode":                keySet("mode"),
		"set_profile":             keySet("profile"),
		"enable_away":             keySet("until", "temperature"),
		"enable_away_by_calendar": keySet("end", "away_temperature"),
		"enable_away_by_duration": keySet("hours", "duration_seconds", "away_temperature"),
		"disable_away":            keySet(),
	},
	// dispatchCover (cover.Cover) union dispatchGarage (cover.Garage) union
	// dispatchBlind (cover.Blind) — CoverTile.svelte renders `cover`,
	// `cover_blind`, and `cover_garage` from the same file.
	"CoverTile.svelte": {
		"open":         keySet(),
		"close":        keySet(),
		"stop":         keySet(),
		"ventilate":    keySet(),
		"set_position": keySet("position"),
		"set_tilt":     keySet("tilt"),
		"set_combined": keySet("level", "tilt"),
		"open_tilt":    keySet(),
		"close_tilt":   keySet(),
		"stop_tilt":    keySet(),
	},
	// dispatchLock (lock.Lock).
	"LockTile.svelte": {
		"lock":   keySet(),
		"unlock": keySet(),
		"open":   keySet(),
	},
	// dispatchSiren (siren.Siren).
	"SirenTile.svelte": {
		"turn_on":  keySet("duration", "duration_seconds", "acoustic", "optical"),
		"turn_off": keySet(),
		"stop":     keySet(),
	},
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
