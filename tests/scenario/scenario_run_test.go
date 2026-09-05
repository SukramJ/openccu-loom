// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package scenario replays the Matter behaviour corpus under
// notes/parity/matter/scenarios against a live go-fabric bridge.
//
// The corpus is single-sourced: TestScenarioCoverage in
// tests/contract/matter_scenario_gate_test.go derives the required set
// from internal/model/custom and reads the same directory this runner
// walks. A second copy would let a new scenario satisfy the gate while
// never being replayed here, so the harness reaches the corpus through
// the module root instead of through a testdata copy.
//
// The bridge is driven only through go-fabric's exported surface, which
// makes this the first outside consumer of that API and therefore the
// place where a missing export shows up as a failing scenario rather
// than as a design opinion.
package scenario

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scenarioCorpus returns the directory the harness scans for *.json
// scenario files. The module root is found by walking up from this
// file to go.mod, which is independent both of the working directory
// `go test` was invoked from and of where this package sits.
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
// and replays it through the harness. New scenarios drop in as data
// files — no code change required. Failed scenarios surface as
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
			// Sidecar fixtures emitted by the recorder — paired data,
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
