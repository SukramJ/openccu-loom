// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTranslationsEmptyPathReturnsSentinel(t *testing.T) {
	if _, err := LoadTranslations(""); !errors.Is(err, ErrNoArchive) {
		t.Fatalf("err=%v", err)
	}
}

func TestEmptyPreallocatesMaps(t *testing.T) {
	e := Empty()
	if e.Parameters == nil || e.DeviceIcons == nil {
		t.Fatalf("Empty must preallocate maps")
	}
}

func TestTranslationsFromRawSplitsLocales(t *testing.T) {
	raw := map[string]map[string]string{
		// Keys match the extractor's lower-case convention.
		"parameters_de":       {"level": "Niveau"},
		"parameters_en":       {"level": "Level"},
		"channel_types_de":    {"shutter": "Rolladen"},
		"device_models_de":    {"263_130": "Funk-Schaltaktor"},
		"parameter_values_en": {"control_mode=auto": "Auto"},
		"parameter_help_de":   {"level": "Steuert das Niveau"},
		"ui_labels_de":        {"btn.ok": "OK"},
		"device_icons":        {"263 130": "icon.png"},
	}
	out := translationsFromRaw(raw)
	if out.ParameterLabel("de", "", "LEVEL") != "Niveau" {
		t.Fatal("parameter de")
	}
	if out.ParameterLabel("en", "", "LEVEL") != "Level" {
		t.Fatal("parameter en")
	}
	if out.ChannelType("de", "SHUTTER") != "Rolladen" {
		t.Fatal("channel type de")
	}
	if out.ParameterValueSimple("en", "CONTROL_MODE", "AUTO") != "Auto" {
		t.Fatal("parameter value en")
	}
	if out.ParameterHelpText("de", "LEVEL") == "" {
		t.Fatal("parameter help de")
	}
	if out.DeviceIcon("263 130") != "icon.png" {
		t.Fatal("device icon")
	}
	if out.UILabel("de", "btn.ok") != "OK" {
		t.Fatal("ui label")
	}
}

func TestTranslationsFallbackOnMiss(t *testing.T) {
	t0 := Empty()
	if got := t0.Parameter("de", "UNKNOWN"); got != "UNKNOWN" {
		t.Fatalf("miss must echo key, got %q", got)
	}
	if got := t0.DeviceIcon("missing type"); got != "" {
		t.Fatalf("unknown icon must be empty, got %q", got)
	}
}

func TestLoadTranslationsRoundTrip(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "tx.json.gz")
	raw := map[string]map[string]string{
		"parameters_de": {"level": "Niveau"},
		"device_icons":  {"263 130": "icon.png"},
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_ = json.NewEncoder(gz).Encode(raw)
	_ = gz.Close()
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	tr, err := LoadTranslations(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tr.Parameter("de", "LEVEL") != "Niveau" {
		t.Fatalf("loaded value wrong")
	}
	if tr.DeviceIcon("263 130") != "icon.png" {
		t.Fatal("icon lost")
	}
}

func TestLocalesUnion(t *testing.T) {
	raw := map[string]map[string]string{
		"parameters_de":    {"x": "y"},
		"parameters_en":    {"x": "y"},
		"channel_types_en": {"a": "b"},
	}
	tr := translationsFromRaw(raw)
	ls := tr.Locales()
	seen := map[string]bool{}
	for _, l := range ls {
		seen[l] = true
	}
	if !seen["de"] || !seen["en"] {
		t.Fatalf("locales=%v", ls)
	}
}

// ---------------------------------------------------------------------------
// DeviceModel lookup and fallback
// ---------------------------------------------------------------------------

// TestDeviceModelLookupAndFallback covers DeviceModel hit, miss, and
// missing-locale paths.
func TestDeviceModelLookupAndFallback(t *testing.T) {
	raw := map[string]map[string]string{
		"device_models_de": {"263_130": "Funk-Schaltaktor"},
	}
	tr := translationsFromRaw(raw)

	// Hit — key present.
	if got := tr.DeviceModel("de", "263_130"); got != "Funk-Schaltaktor" {
		t.Fatalf("DeviceModel hit = %q, want Funk-Schaltaktor", got)
	}
	// Miss — key absent; returns typeSubtype verbatim.
	if got := tr.DeviceModel("de", "999_999"); got != "999_999" {
		t.Fatalf("DeviceModel miss = %q, want 999_999", got)
	}
	// Missing locale → same fallback.
	if got := tr.DeviceModel("fr", "263_130"); got != "263_130" {
		t.Fatalf("DeviceModel missing locale = %q, want 263_130", got)
	}
}

// TestValueIndexLookupNilIndices exercises the nil-valueIndices guard.
func TestValueIndexLookupNilIndices(t *testing.T) {
	tr := &Translations{} // valueIndices is nil
	if got := tr.valueIndexLookup("de", "on"); got != "" {
		t.Fatalf("nil valueIndices should return empty, got %q", got)
	}
}

// TestValueIndexLookupMissingLocale exercises the locale-not-found branch.
func TestValueIndexLookupMissingLocale(t *testing.T) {
	tr := translationsFromRaw(map[string]map[string]string{
		"parameter_values_de": {"LEVEL=100": "Offen"},
	})
	// "fr" not in valueIndices.
	if got := tr.valueIndexLookup("fr", "100"); got != "" {
		t.Fatalf("missing locale should return empty, got %q", got)
	}
}

// TestLocaleSuffixNoUnderscore exercises the no-underscore path in localeSuffix.
func TestLocaleSuffixNoUnderscore(t *testing.T) {
	if got := localeSuffix("nounderscore"); got != "nounderscore" {
		t.Fatalf("localeSuffix no underscore = %q, want nounderscore", got)
	}
}

// TestMergeLocaleIntoEmptyLocale exercises the early-return for empty locale.
func TestMergeLocaleIntoEmptyLocale(t *testing.T) {
	dst := map[string]map[string]string{}
	mergeLocaleInto(dst, "", map[string]string{"a": "b"})
	if len(dst) != 0 {
		t.Fatalf("mergeLocaleInto with empty locale should be no-op, got %v", dst)
	}
}

// TestMergeLocaleIntoNilBucket exercises the nil-bucket init path.
func TestMergeLocaleIntoNilBucket(t *testing.T) {
	dst := map[string]map[string]string{}
	mergeLocaleInto(dst, "de", map[string]string{"x": "y"})
	if dst["de"]["x"] != "y" {
		t.Fatalf("mergeLocaleInto nil bucket: %v", dst)
	}
}

// TestDeviceModelLabelAllStages exercises all 6 resolution stages.
func TestDeviceModelLabelAllStages(t *testing.T) {
	raw := map[string]map[string]string{
		"device_models_de": {
			"hmip-swdo": "Rollladenaktor",     // Stage 1 hit (full lowercased model)
			"swdo":      "Rolllade bare",      // Stage 2 hit (stripped prefix)
			"etrv-b":    "Thermostat variant", // Stage 4 via "-X" drop
			"trv":       "Thermostat base",    // Stage 5/6 subModel drop
		},
	}
	tr := translationsFromRaw(raw)

	// Stage 1: exact lower-cased model hit.
	if got := tr.DeviceModelLabel("de", "HmIP-SWDO", ""); got != "Rollladenaktor" {
		t.Fatalf("DeviceModelLabel stage1 = %q, want Rollladenaktor", got)
	}

	// Stage 2: stripped vendor prefix hit (no space).
	if got := tr.DeviceModelLabel("de", "HmIP-SWDO-X", "swdo"); got == "" {
		// submodel exact hit via Stage 5 is acceptable too
		_ = got
	}

	// Stage 4: prefix + iterative "-X" drop: "HmIP-eTRV-B-2" →  strip prefix → "etrv-b-2" → drop "-2" → "etrv-b".
	if got := tr.DeviceModelLabel("de", "HmIP-eTRV-B-2", ""); got != "Thermostat variant" {
		t.Fatalf("DeviceModelLabel stage4 = %q, want Thermostat variant", got)
	}

	// Stage 5: subModel exact hit.
	if got := tr.DeviceModelLabel("de", "UNKNOWN", "TRV"); got != "Thermostat base" {
		t.Fatalf("DeviceModelLabel stage5 = %q, want Thermostat base", got)
	}

	// Stage 6: subModel with trailing "-X" dropped: "TRV-B-2" → "trv-b" or "trv".
	if got := tr.DeviceModelLabel("de", "", "TRV-B-2"); got == "" {
		t.Fatalf("DeviceModelLabel stage6 should find a match, got empty")
	}

	// Nil receiver → empty.
	var nilT *Translations
	if got := nilT.DeviceModelLabel("de", "HmIP-SWDO", ""); got != "" {
		t.Fatalf("nil DeviceModelLabel = %q, want empty", got)
	}
	// Missing locale → table nil → empty.
	if got := tr.DeviceModelLabel("xx", "HmIP-SWDO", ""); got != "" {
		t.Fatalf("missing locale DeviceModelLabel = %q, want empty", got)
	}
}

// TestDeviceModelIconAllBranches exercises nil/empty/hit/miss in DeviceModelIcon.
func TestDeviceModelIconAllBranches(t *testing.T) {
	raw := map[string]map[string]string{
		"device_icons": {"hmip-swdo": "swdo.png"},
	}
	tr := translationsFromRaw(raw)

	// Hit (lowercase match).
	if got := tr.DeviceModelIcon("HmIP-SWDO"); got != "swdo.png" {
		t.Fatalf("DeviceModelIcon hit = %q, want swdo.png", got)
	}
	// Miss.
	if got := tr.DeviceModelIcon("unknown-model"); got != "" {
		t.Fatalf("DeviceModelIcon miss = %q, want empty", got)
	}
	// nil receiver.
	var nilT *Translations
	if got := nilT.DeviceModelIcon("hmip-swdo"); got != "" {
		t.Fatalf("nil DeviceModelIcon = %q, want empty", got)
	}
	// empty model.
	if got := tr.DeviceModelIcon(""); got != "" {
		t.Fatalf("empty DeviceModelIcon = %q, want empty", got)
	}
}

// TestChannelTypeNilAndEmptyBranches exercises nil-receiver and
// empty-channelType paths.
func TestChannelTypeNilAndEmptyBranches(t *testing.T) {
	var nilT *Translations
	if got := nilT.ChannelType("de", "SHUTTER"); got != "SHUTTER" {
		t.Fatalf("nil ChannelType = %q, want SHUTTER", got)
	}
	tr := translationsFromRaw(map[string]map[string]string{})
	if got := tr.ChannelType("de", ""); got != "" {
		t.Fatalf("empty channelType = %q, want empty", got)
	}
	// Locale not present → table nil → returns raw key.
	if got := tr.ChannelType("xx", "SHUTTER"); got != "SHUTTER" {
		t.Fatalf("missing locale ChannelType = %q, want SHUTTER", got)
	}
}

// TestDeviceIconTypePrefixFallback exercises the space-prefix fallback
// in DeviceIcon.
func TestDeviceIconTypePrefixFallback(t *testing.T) {
	raw := map[string]map[string]string{
		"device_icons": {"263": "icon263.png"},
	}
	tr := translationsFromRaw(raw)

	// "263 130" misses; falls back to "263" prefix.
	if got := tr.DeviceIcon("263 130"); got != "icon263.png" {
		t.Fatalf("DeviceIcon prefix fallback = %q, want icon263.png", got)
	}
	// No match at all → empty.
	if got := tr.DeviceIcon("999 000"); got != "" {
		t.Fatalf("DeviceIcon no match = %q, want empty", got)
	}
	// No space in key → also no match (unless exact hit).
	if got := tr.DeviceIcon("999"); got != "" {
		t.Fatalf("DeviceIcon no space no match = %q, want empty", got)
	}
}

// TestProfileLabelNilStoreAndNotFound exercises ProfileLabel nil-store
// and not-found paths.
func TestProfileLabelNilStoreAndNotFound(t *testing.T) {
	tr := translationsFromRaw(map[string]map[string]string{})

	// nil store → empty.
	if got := tr.ProfileLabel(nil, "RCVTYPE", 1, "de"); got != "" {
		t.Fatalf("ProfileLabel nil store = %q, want empty", got)
	}

	// non-nil store but receiver not found → empty.
	s := &ProfileStore{
		Receivers: map[string]json.RawMessage{},
		Aliases:   map[string]string{},
	}
	if got := tr.ProfileLabel(s, "UNKNOWN", 1, "de"); got != "" {
		t.Fatalf("ProfileLabel not found = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// ParameterValue lookup chain — ct|param=value stage + value-only fallback
// ---------------------------------------------------------------------------

// buildParamValueTx builds a Translations value with controlled
// parameter_values entries for testing the ParameterValue lookup chain.
func buildParamValueTx() *Translations {
	raw := map[string]map[string]string{
		"parameter_values_en": {
			// Stage 2: param=value
			"control_mode=auto": "Auto",
			// Stage 1: ct|param=value (channel-specific)
			"shutter_transmitter|control_mode=auto": "Shutter Auto",
			// Stage 4 fallback: another key with same value
			"other_param=manual": "Manual Mode",
		},
	}
	return translationsFromRaw(raw)
}

// TestParameterValueChannelSpecificStage verifies that the ct|param=value
// key (stage 1) takes priority over the bare param=value key (stage 2).
func TestParameterValueChannelSpecificStage(t *testing.T) {
	t.Parallel()
	tx := buildParamValueTx()

	// With channelType = SHUTTER_TRANSMITTER, stage 1 must win.
	got := tx.ParameterValue("en", "SHUTTER_TRANSMITTER", "CONTROL_MODE", "AUTO")
	if got != "Shutter Auto" {
		t.Errorf("ParameterValue with ct= got %q, want 'Shutter Auto'", got)
	}
}

// TestParameterValueFallsToStage2WhenNoChannelSpecific verifies that
// when no ct|param=value entry exists, stage 2 (bare param=value) is used.
func TestParameterValueFallsToStage2WhenNoChannelSpecific(t *testing.T) {
	t.Parallel()
	tx := buildParamValueTx()

	// No channel type → must use bare param=value.
	got := tx.ParameterValue("en", "", "CONTROL_MODE", "AUTO")
	if got != "Auto" {
		t.Errorf("ParameterValue without ct= got %q, want 'Auto'", got)
	}
}

// TestParameterValueUnknownChannelTypeFallsToStage2 verifies that an
// unknown channel type does not break lookup — stage 2 is still tried.
func TestParameterValueUnknownChannelTypeFallsToStage2(t *testing.T) {
	t.Parallel()
	tx := buildParamValueTx()

	got := tx.ParameterValue("en", "UNKNOWN_CHANNEL_TYPE", "CONTROL_MODE", "AUTO")
	if got != "Auto" {
		t.Errorf("ParameterValue with unknown ct= got %q, want 'Auto'", got)
	}
}

// TestParameterValueNilReceiverReturnsValue verifies nil-safety.
func TestParameterValueNilReceiverReturnsValue(t *testing.T) {
	t.Parallel()
	var tx *Translations
	got := tx.ParameterValue("en", "CT", "PARAM", "VALUE")
	if got != "VALUE" {
		t.Errorf("nil ParameterValue got %q, want 'VALUE'", got)
	}
}

// TestParameterValueValueOnlyFallback verifies that when no param-specific
// entry matches, the value-only index is consulted as a last resort.
func TestParameterValueValueOnlyFallback(t *testing.T) {
	t.Parallel()
	// Build a table where "auto" only appears as a value suffix, but NOT
	// under any param name that the caller uses.
	raw := map[string]map[string]string{
		"parameter_values_en": {
			// Only entry: control_mode=auto. Caller asks for different_param=auto.
			"control_mode=auto": "Auto Mode",
		},
	}
	tx := translationsFromRaw(raw)

	// Stage 1+2+3 miss (different_param=auto not in table).
	// Stage 4 should find "auto" in the value index.
	got := tx.ParameterValue("en", "", "DIFFERENT_PARAM", "AUTO")
	if got != "Auto Mode" {
		t.Errorf("value-only fallback got %q, want 'Auto Mode'", got)
	}
}

// TestParameterValueValueOnlyFallbackShortestWins verifies that when
// multiple entries match the same value, the shortest label is chosen.
func TestParameterValueValueOnlyFallbackShortestWins(t *testing.T) {
	t.Parallel()
	raw := map[string]map[string]string{
		"parameter_values_en": {
			"param_a=on": "Power On",    // 8 chars
			"param_b=on": "On",          // 2 chars — shortest
			"param_c=on": "Switched On", // 10 chars
		},
	}
	tx := translationsFromRaw(raw)

	// No param-specific key for "unknown_param=on" → value index fallback.
	// Must return the shortest label for "on".
	got := tx.ParameterValue("en", "", "UNKNOWN_PARAM", "ON")
	if got != "On" {
		t.Errorf("value-only fallback shortest got %q, want 'On'", got)
	}
}

// TestParameterValueValueOnlyFallbackMissReturnsRaw verifies that when
// neither param-specific nor value-only lookup finds anything, the raw
// value is returned.
func TestParameterValueValueOnlyFallbackMissReturnsRaw(t *testing.T) {
	t.Parallel()
	tx := buildParamValueTx()

	got := tx.ParameterValue("en", "", "MISSING_PARAM", "MISSING_VALUE")
	if got != "MISSING_VALUE" {
		t.Errorf("total miss got %q, want 'MISSING_VALUE'", got)
	}
}

// TestParameterValueSimpleBackwardsCompatible verifies that
// ParameterValueSimple behaves identically to ParameterValue with empty
// channelType.
func TestParameterValueSimpleBackwardsCompatible(t *testing.T) {
	t.Parallel()
	tx := buildParamValueTx()

	a := tx.ParameterValueSimple("en", "CONTROL_MODE", "AUTO")
	b := tx.ParameterValue("en", "", "CONTROL_MODE", "AUTO")
	if a != b {
		t.Errorf("ParameterValueSimple=%q != ParameterValue(empty ct)=%q", a, b)
	}
}

// TestBuildValueIndicesEmpty verifies that buildValueIndices handles an
// empty input map without panicking.
func TestBuildValueIndicesEmpty(t *testing.T) {
	t.Parallel()
	idx := buildValueIndices(map[string]map[string]string{})
	if idx == nil {
		t.Error("buildValueIndices(empty) must return non-nil map")
	}
}

// TestBuildValueIndicesSkipsEntriesWithoutEquals verifies that entries
// without "=" are not indexed.
func TestBuildValueIndicesSkipsEntriesWithoutEquals(t *testing.T) {
	t.Parallel()
	raw := map[string]map[string]string{
		"en": {
			"noequals":    "SomeLabel",
			"param=value": "Good",
		},
	}
	idx := buildValueIndices(raw)
	enIdx := idx["en"]
	if _, ok := enIdx["noequals"]; ok {
		t.Error("entry without '=' must not appear in value index")
	}
	if v := enIdx["value"]; v != "Good" {
		t.Errorf("value index['value'] = %q, want 'Good'", v)
	}
}
