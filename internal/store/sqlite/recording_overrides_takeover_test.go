// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// writeHistoryFile builds a stand-alone history database at path. With
// withTable it carries the measurement_recording_overrides table (the
// schema migration 053 creates in the main database) and two rows.
func writeHistoryFile(t *testing.T, path string, withTable bool) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open(DriverName, "file:"+path)
	if err != nil {
		t.Fatalf("open history file: %v", err)
	}
	defer func() { _ = db.Close() }()

	stmts := []string{`CREATE TABLE unrelated (id INTEGER PRIMARY KEY)`}
	if withTable {
		stmts = append(
			stmts,
			`CREATE TABLE measurement_recording_overrides (
                central_name    TEXT    NOT NULL,
                interface_id    TEXT    NOT NULL,
                channel_address TEXT    NOT NULL,
                parameter       TEXT    NOT NULL,
                record          INTEGER NOT NULL,
                updated_by      TEXT    NOT NULL DEFAULT '',
                updated_at      TEXT    NOT NULL DEFAULT '',
                PRIMARY KEY (central_name, interface_id, channel_address, parameter)
            ) WITHOUT ROWID`,
			`INSERT INTO measurement_recording_overrides VALUES
                ('ccu1', 'HmIP-RF', 'DEV:1', 'POWER', 1, 'alice', '2026-01-01T00:00:00Z'),
                ('ccu1', 'HmIP-RF', 'DEV:2', 'ENERGY_COUNTER', 0, 'bob', '2026-01-02T00:00:00Z')`,
		)
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("history file %q: %v", s, err)
		}
	}
}

func assertUpgradeStepDone(t *testing.T, db *sql.DB) {
	t.Helper()
	done, err := UpgradeStepDone(context.Background(), db, upgradeStepRecordingOverrides)
	if err != nil {
		t.Fatalf("UpgradeStepDone: %v", err)
	}
	if !done {
		t.Errorf("upgrade step %q not recorded", upgradeStepRecordingOverrides)
	}
}

// TestTakeOverRecordingOverrides_CopiesOnce pins that the copy runs on the
// first start only: an override the operator cleared afterwards stays
// cleared on the next start, although the history file still carries it.
func TestTakeOverRecordingOverrides_CopiesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, "main.db")
	histPath := filepath.Join(t.TempDir(), "history.db")
	writeHistoryFile(t, histPath, true)

	n, err := TakeOverRecordingOverrides(ctx, db, histPath)
	if err != nil {
		t.Fatalf("first TakeOverRecordingOverrides: %v", err)
	}
	if n != 2 {
		t.Fatalf("first call copied %d rows, want 2", n)
	}
	store := NewRecordingOverrideStore(db)
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("main database holds %d overrides after the copy, want 2: %+v", len(rows), rows)
	}
	assertUpgradeStepDone(t, db)

	if err := store.Clear(ctx, "ccu1", "HmIP-RF", "DEV:1", "POWER"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	n, err = TakeOverRecordingOverrides(ctx, db, histPath)
	if err != nil {
		t.Fatalf("second TakeOverRecordingOverrides: %v", err)
	}
	if n != 0 {
		t.Errorf("second call copied %d rows, want 0", n)
	}
	rows, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelAddress != "DEV:2" {
		t.Errorf("after the second call the main database holds %+v, want only DEV:2 (the cleared DEV:1 must stay cleared)", rows)
	}
}

// TestTakeOverRecordingOverrides_MissingFileRecordsStep pins that a daemon
// without a history database copies nothing and does not look again.
func TestTakeOverRecordingOverrides_MissingFileRecordsStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, "main.db")

	n, err := TakeOverRecordingOverrides(ctx, db, filepath.Join(t.TempDir(), "absent.db"))
	if err != nil {
		t.Fatalf("TakeOverRecordingOverrides: %v", err)
	}
	if n != 0 {
		t.Errorf("copied %d rows from a missing file, want 0", n)
	}
	assertUpgradeStepDone(t, db)
}

// TestTakeOverRecordingOverrides_FileWithoutTable pins that a history
// database that never held overrides copies nothing and does not fail.
func TestTakeOverRecordingOverrides_FileWithoutTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t, "main.db")
	histPath := filepath.Join(t.TempDir(), "history.db")
	writeHistoryFile(t, histPath, false)

	n, err := TakeOverRecordingOverrides(ctx, db, histPath)
	if err != nil {
		t.Fatalf("TakeOverRecordingOverrides: %v", err)
	}
	if n != 0 {
		t.Errorf("copied %d rows from a file without the table, want 0", n)
	}
	assertUpgradeStepDone(t, db)
}
