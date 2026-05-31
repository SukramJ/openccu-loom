// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// translations_parity_test.go covers the i18n / CCU-translation parity
// Items drawn
// functions). All tests use the in-process translationsFromRaw helper so
// they run without the OCCU archive on disk.

package ccudata

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildTx builds a Translations value from inline raw data.
// Mirrors the raw archive shape so every test is self-contained.
func buildTx(raw map[string]map[string]string) *Translations {
	return translationsFromRaw(raw)
}

// minimalTx builds a Translations value that carries just enough data
// to exercise the common fallback paths without pulling the OCCU archive.
func minimalTx() *Translations {
	return buildTx(map[string]map[string]string{
		"parameters_de": {
			"on_level":            `Pegel im Zustand "ein"`,
			"level":               "Niveau",
			"low_bat":             "Batterie leer",
			"maintenance|low_bat": "Batterie",
			"rampon_time":         "Rampenzeit beim Einschalten",
		},
		"parameters_en": {
			"on_level":            "On level",
			"level":               "Level",
			"low_bat":             "Battery empty",
			"maintenance|low_bat": "Battery",
			"rampon_time":         "Ramp-on time",
		},
		"channel_types_de": {
			"dimmer":  "Dimmer",
			"shutter": "Rolladen",
		},
		"channel_types_en": {
			"dimmer":  "Dimmer",
			"shutter": "Shutter",
		},
		"device_models_de": {
			"hmip-swdo": "Rollladensteuerung",
			"ps":        "Steckdose",
		},
		"device_models_en": {
			"hmip-swdo": "Shutter actuator",
			"ps":        "Plug",
		},
		"parameter_values_en": {
			"control_mode=auto":           "Auto",
			"acoustic_alarm_active=true":  "Acoustic signal activated",
			"acoustic_alarm_active=false": "Acoustic signal deactivated",
			"action_type=jump_to_target":  "Jump to target",
		},
		"parameter_values_de": {
			"control_mode=auto":          "Automatik",
			"action_type=jump_to_target": "Zum Ziel springen",
		},
		"parameter_help_de": {
			"level": "Steuert das Niveau des Geräts",
		},
		"parameter_help_en": {
			"level": "Controls the device level",
		},
		"ui_labels_de": {"btn.ok": "OK", "btn.cancel": "Abbrechen"},
		"ui_labels_en": {"btn.ok": "OK", "btn.cancel": "Cancel"},
		"device_icons": {"263 130": "icon.png", "hmip-swdo": "swdo.png"},
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Locale fallback / normalisation
// ─────────────────────────────────────────────────────────────────────────────

// TestLocaleFallbackUnknownLocale mirrors
// test_ccu_translations.py::TestGetLocale::test_unsupported_locale_falls_back_to_english
// The Go API does NOT auto-normalise locales; callers are responsible.
// What we verify: the fallback is to return the raw key, not panic.
func TestParityLocaleUnknownReturnsKey(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ChannelType("fr", "DIMMER")
	if got != "DIMMER" {
		t.Fatalf("unknown locale must fall back to key, got %q", got)
	}
}

func TestParityLocaleDEAndENBothPresent(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	de := tx.Parameter("de", "LEVEL")
	en := tx.Parameter("en", "LEVEL")
	if de == "" || en == "" {
		t.Fatalf("expected non-empty for both locales: de=%q en=%q", de, en)
	}
	if de == en {
		t.Fatalf("de and en should differ for LEVEL, both got %q", de)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ChannelType — case-insensitive lookup
// ─────────────────────────────────────────────────────────────────────────────

// TestParityChannelTypeCaseInsensitive mirrors
// TestGetChannelTypeTranslation::test_case_insensitive
func TestParityChannelTypeCaseInsensitive(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	upper := tx.ChannelType("en", "DIMMER")
	lower := tx.ChannelType("en", "dimmer")
	if upper != lower {
		t.Fatalf("case-insensitive mismatch: %q vs %q", upper, lower)
	}
}

func TestParityChannelTypeKnown(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ChannelType("en", "DIMMER")
	if got == "DIMMER" {
		t.Fatalf("expected translation, not raw key, got %q", got)
	}
}

func TestParityChannelTypeUnknownReturnsKey(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ChannelType("en", "NONEXISTENT_TYPE_XYZ")
	if got != "NONEXISTENT_TYPE_XYZ" {
		t.Fatalf("unknown type must echo key, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceModelLabel — model + sub-model fallback
// ─────────────────────────────────────────────────────────────────────────────

// TestParityDeviceModelCaseInsensitive mirrors
// TestGetDeviceModelDescription::test_case_insensitive
func TestParityDeviceModelCaseInsensitive(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	upper := tx.DeviceModelLabel("en", "HmIP-SWDO", "")
	lower := tx.DeviceModelLabel("en", "hmip-swdo", "")
	if upper != lower {
		t.Fatalf("case mismatch: %q vs %q", upper, lower)
	}
}

func TestParityDeviceModelKnown(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.DeviceModelLabel("en", "HmIP-SWDO", "")
	if got == "" {
		t.Fatal("expected non-empty label for known model")
	}
}

// TestParityDeviceModelSubModelFallback mirrors
// TestGetDeviceModelDescription::test_sub_model_fallback
func TestParityDeviceModelSubModelFallback(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	// "PS" is registered under device_models_en as sub-model.
	got := tx.DeviceModelLabel("en", "NONEXISTENT_MODEL_XYZ", "PS")
	if got == "" {
		t.Fatal("expected sub-model fallback to return non-empty")
	}
}

// TestParityDeviceModelSubModelNotUsedWhenModelFound mirrors
// test_sub_model_not_used_when_model_found
func TestParityDeviceModelSubModelNotUsedWhenModelFound(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	withSub := tx.DeviceModelLabel("en", "HmIP-SWDO", "PS")
	withoutSub := tx.DeviceModelLabel("en", "HmIP-SWDO", "")
	if withSub != withoutSub {
		t.Fatalf("sub-model should be ignored when full model found: %q vs %q", withSub, withoutSub)
	}
}

func TestParityDeviceModelUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.DeviceModelLabel("en", "NONEXISTENT_MODEL_XYZ", "")
	if got != "" {
		t.Fatalf("unknown model must return empty, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ParameterLabel — channel-type override + fallback chain
// ─────────────────────────────────────────────────────────────────────────────

// TestParityParameterLabelCaseInsensitive mirrors
// TestGetParameterTranslation::test_case_insensitive
func TestParityParameterLabelCaseInsensitive(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	upper := tx.ParameterLabel("en", "", "ON_LEVEL")
	lower := tx.ParameterLabel("en", "", "on_level")
	if upper != lower {
		t.Fatalf("case-insensitive mismatch: %q vs %q", upper, lower)
	}
}

// TestParityParameterLabelChannelTypeOverridesGlobal mirrors
// test_channel_specific_overrides_global — channel-scoped entry wins.
func TestParityParameterLabelChannelTypeOverridesGlobal(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	global := tx.ParameterLabel("de", "", "LOW_BAT")
	channelSpecific := tx.ParameterLabel("de", "MAINTENANCE", "LOW_BAT")
	// maintenance|low_bat is "Batterie", bare low_bat is "Batterie leer"
	if channelSpecific == global {
		t.Fatalf("channel-specific should override global: channel=%q global=%q", channelSpecific, global)
	}
	if channelSpecific != "Batterie" {
		t.Fatalf("channel-specific label de expected 'Batterie', got %q", channelSpecific)
	}
}

// TestParityParameterLabelChannelTypeCaseInsensitive mirrors
// test_channel_type_case_insensitive
func TestParityParameterLabelChannelTypeCaseInsensitive(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	upper := tx.ParameterLabel("en", "MAINTENANCE", "LOW_BAT")
	lower := tx.ParameterLabel("en", "maintenance", "LOW_BAT")
	if upper != lower {
		t.Fatalf("channel type case-insensitive mismatch: %q vs %q", upper, lower)
	}
}

// TestParityParameterLabelMaintenanceLowBatDE mirrors
// test_maintenance_low_bat_custom_override
func TestParityParameterLabelMaintenanceLowBatDE(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	de := tx.ParameterLabel("de", "MAINTENANCE", "LOW_BAT")
	en := tx.ParameterLabel("en", "MAINTENANCE", "LOW_BAT")
	if de != "Batterie" {
		t.Fatalf("de LOW_BAT via MAINTENANCE: want 'Batterie', got %q", de)
	}
	if en != "Battery" {
		t.Fatalf("en LOW_BAT via MAINTENANCE: want 'Battery', got %q", en)
	}
}

func TestParityParameterLabelUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterLabel("en", "", "TOTALLY_UNKNOWN_PARAM_XYZ")
	if got != "" {
		t.Fatalf("unknown parameter must return empty, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LINK prefix stripping (SHORT_ / LONG_)
// ─────────────────────────────────────────────────────────────────────────────

// TestParityStripLinkPrefixShort mirrors _match_link_prefix::test_short_prefix
func TestParityStripLinkPrefixShort(t *testing.T) {
	t.Parallel()
	base, ok := stripLinkPrefix("SHORT_ON_LEVEL")
	if !ok || base != "ON_LEVEL" {
		t.Fatalf("stripLinkPrefix SHORT_ON_LEVEL: got (%q, %v)", base, ok)
	}
}

func TestParityStripLinkPrefixLong(t *testing.T) {
	t.Parallel()
	base, ok := stripLinkPrefix("LONG_RAMPON_TIME")
	if !ok || base != "RAMPON_TIME" {
		t.Fatalf("stripLinkPrefix LONG_RAMPON_TIME: got (%q, %v)", base, ok)
	}
}

func TestParityStripLinkPrefixNone(t *testing.T) {
	t.Parallel()
	_, ok := stripLinkPrefix("ON_LEVEL")
	if ok {
		t.Fatal("stripLinkPrefix must return ok=false for non-prefixed parameter")
	}
}

func TestParityStripLinkPrefixUnknown(t *testing.T) {
	t.Parallel()
	_, ok := stripLinkPrefix("MEDIUM_ON_LEVEL")
	if ok {
		t.Fatal("stripLinkPrefix must return ok=false for unrelated prefix")
	}
}

// TestParityLinkPrefixSuffixDE mirrors the German suffix table.
func TestParityLinkPrefixSuffixDE(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param, want string
	}{
		{"SHORT_X", "kurz"},
		{"LONG_X", "lang"},
	}
	for _, tc := range cases {
		got := linkPrefixSuffix(tc.param, "de")
		if got != tc.want {
			t.Errorf("linkPrefixSuffix(%q, de) = %q, want %q", tc.param, got, tc.want)
		}
	}
}

func TestParityLinkPrefixSuffixEN(t *testing.T) {
	t.Parallel()
	if got := linkPrefixSuffix("SHORT_X", "en"); got != "short" {
		t.Fatalf("linkPrefixSuffix SHORT en = %q, want short", got)
	}
	if got := linkPrefixSuffix("LONG_X", "en"); got != "long" {
		t.Fatalf("linkPrefixSuffix LONG en = %q, want long", got)
	}
}

// TestParityParameterLabelShortFallbackDE mirrors
// TestGetParameterTranslationLinkPrefix::test_short_suffix_fallback_de
func TestParityParameterLabelShortFallbackDE(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterLabel("de", "", "SHORT_ON_LEVEL")
	if !strings.HasSuffix(got, "(kurz)") {
		t.Fatalf("SHORT_ DE: expected suffix '(kurz)', got %q", got)
	}
}

// TestParityParameterLabelLongFallbackDE mirrors
// test_long_suffix_fallback_de
func TestParityParameterLabelLongFallbackDE(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterLabel("de", "", "LONG_ON_LEVEL")
	if !strings.HasSuffix(got, "(lang)") {
		t.Fatalf("LONG_ DE: expected suffix '(lang)', got %q", got)
	}
}

// TestParityParameterLabelShortFallbackEN mirrors
// test_short_suffix_fallback_en
func TestParityParameterLabelShortFallbackEN(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	base := tx.ParameterLabel("en", "", "ON_LEVEL")
	got := tx.ParameterLabel("en", "", "SHORT_ON_LEVEL")
	if !strings.HasSuffix(got, "(short)") {
		t.Fatalf("SHORT_ EN: expected suffix '(short)', got %q", got)
	}
	if base != "" && !strings.HasPrefix(got, base) {
		t.Fatalf("SHORT_ EN should contain base label %q, got %q", base, got)
	}
}

// TestParityParameterLabelShortAndLongDifferOnlySuffix mirrors
// test_short_and_long_differ_only_by_suffix
func TestParityParameterLabelShortAndLongDifferOnlySuffix(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	short := tx.ParameterLabel("de", "", "SHORT_ON_LEVEL")
	long := tx.ParameterLabel("de", "", "LONG_ON_LEVEL")
	if short == "" || long == "" {
		t.Fatalf("both SHORT_ and LONG_ must return non-empty; short=%q long=%q", short, long)
	}
	if short == long {
		t.Fatalf("SHORT_ and LONG_ must differ: both = %q", short)
	}
	if strings.Replace(short, "(kurz)", "(lang)", 1) != long {
		t.Fatalf("only suffix should differ: short=%q long=%q", short, long)
	}
}

func TestParityParameterLabelShortUnknownBaseReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterLabel("en", "", "SHORT_NONEXISTENT_XYZ")
	if got != "" {
		t.Fatalf("SHORT_ with unknown base must return empty, got %q", got)
	}
}

func TestParityParameterLabelLongUnknownBaseReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterLabel("en", "", "LONG_NONEXISTENT_XYZ")
	if got != "" {
		t.Fatalf("LONG_ with unknown base must return empty, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ParameterHelpText
// ─────────────────────────────────────────────────────────────────────────────

func TestParityParameterHelpTextKnown(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterHelpText("de", "LEVEL")
	if got == "" {
		t.Fatal("expected non-empty help text for known parameter")
	}
}

func TestParityParameterHelpTextUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterHelpText("de", "TOTALLY_UNKNOWN_XYZ")
	if got != "" {
		t.Fatalf("unknown param help must return empty, got %q", got)
	}
}

func TestParityParameterHelpTextLinkPrefixStripped(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	direct := tx.ParameterHelpText("de", "LEVEL")
	short := tx.ParameterHelpText("de", "SHORT_LEVEL")
	if direct == "" {
		t.Skip("no help text for LEVEL in test data — skip")
	}
	if short == "" {
		t.Fatalf("SHORT_LEVEL help should strip prefix and return same as LEVEL, got empty")
	}
	if short != direct {
		t.Fatalf("help text with/without prefix should match: direct=%q short=%q", direct, short)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ParameterValue — value translation + LINK prefix stripping
// ─────────────────────────────────────────────────────────────────────────────

func TestParityParameterValueDirectLookup(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterValue("en", "", "ACOUSTIC_ALARM_ACTIVE", "TRUE")
	if got != "Acoustic signal activated" {
		t.Fatalf("want 'Acoustic signal activated', got %q", got)
	}
}

func TestParityParameterValueLinkPrefixLong(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	direct := tx.ParameterValue("en", "", "ACOUSTIC_ALARM_ACTIVE", "FALSE")
	long := tx.ParameterValue("en", "", "LONG_ACOUSTIC_ALARM_ACTIVE", "FALSE")
	if direct == "FALSE" {
		t.Skip("acoustic alarm active not in test data; skip")
	}
	if long != direct {
		t.Fatalf("LONG_ prefix should strip: direct=%q long=%q", direct, long)
	}
}

func TestParityParameterValueLinkPrefixShort(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	direct := tx.ParameterValue("en", "", "ACOUSTIC_ALARM_ACTIVE", "TRUE")
	short := tx.ParameterValue("en", "", "SHORT_ACOUSTIC_ALARM_ACTIVE", "TRUE")
	if direct != "Acoustic signal activated" {
		t.Skip("acoustic alarm active not in test data; skip")
	}
	if short != direct {
		t.Fatalf("SHORT_ prefix should strip: direct=%q short=%q", direct, short)
	}
}

func TestParityParameterValueUnknownReturnsValue(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ParameterValue("en", "", "NONEXISTENT_PARAM_XYZ", "NONEXISTENT_VALUE_XYZ")
	if got != "NONEXISTENT_VALUE_XYZ" {
		t.Fatalf("unknown value must echo raw value, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceModelIcon
// ─────────────────────────────────────────────────────────────────────────────

func TestParityDeviceIconTypeSubtype(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.DeviceIcon("263 130")
	if got != "icon.png" {
		t.Fatalf("DeviceIcon('263 130') = %q, want icon.png", got)
	}
}

func TestParityDeviceIconModelKey(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	// DeviceModelIcon uses lowercase lookup.
	got := tx.DeviceModelIcon("hmip-swdo")
	if got != "swdo.png" {
		t.Fatalf("DeviceModelIcon('hmip-swdo') = %q, want swdo.png", got)
	}
}

func TestParityDeviceIconUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.DeviceIcon("999 999")
	if got != "" {
		t.Fatalf("unknown icon must return empty, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// UILabel / Stringtable
// ─────────────────────────────────────────────────────────────────────────────

func TestParityUILabelKnown(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	if got := tx.UILabel("de", "btn.cancel"); got != "Abbrechen" {
		t.Fatalf("UILabel de btn.cancel = %q, want Abbrechen", got)
	}
	if got := tx.UILabel("en", "btn.cancel"); got != "Cancel" {
		t.Fatalf("UILabel en btn.cancel = %q, want Cancel", got)
	}
}

func TestParityUILabelCaseInsensitive(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	lower := tx.UILabel("de", "btn.ok")
	upper := tx.UILabel("de", "BTN.OK")
	if lower != upper {
		t.Fatalf("UILabel case-insensitive: lower=%q upper=%q", lower, upper)
	}
}

func TestParityUILabelMissingReturnsKey(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.UILabel("de", "nonexistent.key")
	if got != "nonexistent.key" {
		t.Fatalf("missing UILabel must echo key, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ResolveChannelType — HmIP prefix resolver
// ─────────────────────────────────────────────────────────────────────────────

// TestParityResolveChannelTypeNonHmIP mirrors resolve_channel_type when
// isHmIP=false — the type must pass through unchanged.
func TestParityResolveChannelTypeNonHmIP(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	got := tx.ResolveChannelType("DIMMER", false)
	if got != "DIMMER" {
		t.Fatalf("non-HmIP type must be unchanged: %q", got)
	}
}

// TestParityResolveChannelTypeHmIPNoMatch verifies that when no
// CHANNEL_TYPE_HMIP key exists in the parameters table the original type
// is returned.
func TestParityResolveChannelTypeHmIPNoMatch(t *testing.T) {
	t.Parallel()
	tx := minimalTx() // no "_hmip|" keys in minimalTx
	got := tx.ResolveChannelType("DIMMER", true)
	if got != "DIMMER" {
		t.Fatalf("no _HMIP key => original must be returned, got %q", got)
	}
}

// TestParityResolveChannelTypeHmIPWithMatch verifies the _HMIP suffix
// lookup: when a "CHANNEL_TYPE_HMIP|param" entry exists, the candidate
// (CHANNEL_TYPE_HMIP) is returned.
func TestParityResolveChannelTypeHmIPWithMatch(t *testing.T) {
	t.Parallel()
	tx := buildTx(map[string]map[string]string{
		"parameters_en": {
			"shutter_contact_hmip|state": "State",
		},
	})
	got := tx.ResolveChannelType("SHUTTER_CONTACT", true)
	if got != "SHUTTER_CONTACT_HMIP" {
		t.Fatalf("HmIP resolve: want SHUTTER_CONTACT_HMIP, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Empty-Translations / nil-safety
// ─────────────────────────────────────────────────────────────────────────────

func TestParityEmptyTranslationsMapsAllocated(t *testing.T) {
	t.Parallel()
	e := Empty()
	if e.Parameters == nil || e.DeviceIcons == nil || e.UILabels == nil {
		t.Fatal("Empty() must preallocate all maps")
	}
}

func TestParityNilTranslationsDoNotPanic(t *testing.T) {
	t.Parallel()
	var tx *Translations
	// Only methods that have explicit nil-receiver guards in translations.go
	// are tested here (see `if t == nil` guards). Methods without guards
	// (DeviceModel, DeviceIcon, Parameter, Locales) panic on nil and are
	// therefore not tested with a nil receiver.
	_ = tx.ChannelType("de", "X")
	_ = tx.DeviceModelLabel("de", "X", "Y")
	_ = tx.DeviceModelIcon("X")
	_ = tx.ParameterLabel("de", "", "X")
	_ = tx.ParameterValue("de", "", "X", "Y")
	_ = tx.ParameterHelpText("de", "X")
	_ = tx.UILabel("de", "X")
	_ = tx.ResolveChannelType("X", true)
}

// ─────────────────────────────────────────────────────────────────────────────
// Custom-overlay / translationsFromRaw round-trip
// ─────────────────────────────────────────────────────────────────────────────

func TestParityTranslationsFromRawSplitsAllBuckets(t *testing.T) {
	t.Parallel()
	raw := map[string]map[string]string{
		"parameters_de":       {"level": "Niveau"},
		"parameters_en":       {"level": "Level"},
		"channel_types_de":    {"shutter": "Rolladen"},
		"device_models_de":    {"263_130": "Funk-Schaltaktor"},
		"parameter_values_en": {"control_mode=auto": "Auto"},
		"parameter_help_de":   {"level": "Steuert das Niveau"},
		"ui_labels_de":        {"btn.ok": "OK"},
		"device_icons":        {"263 130": "icon.png"},
	}
	tx := translationsFromRaw(raw)
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"ParameterLabel de", tx.ParameterLabel("de", "", "LEVEL"), "Niveau"},
		{"ParameterLabel en", tx.ParameterLabel("en", "", "LEVEL"), "Level"},
		{"ChannelType de", tx.ChannelType("de", "shutter"), "Rolladen"},
		{"ParameterValue en", tx.ParameterValue("en", "", "CONTROL_MODE", "AUTO"), "Auto"},
		{"ParameterHelpText de", tx.ParameterHelpText("de", "LEVEL"), "Steuert das Niveau"},
		{"UILabel de", tx.UILabel("de", "btn.ok"), "OK"},
		{"DeviceIcon", tx.DeviceIcon("263 130"), "icon.png"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestParityLocalesUnionCoversAllBuckets(t *testing.T) {
	t.Parallel()
	tx := minimalTx()
	ls := tx.Locales()
	seen := map[string]bool{}
	for _, l := range ls {
		seen[l] = true
	}
	if !seen["de"] {
		t.Error("Locales() must include 'de'")
	}
	if !seen["en"] {
		t.Error("Locales() must include 'en'")
	}
}
