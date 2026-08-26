// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

// TestMigrationClearsUnusableAlarmZoneSlugs covers the upgrade of an
// installation whose zones predate the slug column. The 034 backfill derived
// the slug in SQL — ASCII LOWER() plus four literal replacements — so a zone
// named `Küche` was stored as `küche`, which is outside the identifier
// grammar a Home Assistant object_id accepts: the zone's entities never
// appear, while the same zone created after the upgrade works because the Go
// side derives `kuche`. Clearing the stored value hands the derivation back
// to the domain, which uses the same rule for every zone.
//
// A slug that is already a usable identifier is left alone even when the Go
// derivation would have spelled it with hyphens: it is live in a consumer's
// entity registry, and moving it would orphan a working entity.
//
// Not marked t.Parallel(): openDBAtVersion holds the package-level openMu
// across two sequential goose calls.
func TestMigrationClearsUnusableAlarmZoneSlugs(t *testing.T) {
	ctx := context.Background()

	// Migrate to the revision that introduced the slug column, then plant the
	// rows an installation upgraded across it would carry.
	db := openDBAtVersion(t, "alarm_zone_slug_charset.db", 34)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO alarm_zones (id, name, slug, position, config_json, created_at_ms, updated_at_ms)
		 VALUES ('z1', 'Küche',            'küche',            0, '{}', 0, 0),
		        ('z2', 'EG (Innen)',       'eg_(innen)',       1, '{}', 0, 0),
		        ('z3', 'Außen',            'außen',            2, '{}', 0, 0),
		        ('z4', 'Erdgeschoss Flur', 'erdgeschoss_flur', 3, '{}', 0, 0),
		        ('z5', 'Garage',           'garage',           4, '{}', 0, 0)`); err != nil {
		t.Fatalf("seed alarm zones: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT id, slug FROM alarm_zones ORDER BY id`)
	if err != nil {
		t.Fatalf("query alarm zones: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := make(map[string]string)
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			t.Fatalf("scan alarm zone row: %v", err)
		}
		got[id] = slug
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate alarm zones: %v", err)
	}

	want := map[string]string{
		"z1": "",                 // umlaut — unusable as an object_id
		"z2": "",                 // parentheses — unusable as an object_id
		"z3": "",                 // sharp s — unusable as an object_id
		"z4": "erdgeschoss_flur", // usable and live: must not move
		"z5": "garage",           // usable and live: must not move
	}
	for id, wantSlug := range want {
		if got[id] != wantSlug {
			t.Errorf("zone %s slug = %q, want %q", id, got[id], wantSlug)
		}
	}
}
