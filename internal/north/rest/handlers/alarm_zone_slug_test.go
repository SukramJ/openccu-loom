// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"testing"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestUniqueZoneSlugFollowsTheSharedRule pins the zone-creation path to the
// collision rule the security index applies when it re-derives the effective
// slug of a row whose stored slug is blank.
//
// Both must answer the same, or a newly created zone is handed the identity an
// existing one already resolves to. They cannot import each other, so the rule
// lives in routingkey — this test is here rather than beside it because a rule
// tested only at its definition passes while a caller quietly reimplements it.
func TestUniqueZoneSlugFollowsTheSharedRule(t *testing.T) {
	t.Parallel()
	rows := func(names ...string) []sqlitestore.AlarmZoneRow {
		out := make([]sqlitestore.AlarmZoneRow, 0, len(names))
		for _, n := range names {
			out = append(out, sqlitestore.AlarmZoneRow{Name: n})
		}
		return out
	}
	for _, c := range []struct {
		name     string
		existing []sqlitestore.AlarmZoneRow
		want     string
	}{
		{name: "Erdgeschoss", want: "erdgeschoss"},
		{name: "Erdgeschoss", existing: rows("Erdgeschoss"), want: "erdgeschoss-2"},
		{name: "Erdgeschoss", existing: rows("Erdgeschoss", "erdgeschoss-2"), want: "erdgeschoss-3"},
		// Unsluggable names still need an identifier.
		{name: "🏠", want: "zone"},
		{name: "🏠", existing: rows("🏠"), want: "zone-2"},
	} {
		if got := uniqueZoneSlug(c.name, c.existing); got != c.want {
			t.Errorf("uniqueZoneSlug(%q, %d existing) = %q, want %q", c.name, len(c.existing), got, c.want)
		}
	}
}
