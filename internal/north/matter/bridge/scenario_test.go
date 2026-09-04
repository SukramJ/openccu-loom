// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scenarioCorpus returns the directory the harness scans for *.json
// scenario files.
//
// The corpus is single-sourced under notes/parity/matter/scenarios and is
// deliberately not copied into this package's testdata: the coverage gate in
// tests/contract/matter_scenario_gate_test.go reads that same directory, so a
// second copy would let a new scenario satisfy the gate while never being
// replayed here. Since the corpus therefore has to be reached from outside the
// package, the module root is found by walking up from this file to go.mod —
// which is independent both of the working directory `go test` was invoked
// from and of where this package sits in the tree.
func scenarioCorpus(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed: cannot locate the module root")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "notes", "parity", "matter", "scenarios")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// TestScenarios walks every *.json file under notes/parity/matter/scenarios
// and replays it through the harness. New scenarios drop in as
// data files — no code change required. Failed scenarios surface as
// individual subtest failures so CI attribution stays tight.
func TestScenarios(t *testing.T) {
	t.Parallel()

	scenarioDir := scenarioCorpus(t)
	entries, err := os.ReadDir(scenarioDir)
	if err != nil {
		t.Fatalf("read scenarios dir %s: %v", scenarioDir, err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, "_") {
			// Reserved for schema / recorder artefacts.
			continue
		}
		if strings.Contains(name, "__matter_js_reference.") {
			// Sidecar fixtures emitted by _record.ts — paired data,
			// not standalone scenarios.
			continue
		}
		found = true
		fpath := filepath.Join(scenarioDir, name)
		t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
			t.Parallel()
			s, err := loadScenarioFile(fpath)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			h := newScenarioHarness(t, s)
			h.run()
		})
	}
	if !found {
		t.Fatalf("no scenario JSON files found under %s", scenarioDir)
	}
}
