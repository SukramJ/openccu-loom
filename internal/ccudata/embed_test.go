// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "testing"

func TestLoadTranslationsEmbeddedPopulatesLocales(t *testing.T) {
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	locales := tr.Locales()
	if len(locales) == 0 {
		t.Fatalf("embedded translations carry no locales")
	}
	// Sanity-check the two first-tier locales we produce in
	wantAny := map[string]bool{"de": false, "en": false}
	for _, l := range locales {
		if _, ok := wantAny[l]; ok {
			wantAny[l] = true
		}
	}
	for l, seen := range wantAny {
		if !seen {
			t.Errorf("embedded translations missing locale %q", l)
		}
	}
	// Sanity-check that at least one device-model translation is
	// present. The CCU extract uses numeric `type_subtype` keys
	// (e.g. "263_130") as well as string-form model IDs; which one
	// gets populated depends on the OCCU firmware. We only assert
	// "non-empty" here — pinning a specific key would drift with
	// every extractor refresh.
	if len(tr.DeviceModels["de"]) == 0 {
		t.Error("embedded translations carry no device_models_de entries")
	}
}

func TestLoadEasymodeEmbeddedDecodes(t *testing.T) {
	em, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}
	if em == nil || (len(em.ChannelMetadata) == 0 && len(em.OptionPresets) == 0) {
		t.Fatal("embedded easymode archive is empty")
	}
}

func TestLoadProfilesEmbeddedPopulates(t *testing.T) {
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	if len(store.Receivers) == 0 {
		t.Fatal("embedded profiles carry no receivers")
	}
	// Aliases table is optional per archive, but
	// ships it — flag its absence so we notice regressions.
	if len(store.Aliases) == 0 {
		t.Error("embedded profiles carry no aliases (expected _receiver_type_aliases.json)")
	}
	// Sanity: one alias should resolve to a known receiver.
	for src, dst := range store.Aliases {
		if _, ok := store.Receivers[dst]; !ok {
			t.Errorf("alias %s → %s targets missing receiver", src, dst)
		}
		break
	}
}
