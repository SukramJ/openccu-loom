// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import "testing"

// TestWKPPairIndexSeparatorEdges pins the ":" split edges.
func TestWKPPairIndexSeparatorEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"000A:1", 1, true},
		{"000A:16", 8, true},
		{"000A", 0, false},
		{":3", 2, true},
		{"000A:", 0, false},
		{"a:b:4", 2, true},
	} {
		got, ok := wkpPairIndex(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("wkpPairIndex(%q) = %d %v, want %d %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
