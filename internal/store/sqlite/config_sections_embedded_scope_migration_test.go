// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// legacyEmbeddedUIPayload is the shape a `north.ui` row carried before
// 0.56.0 added EmbeddedScope: `embedded: true` with no sibling scope key at
// all, because the field did not exist yet.
const legacyEmbeddedUIPayload = `{"enabled":true,"embedded":true}`

// insertSectionRowAt writes a config_sections row directly with an explicit
// updated_at, so a test can simulate a row the daemon has not re-saved since
// a given point in time — the discriminator migration 039 relies on.
func insertSectionRowAt(t *testing.T, db *sql.DB, section, payload, updatedAt string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO config_sections (section, value_json, version, schema_version, updated_at, updated_by)
		 VALUES (?, ?, 1, ?, ?, 'yaml-bootstrap')`,
		section, payload, ConfigSectionSchemaVersion, updatedAt)
	if err != nil {
		t.Fatalf("insert %s row: %v", section, err)
	}
}

// downUpMigrate walks a database back to version and forward again, the way
// goose replays every migration on a database restored from a backup taken
// before the newest one existed.
func downUpMigrate(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	openMu.Lock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		openMu.Unlock()
		t.Fatalf("SetDialect: %v", err)
	}
	err := goose.DownToContext(context.Background(), db, "migrations", version)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("goose down to %d: %v", version, err)
	}
	migrateUp(t, db)
}

// embeddedScopeOf decodes a stored `north.ui` payload the way the config
// overlay does and resolves the effective scope ActiveProfileFor consults.
func embeddedScopeOf(t *testing.T, payload string) config.EmbeddedScope {
	t.Helper()
	var ui config.NorthUI
	if err := json.Unmarshal([]byte(payload), &ui); err != nil {
		t.Fatalf("decode north.ui payload: %v", err)
	}
	return ui.EmbeddedScopeOrDefault()
}

// TestMigration_039_RepairsPreEmbeddedScopeRow pins the repair described in
// migrations/039_config_sections_embedded_scope.sql: a `north.ui` row that
// predates EmbeddedScope and declared `embedded: true` under the old
// daemon-wide semantics must keep behaving that way after the upgrade,
// instead of silently resolving to the newer, narrower inside_ha default.
func TestMigration_039_RepairsPreEmbeddedScopeRow(t *testing.T) {
	db := openDBAtVersion(t, "sections_pre_embedded_scope.db", 38)
	insertSectionRow(t, db, "north.ui", legacyEmbeddedUIPayload) // updated_at 2026-01-01, before the 039 cutoff

	if got := embeddedScopeOf(t, legacyEmbeddedUIPayload); got != config.EmbeddedScopeInsideHA {
		t.Fatalf("stored legacy row already decodes to %q, want the premise to be inside_ha (the pre-migration default)", got)
	}

	migrateUp(t, db)

	repaired := readSectionRow(t, db, "north.ui")
	if got := embeddedScopeOf(t, repaired); got != config.EmbeddedScopeAlways {
		t.Errorf("after migration scope = %q, want always; row = %s", got, repaired)
	}
	if !strings.Contains(repaired, `"embedded":true`) {
		t.Errorf("repaired row lost the embedded flag: %s", repaired)
	}
}

// TestMigration_039_LeavesPostCutoverRowUntouched pins the other half of the
// guard: a `north.ui` row saved after EmbeddedScope shipped had every
// opportunity to state a scope explicitly, so its absence is a deliberate
// (or at least reachable) choice of the new default and must not be
// overridden.
func TestMigration_039_LeavesPostCutoverRowUntouched(t *testing.T) {
	db := openDBAtVersion(t, "sections_post_embedded_scope.db", 38)
	insertSectionRowAt(t, db, "north.ui", legacyEmbeddedUIPayload, "2026-08-15T00:00:00Z")

	migrateUp(t, db)

	stored := readSectionRow(t, db, "north.ui")
	if got := embeddedScopeOf(t, stored); got != config.EmbeddedScopeInsideHA {
		t.Errorf("post-cutover row scope = %q, want inside_ha (untouched); row = %s", got, stored)
	}
}

// TestMigration_039_LeavesEmbeddedFalseAlone verifies the repair only
// applies to rows that actually declared `embedded: true` — a row with the
// UI feature off, or never declared, carries no ambiguity to repair.
func TestMigration_039_LeavesEmbeddedFalseAlone(t *testing.T) {
	db := openDBAtVersion(t, "sections_embedded_false.db", 38)
	const payload = `{"enabled":true,"embedded":false}`
	insertSectionRow(t, db, "north.ui", payload)

	migrateUp(t, db)

	if got := readSectionRow(t, db, "north.ui"); got != payload {
		t.Errorf("embedded:false row rewritten:\n got %s\nwant %s", got, payload)
	}
}

// TestMigration_039_IsIdempotent runs the repair twice against the same
// database, the way goose replays every migration on a database restored
// from a backup taken before 039 existed.
func TestMigration_039_IsIdempotent(t *testing.T) {
	db := openDBAtVersion(t, "sections_embedded_scope_idempotent.db", 38)
	insertSectionRow(t, db, "north.ui", legacyEmbeddedUIPayload)

	migrateUp(t, db)
	first := readSectionRow(t, db, "north.ui")

	downUpMigrate(t, db, 38)

	second := readSectionRow(t, db, "north.ui")
	if first != second {
		t.Errorf("second application changed the row:\nfirst  = %s\nsecond = %s", first, second)
	}
	if got := embeddedScopeOf(t, second); got != config.EmbeddedScopeAlways {
		t.Errorf("after re-application scope = %q, want always", got)
	}
}

// TestMigration_039_LeavesOtherSectionsAlone verifies the repair is scoped
// to `north.ui` — a migration that keyed on the payload shape alone would
// rewrite any section that happens to carry an "embedded" key.
func TestMigration_039_LeavesOtherSectionsAlone(t *testing.T) {
	db := openDBAtVersion(t, "sections_embedded_scope_scope.db", 38)
	const mqtt = `{"enabled":true,"broker_url":"tcp://mqtt.local:1883"}`
	insertSectionRow(t, db, "north.mqtt", mqtt)

	migrateUp(t, db)

	if got := readSectionRow(t, db, "north.mqtt"); got != mqtt {
		t.Errorf("north.mqtt row rewritten:\n got %s\nwant %s", got, mqtt)
	}
}
