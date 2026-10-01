// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import "testing"

// TestDeviceAddressSeparatorEdges pins the ":" split edges.
func TestDeviceAddressSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"000A:1": "000A",
		"000A":   "000A",
		":1":     "",
		"000A:":  "000A",
		"A:B:1":  "A:B",
	} {
		if got := deviceAddress(in); got != want {
			t.Errorf("deviceAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
