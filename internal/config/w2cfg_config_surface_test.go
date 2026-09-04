// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

// w2CfgYAMLTypesOutsideTheConfigTree lists the struct types in this
// package that carry yaml tags without being part of [Config] or
// [BootstrapConfig]. Every entry needs a reason a reader can check,
// because a yaml-tagged type nobody can reach from a config tier is a
// config surface that does not exist: an operator writing its keys gets
// silence, and a developer reading it takes its defaults for the values
// the daemon runs on.
//
// The list is empty. Two entries were removed rather than justified:
// ScheduleTimerConfig and TimeoutConfig described the south-bound
// cadence and timeout policy with defaults that had already drifted away
// from the constants the scheduler actually runs on, and neither was
// reachable from any config tier.
var w2CfgYAMLTypesOutsideTheConfigTree = map[string]string{}

// TestW2CfgEveryYAMLTaggedTypeIsReachableFromAConfigTier walks the
// package's own source for struct types with yaml tags and fails when
// one of them cannot be reached from [Config] or [BootstrapConfig].
//
// The two tiers are the only YAML the daemon parses (Config.Parse and
// ParseBootstrap), so reachability from one of them is what makes a
// yaml tag mean anything at all.
func TestW2CfgEveryYAMLTaggedTypeIsReachableFromAConfigTier(t *testing.T) {
	t.Parallel()

	declared := w2CfgYAMLTaggedTypes(t)
	if len(declared) == 0 {
		t.Fatal("found no yaml-tagged struct types in the package source — the parser is not reading what it thinks it is")
	}

	reachable := map[string]bool{}
	w2CfgCollectTypes(reflect.TypeOf(Config{}), reachable)
	w2CfgCollectTypes(reflect.TypeOf(BootstrapConfig{}), reachable)
	if !reachable["CentralConfig"] {
		t.Fatal("the reflective walk did not reach CentralConfig — it is not walking the config tree")
	}

	for _, name := range declared {
		if reachable[name] {
			continue
		}
		if why, ok := w2CfgYAMLTypesOutsideTheConfigTree[name]; ok {
			t.Logf("%s is yaml-tagged and unreachable, declared: %s", name, why)
			continue
		}
		t.Errorf("%s carries yaml tags but is reachable from neither Config nor BootstrapConfig: "+
			"no YAML the daemon parses can ever set its fields", name)
	}
}

// w2CfgYAMLTaggedTypes returns the names of every struct type declared in
// the package's non-test sources that has at least one yaml-tagged field.
func w2CfgYAMLTaggedTypes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, f := range st.Fields.List {
				if f.Tag != nil && strings.Contains(f.Tag.Value, "yaml:") {
					names = append(names, ts.Name.Name)
					return true
				}
			}
			return true
		})
	}
	return names
}

// w2CfgCollectTypes records the name of every struct type reachable from
// rt through fields, pointers, slices, arrays and map values.
func w2CfgCollectTypes(rt reflect.Type, seen map[string]bool) {
	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice ||
		rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct || seen[rt.Name()] {
		return
	}
	if rt.Name() != "" {
		seen[rt.Name()] = true
	}
	for i := range rt.NumField() {
		w2CfgCollectTypes(rt.Field(i).Type, seen)
	}
}
