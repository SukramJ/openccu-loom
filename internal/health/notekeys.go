// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package health

// NoteKeys maps the stable English health-note sentinel to its i18n catalogue
// key for localized display. Only the static notes are mapped; interpolated
// notes (which carry dynamic, un-localized values) resolve to "" and render
// from the English [Sample.Note]. The Note string itself is never localized —
// the scoring and aggregation logic matches on it.
//
// The catalogue lives beside the tracker rather than in the wiring layer so
// every producer of a [Sample] can reach it. A note produced here that the
// wiring layer could not see was the reason one static note shipped with no
// key at all.
// loom:reachable:reason="read by NoteKeyFor in this package, which the tracker calls on every static note; RTA scores call edges only, so a map read is invisible to it"
var NoteKeys = map[string]string{
	"initial-sync: connected":     "health.note.initial_sync_connected",
	"initial-sync: not connected": "health.note.initial_sync_not_connected",
	"client connected":            "health.note.client_connected",
	"event-received":              "health.note.event_received",
	"breaker closed":              "health.note.breaker_closed",
	"breaker half-open":           "health.note.breaker_half_open",
	"breaker open":                "health.note.breaker_open",
	"recovery started":            "health.note.recovery_started",
	"recovery completed":          "health.note.recovery_completed",
}

// NoteKeyFor returns the i18n key for a static health note, or "" for an
// interpolated or unknown note (which then renders from the English Note).
func NoteKeyFor(note string) string { return NoteKeys[note] }
