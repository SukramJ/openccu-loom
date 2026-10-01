// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package taxonomy

import "testing"

// TestRefParentSeparatorEdges pins Parent's split at the last "/".
func TestRefParentSeparatorEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want Path
		ok       bool
	}{
		{"eg/wohnzimmer/ecke", "eg/wohnzimmer", true},
		{"eg", "", false},
		{"/eg", "", true},
		{"eg/", "eg", true},
	} {
		got, ok := Ref{Enum: "room", Path: tc.in}.Parent()
		want := Ref{}
		if tc.ok {
			want = Ref{Enum: "room", Path: tc.want}
		}
		if got != want || ok != tc.ok {
			t.Errorf("Parent(%q) = %v %v, want %v %v", tc.in, got, ok, want, tc.ok)
		}
	}
}
