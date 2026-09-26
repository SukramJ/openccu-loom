// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestSystemTypeNormalizeMapsEmptyToCCU(t *testing.T) {
	t.Parallel()
	cases := map[SystemType]SystemType{
		"":                SystemTypeCCU,
		"  ":              SystemTypeCCU,
		"CCU":             SystemTypeCCU,
		" openccu-lite ":  SystemTypeOpenCCULite,
		"OpenCCU-Lite":    SystemTypeOpenCCULite,
		"auto":            SystemTypeAuto,
		"homegear":        "homegear",
		"Something Else ": "something else",
	}
	for in, want := range cases {
		if got := in.Normalize(); got != want {
			t.Errorf("SystemType(%q).Normalize() = %q, want %q", in, got, want)
		}
	}
}

func TestSystemTypeValid(t *testing.T) {
	t.Parallel()
	for _, ok := range []SystemType{"", "ccu", "auto", "openccu-lite", "OPENCCU-LITE"} {
		if !ok.Valid() {
			t.Errorf("SystemType(%q).Valid() = false, want true", ok)
		}
	}
	for _, bad := range []SystemType{"homegear", "lite", "ccu3"} {
		if bad.Valid() {
			t.Errorf("SystemType(%q).Valid() = true, want false", bad)
		}
	}
}
