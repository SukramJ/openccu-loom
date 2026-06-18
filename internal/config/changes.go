// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"bytes"
	"encoding/json"
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
	bootTree := configTree(boot)
	effTree := configTree(eff)
	fields := ClassifyFields(&Config{})
	out := make([]string, 0, 8)
	for _, f := range fields {
		if !jsonEqual(valueAtPath(bootTree, f.Path), valueAtPath(effTree, f.Path)) {
			out = append(out, f.Path)
		}
	}
	return out
}

// configTree marshals a config to a generic JSON tree so values can be
// compared at arbitrary dotted paths without per-field reflection. JSON
// and YAML tags match across the config structs, so the tree keys line
// up with the schema paths from [ClassifyFields].
func configTree(c *Config) map[string]any {
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
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
