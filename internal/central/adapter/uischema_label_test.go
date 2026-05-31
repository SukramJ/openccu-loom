// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
)

// TestI18nLookupChainResolvesEmbeddedLabels verifies that the
// ccudata translations actually contain non-empty localised labels
// for the parameters our UX relies on. Exact wording can drift with
// Each
// raw key" rather than literal strings — the test fails loudly if
// translations regress to raw keys but tolerates harmless wording
// updates.
func TestI18nLookupChainResolvesEmbeddedLabels(t *testing.T) {
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}

	cases := []struct {
		name      string
		locale    string
		parameter string
	}{
		{"de actual_temperature", "de", "ACTUAL_TEMPERATURE"},
		{"en actual_temperature", "en", "ACTUAL_TEMPERATURE"},
		{"de level", "de", "LEVEL"},
		{"en level", "en", "LEVEL"},
		{"de humidity", "de", "HUMIDITY"},
		{"de control_mode", "de", "CONTROL_MODE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tr.ParameterLabel(c.locale, "", c.parameter)
			if got == "" {
				t.Fatalf("ParameterLabel(%q, _, %q) returned empty — translation regressed",
					c.locale, c.parameter)
			}
			if got == c.parameter {
				t.Fatalf("ParameterLabel(%q, _, %q) = raw key — not localised",
					c.locale, c.parameter)
			}
		})
	}
}

func TestI18nFallbackToRawWhenMissing(t *testing.T) {
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}

	got := tr.Parameter("de", "DEFINITELY_NOT_A_REAL_PARAMETER_X9")
	if got != "DEFINITELY_NOT_A_REAL_PARAMETER_X9" {
		t.Fatalf("missing key must fall back to raw, got %q", got)
	}
}

func TestI18nLocaleFallback(t *testing.T) {
	tr, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}

	// Italian is not embedded — translations.Parameter returns the
	// raw key. The adapter layer is expected to fall back to English
	// before showing the raw key, but the bare API matches python
	// (no automatic locale fallback).
	got := tr.Parameter("it", "ACTUAL_TEMPERATURE")
	if got != "ACTUAL_TEMPERATURE" {
		t.Fatalf("missing locale must fall back to raw key, got %q", got)
	}
	// English does exist — verify it works as the natural fallback.
	got = tr.Parameter("en", "ACTUAL_TEMPERATURE")
	if got == "ACTUAL_TEMPERATURE" {
		t.Fatal("english label should be present")
	}
}
