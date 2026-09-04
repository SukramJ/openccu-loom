// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmAdpSysvarTypeCase is one SysVar.getAll entry as the CCU reports it: a
// declared TYPE token plus the raw VALUE payload of the same entry.
type hmAdpSysvarTypeCase struct {
	name     string
	declared string
	rawValue string
}

// TestHmAdpNumberSysvarIsAlwaysFloat pins the CCU's own type model: a NUMBER
// system variable is ivtFloat + istGeneric, always, so its kind may not be
// re-derived from the current value string.
//
// Firmware, verbatim: www/api/methods/sysvar/getall.tcl:43-48 computes the
// emitted TYPE from ValueSubType() alone — `if (sv.ValueSubType() == istGeneric)
// { sv_type = "NUMBER"; }` — and never emits ValueType(), so the payload we
// parse carries no int/float distinction at all. Every creation path pairs
// istGeneric with ivtFloat (www/rega/esp/system.fn:830-836,
// www/api/methods/sysvar/createfloat.tcl:21); the single ivtInteger pairing is
// with istEnum, i.e. LIST (system.fn:838-842). The WebUI classifies a variable
// as NUMBER only for `(iVT==ivtFloat) && (iST==istGeneric)` (system.fn:464).
func TestHmAdpNumberSysvarIsAlwaysFloat(t *testing.T) {
	t.Parallel()

	cases := []hmAdpSysvarTypeCase{
		{"integral value", "NUMBER", `"21"`},
		{"fractional value", "NUMBER", `"21.5"`},
		{"empty value", "NUMBER", `""`},
		{"lower case declaration", "number", `"0"`},
		{"integral value at .0", "NUMBER", `"21.0"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inferSysvarType(tc.declared, json.RawMessage(tc.rawValue))
			if got != hmenum.HubValueTypeFloat {
				t.Fatalf("inferSysvarType(%q, %s) = %s, want %s — the CCU has no integer NUMBER sysvar",
					tc.declared, tc.rawValue, got, hmenum.HubValueTypeFloat)
			}
		})
	}
}

// TestHmAdpSysvarTypeDoesNotDependOnTheValue is the stability half: the
// declared kind may not flip between two scans of the same variable because
// its live value crossed an integral boundary.
func TestHmAdpSysvarTypeDoesNotDependOnTheValue(t *testing.T) {
	t.Parallel()

	values := []string{`"21"`, `"21.5"`, `""`, `"-3"`, `"1e3"`}
	first := inferSysvarType("NUMBER", json.RawMessage(values[0]))
	for _, v := range values[1:] {
		if got := inferSysvarType("NUMBER", json.RawMessage(v)); got != first {
			t.Fatalf("inferSysvarType flipped with the live value: %s → %s, %s → %s",
				values[0], first, v, got)
		}
	}
}

// TestHmAdpNonNumberSysvarTypesAreUsedAsDeclared guards the negative control:
// the NUMBER rule must not swallow the other declared types.
func TestHmAdpNonNumberSysvarTypesAreUsedAsDeclared(t *testing.T) {
	t.Parallel()

	cases := map[string]hmenum.HubValueType{
		"LOGIC":  hmenum.HubValueTypeLogic,
		"ALARM":  hmenum.HubValueTypeAlarm,
		"LIST":   hmenum.HubValueTypeList,
		"STRING": hmenum.HubValueTypeString,
	}
	for declared, want := range cases {
		if got := inferSysvarType(declared, json.RawMessage(`"1"`)); got != want {
			t.Fatalf("inferSysvarType(%q) = %s, want %s", declared, got, want)
		}
	}
}
