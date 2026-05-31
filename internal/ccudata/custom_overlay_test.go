// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "testing"

// TestCustomOverlayMergesDEParameters verifies that the translation_custom
// files are actually overlaid. Picks a key that the curated overlay is
// guaranteed to contribute — `abs_luftfeuchte` is present in
// translation_custom/parameters_de.json and absent from the raw CCU
// stringtable.
func TestCustomOverlayMergesDEParameters(t *testing.T) {
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	// Key is present in translation_custom/parameters_de.json.
	got := tr.ParameterLabel("de", "", "abs_luftfeuchte")
	if got == "" {
		t.Fatalf("curated de parameter overlay missing")
	}
}

// TestEmbeddedProfilesCount guards against accidental archive trim:
func TestEmbeddedProfilesCount(t *testing.T) {
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	if len(store.Receivers) < 50 {
		t.Fatalf("unexpectedly few receiver profiles: %d", len(store.Receivers))
	}
	if len(store.Aliases) == 0 {
		t.Fatalf("aliases not loaded")
	}
}
