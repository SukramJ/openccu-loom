// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigration_004_IncidentsJournalExcerpt_UpDown verifies that
// migration 004_incidents_journal_excerpt.sql adds the journal_excerpt
// column on the Up path and removes it cleanly on the Down path.
//
// Not marked t.Parallel() — openMu is a package-level mutex shared
// with other migration tests; holding it across sequential goose calls
// while parallel tests contend would deadlock (same reason as
// TestMigration_005_SessionRecorder_UpDown).
func TestMigration_004_IncidentsJournalExcerpt_UpDown(t *testing.T) {
	ctx := context.Background()

	// -----------------------------------------------------------------------
	// Up path — migrate to 004.
	// -----------------------------------------------------------------------
	db := openDBAtVersion(t, "migration_004.db", 4)

	if !tableExists(t, db, "incidents") {
		t.Fatal("migration 004 Up: table incidents not found")
	}

	cols := columnNames(t, db, "incidents")
	if _, ok := cols["journal_excerpt"]; !ok {
		t.Fatal("migration 004 Up: column journal_excerpt missing from incidents")
	}

	// -----------------------------------------------------------------------
	// Down path — migrate back to 003.
	// -----------------------------------------------------------------------
	openMu.Lock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 004 Down: SetDialect: %v", err)
	}
	if err := goose.DownToContext(ctx, db, "migrations", 3); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 004 Down: DownTo 3: %v", err)
	}
	openMu.Unlock()

	// Column must be gone; table itself must still exist (owned by migration 001).
	if !tableExists(t, db, "incidents") {
		t.Fatal("migration 004 Down: table incidents disappeared — should still exist")
	}
	colsAfter := columnNames(t, db, "incidents")
	if _, ok := colsAfter["journal_excerpt"]; ok {
		t.Error("migration 004 Down: column journal_excerpt still present after down migration")
	}
}

// TestMigration_011_MatterDiagnostics_UpDown verifies that migration
// 011_matter_diagnostics.sql creates the matter_diagnostics table and
// seeds the singleton row on the Up path, and removes the table cleanly
// on the Down path.
//
// Not marked t.Parallel() for the same reason as the other migration tests.
func TestMigration_011_MatterDiagnostics_UpDown(t *testing.T) {
	ctx := context.Background()

	// -----------------------------------------------------------------------
	// Up path — migrate to 011.
	// -----------------------------------------------------------------------
	db := openDBAtVersion(t, "migration_011.db", 11)

	if !tableExists(t, db, "matter_diagnostics") {
		t.Fatal("migration 011 Up: table matter_diagnostics not found")
	}

	// Verify all columns defined in the migration DDL.
	wantCols := []string{"id", "reboot_count", "base_operational_hours", "updated_at"}
	cols := columnNames(t, db, "matter_diagnostics")
	for _, col := range wantCols {
		if _, ok := cols[col]; !ok {
			t.Errorf("migration 011 Up: column %q missing from matter_diagnostics", col)
		}
	}

	// Singleton seed row (id=1) must exist after the Up migration.
	var count int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM matter_diagnostics WHERE id = 1`,
	).Scan(&count); err != nil {
		t.Fatalf("migration 011 Up: seed row query: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 011 Up: expected seed row with id=1, got count=%d", count)
	}

	// -----------------------------------------------------------------------
	// Down path — migrate back to 010.
	// -----------------------------------------------------------------------
	openMu.Lock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 011 Down: SetDialect: %v", err)
	}
	if err := goose.DownToContext(ctx, db, "migrations", 10); err != nil {
		openMu.Unlock()
		t.Fatalf("migration 011 Down: DownTo 10: %v", err)
	}
	openMu.Unlock()

	if tableExists(t, db, "matter_diagnostics") {
		t.Error("migration 011 Down: table matter_diagnostics still exists after down migration")
	}
}
