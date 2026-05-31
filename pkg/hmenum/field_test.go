// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

// TestFieldSpotChecks verifies that a representative set of Field constants
// Carry the exact lowercase wire values from the
func TestFieldSpotChecks(t *testing.T) {
	cases := []struct {
		name Field
		want string
	}{
		{FieldSetpoint, "setpoint"},
		{FieldLevel, "level"},
		{FieldDirection, "direction"},
		{FieldActiveProfile, "active_profile"},
		{FieldState, "state"},
		{FieldTemperature, "temperature"},
		{FieldHumidity, "humidity"},
		{FieldValveState, "valve_state"},
		{FieldLowBat, "low_bat"},
		{FieldWeekProgramPointer, "week_program_pointer"},
	}
	for _, c := range cases {
		t.Run(string(c.name), func(t *testing.T) {
			if string(c.name) != c.want {
				t.Fatalf("Field %q: got %q, want %q", c.name, string(c.name), c.want)
			}
		})
	}
}

// TestFieldDeviatingValues verifies the five constants where the
// enum name and the string value intentionally differ:
// COLOR_LEVEL="color_temp", ON_TIME_LIST="on_time_list_1",
// OPERATION_MODE="channel_operation_mode", SWITCH_V1="vswitch_1",
// SWITCH_V2="vswitch_2".
func TestFieldDeviatingValues(t *testing.T) {
	cases := []struct {
		goName string
		field  Field
		want   string
	}{
		{"FieldColorLevel", FieldColorLevel, "color_temp"},
		{"FieldOnTimeList", FieldOnTimeList, "on_time_list_1"},
		{"FieldOperationMode", FieldOperationMode, "channel_operation_mode"},
		{"FieldSwitchV1", FieldSwitchV1, "vswitch_1"},
		{"FieldSwitchV2", FieldSwitchV2, "vswitch_2"},
	}
	for _, c := range cases {
		t.Run(c.goName, func(t *testing.T) {
			if string(c.field) != c.want {
				t.Fatalf("%s: got %q, want %q", c.goName, string(c.field), c.want)
			}
		})
	}
}

// TestAllFieldsNonEmpty verifies that every Field constant returned by
// AllFields is non-empty — an empty string would be indistinguishable from a
// zero value and break map lookups.
func TestAllFieldsNonEmpty(t *testing.T) {
	fields := AllFields()
	if len(fields) == 0 {
		t.Fatal("AllFields returned empty slice")
	}
	for _, f := range fields {
		if string(f) == "" {
			t.Errorf("AllFields contains empty string entry")
		}
	}
}

// TestAllFieldsCount verifies that AllFields returns exactly 98 entries,
// Py, class Field
// commit reference: Wave-X Phase A.1).
func TestAllFieldsCount(t *testing.T) {
	const wantCount = 98
	got := len(AllFields())
	if got != wantCount {
		t.Fatalf("AllFields count = %d, want %d (aiohomematic parity)", got, wantCount)
	}
}

// TestAllFieldsUnique verifies that no two Field constants share the same
// wire value, which would cause silent aliasing when used as map keys.
func TestAllFieldsUnique(t *testing.T) {
	seen := make(map[string]Field, len(AllFields()))
	for _, f := range AllFields() {
		v := string(f)
		if prev, dup := seen[v]; dup {
			t.Errorf("duplicate wire value %q: used by %q and %q", v, prev, f)
		}
		seen[v] = f
	}
}

// TestAllFieldsLowercase verifies that every Field value is lowercase,
// lowercase strings, unlike Parameter which is UPPER_CASE).
func TestAllFieldsLowercase(t *testing.T) {
	for _, f := range AllFields() {
		v := string(f)
		for i := 0; i < len(v); i++ {
			c := v[i]
			if c >= 'A' && c <= 'Z' {
				t.Errorf("Field %q contains uppercase character %q at pos %d", v, c, i)
				break
			}
		}
	}
}
