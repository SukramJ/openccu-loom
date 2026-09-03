// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmAdpMarkerPredicateIsOneRule couples the two live answers to a single
// question — does this description carry marker M. Both run on the same
// rawDesc in the same function body (upsertSysvar): parseSysvarDescription
// decides IsExtended, hubEnabledDefault → markerMatch decides EnabledDefault.
//
// A prefix test on one side and a substring test on the other produced one
// sysvar in its extended, writable shape and simultaneously disabled by
// default, from the same string.
func TestHmAdpMarkerPredicateIsOneRule(t *testing.T) {
	t.Parallel()

	markers := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}
	descs := []string{
		"HAHM Heizung",
		"Heizung HAHM",
		"Heizung HAHM Nord",
		"#HAHM# External",
		"  HAHM  ",
		"Heizung",
		"",
	}
	for _, desc := range descs {
		_, isExtended := parseSysvarDescription(desc)
		enabled := hubEnabledDefault(false, desc, markers)
		if isExtended != enabled {
			t.Fatalf("description %q: parseSysvarDescription says carries-HAHM=%v, markerMatch says %v — one string, two answers",
				desc, isExtended, enabled)
		}
	}
}

// TestHmAdpMarkerMatchIsASubstringTest states the predicate directly. The
// marker convention has no firmware source — no CCU script defines it, so the
// authority is the reference stack the doc comment on markerMatch invokes, and
// that stack matches by substring: its element_matches_key is called with
// do_left_wildcard_search=True while do_right_wildcard_search defaults to
// True, which reduces to a plain containment test, and its own fixture is
// "#HAHM# External" — a description in which the marker is deliberately not a
// prefix.
func TestHmAdpMarkerMatchIsASubstringTest(t *testing.T) {
	t.Parallel()

	hahm := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}
	both := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM, hmenum.DescriptionMarkerMQTT}
	for _, tc := range []struct {
		desc    string
		markers []hmenum.DescriptionMarker
		want    bool
	}{
		{"anything", nil, true},
		{"HAHM kitchen", hahm, true},
		{"kitchen HAHM", hahm, true},
		{"#HAHM# External", hahm, true},
		{"kitchen", hahm, false},
		{"plain", both, false},
		{"light MQTT", both, true},
	} {
		if got := markerMatch(tc.desc, tc.markers); got != tc.want {
			t.Fatalf("markerMatch(%q, %v) = %v, want %v", tc.desc, tc.markers, got, tc.want)
		}
	}
}
