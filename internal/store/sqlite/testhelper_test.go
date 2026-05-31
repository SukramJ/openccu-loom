// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// openMu serialises calls to Open across parallel tests.
// goose.SetBaseFS writes a package-level global in the goose library;
// concurrent Open calls from parallel tests trigger a data race.
// Holding this mutex for the duration of Open avoids the race without
// modifying production code.
var openMu sync.Mutex

// openTestDB opens (and migrates) a fresh file-backed SQLite database
// in t's temp directory and registers a cleanup to close it.
func openTestDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), name) + "?_pragma=journal_mode(WAL)"
	openMu.Lock()
	db, err := Open(context.Background(), dsn)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
