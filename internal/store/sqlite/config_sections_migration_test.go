// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// legacyRESTSectionPayload is the shape a `north.rest` row carried while the
// auth switches were plain bools: three keys, none of them with omitempty and
// none of them ever read, so the whole sub-tree was serialised with literal
// `false` on the first-boot seed of every installation. session_enabled is the
// marker that dates the row — the change that made basic/bearer load-bearing
// removed that field, so no daemon since has been able to write it.
const legacyRESTSectionPayload = `{
	"cors": ["https://loom.example.de"],
	"public_url": "https://loom.example.de",
	"enabled": true,
	"auth": {
		"basic_enabled": false,
		"bearer_enabled": false,
		"session_enabled": false,
		"oidc": {"enabled": false},
		"ccu": {"enabled": true}
	},
	"csrf_enabled": true
}`

// currentRESTSectionPayload is what a daemon that understands the tri-state
// gates writes when the operator deliberately turns HTTP Basic off: an
// explicit false, and no session_enabled anywhere.
const currentRESTSectionPayload = `{
	"enabled": true,
	"auth": {
		"basic_enabled": false,
		"oidc": {"enabled": false}
	}
}`

// insertSectionRow writes a config_sections row directly, bypassing the store
// so the payload lands verbatim — the point of these tests is a payload no
// current code path can produce.
func insertSectionRow(t *testing.T, db *sql.DB, section, payload string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO config_sections (section, value_json, version, schema_version, updated_at, updated_by)
		 VALUES (?, ?, 1, ?, '2026-01-01T00:00:00Z', 'yaml-bootstrap')`,
		section, payload, ConfigSectionSchemaVersion)
	if err != nil {
		t.Fatalf("insert %s row: %v", section, err)
	}
}

// readSectionRow returns the raw stored payload of one section.
func readSectionRow(t *testing.T, db *sql.DB, section string) string {
	t.Helper()
	var raw string
	if err := db.QueryRowContext(context.Background(),
		`SELECT value_json FROM config_sections WHERE section = ?`, section).Scan(&raw); err != nil {
		t.Fatalf("select %s row: %v", section, err)
	}
	return raw
}

// migrateUp runs the remaining migrations on an already-open database.
func migrateUp(t *testing.T, db *sql.DB) {
	t.Helper()
	openMu.Lock()
	defer openMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		t.Fatalf("SetDialect: %v", err)
	}
	if err := goose.UpContext(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("goose up: %v", err)
	}
}

// authGatesOf decodes a stored `north.rest` payload the way the config overlay
// does and resolves the two gates the middleware actually consults.
func authGatesOf(t *testing.T, payload string) (basic, bearer bool) {
	t.Helper()
	var rest config.NorthREST
	if err := json.Unmarshal([]byte(payload), &rest); err != nil {
		t.Fatalf("decode north.rest payload: %v", err)
	}
	return rest.Auth.BasicAuthEnabled(), rest.Auth.BearerAuthEnabled()
}

// TestMigration_038_RepairsPreTriStateAuthGates pins the repair of a
// `north.rest` row written while basic_enabled/bearer_enabled were unread
// plain bools. Such a row stores them as `false`, which today means "reject
// this scheme": every HTTP Basic and Bearer request answers 401 after an
// upgrade while /health stays green and the SPA login keeps working, so
// nothing points at the cause. The migration must leave the row decoding to
// the documented default (both schemes enabled) without touching anything else
// it carries.
func TestMigration_038_RepairsPreTriStateAuthGates(t *testing.T) {
	db := openDBAtVersion(t, "sections_pre_tristate.db", 37)
	insertSectionRow(t, db, "north.rest", legacyRESTSectionPayload)

	// The premise: as stored, the row switches both schemes off.
	if basic, bearer := authGatesOf(t, legacyRESTSectionPayload); basic || bearer {
		t.Fatalf("stored legacy row already decodes to basic=%v bearer=%v, want both false", basic, bearer)
	}

	migrateUp(t, db)

	repaired := readSectionRow(t, db, "north.rest")
	basic, bearer := authGatesOf(t, repaired)
	if !basic || !bearer {
		t.Errorf("after migration basic=%v bearer=%v, want both enabled; row = %s", basic, bearer, repaired)
	}
	for _, key := range []string{"basic_enabled", "bearer_enabled", "session_enabled"} {
		if strings.Contains(repaired, key) {
			t.Errorf("repaired row still carries %q: %s", key, repaired)
		}
	}

	// Everything the row carries besides the three auth switches must survive:
	// the migration repairs two booleans, it does not reset the section.
	var rest config.NorthREST
	if err := json.Unmarshal([]byte(repaired), &rest); err != nil {
		t.Fatalf("decode repaired payload: %v", err)
	}
	if rest.PublicURL != "https://loom.example.de" {
		t.Errorf("public_url = %q, want it preserved", rest.PublicURL)
	}
	if len(rest.CORS) != 1 || rest.CORS[0] != "https://loom.example.de" {
		t.Errorf("cors = %v, want it preserved", rest.CORS)
	}
	if rest.Enabled == nil || !*rest.Enabled {
		t.Errorf("enabled = %v, want it preserved as true", rest.Enabled)
	}
	if rest.Auth.CCU.Enabled == nil || !*rest.Auth.CCU.Enabled {
		t.Errorf("auth.ccu.enabled = %v, want the neighbouring sub-tree preserved as true", rest.Auth.CCU.Enabled)
	}
}

// TestMigration_038_KeepsDeliberatelyDisabledAuthGate pins the other half of
// the guard: a row written by a daemon that understands the tri-state gates
// carries no session_enabled, so its explicit `false` is an operator decision
// and must survive. Repairing it would silently re-open an auth scheme the
// operator turned off.
func TestMigration_038_KeepsDeliberatelyDisabledAuthGate(t *testing.T) {
	db := openDBAtVersion(t, "sections_tristate.db", 37)
	insertSectionRow(t, db, "north.rest", currentRESTSectionPayload)

	migrateUp(t, db)

	stored := readSectionRow(t, db, "north.rest")
	basic, bearer := authGatesOf(t, stored)
	if basic {
		t.Errorf("basic gate re-enabled by the migration; row = %s", stored)
	}
	if !bearer {
		t.Errorf("bearer = false, want the unset gate to stay enabled; row = %s", stored)
	}
}

// TestMigration_038_IsIdempotent runs the repair twice against the same
// database. A migration that rewrites a payload has to be safe to re-apply:
// goose re-runs it on any database restored from a backup taken before it
// existed, and a second pass must not strip a gate the operator set since.
func TestMigration_038_IsIdempotent(t *testing.T) {
	db := openDBAtVersion(t, "sections_idempotent.db", 37)
	insertSectionRow(t, db, "north.rest", legacyRESTSectionPayload)

	migrateUp(t, db)
	first := readSectionRow(t, db, "north.rest")

	// Re-apply by walking back over the repair and forward again, which is
	// what a restored older database does on the next boot.
	openMu.Lock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		openMu.Unlock()
		t.Fatalf("SetDialect: %v", err)
	}
	err := goose.DownToContext(context.Background(), db, "migrations", 37)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("goose down to 37: %v", err)
	}
	migrateUp(t, db)

	second := readSectionRow(t, db, "north.rest")
	if first != second {
		t.Errorf("second application changed the row:\nfirst  = %s\nsecond = %s", first, second)
	}
	if basic, bearer := authGatesOf(t, second); !basic || !bearer {
		t.Errorf("after re-application basic=%v bearer=%v, want both enabled", basic, bearer)
	}
}

// TestMigration_038_LeavesOtherSectionsAlone verifies the repair is scoped to
// the one section whose shape changed. Every section row shares the table, and
// a migration that keyed on the payload alone would rewrite unrelated rows.
func TestMigration_038_LeavesOtherSectionsAlone(t *testing.T) {
	db := openDBAtVersion(t, "sections_scope.db", 37)
	const mqtt = `{"enabled":true,"broker_url":"tcp://mqtt.local:1883","session_enabled":false}`
	insertSectionRow(t, db, "north.mqtt", mqtt)

	migrateUp(t, db)

	if got := readSectionRow(t, db, "north.mqtt"); got != mqtt {
		t.Errorf("north.mqtt row rewritten:\n got %s\nwant %s", got, mqtt)
	}
}
