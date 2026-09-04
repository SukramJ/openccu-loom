// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// TestNoteKeyFor_StaticVsInterpolated pins that static health notes map to an
// i18n key (localized display) while interpolated / unknown notes resolve to ""
// so they render from the stable English [health.Sample.Note] — which stays the
// sentinel the scoring logic matches on.
func TestNoteKeyFor_StaticVsInterpolated(t *testing.T) {
	t.Parallel()
	if got := health.NoteKeyFor("breaker open"); got != "health.note.breaker_open" {
		t.Errorf("health.NoteKeyFor(breaker open) = %q, want health.note.breaker_open", got)
	}
	if got := health.NoteKeyFor("client connected"); got != "health.note.client_connected" {
		t.Errorf("health.NoteKeyFor(client connected) = %q, want health.note.client_connected", got)
	}
	// "event-received" was the one static note with no catalogue row, so
	// the German UI rendered the English token. It has one now, and
	// RecordEventReceived stamps the key on the sample it produces.
	if got := health.NoteKeyFor("event-received"); got != "health.note.event_received" {
		t.Errorf("health.NoteKeyFor(event-received) = %q, want health.note.event_received", got)
	}
	// Notes with no key render from the English Note: each is interpolated
	// and carries a dynamic value that could not be translated anyway.
	for _, note := range []string{"connection lost: timeout", "client Reconnecting", "recovery completed: partial"} {
		if got := health.NoteKeyFor(note); got != "" {
			t.Errorf("health.NoteKeyFor(%q) = %q, want \"\" (no catalogue key)", note, got)
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
	for note, key := range health.NoteKeys {
		for _, loc := range []string{"en", "de"} {
			if got := cat.T(loc, key); got == "" || got == key {
				t.Errorf("note %q key %q unresolved in locale %q (got %q)", note, key, loc, got)
			}
		}
	}
}
