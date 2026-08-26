// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestScenarioCoverage enforces that every custom-DP type with a
// Matter-side integration (a `<type>/matter.go` under
// internal/model/custom/) has at least one scenario tagged with its
// type name in notes/parity/matter/scenarios. New Matter-mappable
// custom DPs cannot land without scenario coverage — the
// behavior-scenario harness is the regression net for the very class
// of cross-layer bug single-package unit tests cannot catch (see F4).
//
// The gate is intentionally simple: the tag matrix lives in the
// scenario JSON itself, so adding a scenario for a new type
// (e.g. groupedlight tagged ["groupedlight", "matter", ...]) closes
// the gate automatically.
func TestScenarioCoverage(t *testing.T) {
	t.Parallel()

	customDPRoot := scenarioRepoPath(t, "internal/model/custom")
	scenarioDir := scenarioRepoPath(t, "notes/parity/matter/scenarios")

	withMatter, err := customDPsWithMatterIntegration(customDPRoot)
	if err != nil {
		t.Fatalf("enumerate custom DPs: %v", err)
	}
	if len(withMatter) == 0 {
		t.Fatal("no custom DPs with matter.go discovered — directory layout assumption broken")
	}

	tagsBySection, err := tagsFromScenarios(scenarioDir)
	if err != nil {
		t.Fatalf("scan scenarios: %v", err)
	}

	var gaps []string
	for _, typ := range withMatter {
		if !tagsBySection[typ] {
			gaps = append(gaps, typ)
		}
	}
	if len(gaps) > 0 {
		sort.Strings(gaps)
		t.Errorf(`%d custom-DP type(s) with Matter integration have NO scenario tagged with their name:
  %s

Add a scenario under notes/parity/matter/scenarios/ tagged with each missing type.
A minimal template:

  {
    "name": "<type>__<observable>_via_engine",
    "description": "<one paragraph regression rationale>",
    "tags": ["matter", "subscribe", "f4", "<type>", "engine-tick"],
    "given": {
      "session_id": <unique uint16>,
      "peer_subscribe_exchange_id": <unique uint16>,
      "subscription": {"endpoint": <ep>, "cluster": <cluster_id>, "attribute": <attr_id>}
    },
    "steps": [
      {"actor": "ccu", "kind": "fire_via_engine"},
      {"actor": "bridge", "kind": "expect_tx",
        "opcode": "ReportData", "initiator": true,
        "exchange_id_fresh": true, "exchange_id_neq_subscribe": true,
        "tlv_tags_present": [0, 4], "bind_exchange_id_to": "$fresh"},
      {"actor": "peer", "kind": "send_status_response", "exchange": "$fresh", "status": "Success"},
      {"actor": "bridge", "kind": "expect_log", "msg": "matter.rx.im.status_ack", "match_exchange": "$fresh"}
    ]
  }
`, len(gaps), strings.Join(gaps, ", "))
	}
}

func scenarioRepoPath(t *testing.T, rel string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	return filepath.Join(root, rel)
}

// customDPsWithMatterIntegration returns the sorted list of
// `<type>` directory names under `customDPRoot` that carry a
// matter.go (or matter_*.go). The presence of any such file marks
// the type as Matter-mappable — write-only DPs (e.g. textdisplay)
// and DPs without Matter exposure (e.g. valve) are excluded.
func customDPsWithMatterIntegration(customDPRoot string) ([]string, error) {
	entries, err := os.ReadDir(customDPRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "builtins" {
			continue
		}
		dir := filepath.Join(customDPRoot, name)
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		hasMatter := false
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			fn := f.Name()
			if strings.HasSuffix(fn, "_test.go") {
				continue
			}
			if fn == "matter.go" || strings.HasPrefix(fn, "matter_") && strings.HasSuffix(fn, ".go") {
				hasMatter = true
				break
			}
		}
		if hasMatter {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// tagsFromScenarios scans scenarioDir for *.json scenarios (skipping
// recorder sidecars + reserved underscore-prefixed files) and
// returns the union of tags as a presence set.
func tagsFromScenarios(scenarioDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(scenarioDir)
	if err != nil {
		return nil, err
	}
	tags := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, "_") {
			continue
		}
		if strings.Contains(name, "__matter_js_reference.") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(scenarioDir, name)) //nolint:gosec // repo-anchored
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var meta struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		for _, t := range meta.Tags {
			tags[t] = true
		}
	}
	return tags, nil
}
