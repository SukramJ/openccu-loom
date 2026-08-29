// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// TestUISchemaDeclarationMatchesThePayload validates a fully populated
// [hmapi.UISchema] — the exact value the handler marshals — against the
// `UISchema` component in the specification.
//
// Writing a schema by reading a Go type is the same act that produced the
// defect this suite's sibling guard exists for: a consumer read
// `CustomDPSummary` for a route that answers with something else, and nothing
// said so until the code ran. A declaration checked only by eye has the same
// standing as that guess. So the schema is checked against a marshalled
// payload rather than against a reading of the struct.
//
// Every optional field is populated on purpose. A schema that names a field
// the payload cannot carry, or types one wrongly, only fails when that field
// is present — so a fixture that leaves the optional half empty would pass
// while the declaration is wrong exactly where a consumer would trust it.
func TestUISchemaDeclarationMatchesThePayload(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPISpec(t)
	ref := spec.Components.Schemas["UISchema"]
	if ref == nil || ref.Value == nil {
		t.Fatal("openapi.yaml declares no UISchema component")
	}

	raw := json.RawMessage(`42`)
	optInt := 3
	payload := hmapi.UISchema{
		Channel: hmapi.UISchemaChannel{
			Address: "VCU1234567:1", Number: 1, Type: "SWITCH_VIRTUAL_RECEIVER",
			Label: "Schaltaktor", Device: "VCU1234567", Paramset: "MASTER",
		},
		Groups:         []hmapi.UISchemaGroup{{ID: "g1", Label: "Allgemein", Parameters: []string{"LEVEL"}}},
		ParameterOrder: []string{"LEVEL"},
		Parameters: []hmapi.UISchemaParameter{{
			Name: "LEVEL", Label: "Level", Help: "help", Type: "FLOAT", Unit: "%",
			Min: raw, Max: raw, Default: raw,
			ValueList:  []hmapi.UISchemaValueListEntry{{Value: 0, Key: "OFF", Label: "Aus"}},
			Operations: hmapi.UISchemaParameterOps{Read: true, Write: true, Event: true, Determine: true},
			Flags:      hmapi.UISchemaParameterFlags{Visible: true, Internal: false, Service: false},
			Control:    "BUTTON.SHORT", Value: 0.5, Observed: true,
			ModifiedAt: "2026-08-29T10:00:00Z", GroupID: "g1", Preset: "p", Category: "config",
			KeypressGroup: "kg", DisplayAsPercent: true, DisplayValue: "50 %", Multiplier: 100,
			HasLastValue: true, HiddenByDefault: true,
			TimePairID: "tp", TimeSelectorType: "duration",
			TimePresets:      []hmapi.UISchemaTimePreset{{Base: 1, Factor: 2, Label: "2s"}},
			Presets:          []hmapi.UISchemaPreset{{Label: "On", Value: raw}},
			AllowCustomValue: true, SubsetGroupID: "s1",
		}},
		Visibility: []hmapi.UISchemaVisibility{{
			Show: []string{"LEVEL"}, Hide: []string{"RAMP_TIME"},
			Trigger: "MODE", TriggerValue: 2,
		}},
		CrossValidations: []hmapi.UISchemaCrossValidation{{
			ID: "cv1", Rule: "less_than", ParamA: "A", ParamB: "B", Param: "P",
			MinParam: "MIN", MaxParam: "MAX", AppliesToParams: []string{"A", "B"}, Error: "A < B",
		}},
		Profile: &hmapi.UISchemaProfile{
			ReceiverType: "SWITCH", SenderType: "KEY", ActiveProfileID: 1, Raw: raw,
		},
		SubsetGroups: []hmapi.UISchemaSubsetGroup{{
			ID: "s1", Label: "Licht", MemberParams: []string{"LEVEL"},
			CurrentOptionID: &optInt,
			Options:         []hmapi.UISchemaSubsetOpt{{ID: 1, Label: "Warm", Values: map[string]any{"LEVEL": 0.3}}},
		}},
		ModelDescription: "Funk-Schaltsteckdose",
		DeviceIcon:       "switch",
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal UISchema: %v", err)
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		t.Fatalf("re-parse UISchema JSON: %v", err)
	}
	if err := ref.Value.VisitJSON(instance); err != nil {
		t.Errorf("the declared UISchema does not accept what the handler emits: %v", err)
	}

	// The minimal payload — only the two required members — must also pass,
	// or the declaration would demand fields a real answer can omit.
	minimal, err := json.Marshal(hmapi.UISchema{
		Channel:    hmapi.UISchemaChannel{Address: "VCU1:1", Number: 1, Type: "T", Device: "VCU1", Paramset: "VALUES"},
		Parameters: []hmapi.UISchemaParameter{},
	})
	if err != nil {
		t.Fatalf("marshal minimal UISchema: %v", err)
	}
	var minimalInstance any
	if err := json.Unmarshal(minimal, &minimalInstance); err != nil {
		t.Fatalf("re-parse minimal UISchema JSON: %v", err)
	}
	if err := ref.Value.VisitJSON(minimalInstance); err != nil {
		t.Errorf("the declared UISchema rejects a minimal valid answer: %v", err)
	}
}
