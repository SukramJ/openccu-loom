// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package build

import "testing"

// TestIsAddonInstallPath pins the runtime add-on detection rule: only
// executables under the add-on install prefix count. The rule keeps
// released tarballs honest — they package prebuilt standalone binaries
// that never carry the build-time stamp.
func TestIsAddonInstallPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/usr/local/addons/openccu-loom/openccu-loom.arm64", true},
		{"/usr/local/addons/openccu-loom/bin/openccu-loom", true},
		{"/usr/local/addons/openccu-loomX/openccu-loom", false},
		{"/usr/local/addons/other/openccu-loom", false},
		{"/usr/bin/openccu-loom", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isAddonInstallPath(tc.path); got != tc.want {
			t.Errorf("isAddonInstallPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
