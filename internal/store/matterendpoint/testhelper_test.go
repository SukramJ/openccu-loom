// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// openTestDB opens a fresh file-backed database in t's temp directory
// with every migration applied, and registers a cleanup to close it.
//
// It runs the daemon's real migration set rather than a hand-copied DDL
// fixture. The tables under test here — matter_endpoints (007),
// matter_exposures (009), matter_metadata (013) and the next_endpoint_id
// seed (036) — are owned by this repository, so the migrations are
// reachable and a copy could only drift away from what a deployment
// actually has. Their Down comments spell out what a schema reset costs
// a commissioned controller; a test running against a stale copy of that
// schema would be measuring nothing.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "matter.db")
	db, err := sqlitestore.Open(context.Background(), sqlitestore.FileDSN(path))
	if err != nil {
		t.Fatalf("open migrated test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
