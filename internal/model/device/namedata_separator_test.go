// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import "testing"

// TestStripChannelAddressSuffixSeparatorEdges pins the ":" split edges.
func TestStripChannelAddressSuffixSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Name:3":   "Name",
		"Name":     "Name",
		":3":       ":3",
		"Name:":    "Name:",
		"a:b:3":    "a:b",
		"Name:abc": "Name:abc",
	} {
		if got := stripChannelAddressSuffix(in); got != want {
			t.Errorf("stripChannelAddressSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
