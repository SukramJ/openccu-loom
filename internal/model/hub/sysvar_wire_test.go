// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// toWire must normalise the caller's value to the declared sysvar type
// before it reaches the wire dispatch: the writer routes bool to
// SysVar.setBool, numerics to SysVar.setFloat and strings to the
// string-only Rega script, so a type mismatch here silently picks the
// wrong wire method.

func sysvarOfType(vt hmenum.HubValueType, valueList ...string) *Sysvar {
	return &Sysvar{
		HubDataPoint: HubDataPoint{Name: "sv"},
		ValueType:    vt,
		ValueList:    valueList,
	}
}

func TestSysvarToWireLogicAndAlarm(t *testing.T) {
	for _, vt := range []hmenum.HubValueType{hmenum.HubValueTypeLogic, hmenum.HubValueTypeAlarm} {
		s := sysvarOfType(vt)
		ok := []struct {
			in   hmtypes.ParamValue
			want bool
		}{
			{hmtypes.BoolValue(true), true},
			{hmtypes.BoolValue(false), false},
			{hmtypes.StringValue("true"), true},
			{hmtypes.StringValue("TRUE"), true},
			{hmtypes.StringValue("on"), true},
			{hmtypes.StringValue("yes"), true},
			{hmtypes.StringValue("1"), true},
			{hmtypes.StringValue("false"), false},
			{hmtypes.StringValue("off"), false},
			{hmtypes.StringValue("no"), false},
			{hmtypes.StringValue("0"), false},
			{hmtypes.IntValue(1), true},
			{hmtypes.IntValue(0), false},
			{hmtypes.FloatValue(1), true},
			{hmtypes.FloatValue(0), false},
		}
		for _, tc := range ok {
			got, err := s.toWire(tc.in)
			if err != nil {
				t.Errorf("%s toWire(%v): %v", vt, tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("%s toWire(%v) = %v (%T), want %v", vt, tc.in, got, got, tc.want)
			}
		}
		for _, bad := range []hmtypes.ParamValue{
			hmtypes.StringValue("vielleicht"),
			hmtypes.IntValue(2),
			hmtypes.FloatValue(0.5),
			hmtypes.ListValue([]string{"a"}),
		} {
			if _, err := s.toWire(bad); err == nil {
				t.Errorf("%s toWire(%v): expected error", vt, bad)
			}
		}
	}
}

func TestSysvarToWireFloatAndNumber(t *testing.T) {
	for _, vt := range []hmenum.HubValueType{hmenum.HubValueTypeFloat, hmenum.HubValueTypeNumber} {
		s := sysvarOfType(vt)
		ok := []struct {
			in   hmtypes.ParamValue
			want float64
		}{
			{hmtypes.FloatValue(21.5), 21.5},
			{hmtypes.IntValue(42), 42},
			{hmtypes.StringValue("21.5"), 21.5},
			{hmtypes.StringValue("-3"), -3},
		}
		for _, tc := range ok {
			got, err := s.toWire(tc.in)
			if err != nil {
				t.Errorf("%s toWire(%v): %v", vt, tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("%s toWire(%v) = %v (%T), want %v", vt, tc.in, got, got, tc.want)
			}
		}
		for _, bad := range []hmtypes.ParamValue{
			hmtypes.StringValue("warm"),
			hmtypes.BoolValue(true),
			hmtypes.ListValue([]string{"a"}),
		} {
			if _, err := s.toWire(bad); err == nil {
				t.Errorf("%s toWire(%v): expected error", vt, bad)
			}
		}
	}
}

func TestSysvarToWireInteger(t *testing.T) {
	s := sysvarOfType(hmenum.HubValueTypeInteger)
	ok := []struct {
		in   hmtypes.ParamValue
		want int
	}{
		{hmtypes.IntValue(7), 7},
		{hmtypes.FloatValue(3), 3},
		{hmtypes.StringValue("5"), 5},
		{hmtypes.StringValue("-2"), -2},
	}
	for _, tc := range ok {
		got, err := s.toWire(tc.in)
		if err != nil {
			t.Errorf("toWire(%v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toWire(%v) = %v (%T), want %v", tc.in, got, got, tc.want)
		}
	}
	for _, bad := range []hmtypes.ParamValue{
		hmtypes.StringValue("5.5"),
		hmtypes.StringValue("viele"),
		hmtypes.FloatValue(3.7), // fractional part would be silently lost
		hmtypes.BoolValue(true),
		hmtypes.ListValue([]string{"a"}),
	} {
		if _, err := s.toWire(bad); err == nil {
			t.Errorf("toWire(%v): expected error", bad)
		}
	}
}

func TestSysvarToWireString(t *testing.T) {
	s := sysvarOfType(hmenum.HubValueTypeString)
	ok := []struct {
		in   hmtypes.ParamValue
		want string
	}{
		{hmtypes.StringValue("Fenster offen"), "Fenster offen"},
		{hmtypes.StringValue(""), ""},
		{hmtypes.IntValue(5), "5"},
		{hmtypes.FloatValue(21.5), "21.5"},
		{hmtypes.BoolValue(true), "true"},
	}
	for _, tc := range ok {
		got, err := s.toWire(tc.in)
		if err != nil {
			t.Errorf("toWire(%v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toWire(%v) = %v (%T), want %q", tc.in, got, got, tc.want)
		}
	}
	if _, err := s.toWire(hmtypes.ListValue([]string{"a"})); err == nil {
		t.Error("toWire(list): expected error for STRING sysvar")
	}
}

func TestSysvarToWireList(t *testing.T) {
	s := sysvarOfType(hmenum.HubValueTypeList, "Aus", "Niedrig", "Normal", "Hoch")
	ok := []struct {
		in   hmtypes.ParamValue
		want int
	}{
		{hmtypes.StringValue("Normal"), 2},
		{hmtypes.StringValue("Aus"), 0},
		{hmtypes.IntValue(3), 3},
		{hmtypes.FloatValue(1), 1},
	}
	for _, tc := range ok {
		got, err := s.toWire(tc.in)
		if err != nil {
			t.Errorf("toWire(%v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toWire(%v) = %v (%T), want %d", tc.in, got, got, tc.want)
		}
	}
	for _, bad := range []hmtypes.ParamValue{
		hmtypes.StringValue("Turbo"), // not in the value list
		hmtypes.IntValue(4),          // out of range
		hmtypes.IntValue(-1),
		hmtypes.BoolValue(true),
	} {
		if _, err := s.toWire(bad); err == nil {
			t.Errorf("toWire(%v): expected error", bad)
		}
	}
}

func TestSysvarToWireListWithoutValueList(t *testing.T) {
	// A LIST sysvar whose descriptor carries no labels still writes a
	// numeric index.
	s := sysvarOfType(hmenum.HubValueTypeList)
	got, err := s.toWire(hmtypes.IntValue(2))
	if err != nil {
		t.Fatalf("toWire(2): %v", err)
	}
	if got != 2 {
		t.Fatalf("toWire(2) = %v (%T), want 2", got, got)
	}
	if _, err := s.toWire(hmtypes.StringValue("Normal")); err == nil {
		t.Fatal("label without a value list cannot resolve — expected error")
	}
}

// capturingSysvarWriter records the wire value handed to the writer.
type capturingSysvarWriter struct {
	name  string
	value any
}

func (c *capturingSysvarWriter) SetSysvar(_ context.Context, name string, value any) error {
	c.name = name
	c.value = value
	return nil
}

// TestSysvarSetDeliversTypedWireValues locks the full Set path: the
// declared sysvar type — not the caller's payload shape — decides the
// Go type the writer receives, which in turn selects the wire method.
func TestSysvarSetDeliversTypedWireValues(t *testing.T) {
	cases := []struct {
		name      string
		valueType hmenum.HubValueType
		valueList []string
		in        hmtypes.ParamValue
		want      any
	}{
		{"logic from string", hmenum.HubValueTypeLogic, nil, hmtypes.StringValue("true"), true},
		{"alarm from bool", hmenum.HubValueTypeAlarm, nil, hmtypes.BoolValue(false), false},
		{"float from string", hmenum.HubValueTypeFloat, nil, hmtypes.StringValue("21.5"), 21.5},
		{"number from int", hmenum.HubValueTypeNumber, nil, hmtypes.IntValue(7), float64(7)},
		{"integer from string", hmenum.HubValueTypeInteger, nil, hmtypes.StringValue("5"), 5},
		{"list label to index", hmenum.HubValueTypeList, []string{"Aus", "Niedrig", "Normal", "Hoch"}, hmtypes.StringValue("Hoch"), 3},
		{"string from float", hmenum.HubValueTypeString, nil, hmtypes.FloatValue(1.5), "1.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &capturingSysvarWriter{}
			s := &Sysvar{
				HubDataPoint: HubDataPoint{Name: "sv"},
				ValueType:    tc.valueType,
				ValueList:    tc.valueList,
				Writer:       w,
			}
			if err := s.Set(context.Background(), tc.in); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if w.value != tc.want {
				t.Fatalf("writer received %v (%T), want %v (%T)", w.value, w.value, tc.want, tc.want)
			}
		})
	}
}
