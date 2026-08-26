// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package i18n

import (
	"strings"
	"testing"
)

// newTFTestCatalogs builds a minimal Catalogs instance carrying exactly one
// "en" message under key, bypassing the embedded catalogue so a test can
// pin an arbitrary placeholder template without depending on real
// catalogue prose.
func newTFTestCatalogs(key, msg string) *Catalogs {
	return &Catalogs{
		DefaultLocale: "en",
		catalogs: map[string]*Catalog{
			"en": {Locale: "en", Messages: map[string]string{key: msg}},
		},
	}
}

func TestCatalogsLoadsEmbeddedFiles(t *testing.T) {
	c, err := NewCatalogs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	locales := c.Locales()
	if len(locales) < 2 {
		t.Fatalf("expected en+de, got %v", locales)
	}
}

func TestCatalogsTranslates(t *testing.T) {
	c, _ := NewCatalogs()
	if got := c.T("de", "nav.devices"); got != "Geräte" {
		t.Fatalf("de: %s", got)
	}
	if got := c.T("en", "nav.devices"); got != "Devices" {
		t.Fatalf("en: %s", got)
	}
}

func TestCatalogsFallsBackToDefaultLocale(t *testing.T) {
	c, _ := NewCatalogs()
	if got := c.T("fr", "nav.devices"); got != "Devices" {
		t.Fatalf("fallback: %s", got)
	}
}

func TestCatalogsReturnsKeyOnMiss(t *testing.T) {
	c, _ := NewCatalogs()
	if got := c.T("de", "nope.absent"); got != "nope.absent" {
		t.Fatalf("fallthrough: %s", got)
	}
}

// TestCatalogsTFSubstitutesSinglePlaceholder verifies the basic {name}
// substitution.
func TestCatalogsTFSubstitutesSinglePlaceholder(t *testing.T) {
	c := newTFTestCatalogs("test.greet", "Hello {name}!")
	got := c.TF("en", "test.greet", map[string]string{"name": "Ada"})
	if want := "Hello Ada!"; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFSubstitutesMultiplePlaceholders verifies that several
// distinct placeholders in one template are each substituted from their
// own argument.
func TestCatalogsTFSubstitutesMultiplePlaceholders(t *testing.T) {
	c := newTFTestCatalogs("test.multi", "{greeting} {name}, today is {day}.")
	args := map[string]string{"greeting": "Hi", "name": "Bob", "day": "Monday"}
	got := c.TF("en", "test.multi", args)
	if want := "Hi Bob, today is Monday."; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFSubstitutesRepeatedPlaceholder verifies that a
// placeholder occurring more than once in the same template is
// substituted at every occurrence from the single matching argument.
func TestCatalogsTFSubstitutesRepeatedPlaceholder(t *testing.T) {
	c := newTFTestCatalogs("test.repeat", "{name} said hello to {name}.")
	got := c.TF("en", "test.repeat", map[string]string{"name": "Eve"})
	if want := "Eve said hello to Eve."; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFIgnoresArgWithoutPlaceholder verifies that an argument
// key with no matching placeholder in the template is simply unused,
// rather than appended or causing an error.
func TestCatalogsTFIgnoresArgWithoutPlaceholder(t *testing.T) {
	c := newTFTestCatalogs("test.noph", "Hello {name}!")
	args := map[string]string{"name": "Ada", "unused": "ignored"}
	got := c.TF("en", "test.noph", args)
	if want := "Hello Ada!"; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFLeavesMissingPlaceholderVerbatim verifies that a
// placeholder with no matching argument is left standing in the output
// rather than blanked, so a missing argument stays visible.
func TestCatalogsTFLeavesMissingPlaceholderVerbatim(t *testing.T) {
	c := newTFTestCatalogs("test.missing", "{count} detectors in {zone}.")
	got := c.TF("en", "test.missing", map[string]string{"count": "3"})
	if want := "3 detectors in {zone}."; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
	if !strings.Contains(got, "{zone}") {
		t.Fatalf("TF = %q, want the literal {zone} placeholder to survive", got)
	}
}

// TestCatalogsTFSinglePassDoesNotReinjectPlaceholders verifies that
// substitution runs exactly once: an argument VALUE that itself contains
// braces must not be scanned for further placeholders. Device names
// arrive from the CCU and are not trusted input, so a name containing
// "{other}" must reach the output as a literal, not as a second
// substitution of the "other" argument.
func TestCatalogsTFSinglePassDoesNotReinjectPlaceholders(t *testing.T) {
	c := newTFTestCatalogs("test.untrusted", "Name: {name}")
	args := map[string]string{
		"name":  "{other}",
		"other": "INJECTED",
	}
	got := c.TF("en", "test.untrusted", args)
	if want := "Name: {other}"; got != want {
		t.Fatalf("TF = %q, want %q (single-pass must not re-substitute)", got, want)
	}
	if strings.Contains(got, "INJECTED") {
		t.Fatalf("TF result %q contains the injected value; substitution ran more than once", got)
	}
}

// TestCatalogsTFUnclosedBraceDoesNotPanic verifies that a template with
// an opening brace and no matching closing brace passes through
// unchanged rather than panicking.
func TestCatalogsTFUnclosedBraceDoesNotPanic(t *testing.T) {
	c := newTFTestCatalogs("test.unclosed", "progress: {pct")
	got := c.TF("en", "test.unclosed", map[string]string{"pct": "50"})
	if want := "progress: {pct"; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFLoneClosingBraceDoesNotPanic verifies that a template
// carrying a closing brace with no preceding opening brace passes
// through unchanged rather than panicking.
func TestCatalogsTFLoneClosingBraceDoesNotPanic(t *testing.T) {
	c := newTFTestCatalogs("test.lone", "score: 3} done")
	got := c.TF("en", "test.lone", map[string]string{"score": "3"})
	if want := "score: 3} done"; got != want {
		t.Fatalf("TF = %q, want %q", got, want)
	}
}

// TestCatalogsTFEmptyArgsReturnsTUnchanged verifies that calling TF with
// no arguments returns exactly what T would, braces and all.
func TestCatalogsTFEmptyArgsReturnsTUnchanged(t *testing.T) {
	c := newTFTestCatalogs("test.empty", "Hello {name}!")
	got := c.TF("en", "test.empty", nil)
	want := c.T("en", "test.empty")
	if got != want {
		t.Fatalf("TF(nil args) = %q, want T() result %q", got, want)
	}
	if got != "Hello {name}!" {
		t.Fatalf("TF(nil args) = %q, want literal template unchanged", got)
	}
}
