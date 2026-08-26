// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// sessions_migration_test.go — Item 1: schema verification for
// migration 005_session_recorder.sql.
//
// Tests:
// TestMigration_005_SessionRecorder_UpDown
// - migrates a fresh DB all the way up (includes 005)
// - verifies the session_recorder table and all its columns exist
// - verifies the session_recorder_lookup composite index exists
// - migrates back down to version 004
// - verifies the table and index are gone

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// openDBAtVersion returns a freshly migrated file-backed database
// (to support goose down) migrated to exactly the requested version.
// The returned db is registered for cleanup on t.
func openDBAtVersion(t *testing.T, name string, version int64) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), name) + "?_pragma=journal_mode(WAL)"
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		t.Fatalf("openDBAtVersion Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := applyPragmas(context.Background(), db); err != nil {
		t.Fatalf("openDBAtVersion applyPragmas: %v", err)
	}
	openMu.Lock()
	defer openMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		t.Fatalf("openDBAtVersion SetDialect: %v", err)
	}
	if err := goose.UpToContext(context.Background(), db, "migrations", version); err != nil {
		t.Fatalf("openDBAtVersion UpTo %d: %v", version, err)
	}
	return db
}

// tableExists reports whether the given table is present in the SQLite schema.
func tableExists(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
		tableName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("tableExists %q: %v", tableName, err)
	}
	return n > 0
}

// indexExists reports whether the given index is present in the SQLite schema.
func indexExists(t *testing.T, db *sql.DB, indexName string) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`,
		indexName,
	).Scan(&n)
	if err != nil {
		t.Fatalf("indexExists %q: %v", indexName, err)
	}
	return n > 0
}

// columnNames returns the set of column names for the given table.
func columnNames(t *testing.T, db *sql.DB, tableName string) map[string]struct{} {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM pragma_table_info(?)`, tableName)
	if err != nil {
		t.Fatalf("columnNames %q: %v", tableName, err)
	}
	defer func() { _ = rows.Close() }()
	cols := make(map[string]struct{})
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("columnNames scan: %v", err)
		}
		cols[col] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columnNames rows: %v", err)
	}
	return cols
}

// TestMigration_005_SessionRecorder_UpDown verifies the Up path of
// 005_session_recorder.sql creates the expected table and composite
// index, and that the Down path removes both cleanly.
//
// This test is NOT marked t.Parallel() because openMu is a package-level
// mutex shared with all other parallel tests; holding it across two
// sequential goose calls while parallel tests also contend would cause
// a deadlock. Serial execution is the safe choice here (identical
// rationale to TestOpenAutomaticallyWipesOutdatedRows).
func TestMigration_005_SessionRecorder_UpDown(t *testing.T) {
	ctx := context.Background()

	// -----------------------------------------------------------------------
	// Up path — migrate to 005.
	// -----------------------------------------------------------------------
	db := openDBAtVersion(t, "migration_005.db", 5)

	// Table must exist after migration 005.
	if !tableExists(t, db, "session_recorder") {
		t.Fatal("migration 005 Up: table session_recorder not found")
	}

	// Verify all columns defined in the migration DDL.
	wantCols := []string{
		"id",
		"central_name",
		"slug",
		"rpc_type",
		"method",
		"frozen_params",
		"response_json",
		"recorded_at",
		"ttl_seconds",
	}
	cols := columnNames(t, db, "session_recorder")
	for _, col := range wantCols {
		if _, ok := cols[col]; !ok {
			t.Errorf("migration 005 Up: column %q missing from session_recorder", col)
		}
	}

	// Composite index on (central_name, slug, recorded_at DESC) must exist.
	if !indexExists(t, db, "session_recorder_lookup") {
		t.Fatal("migration 005 Up: index session_recorder_lookup not found")
	}

	// Sanity: the table must accept a row insert.
	_, err := db.ExecContext(ctx,
		`INSERT INTO session_recorder
		    (central_name, slug, rpc_type, method, frozen_params, response_json, recorded_at, ttl_seconds)
		 VALUES ('main', 'test-slug', 'xml', 'ping', '<nil>', '"pong"', '2026-01-01T00:00:00Z', 0)`)
	if err != nil {
		t.Fatalf("migration 005 Up: test INSERT failed: %v", err)
	}

	// -----------------------------------------------------------------------
	// Down path — migrate back to 004.
	// -----------------------------------------------------------------------
	openMu.Lock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 005 Down: SetDialect: %v", err)
	}
	if err := goose.DownToContext(ctx, db, "migrations", 4); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 005 Down: DownTo 4: %v", err)
	}
	openMu.Unlock()

	// Table must be gone after the Down migration.
	if tableExists(t, db, "session_recorder") {
		t.Error("migration 005 Down: table session_recorder still exists after down migration")
	}

	// Index must be gone too.
	if indexExists(t, db, "session_recorder_lookup") {
		t.Error("migration 005 Down: index session_recorder_lookup still exists after down migration")
	}
}
