// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"os"
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

// The migration chain has grown past 30 files, and running it for every
// test database dominates the package's runtime on slow-fsync CI
// runners (each DDL statement journals a schema rewrite). The template
// is migrated ONCE per test process and closed (checkpointing the WAL),
// then every test starts from a byte copy; Open on the copy still runs
// goose, which sees the up-to-date version table and no-ops with a
// single read.
var (
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

func templateDB(t *testing.T) string {
	t.Helper()
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "loom-sqlite-template-*")
		if err != nil {
			templateErr = err
			return
		}
		p := filepath.Join(dir, "template.db")
		openMu.Lock()
		db, err := Open(context.Background(), FileDSN(p))
		openMu.Unlock()
		if err != nil {
			templateErr = err
			return
		}
		if err := db.Close(); err != nil {
			templateErr = err
			return
		}
		templatePath = p
	})
	if templateErr != nil {
		t.Fatalf("migrate template db: %v", templateErr)
	}
	return templatePath
}

// openTestDB opens a fresh file-backed SQLite database in t's temp
// directory — copied from the pre-migrated template — and registers a
// cleanup to close it.
func openTestDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	data, err := os.ReadFile(templateDB(t))
	if err != nil {
		t.Fatalf("read template db: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("copy template db: %v", err)
	}
	openMu.Lock()
	db, err := Open(context.Background(), FileDSN(path))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
