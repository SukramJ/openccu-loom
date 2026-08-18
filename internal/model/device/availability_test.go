// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"encoding/json"
	"testing"
)

// TestAvailabilityInfoJSONShapeMatchesDocumentedSchema pins
// AvailabilityInfo's wire shape against assets/openapi.yaml's
// DeviceDetail.availability schema: exactly the five documented keys
// (IsReachable, LastUpdated, BatteryLevel, LowBattery, SignalStrength).
// Before explicit json tags were added, the struct had none at all, so
// encoding/json fell back to the literal Go field names — which happened
// to already match the documented casing for the first five, but also
// leaked the undocumented RSSIPeer field onto the wire with no contract
// or generated client type describing it.
func TestAvailabilityInfoJSONShapeMatchesDocumentedSchema(t *testing.T) {
	rssi := -55
	info := AvailabilityInfo{
		IsReachable: true,
		RSSIPeer:    &rssi,
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{"IsReachable", "LastUpdated", "BatteryLevel", "LowBattery", "SignalStrength"}
	if len(m) != len(want) {
		t.Fatalf("AvailabilityInfo JSON has %d keys, want %d: %v", len(m), len(want), keysOfMap(m))
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("AvailabilityInfo JSON missing documented key %q; got %v", k, keysOfMap(m))
		}
	}
	if _, ok := m["RSSIPeer"]; ok {
		t.Error("AvailabilityInfo JSON must not leak the undocumented RSSIPeer key")
	}
}

func keysOfMap(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
