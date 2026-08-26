// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"strings"
	"testing"
)

// TestEmbeddedProfilesCarryNoHTMLReferences pins that the raw profile
// documents handed to the SPA are plain text. The archives are lifted from
// the CCU WebUI, where display strings are HTML fragments
// ("Bew&auml;sserungsaktor"); the UI renders them verbatim, so an undecoded
// reference is shown to the operator.
func TestEmbeddedProfilesCarryNoHTMLReferences(t *testing.T) {
	t.Parallel()
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	if len(store.Receivers) == 0 {
		t.Fatal("no receiver documents loaded")
	}
	// Both reference classes present in the archive: the named entity and
	// the ampersand.
	for _, ref := range []string{"&auml;", "&Auml;", "&ouml;", "&uuml;", "&szlig;", "&amp;"} {
		for receiver, raw := range store.Receivers {
			if strings.Contains(string(raw), ref) {
				t.Errorf("receiver %s still carries %q in its raw document", receiver, ref)
				break
			}
		}
	}
}

// TestResolvedProfileDecodesReferences checks the resolved surface, which
// feeds the display name the operator reads.
//
// The assertion aggregates over the whole catalogue on purpose:
// ResolvedProfile scans the receiver's sender types for the id, and Go map
// iteration is unordered, so a receiver whose senders disagree on what
// profile 2 is called returns either name. Asserting one name would be a
// coin flip.
func TestResolvedProfileDecodesReferences(t *testing.T) {
	t.Parallel()
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	var resolved, withUmlaut int
	for receiver := range store.Receivers {
		for id := range 12 {
			got, ok := store.ResolvedProfile(receiver, id, "de")
			if !ok {
				continue
			}
			resolved++
			for _, text := range []string{got.Name, got.Description} {
				if strings.Contains(text, "&") && strings.Contains(text, ";") {
					t.Errorf("%s profile %d still carries an HTML reference: %q", receiver, id, text)
				}
				if strings.ContainsAny(text, "\u00e4\u00f6\u00fc\u00c4\u00d6\u00dc\u00df") {
					withUmlaut++
				}
			}
		}
	}
	if resolved == 0 {
		t.Fatal("no profile resolved from the embedded catalogue")
	}
	if withUmlaut == 0 {
		t.Error("expected decoded umlauts somewhere in the resolved catalogue")
	}
	t.Logf("resolved %d profiles, %d texts carry umlauts", resolved, withUmlaut)
}
