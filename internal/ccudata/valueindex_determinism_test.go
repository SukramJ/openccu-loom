// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import "testing"

// TestValueOnlyIndexIsDeterministicAcrossLoads pins that two labels of
// equal length resolve to the same one on every load.
//
// The index keeps the shortest label a value carries anywhere in the
// table, and the reference implementation leaves a tie to whichever entry
// the map yields first. In Go that is randomised per process, so the value
// "off" — which has both "Aus" and "aus" at three characters — produced a
// different enum label after a restart, with nothing in the data having
// changed. Measured before the fix over 40 loads of the embedded extract:
// 29 "Aus", 11 "aus". The label reaches operators through the SPA, the
// REST DTOs and the MQTT discovery payload.
//
// A single load cannot see this; the repetition is the test.
func TestValueOnlyIndexIsDeterministicAcrossLoads(t *testing.T) {
	t.Parallel()
	const loads = 40
	for _, value := range []string{"off", "on"} {
		seen := map[string]int{}
		for range loads {
			tr, err := LoadTranslationsEmbedded()
			if err != nil {
				t.Fatalf("LoadTranslationsEmbedded: %v", err)
			}
			seen[tr.valueIndexLookup("de", value)]++
		}
		if len(seen) != 1 {
			t.Errorf("value-only index for %q resolved to %v across %d loads — "+
				"the label an operator sees depends on map iteration order", value, seen, loads)
		}
	}
}

// TestValueOnlyIndexTieGoesToTheSmallerLabel records which side of a tie
// wins, so the rule is stated rather than inherited from whatever the last
// change happened to produce.
func TestValueOnlyIndexTieGoesToTheSmallerLabel(t *testing.T) {
	t.Parallel()
	idx := buildValueIndices(map[string]map[string]string{
		"de": {
			"alpha=x": "aus",
			"beta=x":  "Aus",
			"gamma=x": "Ausgeschaltet",
		},
	})
	if got := idx["de"]["x"]; got != "Aus" {
		t.Errorf("tie between equally short labels resolved to %q, want \"Aus\" — "+
			"shortest first, then the lexicographically smaller label", got)
	}
}
