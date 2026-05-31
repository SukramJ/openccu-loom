// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"
)

// fakeTranslationProvider is a minimal [TranslationProvider] +
// [ParameterHelpProvider] for label tests.
type fakeTranslationProvider struct {
	translations map[string]string // "<param>/<channelType>/<locale>" → label
	helpTexts    map[string]string // "<locale>/<param>" → help text
}

func (f *fakeTranslationProvider) ParameterTranslation(param, channelType, locale string) string {
	key := param + "/" + channelType + "/" + locale
	return f.translations[key]
}

func (f *fakeTranslationProvider) ParameterHelpText(locale, param string) string {
	return f.helpTexts[locale+"/"+param]
}

// TestHasTranslationNilResolver verifies that HasTranslation on a nil
// resolver always returns false and does not panic.
func TestHasTranslationNilResolver(t *testing.T) {
	t.Parallel()

	var r *LabelResolver
	if r.HasTranslation("TEMPERATURE_OFFSET", "CLIMATE_TRANSCEIVER") {
		t.Fatal("nil resolver should not report HasTranslation=true")
	}
}

// TestHasTranslationNilProvider verifies that HasTranslation returns
// false when the provider is nil.
func TestHasTranslationNilProvider(t *testing.T) {
	t.Parallel()

	r := NewLabelResolver(nil, "en")
	if r.HasTranslation("TEMPERATURE_OFFSET", "CLIMATE_TRANSCEIVER") {
		t.Fatal("nil provider should not report HasTranslation=true")
	}
}

// TestHasTranslationChannelSpecific exercises the channel-specific
// lookup path (most specific wins) and the parameter-only fallback.
func TestHasTranslationChannelSpecific(t *testing.T) {
	t.Parallel()

	p := &fakeTranslationProvider{
		translations: map[string]string{
			"TEMPERATURE_OFFSET/CLIMATE_TRANSCEIVER/en": "Temperature Offset",
			"BOOST_MODE//en": "Boost",
		},
	}
	r := NewLabelResolver(p, "en")

	// Channel-specific entry exists.
	if !r.HasTranslation("TEMPERATURE_OFFSET", "CLIMATE_TRANSCEIVER") {
		t.Fatal("expected HasTranslation=true for channel-specific entry")
	}
	// Parameter-only fallback (no channel-type key, but base key exists).
	if !r.HasTranslation("BOOST_MODE", "") {
		t.Fatal("expected HasTranslation=true for parameter-only entry")
	}
	// Completely absent parameter.
	if r.HasTranslation("UNKNOWN_PARAM", "CLIMATE_TRANSCEIVER") {
		t.Fatal("expected HasTranslation=false for absent parameter")
	}
}

// TestHelpTextNilProvider exercises the HelpText method on a resolver
// whose provider does not implement ParameterHelpProvider.
func TestHelpTextNilProvider(t *testing.T) {
	t.Parallel()

	r := NewLabelResolver(nil, "en")
	if got := r.HelpText("TEMPERATURE_OFFSET"); got != "" {
		t.Fatalf("HelpText on nil provider = %q, want empty", got)
	}
}

// TestHelpTextNoHelpProvider checks that HelpText returns "" when the
// provider doesn't implement ParameterHelpProvider.
type noHelpProvider struct{}

func (noHelpProvider) ParameterTranslation(_, _, _ string) string { return "" }

func TestHelpTextNoHelpProvider(t *testing.T) {
	t.Parallel()

	r := NewLabelResolver(noHelpProvider{}, "en")
	if got := r.HelpText("TEMPERATURE_OFFSET"); got != "" {
		t.Fatalf("HelpText without ParameterHelpProvider = %q, want empty", got)
	}
}

// TestHelpTextWithProvider exercises the happy path where the provider
// implements ParameterHelpProvider and has a registered help text.
func TestHelpTextWithProvider(t *testing.T) {
	t.Parallel()

	p := &fakeTranslationProvider{
		helpTexts: map[string]string{
			"en/TEMPERATURE_OFFSET": "Offset applied to measured temperature.",
		},
	}
	r := NewLabelResolver(p, "en")

	got := r.HelpText("TEMPERATURE_OFFSET")
	if got != "Offset applied to measured temperature." {
		t.Fatalf("HelpText = %q, want help text", got)
	}
	// Parameter without help text returns empty.
	if got2 := r.HelpText("UNKNOWN"); got2 != "" {
		t.Fatalf("HelpText for unknown = %q, want empty", got2)
	}
}

// TestHelpTextNilResolver ensures HelpText on a nil resolver is safe.
func TestHelpTextNilResolver(t *testing.T) {
	t.Parallel()

	var r *LabelResolver
	if got := r.HelpText("TEMPERATURE_OFFSET"); got != "" {
		t.Fatalf("HelpText on nil resolver = %q, want empty", got)
	}
}

// TestTitleCase exercises the unexported titleCase helper for the
// empty-string and normal paths.
func TestTitleCase(t *testing.T) {
	t.Parallel()

	if got := titleCase(""); got != "" {
		t.Fatalf("titleCase(\"\") = %q, want empty", got)
	}
	if got := titleCase("hello"); got != "Hello" {
		t.Fatalf("titleCase(\"hello\") = %q, want Hello", got)
	}
}

// TestHumanizeEmpty verifies the empty-string short-circuit.
func TestHumanizeEmpty(t *testing.T) {
	t.Parallel()

	if got := Humanize(""); got != "" {
		t.Fatalf("Humanize(\"\") = %q, want empty", got)
	}
}

// TestDefaultLocaleApplied verifies that passing "" as locale gives
// DefaultLocale.
func TestDefaultLocaleApplied(t *testing.T) {
	t.Parallel()

	r := NewLabelResolver(nil, "")
	if r.Locale() != DefaultLocale {
		t.Fatalf("Locale() = %q, want %q", r.Locale(), DefaultLocale)
	}
}
