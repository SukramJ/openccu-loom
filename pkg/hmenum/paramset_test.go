// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestParseParamsetKeyAcceptsOnlyTheThreeWireKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want ParamsetKey
		ok   bool
	}{
		{"MASTER", ParamsetKeyMaster, true},
		{"VALUES", ParamsetKeyValues, true},
		{"LINK", ParamsetKeyLink, true},
		{"", "", false},
		{"master", "", false},
		{" MASTER", "", false},
		{"SERVICE", "", false},
		{"CALCULATED", "", false},
		{"COMBINED", "", false},
		{"DUMMY", "", false},
		// A peer address is exactly what the daemon would misread a
		// stray key as; it must never parse.
		{"JEQ0123456:1", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseParamsetKey(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseParamsetKey(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
