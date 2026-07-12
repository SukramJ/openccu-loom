// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// TestNoteKeyFor_StaticVsInterpolated pins that static health notes map to an
// i18n key (localized display) while interpolated / unknown notes resolve to ""
// so they render from the stable English [health.Sample.Note] — which stays the
// sentinel the scoring logic matches on.
func TestNoteKeyFor_StaticVsInterpolated(t *testing.T) {
	t.Parallel()
	if got := noteKeyFor("breaker open"); got != "health.note.breaker_open" {
		t.Errorf("noteKeyFor(breaker open) = %q, want health.note.breaker_open", got)
	}
	if got := noteKeyFor("client connected"); got != "health.note.client_connected" {
		t.Errorf("noteKeyFor(client connected) = %q, want health.note.client_connected", got)
	}
	// Interpolated / unknown notes have no key (English Note is rendered).
	for _, note := range []string{"connection lost: timeout", "client Reconnecting", "recovery completed: partial", "event-received"} {
		if got := noteKeyFor(note); got != "" {
			t.Errorf("noteKeyFor(%q) = %q, want \"\" (interpolated/unknown)", note, got)
		}
	}
}

// TestHealthNoteKeys_ResolveInCatalog guards the linkage between the note→key
// map and the i18n catalogues: every mapped key must resolve to a real, distinct
// translation in both locales (a typo would otherwise silently render the raw
// key).
func TestHealthNoteKeys_ResolveInCatalog(t *testing.T) {
	t.Parallel()
	cat, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	for note, key := range healthNoteKeys {
		for _, loc := range []string{"en", "de"} {
			if got := cat.T(loc, key); got == "" || got == key {
				t.Errorf("note %q key %q unresolved in locale %q (got %q)", note, key, loc, got)
			}
		}
	}
}
