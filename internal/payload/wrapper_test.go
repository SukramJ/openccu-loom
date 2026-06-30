// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPerDPStateAdditionalInformationPresent verifies that a PerDPState
// carrying a non-nil AdditionalInformation map marshals the field into JSON
// with the expected keys and values.
func TestPerDPStateAdditionalInformationPresent(t *testing.T) {
	t.Parallel()
	s := PerDPState{
		Value:     2.9,
		Available: true,
		AdditionalInformation: map[string]any{
			"Battery Type": "LR03",
			"Battery Qty":  2,
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, `"additional_information"`) {
		t.Errorf("JSON must contain additional_information key; got %s", got)
	}
	if !strings.Contains(got, `"Battery Type"`) {
		t.Errorf("JSON must contain Battery Type; got %s", got)
	}
	if !strings.Contains(got, `"LR03"`) {
		t.Errorf("JSON must contain LR03; got %s", got)
	}
	if !strings.Contains(got, `"Battery Qty"`) {
		t.Errorf("JSON must contain Battery Qty; got %s", got)
	}
	// Unmarshal and verify the nested value types round-trip correctly.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	ai, ok := m["additional_information"].(map[string]any)
	if !ok {
		t.Fatalf("additional_information is not an object: %T", m["additional_information"])
	}
	if ai["Battery Type"] != "LR03" {
		t.Errorf("Battery Type = %v, want LR03", ai["Battery Type"])
	}
}

// TestPerDPStateAdditionalInformationOmittedWhenNil verifies that a PerDPState
// with no AdditionalInformation does not emit the field in JSON (additive/
// omitempty guarantee), while standard fields are still present.
func TestPerDPStateAdditionalInformationOmittedWhenNil(t *testing.T) {
	t.Parallel()
	s := PerDPState{
		Value:     21.6,
		Available: true,
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(data)

	if strings.Contains(got, "additional_information") {
		t.Errorf("JSON must NOT contain additional_information when nil; got %s", got)
	}
	if !strings.Contains(got, `"value"`) {
		t.Errorf("JSON must still contain value field; got %s", got)
	}
	if !strings.Contains(got, `"available"`) {
		t.Errorf("JSON must still contain available field; got %s", got)
	}
}
