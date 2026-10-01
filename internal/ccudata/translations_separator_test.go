// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import "testing"

// TestBuildValueIndicesSeparatorEdges pins the value taken after the last "=".
func TestBuildValueIndicesSeparatorEdges(t *testing.T) {
	t.Parallel()
	got := buildValueIndices(map[string]map[string]string{"en": {
		"dev|PARAM=ON": "On",
		"noequals":     "Skipped",
		"=LEAD":        "Lead",
		"trail=":       "Trail",
		"a=b=LAST":     "Last",
	}})["en"]
	want := map[string]string{"ON": "On", "LEAD": "Lead", "": "Trail", "LAST": "Last"}
	if len(got) != len(want) {
		t.Fatalf("buildValueIndices = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("buildValueIndices[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestLocaleSuffixSeparatorEdges pins the "_" split edges.
func TestLocaleSuffixSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"stringtable_de": "de",
		"plain":          "plain",
		"_de":            "_de",
		"key_":           "",
		"a_b_en":         "en",
	} {
		if got := localeSuffix(in); got != want {
			t.Errorf("localeSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
