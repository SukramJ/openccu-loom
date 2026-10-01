// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import "testing"

// TestParseWireNameSeparatorEdges pins the "@" split edges.
func TestParseWireNameSeparatorEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, param string
		no        int
		exact     bool
	}{
		{"LEVEL@5", "LEVEL", 5, true},
		{"LEVEL", "LEVEL", 0, false},
		{"@5", "", 5, true},
		{"LEVEL@", "LEVEL@", 0, false},
		{"A@B@3", "A@B", 3, true},
		{"A@3@x", "A@3@x", 0, false},
	} {
		p, no, exact := ParseWireName(tc.in)
		if p != tc.param || no != tc.no || exact != tc.exact {
			t.Errorf("ParseWireName(%q) = %q %d %v", tc.in, p, no, exact)
		}
	}
}
