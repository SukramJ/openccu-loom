// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParseParamsetKeyForwardsOnlyChannelWireKeys pins the key gate shared
// by read_paramset and write_paramset: normalised MASTER/VALUES resolve,
// LINK and every non-wire string are refused, so no free-form key can reach
// the CCU.
func TestParseParamsetKeyForwardsOnlyChannelWireKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want hmenum.ParamsetKey
		ok   bool
	}{
		{"MASTER", hmenum.ParamsetKeyMaster, true},
		{" master ", hmenum.ParamsetKeyMaster, true},
		{"values", hmenum.ParamsetKeyValues, true},
		{"LINK", "", false},
		{"", "", false},
		{"SERVICE", "", false},
		{"JEQ0123456:1", "", false},
	}
	for _, tc := range cases {
		got, ok := parseParamsetKey(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseParamsetKey(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
