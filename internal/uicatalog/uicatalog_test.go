// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package uicatalog

import (
	"path/filepath"
	"testing"
)

func TestParseHandlesTheCatalogueStringForms(t *testing.T) {
	src := `
type Catalog = Record<string, string>;
const EN: Catalog = {
  // a line comment with "quotes": inside
  "plain": "one",
  /* block */ "wrapped":
    "two lines",
  'single': 'it\'s "quoted"',
  "tmpl": ` + "`back`" + `,
  "joined": "a" +
    "b",
  "esc": "x\nä\"",
  bare: "ident",
};
const DE: Catalog = {
  "plain": "eins",
};
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{
		"plain":   "one",
		"wrapped": "two lines",
		"single":  `it's "quoted"`,
		"tmpl":    "back",
		"joined":  "ab",
		"esc":     "x\nä\"",
		"bare":    "ident",
	}
	if len(got.EN) != len(want) {
		t.Fatalf("EN has %d entries, want %d: %v", len(got.EN), len(want), got.EN)
	}
	for k, v := range want {
		if got.EN[k] != v {
			t.Errorf("EN[%q] = %q, want %q", k, got.EN[k], v)
		}
	}
	if got.DE["plain"] != "eins" || len(got.DE) != 1 {
		t.Errorf("DE = %v", got.DE)
	}
}

func TestParseRejectsANonStringValue(t *testing.T) {
	if _, err := Parse("const EN = { \"k\": someCall() };\nconst DE = {};"); err == nil {
		t.Fatal("Parse accepted a non-literal value")
	}
	if _, err := Parse("const EN = { \"k\": `a${b}` };\nconst DE = {};"); err == nil {
		t.Fatal("Parse accepted a template literal with a placeholder")
	}
	if _, err := Parse("const EN = {};"); err == nil {
		t.Fatal("Parse accepted a source without a DE catalogue")
	}
}

func TestLoadParsesTheShippedCatalogue(t *testing.T) {
	c, err := Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.EN["config.help.locale"] == "" || c.DE["config.help.locale"] == "" {
		t.Fatalf("config.help.locale missing: EN=%q DE=%q", c.EN["config.help.locale"], c.DE["config.help.locale"])
	}
	if len(c.EN) < 100 || len(c.DE) < 100 {
		t.Fatalf("suspiciously small catalogues: EN=%d DE=%d", len(c.EN), len(c.DE))
	}
}
