// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestCustomOverlayFillsCoolingProfileGaps pins the curated gap-fills the
// CCU stringtable extract lacks: the cooling-mode counterparts of the
// heating-profile parameters on climate transceivers (HmIP-BWTH and
// friends) and the wired-operation toggle on the MAINTENANCE channel.
// Without these entries the parameter editor falls back to the raw
// UPPER_SNAKE identifier. Both locales must resolve — a one-sided entry
// renders untranslated in the other language.
func TestCustomOverlayFillsCoolingProfileGaps(t *testing.T) {
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	cases := []struct {
		channelType string
		parameter   string
	}{
		{"HEATING_CLIMATECONTROL_TRANSCEIVER", "TEMPERATURE_COMFORT_COOLING"},
		{"MAINTENANCE", "SUPPORTING_WIRED_OPERATION_MODE"},
	}
	for _, tc := range cases {
		for _, locale := range []string{"de", "en"} {
			if got := tr.ParameterLabel(locale, tc.channelType, tc.parameter); got == "" {
				t.Errorf("ParameterLabel(%s, %s, %s) = empty, want curated label", locale, tc.channelType, tc.parameter)
			}
		}
	}
}

// TestCustomOverlayFeedsValueOnlyFallback pins the last lookup stage of
// [Translations.ParameterValue] against the curated overlay. `SOUNDFILE_072`
// carries a label only in translation_custom/parameter_values_<locale>.json;
// the raw stringtable extract has no entry for that value under any
// parameter. A parameter that is itself absent from both tables therefore
// misses stages 1-3 and can only be served by the value-only reverse index.
// When that index is not rebuilt after the overlay merge, curated
// gap-fills stay invisible and the operator sees the raw value.
func TestCustomOverlayFeedsValueOnlyFallback(t *testing.T) {
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	cases := []struct {
		locale string
		want   string
	}{
		{"de", "Sounddatei 72"},
		{"en", "Sound file 72"},
	}
	for _, tc := range cases {
		got := tr.ParameterValue(tc.locale, "", "PARAMETER_WITHOUT_VALUE_TABLE", "SOUNDFILE_072")
		if got != tc.want {
			t.Errorf("ParameterValue(%s, value-only fallback) = %q, want %q", tc.locale, got, tc.want)
		}
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
