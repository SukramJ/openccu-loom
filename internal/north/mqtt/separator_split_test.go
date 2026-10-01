// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// TestChannelNameIsBareAddressNoSeparatorEdges pins the ":" split edges.
func TestChannelNameIsBareAddressNoSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"KearneyIP:2": true,
		"KearneyIP":   false,
		":2":          true,
		"KearneyIP:":  false,
		"a:b:2":       false,
		"Kearney:x":   false,
	} {
		if got := channelNameIsBareAddressNo(in); got != want {
			t.Errorf("channelNameIsBareAddressNo(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestRetiredMetricSpellingSeparatorEdges pins the leaf taken after the
// last "/" of the live topic.
func TestRetiredMetricSpellingSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"b/c/system/health_score": "base/ccu/system/health_score",
		"noslash":                 "",
		"/leaf":                   "base/ccu/system/leaf",
		"a/":                      "base/ccu/system/",
	} {
		if got := retiredMetricSpelling("base", "CCU", in); got != want {
			t.Errorf("retiredMetricSpelling(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHubPlaneNodeIDSeparatorEdges pins the leaf taken after the last "_".
func TestHubPlaneNodeIDSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"ccu_wohnung_sysvars": true,
		"sysvars":             false,
		"_sysvars":            true,
		"ccu_":                false,
		"ccu_sysvars_x":       false,
	} {
		if got := hubPlaneNodeID(in); got != want {
			t.Errorf("hubPlaneNodeID(%q) = %v, want %v", in, got, want)
		}
	}
}
