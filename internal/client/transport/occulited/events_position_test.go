// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import "testing"

// TestParsePositionSeparatorEdges pins the "-" split edges of an event id.
func TestParsePositionSeparatorEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want position
	}{
		{"boot-12", position{raw: "boot-12", boot: "boot", seq: 12, ok: true}},
		{"boot", position{raw: "boot"}},
		{"-12", position{raw: "-12"}},
		{"boot-", position{raw: "boot-"}},
		{"a-b-3", position{raw: "a-b-3", boot: "a-b", seq: 3, ok: true}},
		{"boot-x", position{raw: "boot-x"}},
	} {
		if got := parsePosition(tc.in); got != tc.want {
			t.Errorf("parsePosition(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}
