// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package safety

import "testing"

// TestActiveFromRawEmptySelectionIsTheDefaultRule is the compatibility
// guarantee that lets an active-value selection ship without migrating
// any existing enrolment: with no labels the rule reproduces the default
// normalization exactly, value for value, and reports the verdict as
// applied rather than as a fallback — an unconfigured rule that logged
// itself as unresolved would warn about nearly every sensor in the
// installation.
func TestActiveFromRawEmptySelectionIsTheDefaultRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		raw        any
		wantActive bool
		wantKnown  bool
	}{
		{"bool true passes through", true, true, true},
		{"bool false passes through", false, false, true},
		{"int zero is inactive", 0, false, true},
		{"int non-zero is active", 7, true, true},
		{"int32 non-zero is active", int32(3), true, true},
		{"int64 zero is inactive", int64(0), false, true},
		{"float64 zero is inactive", 0.0, false, true},
		{"float64 non-zero is active", 1.5, true, true},
		{"nil is unknown", nil, false, false},
		{"string is unknown", "IDLE_OFF", false, false},
		{"unsupported struct type is unknown", struct{}{}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotActive, gotKnown, res := ActiveFromRaw(nil, tc.raw, nil)
			if res != ActivationApplied {
				t.Fatalf("resolution = %v, want ActivationApplied for an empty selection", res)
			}
			if gotActive != tc.wantActive || gotKnown != tc.wantKnown {
				t.Errorf("ActiveFromRaw(nil, %#v, nil) = (%v, %v), want (%v, %v)",
					tc.raw, gotActive, gotKnown, tc.wantActive, tc.wantKnown)
			}
		})
	}
}

// TestActiveFromRawSmokeDetectorAlarmStatusIndexes is the load-bearing
// case the active-value narrowing exists for: SMOKE_DETECTOR_ALARM_STATUS's
// value list is [IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM,
// SECONDARY_ALARM], and a naive "index != 0" rule would misclassify
// index 2 — the installation's own intrusion-siren command — as a fire.
func TestActiveFromRawSmokeDetectorAlarmStatusIndexes(t *testing.T) {
	t.Parallel()

	labels := []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

	cases := []struct {
		idx  int
		want bool
	}{
		{0, false}, // IDLE_OFF: no detection.
		{1, true},  // PRIMARY_ALARM: a real smoke detection.
		{2, false}, // INTRUSION_ALARM: the daemon's own siren command.
		{3, true},  // SECONDARY_ALARM: a real smoke detection.
	}
	for _, tc := range cases {
		t.Run(valueList[tc.idx], func(t *testing.T) {
			t.Parallel()

			active, known, res := ActiveFromRaw(labels, tc.idx, valueList)
			if !known || res != ActivationApplied {
				t.Fatalf("known=%v resolution=%v, want true/ActivationApplied", known, res)
			}
			if active != tc.want {
				t.Errorf("index %d (%s): active = %v, want %v", tc.idx, valueList[tc.idx], active, tc.want)
			}
		})
	}
}

// TestActiveFromRawNarrowsInt32AsWell pins the integer narrowing the
// restore and index-seed paths depend on: the model reads an enumeration
// back as int32, so an arm that only accepted int would resolve every
// restored enumeration through the default rule instead.
func TestActiveFromRawNarrowsInt32AsWell(t *testing.T) {
	t.Parallel()

	labels := []string{"PRIMARY_ALARM"}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM"}

	for _, raw := range []any{int32(2), int64(2), 2} {
		active, known, res := ActiveFromRaw(labels, raw, valueList)
		if active || !known || res != ActivationApplied {
			t.Errorf("ActiveFromRaw(labels, %#v, list) = (%v,%v,%v), want (false,true,ActivationApplied)",
				raw, active, known, res)
		}
	}
}

// TestActiveFromRawStringLabelNeedsNoValueList verifies a value arriving
// as its label reaches the index verdicts without a value list at all.
func TestActiveFromRawStringLabelNeedsNoValueList(t *testing.T) {
	t.Parallel()

	labels := []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}
	cases := map[string]bool{
		"IDLE_OFF":        false,
		"PRIMARY_ALARM":   true,
		"INTRUSION_ALARM": false,
		"SECONDARY_ALARM": true,
		// A label differing only in case is a different string: a value
		// list is a fixed vocabulary, and a case-insensitive match would
		// silently accept a label the device never emits.
		"primary_alarm": false,
	}
	for label, want := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			active, known, res := ActiveFromRaw(labels, label, nil)
			if !known || res != ActivationApplied {
				t.Fatalf("known=%v resolution=%v, want true/ActivationApplied", known, res)
			}
			if active != want {
				t.Errorf("label %q: active = %v, want %v", label, active, want)
			}
		})
	}
}

// TestActiveFromRawFallsBackWhenTheValueCannotBeMapped verifies that a
// configured selection which cannot be applied — no value list to
// resolve an index against, or a value of a shape no enumeration takes —
// falls back to the default rule and reports ActivationNoValueList, so
// the caller can say so.
func TestActiveFromRawFallsBackWhenTheValueCannotBeMapped(t *testing.T) {
	t.Parallel()

	labels := []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

	cases := []struct {
		name      string
		raw       any
		valueList []string
	}{
		{"missing value list", 2, nil},
		{"empty value list", 0, []string{}},
		{"bool value against an enumerated selection", true, valueList},
		{"float index is not narrowed", 2.0, valueList},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			active, known, res := ActiveFromRaw(labels, tc.raw, tc.valueList)
			if res != ActivationNoValueList {
				t.Errorf("resolution = %v, want ActivationNoValueList", res)
			}
			wantActive, wantKnown := normalizeActive(tc.raw)
			if active != wantActive || known != wantKnown {
				t.Errorf("fallback verdict = (%v,%v), want the default rule's (%v,%v)",
					active, known, wantActive, wantKnown)
			}
		})
	}
}

// TestActiveFromRawIndexOutsideValueListIsInactive is the invariant the
// package documents: a declared value list is exhaustive, so an index it
// does not cover — a firmware that added a value, a list not hydrated on
// this channel — is inactive rather than "not zero, so active". The
// verdict is reported as unresolved so the caller can surface the value
// it did not recognise: inactive, and loud about it.
func TestActiveFromRawIndexOutsideValueListIsInactive(t *testing.T) {
	t.Parallel()

	labels := []string{"PRIMARY_ALARM"}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM"}

	for _, raw := range []any{5, -1, int32(9)} {
		active, known, res := ActiveFromRaw(labels, raw, valueList)
		if active || !known || res != ActivationIndexOutOfRange {
			t.Errorf("ActiveFromRaw(labels, %#v, list) = (%v,%v,%v), want (false,true,ActivationIndexOutOfRange)",
				raw, active, known, res)
		}
	}
}
