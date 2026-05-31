// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package i18n

import "testing"

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
