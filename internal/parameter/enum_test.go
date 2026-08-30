// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestEnumLabelResolvesIndexAndLabelShapes pins both wire shapes an ENUM
// arrives in, and the values that must not be mistaken for an index.
func TestEnumLabelResolvesIndexAndLabelShapes(t *testing.T) {
	t.Parallel()

	desc := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM"},
	}

	cases := []struct {
		name      string
		raw       any
		wantLabel string
		wantOK    bool
	}{
		{name: "int32 index", raw: int32(1), wantLabel: "PRIMARY_ALARM", wantOK: true},
		{name: "int index", raw: 2, wantLabel: "INTRUSION_ALARM", wantOK: true},
		{name: "int64 index", raw: int64(0), wantLabel: "IDLE_OFF", wantOK: true},
		{name: "json float index", raw: float64(1), wantLabel: "PRIMARY_ALARM", wantOK: true},
		{name: "label", raw: "PRIMARY_ALARM", wantLabel: "PRIMARY_ALARM", wantOK: true},
		{name: "empty label", raw: "", wantOK: false},
		{name: "index past value list", raw: int32(3), wantOK: false},
		{name: "negative index", raw: int32(-1), wantOK: false},
		{name: "nil", raw: nil, wantOK: false},
		// A bool is not an enum index: coercing it would resolve `true`
		// to the second VALUE_LIST entry.
		{name: "bool", raw: true, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			label, ok := parameter.EnumLabel(desc, tc.raw)
			if ok != tc.wantOK || label != tc.wantLabel {
				t.Errorf("EnumLabel(%#v) = (%q, %v), want (%q, %v)", tc.raw, label, ok, tc.wantLabel, tc.wantOK)
			}
		})
	}
}

// TestEnumLabelWithoutValueListRejectsIndexes guards the descriptors the
// CCU ships without a VALUE_LIST: there is nothing to resolve an index
// against, so reporting a label would be an invention.
func TestEnumLabelWithoutValueListRejectsIndexes(t *testing.T) {
	t.Parallel()

	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeEnum}
	if label, ok := parameter.EnumLabel(desc, int32(0)); ok {
		t.Errorf("EnumLabel with no VALUE_LIST = (%q, true), want ok=false", label)
	}
	if label, ok := parameter.EnumLabel(desc, "OPEN"); !ok || label != "OPEN" {
		t.Errorf("EnumLabel(label form) = (%q, %v), want (\"OPEN\", true)", label, ok)
	}
}

// TestEnumLabelFromWireCoversEveryWireForm pins the integer forms a CCU
// value arrives in, the numeric-string form a JSON transport produces, and
// the two refusals that make the resolution safe: a non-numeric string is a
// label rather than an index, and a bool is neither — an ENUM read must not
// turn `true` into VALUE_LIST[1].
//
// The MQTT bridge used to carry this matrix itself. It moved here with the
// resolution, so a transport growing another numeric form is answered once
// instead of on whichever plane happens to notice.
func TestEnumLabelFromWireCoversEveryWireForm(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{ValueList: []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K"}}
	cases := []struct {
		in        any
		wantLabel string
		wantOK    bool
	}{
		{int(2), "C", true},
		{int32(3), "D", true},
		{int64(4), "E", true},
		{uint(5), "F", true},
		{uint32(6), "G", true},
		{uint64(7), "H", true},
		{float64(8), "I", true},
		{float32(9), "J", true},
		{"10", "K", true},
		{"", "", false},
		{"abc", "abc", true},
		{nil, "", false},
		{true, "", false},
		{int(99), "", false},
		{"99", "", false},
	}
	for _, c := range cases {
		label, ok := parameter.EnumLabelFromWire(desc, c.in)
		if ok != c.wantOK || label != c.wantLabel {
			t.Errorf("EnumLabelFromWire(%#v) = (%q, %v), want (%q, %v)", c.in, label, ok, c.wantLabel, c.wantOK)
		}
	}
}

// TestEnumLabelKeepsItsNarrowReading holds the other half: [EnumLabel] must
// keep reading a numeric string as a label, because a data point whose
// firmware spells an ENUM out delivers exactly that. The two functions
// answer different callers and must not converge.
func TestEnumLabelKeepsItsNarrowReading(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{ValueList: []string{"A", "B", "C"}}
	if label, ok := parameter.EnumLabel(desc, "2"); !ok || label != "2" {
		t.Fatalf("EnumLabel(desc, \"2\") = (%q, %v), want (\"2\", true)", label, ok)
	}
	if label, ok := parameter.EnumLabelFromWire(desc, "2"); !ok || label != "C" {
		t.Fatalf("EnumLabelFromWire(desc, \"2\") = (%q, %v), want (\"C\", true)", label, ok)
	}
}
