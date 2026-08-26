// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// zoneSlugFor returns the slug snap.Zones carries for a zone by ID
// (Snapshot.Zones is keyed by slug, so a lookup walks the values).
func zoneSlugFor(t *testing.T, svc *Service, zoneID string) (string, bool) {
	t.Helper()
	snap := svc.Snapshot()
	for slug := range snap.Zones {
		if snap.Zones[slug].ID == zoneID {
			return slug, true
		}
	}
	return "", false
}

// TestRefreshZoneSlugsPersistsARepairedSlugAcrossARename pins the fix
// for a migration-blanked zone slug: migration 037 sets alarm_zones.slug
// to an empty string for the rows migration 034's backfill produced outside the
// identifier grammar, on the documented assumption that the security
// domain "derives one with HubSlug whenever the stored value is empty,
// which is the same value a freshly created zone gets" — but a freshly
// created zone's slug is frozen at creation (AlarmZoneStore.Upsert never
// updates it), while the repaired zone's slug used to live only in
// memory and was re-derived from the CURRENT name on every rebuild, so a
// later rename silently moved it instead of staying frozen like every
// other zone's.
func TestRefreshZoneSlugsPersistsARepairedSlugAcrossARename(t *testing.T) {
	t.Parallel()
	svc, stores, _ := newTestService(t)
	ctx := context.Background()

	// Simulate a pre-repair row: migration 037 blanked the slug,
	// nothing has derived one yet.
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: "z1", Name: "Küche", Slug: "", Position: 1,
		ConfigJSON: "{}", CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	wantSlug := routingkey.HubSlug("Küche")
	gotSlug, ok := zoneSlugFor(t, svc, "z1")
	if !ok {
		t.Fatal("zone z1 not present in the snapshot after Start; the rest of this test would pass vacuously")
	}
	if gotSlug != wantSlug {
		t.Fatalf("derived slug = %q, want %q", gotSlug, wantSlug)
	}

	// The defect: the derived slug was never written back, so the
	// stored row's slug stayed empty forever and every rebuild
	// re-derived it from whatever the name happened to be at the time.
	row, ok, err := stores.Zones.Get(ctx, "z1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: zone z1 not found")
	}
	if row.Slug != wantSlug {
		t.Fatalf("stored slug = %q, want %q persisted — a repaired slug that only lives in memory "+
			"is re-derived from the current name on every rebuild instead of being frozen like a "+
			"freshly created zone's", row.Slug, wantSlug)
	}

	// A rename: the REST handler reads the row (now carrying the
	// persisted slug) and writes it back unchanged alongside the new
	// name, exactly as it does for every zone.
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: "z1", Name: "Küche EG", Slug: row.Slug, Position: 1,
		ConfigJSON: "{}", CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: 2000,
	}); err != nil {
		t.Fatalf("rename zone: %v", err)
	}
	if err := svc.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex after rename: %v", err)
	}

	gotSlug, ok = zoneSlugFor(t, svc, "z1")
	if !ok {
		t.Fatal("zone z1 not present in the snapshot after the rename")
	}
	if gotSlug != wantSlug {
		t.Fatalf("slug after rename = %q, want %q (frozen) — every other zone's slug survives a "+
			"rename; a repaired one must too, or it orphans every consumer entity built from it",
			gotSlug, wantSlug)
	}
}
