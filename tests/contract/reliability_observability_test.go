// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestReliabilityInstrumentCoverage verifies that every core reliability
// component (Coalescer, Throttle, Retrier, CircuitBreaker, PingPong tracker)
// exposes an observability interface.
//
// In Go the interface is not a decorator but a hook callback (e.g. `SetHook`,
// `AddOnStateChange`, `SetPublishHook`) or a `Stats()` method for metrics.
// Without a hook, latency/counter data are missing and are only noticed
// during live debugging.
func TestReliabilityInstrumentCoverage(t *testing.T) {
	t.Parallel()

	repoRoot := mustRepoRoot(t)
	relDir := filepath.Join(repoRoot, "internal", "client", "reliability")

	requirements := map[string][]string{
		// file → at least one of these functions must exist as a method
		// (either a stats reporter or a hook setter).
		"coalesce.go": {"Stats", "SetHook", "Hook"},
		"throttle.go": {"Stats", "Snapshot", "SetTelemetry", "BurstCount", "ThrottledCount", "BurstDowngraded"},
		"retry.go":    {"Snapshot", "Stats"},
		"circuit.go":  {"AddOnStateChange", "OnStateChange", "State"},
		"pingpong.go": {"Stats", "SetPublishHook", "OnPublish"},
	}

	for file, methods := range requirements {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(relDir, file)
			present := methodsInFile(t, path)
			matched := false
			for _, m := range methods {
				if present[m] {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("%s: none of the required observability methods %v"+
					" are exposed.\n"+
					"  found methods: %v",
					file, methods, sortedKeys(present))
			}
		})
	}
}

// methodsInFile parses path and returns the set of method receiver
// names declared at the top level (e.g. `func (c *Coalescer) Stats() ...`
// → "Stats" is in the set). Function declarations without a receiver
// are also included so package-level helpers count.
func methodsInFile(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make(map[string]bool)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name == nil || !fn.Name.IsExported() {
			continue
		}
		out[fn.Name.Name] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Cheap stable sort for deterministic test failures without importing sort.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
