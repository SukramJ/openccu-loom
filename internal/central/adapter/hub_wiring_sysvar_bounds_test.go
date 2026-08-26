// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestUpsertSysvarCarriesDeclaredRange pins that the CCU-declared
// minValue/maxValue reach the model. Without them HA advertises the fallback
// float range on the number entity and the model's own range check can never
// fire, because it is skipped whenever either bound is nil.
func TestUpsertSysvarCarriesDeclaredRange(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu-01")
	entry := &sysvarEntry{
		ID:       "1234",
		Name:     "Sollwert",
		Type:     "FLOAT",
		Value:    json.RawMessage(`"21.5"`),
		MinValue: json.RawMessage(`"0.0"`),
		MaxValue: json.RawMessage(`"50.0"`),
	}
	upsertSysvar(h, entry, nil, hubScanOptions{}, "", hmenum.HubValueTypeFloat, nil, "", "", false)

	sv, ok := h.Sysvar("Sollwert")
	if !ok {
		t.Fatal("sysvar was not registered")
	}
	if sv.Min == nil || sv.Max == nil {
		t.Fatalf("declared range dropped: min=%v max=%v", sv.Min, sv.Max)
	}
	if got := sv.Min.Float; got != 0 {
		t.Errorf("min = %v, want 0", got)
	}
	if got := sv.Max.Float; got != 50 {
		t.Errorf("max = %v, want 50", got)
	}
}

// TestUpsertSysvarIntegerRangeIsInt verifies an INTEGER variable keeps an
// integer-kind bound, which is what the north-bound projections switch on.
func TestUpsertSysvarIntegerRangeIsInt(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu-01")
	entry := &sysvarEntry{
		ID:       "77",
		Name:     "Stufe",
		Type:     "INTEGER",
		Value:    json.RawMessage(`"2"`),
		MinValue: json.RawMessage(`"1"`),
		MaxValue: json.RawMessage(`"5"`),
	}
	upsertSysvar(h, entry, nil, hubScanOptions{}, "", hmenum.HubValueTypeInteger, nil, "", "", false)

	sv, _ := h.Sysvar("Stufe")
	if sv.Min == nil || sv.Min.Kind != hmtypes.ValueKindInt || sv.Min.Int != 1 {
		t.Errorf("min = %+v, want int 1", sv.Min)
	}
	if sv.Max == nil || sv.Max.Kind != hmtypes.ValueKindInt || sv.Max.Int != 5 {
		t.Errorf("max = %+v, want int 5", sv.Max)
	}
}

// TestUpsertSysvarNonNumericHasNoRange verifies the bounds the CCU reports for
// non-numeric variables — where they describe the wire encoding, not an
// operator-facing range — are not projected onto the model.
func TestUpsertSysvarNonNumericHasNoRange(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu-01")
	for _, tc := range []struct {
		name string
		vt   hmenum.HubValueType
	}{
		{"Anwesenheit", hmenum.HubValueTypeLogic},
		{"Modus", hmenum.HubValueTypeList},
		{"Notiz", hmenum.HubValueTypeString},
	} {
		entry := &sysvarEntry{
			ID:       "1",
			Name:     tc.name,
			Value:    json.RawMessage(`"0"`),
			MinValue: json.RawMessage(`"0"`),
			MaxValue: json.RawMessage(`"1"`),
		}
		upsertSysvar(h, entry, nil, hubScanOptions{}, "", tc.vt, nil, "", "", false)
		sv, ok := h.Sysvar(tc.name)
		if !ok {
			t.Fatalf("%s: sysvar was not registered", tc.name)
		}
		if sv.Min != nil || sv.Max != nil {
			t.Errorf("%s: expected no range, got min=%v max=%v", tc.name, sv.Min, sv.Max)
		}
	}
}

// TestUpsertSysvarOmittedRangeStaysNil verifies a variable the CCU reports
// without bounds keeps none, rather than picking up a parsed zero.
func TestUpsertSysvarOmittedRangeStaysNil(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu-01")
	entry := &sysvarEntry{ID: "9", Name: "Frei", Value: json.RawMessage(`"1.0"`)}
	upsertSysvar(h, entry, nil, hubScanOptions{}, "", hmenum.HubValueTypeFloat, nil, "", "", false)

	sv, _ := h.Sysvar("Frei")
	if sv.Min != nil || sv.Max != nil {
		t.Errorf("expected no range, got min=%v max=%v", sv.Min, sv.Max)
	}
}
