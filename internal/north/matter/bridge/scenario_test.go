// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scenarioDir is the directory the harness scans for *.json scenario
// files. Resolved relative to the repo root via the bridge test's CWD
// (internal/north/matter/bridge/).
const scenarioDir = "../../../../docs/parity/matter/scenarios"

// TestScenarios walks every *.json file under docs/parity/matter/scenarios
// and replays it through the harness. New scenarios drop in as
// data files — no code change required. Failed scenarios surface as
// individual subtest failures so CI attribution stays tight.
func TestScenarios(t *testing.T) {
	t.Parallel()

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
