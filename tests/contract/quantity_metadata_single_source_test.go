// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// quantity_metadata_single_source_test.go pins that the
// (device model, parameter) → quantity classification has exactly one home,
// internal/parameter, and that the model layer reads it rather than a copy
// of its own.
//
// Scope, stated so the guard is not read as more than it is: it measures the
// classification table, NOT the published MQTT payload. For a binary sensor
// the discovery builder overwrites the quantity-derived device_class with
// the HA-registry rule (applyEntityDescription), so a divergence here is a
// divergence in the domain's answer, not necessarily in the wire.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// quantityMetadataGoldenPath is the HA-registry snapshot. It is an
// independent third carrier of the same knowledge — generated from the
// mqtt rule slice, never from internal/parameter — which is what keeps the
// comparison below from being a table checked against itself.
const quantityMetadataGoldenPath = "testdata/ha_registry_description_rules.json"

// quantityMetadataGoldenRule is the subset of the golden this guard reads.
type quantityMetadataGoldenRule struct {
	Description struct {
		DeviceClass string
	}
	Category   string
	Parameters []string
	Devices    []string
}

// quantityMetadataDeviceScopedRules returns the golden's binary-sensor rules
// that name both a device prefix and a device_class. Device-agnostic rules
// are excluded on purpose: the HA registry and the quantity table classify
// a bare parameter for different audiences and are allowed to differ there
// (POWER_MAINS_FAILURE is "power" in the registry and a problem quantity in
// the classification). A device-scoped rule has no such freedom — it names
// one concrete device's one concrete parameter.
func quantityMetadataDeviceScopedRules(t *testing.T) []quantityMetadataGoldenRule {
	t.Helper()
	raw, err := os.ReadFile(quantityMetadataGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v", quantityMetadataGoldenPath, err)
	}
	var all []quantityMetadataGoldenRule
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("decode %s: %v", quantityMetadataGoldenPath, err)
	}
	var out []quantityMetadataGoldenRule
	for _, r := range all {
		if r.Category != "binary_sensor" || r.Description.DeviceClass == "" {
			continue
		}
		if len(r.Devices) == 0 || len(r.Parameters) == 0 {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		t.Fatalf("%s carries no device-scoped binary-sensor rule; the guard would pass vacuously", quantityMetadataGoldenPath)
	}
	return out
}

// TestQuantityMetadataBinarySensorRulesMatchTheHAGolden asserts that
// internal/parameter classifies every device-scoped binary-sensor rule the
// HA-registry golden carries. hmenum.Quantity's string form is the HA
// device_class vocabulary (pkg/hmenum/quantity.go), so the two can be
// compared without a translation table in the middle.
func TestQuantityMetadataBinarySensorRulesMatchTheHAGolden(t *testing.T) {
	t.Parallel()
	for _, rule := range quantityMetadataDeviceScopedRules(t) {
		for _, model := range rule.Devices {
			for _, param := range rule.Parameters {
				got := parameter.BinarySensorQuantityFor(model, param)
				if string(got) != rule.Description.DeviceClass {
					t.Errorf("parameter.BinarySensorQuantityFor(%q, %q) = %q, want %q (%s)",
						model, param, got, rule.Description.DeviceClass, quantityMetadataGoldenPath)
				}
			}
		}
	}
}

// TestQuantityMetadataModelChainReadsTheParameterTables asserts that a data
// point built through the real generic constructor resolves to the same
// classification. It is the wiring half of the fold: the model layer must
// answer out of internal/parameter, not out of a table of its own. A local
// copy in internal/model/generic that drops or renames a rule turns this
// red while the golden comparison above stays green.
func TestQuantityMetadataModelChainReadsTheParameterTables(t *testing.T) {
	t.Parallel()
	for _, rule := range quantityMetadataDeviceScopedRules(t) {
		for _, model := range rule.Devices {
			for _, param := range rule.Parameters {
				key, err := hmtypes.NewDataPointKey("HmIP-RF", "QMT0001:1", hmenum.ParamsetKeyValues, param)
				if err != nil {
					t.Fatalf("NewDataPointKey(%q): %v", param, err)
				}
				dp := generic.NewBinarySensor(generic.Spec{
					Key:         key,
					Kind:        generic.KindBinarySensor,
					Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead},
					DeviceModel: model,
					CentralName: "c1",
				})
				if got := dp.Quantity(); string(got) != rule.Description.DeviceClass {
					t.Errorf("generic BinarySensor(%q, %q).Quantity() = %q, want %q",
						model, param, got, rule.Description.DeviceClass)
				}
			}
		}
	}
}

// quantityMetadataTableDecl matches a package-level declaration of a
// parameter-keyed classification table.
var quantityMetadataTableDecl = regexp.MustCompile(`(?m)^var\s+\w*(?i:(?:sensorMetadataBy|binarySensorQuantityBy))\w*\s*=`)

// TestQuantityMetadataGenericDeclaresNoLocalTable is the anti-regrowth half.
// The comparisons above can only see a divergence that changes an answer; a
// re-introduced copy that happens to agree today would pass them and drift
// later. This one fails the moment internal/model/generic declares a
// classification table at all.
func TestQuantityMetadataGenericDeclaresNoLocalTable(t *testing.T) {
	t.Parallel()
	const dir = "../../internal/model/generic"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		if loc := quantityMetadataTableDecl.FindIndex(src); loc != nil {
			t.Errorf("internal/model/generic/%s declares its own quantity table (%q); "+
				"the classification has one home, internal/parameter/metadata.go",
				name, strings.TrimSpace(string(src[loc[0]:loc[1]])))
		}
	}
	if scanned == 0 {
		t.Fatalf("scanned no Go file under %s; the guard would pass vacuously", dir)
	}
}
