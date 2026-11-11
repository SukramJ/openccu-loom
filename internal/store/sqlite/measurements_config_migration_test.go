// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"
)

// migrateMeasurementsConfig seeds the persistence section at schema version
// 52, migrates to 53 and returns the decoded persistence row and the decoded
// north.measurements row (nil when migration 053 wrote none).
func migrateMeasurementsConfig(t *testing.T, persistenceJSON string) (persistence, measurements map[string]any) {
	t.Helper()
	ctx := context.Background()
	db := openDBAtVersion(t, "measurements_config.db", 52)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO config_sections (section, value_json, updated_by) VALUES ('persistence', ?, 'seed')`,
		persistenceJSON); err != nil {
		t.Fatalf("seed persistence: %v", err)
	}

	openMu.Lock()
	err := goose.UpToContext(ctx, db, "migrations", 53)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("migrate to 53: %v", err)
	}

	return readSectionJSON(t, db, "persistence"), readSectionJSON(t, db, "north.measurements")
}

// readSectionJSON decodes one config_sections row; nil when absent.
func readSectionJSON(t *testing.T, db *sql.DB, section string) map[string]any {
	t.Helper()
	var raw string
	err := db.QueryRowContext(context.Background(),
		`SELECT value_json FROM config_sections WHERE section = ?`, section).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", section, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s %q: %v", section, raw, err)
	}
	return out
}

// TestMigration053_MovesHistoryBlockToMeasurementsSection pins the settings
// move: the history sub-tree of the persistence section becomes the
// north.measurements section, forwarding is on because capture and export
// were both on with an endpoint, retention and the export switch are
// dropped, and the rest of the persistence section survives.
func TestMigration053_MovesHistoryBlockToMeasurementsSection(t *testing.T) {
	t.Parallel()
	persistence, measurements := migrateMeasurementsConfig(t, `{
		"values_cache": {"enabled": true},
		"history": {
			"enabled": true,
			"include": ["TEMPERATURE", "POWER*"],
			"flush_interval": "30s",
			"retention": "720h",
			"export": {
				"enabled": true,
				"endpoint": "http://influx:8086",
				"bucket": "loom"
			}
		}
	}`)

	if measurements == nil {
		t.Fatal("no north.measurements row after migration 053")
	}
	if got, ok := measurements["enabled"].(bool); !ok || !got {
		t.Errorf("north.measurements.enabled = %#v, want true", measurements["enabled"])
	}
	if got := measurements["endpoint"]; got != "http://influx:8086" {
		t.Errorf("north.measurements.endpoint = %#v, want http://influx:8086", got)
	}
	if got := measurements["bucket"]; got != "loom" {
		t.Errorf("north.measurements.bucket = %#v, want loom", got)
	}
	if got, want := measurements["include"], []any{"TEMPERATURE", "POWER*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("north.measurements.include = %#v, want the JSON array %#v", got, want)
	}
	if got := measurements["flush_interval"]; got != "30s" {
		t.Errorf("north.measurements.flush_interval = %#v, want 30s", got)
	}
	for _, gone := range []string{"retention", "export"} {
		if v, ok := measurements[gone]; ok {
			t.Errorf("north.measurements carries %q = %#v, want it dropped", gone, v)
		}
	}

	if v, ok := persistence["history"]; ok {
		t.Errorf("persistence still carries history = %#v after migration 053", v)
	}
	if _, ok := persistence["values_cache"]; !ok {
		t.Errorf("persistence lost values_cache: %#v", persistence)
	}
}

// TestMigration053_CaptureWithoutExportLeavesForwardingUnset pins the
// enabled rule: capture alone forwarded nothing before, so the migrated
// section carries no enabled key and the default (off) applies.
func TestMigration053_CaptureWithoutExportLeavesForwardingUnset(t *testing.T) {
	t.Parallel()
	_, measurements := migrateMeasurementsConfig(t, `{
		"values_cache": {"enabled": true},
		"history": {"enabled": true, "include": ["TEMPERATURE"]}
	}`)

	if measurements == nil {
		t.Fatal("no north.measurements row after migration 053")
	}
	if v, ok := measurements["enabled"]; ok {
		t.Errorf("north.measurements.enabled = %#v, want the key absent", v)
	}
}

// TestMigration053_NoHistoryBlockWritesNoSection pins that an operator who
// never configured history gets no north.measurements row.
func TestMigration053_NoHistoryBlockWritesNoSection(t *testing.T) {
	t.Parallel()
	persistence, measurements := migrateMeasurementsConfig(t, `{"values_cache": {"enabled": true}}`)

	if measurements != nil {
		t.Errorf("north.measurements row = %#v, want none", measurements)
	}
	if _, ok := persistence["values_cache"]; !ok {
		t.Errorf("persistence lost values_cache: %#v", persistence)
	}
}
