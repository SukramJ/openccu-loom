// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ChangedFields reports the config field paths whose value differs
// between two configs — typically the running (boot) config and the
// freshly assembled persisted config. It is the general counterpart to
// [RestartRequiredDiff]: where that covers only the restart-required
// subset, this walks every schema field. A field counts as changed when
// its value at the schema path differs (by JSON equality), so reverting
// an edit back to the boot value drops it from the list — the overview
// reflects "what changed since the server started", not "what differs
// from the built-in default".
func ChangedFields(boot, eff *Config) []string {
	if boot == nil || eff == nil {
		return nil
	}
	fields := ClassifyFields(&Config{})
	bootTree, err := configTree(boot)
	if err != nil {
		slog.Warn("config: ChangedFields: failed to build boot config tree; reporting all fields as changed", "err", err)
		return fieldPaths(fields)
	}
	effTree, err := configTree(eff)
	if err != nil {
		slog.Warn("config: ChangedFields: failed to build effective config tree; reporting all fields as changed", "err", err)
		return fieldPaths(fields)
	}
	out := make([]string, 0, 8)
	for _, f := range fields {
		if !jsonEqual(valueAtPath(bootTree, f.Path), valueAtPath(effTree, f.Path)) {
			out = append(out, f.Path)
		}
	}
	return out
}

// fieldPaths extracts the dotted path of every descriptor. Used as the
// fail-safe "everything changed" result when [configTree] cannot build a
// comparison tree for one side — see [ChangedFields].
func fieldPaths(fields []FieldDescriptor) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Path
	}
	return out
}

// configTree marshals a config to a generic JSON tree so values can be
// compared at arbitrary dotted paths without per-field reflection. JSON
// and YAML tags match across the config structs, so the tree keys line
// up with the schema paths from [ClassifyFields].
//
// An error here means some field's current value cannot round-trip
// through JSON at all (e.g. a float64 NaN/Inf) — [ChangedFields] must not
// treat that as "no tree, so nothing to compare": on one side, a missing
// tree turns every present-on-the-other-side field into a mismatch: an
// asymmetric marshal failure between the two configs would then report a
// stray superset of changes, and a symmetric failure (both sides caught
// on the very same undialable field) would hide every genuine change
// behind an all-nil-vs-nil comparison. Callers must react to a returned
// error rather than silently comparing against a nil tree.
func configTree(c *Config) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("config: marshal config tree: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("config: unmarshal config tree: %w", err)
	}
	return m, nil
}

// valueAtPath walks a JSON tree along a dotted path; nil when any
// segment is missing.
func valueAtPath(tree map[string]any, dotted string) any {
	var cur any = tree
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func jsonEqual(a, b any) bool {
	ba, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ba, bb)
}
