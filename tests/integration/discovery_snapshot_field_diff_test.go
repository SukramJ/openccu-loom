// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration — cross-stack discovery snapshot field diff.
//
// TestDiscoverySnapshotFieldDiff loads the pre-computed openccu-loom and
// structural field-level diff on the join-key intersection.
//
// This is the simplest possible broker-parity check: it does not need a
// running daemon, broker, or mock CCU. Both snapshot files are produced
// by the respective stack's snapshot-dump tools and committed as
// integration-test golden data.
//
// Fields checked per entity in the join-key intersection:
//
//   - device_class      — must match (hard error on drift)
//   - entity_category   — must match (hard error on drift)
//   - enabled_by_default — must match (hard error on drift)
//   - model_id          — openccu-loom-side invariant only: must not be
//     absent/empty for any device that exists in the HA reference.
//
// The
//
//	  no cross-stack comparison for this field; the test instead asserts
//	  that openccu-loom populates it for every device present in the HA
//	  reference (i.e. every device for which a translation is expected).
//	- name              — warning only: the two stacks use different
//	  naming algorithms (HA: CCU device name; gh: address + channel).
//
// Motivating bugs (originally detected by this test class, now fixed):
//
//  1. HMIP-PS / HMIP-PSM (VCU1366171, VCU3941846): empty model_id —
//     SUBTYPE-based variants use uppercase "HMIP-" prefix that the
//     translation-lookup chain did not recognise as a variant of "HmIP-PS".
//  2. HmIP-eTRV-* family (VCU1494703, VCU1530633, VCU1768323, VCU3609622,
//     VCU6177550, VCU8688276 + 2 more): empty model_id — these eTRV
//     variants are keyed in OCCU translations by SUBTYPE ("eTRV-2 I9F",
//     "eTRV-B-2 R4M") which was not propagated through the lookup chain.
//  3. HmIP-SMO / HmIP-SMO-2 / HmIP-SMO-A (VCU5628817, VCU2573721,
//     VCU5092447): empty model_id — motion-detector variants share a
//     common "SMO" root but each SUBTYPE maps to a distinct translation
//     label that the snapshot-era code did not resolve.
//
// All three classes are resolved by the multi-stage
// Translations.DeviceModelLabel lookup (vendor-prefix strip + suffix
// strip + SUBTYPE fallback), covered by
// internal/ccudata/translations_subtype_lookup_test.go.
//
// Two residual devices had empty model_id for a different reason — a
// missing device-model *label* in the upstream translation catalogue,
// not a lookup bug: HmIP-DLP (Door Lock Drive - pro) and HmIP-UDI-SMI55
// (Universal Dimming Control Element - motion detector). Both ship icons
// + parameter help but no device_models entry; the curated overlay
// (internal/ccudata/embedded/translation_custom/device_models_{en,de}.json)
// now supplies the label, guarded by
// internal/ccudata/device_models_overlay_test.go.
//
// With those closed, the model_id invariant is expected to report 0
// failures on a freshly regenerated snapshot; 0 device_class /
// entity_category / enabled_by_default drift; ~1651 name warnings (by
// design: different naming algorithms). The committed openccu-loom
// snapshot is produced on demand (gitignored) — regenerate via
// `make snapshot-go` to pick up the two overlay labels.
//
// Run:
//
//	go test -tags=integration ./tests/integration/... \
//	  -run TestDiscoverySnapshotFieldDiff -v -count=1
package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestDiscoverySnapshotFieldDiff is the cross-stack field-level diff.
// It does not start any services; it only reads the pre-computed
// snapshot files from testdata/.
//
// Exit codes:
//   - PASS: zero hard-error drift; name warnings are tolerated.
//   - FAIL: any device_class / entity_category / enabled_by_default drift,
//     or any model_id absence for a device present in the HA reference.
//   - SKIP: one or both snapshot files are absent (regenerate with the
//     respective dump tools first).
func TestDiscoverySnapshotFieldDiff(t *testing.T) {
	const (
		ghPath = "testdata/discovery_snapshot_openccu-loom.json"
		haPath = "testdata/discovery_snapshot_homematicip_local.json"
	)

	ghEntities, err := loadSnapshotEntities(ghPath)
	if err != nil {
		t.Skipf("openccu-loom snapshot not available (%v); run TestDiscoverySnapshotDumpAgainstGodevccu first", err)
	}
	haEntities, err := loadSnapshotEntities(haPath)
	if err != nil {
		t.Skipf("homematicip_local snapshot not available (%v); run the Python snapshot script first", err)
	}

	t.Logf("loaded snapshots: openccu-loom=%d entities, homematicip_local=%d entities",
		len(ghEntities), len(haEntities))

	// Build the join-key intersection.
	intersection := make([]string, 0, min(len(ghEntities), len(haEntities)))
	for jk := range ghEntities {
		if _, ok := haEntities[jk]; ok {
			intersection = append(intersection, jk)
		}
	}
	sort.Strings(intersection)
	t.Logf("join-key intersection: %d entities (only_gh=%d only_ha=%d)",
		len(intersection),
		len(ghEntities)-len(intersection),
		len(haEntities)-len(intersection))

	if len(intersection) == 0 {
		t.Fatal("intersection is empty — join-key schemas are incompatible or one snapshot is empty")
	}

	// -----------------------------------------------------------------------
	// 1. model_id invariant: for every device present in the HA reference,
	//    openccu-loom must populate model_id in the MQTT device block.
	// -----------------------------------------------------------------------
	// Collect one representative entity per device address from the intersection.
	haDeviceAddresses := buildDeviceAddressSet(haEntities, intersection)
	ghDeviceModelIDs := buildDeviceModelIDMap(ghEntities, intersection)

	type modelIDFail struct {
		address string
		model   string
	}
	var modelIDFails []modelIDFail
	for addr := range haDeviceAddresses {
		mid, seen := ghDeviceModelIDs[addr]
		if !seen {
			continue // not in gh intersection — different coverage
		}
		if mid.modelID == "" {
			modelIDFails = append(modelIDFails, modelIDFail{addr, mid.model})
		}
	}
	sort.Slice(modelIDFails, func(i, j int) bool { return modelIDFails[i].address < modelIDFails[j].address })
	for _, f := range modelIDFails {
		t.Errorf("INVARIANT model_id: device %s (model=%s) has empty/absent model_id"+
			" in openccu-loom MQTT payload (SUBTYPE not propagated?)", f.address, f.model)
	}

	// -----------------------------------------------------------------------
	// 2. Field-level diff on the intersection.
	// -----------------------------------------------------------------------
	type driftRow struct {
		joinKey  string
		field    string
		gh       any
		ha       any
		warnOnly bool
	}
	var driftRows []driftRow

	for _, jk := range intersection {
		ghE := ghEntities[jk]
		haE := haEntities[jk]

		// device_class
		if ghE.DeviceClass != haE.DeviceClass {
			driftRows = append(driftRows, driftRow{
				joinKey: jk,
				field:   "device_class",
				gh:      ghE.DeviceClass,
				ha:      haE.DeviceClass,
			})
		}
		// entity_category
		if ghE.EntityCategory != haE.EntityCategory {
			driftRows = append(driftRows, driftRow{
				joinKey: jk,
				field:   "entity_category",
				gh:      ghE.EntityCategory,
				ha:      haE.EntityCategory,
			})
		}
		// enabled_by_default
		if ptrBoolStr(ghE.EnabledByDefault) != ptrBoolStr(haE.EnabledByDefault) {
			driftRows = append(driftRows, driftRow{
				joinKey: jk,
				field:   "enabled_by_default",
				gh:      ghE.EnabledByDefault,
				ha:      haE.EnabledByDefault,
			})
		}
		// name — warning only
		if ghE.Name != haE.Name && ghE.Name != "" && haE.Name != "" {
			driftRows = append(driftRows, driftRow{
				joinKey:  jk,
				field:    "name",
				gh:       ghE.Name,
				ha:       haE.Name,
				warnOnly: true,
			})
		}
	}

	sort.Slice(driftRows, func(i, j int) bool {
		if driftRows[i].joinKey != driftRows[j].joinKey {
			return driftRows[i].joinKey < driftRows[j].joinKey
		}
		return driftRows[i].field < driftRows[j].field
	})

	var errCount, warnCount int
	for _, row := range driftRows {
		msg := fmt.Sprintf("%-60s %-20s gh=%-30v ha=%v",
			row.joinKey, row.field, row.gh, row.ha)
		if row.warnOnly {
			t.Logf("WARN  %s", msg)
			warnCount++
		} else {
			t.Errorf("DRIFT %s", msg)
			errCount++
		}
	}

	t.Logf("summary: intersection=%d model_id_fails=%d hard_drift=%d name_warns=%d",
		len(intersection), len(modelIDFails), errCount, warnCount)
}

// ---------------------------------------------------------------------------
// Snapshot loading helpers
// ---------------------------------------------------------------------------

// fieldSnapshotEntity holds the fields we compare from a discovery snapshot.
type fieldSnapshotEntity struct {
	JoinKey          string
	DeviceAddress    string
	Model            string
	ModelID          string // from payload.device.model_id (gh only; ha omits this)
	DeviceClass      string // from payload.device_class
	EntityCategory   string // from payload.entity_category
	EnabledByDefault *bool  // from payload.enabled_by_default
	Name             string // from payload.name
}

// loadSnapshotEntities reads a discovery snapshot JSON file and returns a
// map indexed by join_key.
func loadSnapshotEntities(path string) (map[string]fieldSnapshotEntity, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	var root struct {
		Entities []struct {
			JoinKey       string         `json:"join_key"`
			DeviceAddress string         `json:"device_address"`
			Model         string         `json:"model,omitempty"`
			Payload       map[string]any `json:"payload"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(f).Decode(&root); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	out := make(map[string]fieldSnapshotEntity, len(root.Entities))
	for _, raw := range root.Entities {
		ent := fieldSnapshotEntity{
			JoinKey:       raw.JoinKey,
			DeviceAddress: strings.ToUpper(raw.DeviceAddress),
			Model:         raw.Model,
		}
		if p := raw.Payload; p != nil {
			if dev, ok := p["device"].(map[string]any); ok {
				if mid, ok := dev["model_id"].(string); ok {
					ent.ModelID = mid
				}
			}
			if dc, ok := p["device_class"].(string); ok {
				ent.DeviceClass = dc
			}
			if ec, ok := p["entity_category"].(string); ok {
				ent.EntityCategory = ec
			}
			if v, ok := p["enabled_by_default"].(bool); ok {
				ent.EnabledByDefault = &v
			}
			if n, ok := p["name"].(string); ok {
				ent.Name = n
			}
		}
		out[raw.JoinKey] = ent
	}
	return out, nil
}

// deviceModelIDEntry records the first observed model_id (and model name)
// for a device address in the intersection.
type deviceModelIDEntry struct {
	model   string
	modelID string // empty string = absent / not populated
}

// buildDeviceModelIDMap collects the first model_id seen per device address
// across the intersection join keys in the openccu-loom snapshot.
func buildDeviceModelIDMap(
	entities map[string]fieldSnapshotEntity,
	intersection []string,
) map[string]deviceModelIDEntry {
	out := make(map[string]deviceModelIDEntry)
	for _, jk := range intersection {
		ent := entities[jk]
		addr := ent.DeviceAddress
		if addr == "" {
			continue
		}
		if _, seen := out[addr]; seen {
			// Only record once; prefer non-empty model_id if we get a
			// better entry later.
			if out[addr].modelID == "" && ent.ModelID != "" {
				out[addr] = deviceModelIDEntry{model: ent.Model, modelID: ent.ModelID}
			}
			continue
		}
		out[addr] = deviceModelIDEntry{model: ent.Model, modelID: ent.ModelID}
	}
	return out
}

// buildDeviceAddressSet collects the unique device addresses present in the
// HA snapshot's intersection entities.
func buildDeviceAddressSet(
	entities map[string]fieldSnapshotEntity,
	intersection []string,
) map[string]struct{} {
	out := make(map[string]struct{})
	for _, jk := range intersection {
		addr := entities[jk].DeviceAddress
		if addr != "" {
			out[addr] = struct{}{}
		}
	}
	return out
}

// ptrBoolStr converts *bool to a stable string for comparison.
func ptrBoolStr(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	if *b {
		return "true"
	}
	return "false"
}

// min returns the smaller of a and b. Duplicated here for Go <1.21
// compatibility; the builtin min is available since Go 1.21.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
