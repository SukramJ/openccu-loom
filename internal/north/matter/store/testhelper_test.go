// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	storesqlite "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// openMu serialises sqlite.Open calls across parallel tests; goose
// uses a package-level global that races otherwise. Mirrors the
// helper in internal/store/sqlite/testhelper_test.go.
var openMu sync.Mutex

// migratedSchemaTemplate applies the migration set once per test binary and
// returns the path of a closed, fully migrated database file.
//
// A full goose run costs about a second and cannot run concurrently (see
// openMu), so every test opening its own store queued that second behind the
// same lock — with this package's test count that was the bulk of its
// runtime, all of it re-deriving a schema that never varies. Deriving it once
// and copying the file gives each test a private database for the price of a
// file copy.
var migratedSchemaTemplate = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "matter-store-schema-template")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "template.db")
	db, err := storesqlite.Open(context.Background(), storesqlite.FileDSN(path))
	if err != nil {
		return "", err
	}
	// Closing checkpoints the write-ahead log back into the main file, so
	// the single file copied below carries the whole schema.
	if err := db.Close(); err != nil {
		return "", err
	}
	return path, nil
})

// openTestDB opens a fresh file-backed SQLite database in t's temp directory,
// seeded from the migrated template so the schema is already in place, and
// registers a cleanup to close it. Tests share the schema, never the data.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	templatePath, err := migratedSchemaTemplate()
	if err != nil {
		t.Fatalf("build migrated schema template: %v", err)
	}
	schema, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read schema template: %v", err)
	}
	path := filepath.Join(t.TempDir(), "matter.db")
	if err := os.WriteFile(path, schema, 0o600); err != nil {
		t.Fatalf("seed test db: %v", err)
	}
	// Open still runs goose; it finds the version table already at the
	// latest revision and applies nothing. The lock stays because that
	// check is goose-internal too.
	openMu.Lock()
	db, err := storesqlite.Open(context.Background(), storesqlite.FileDSN(path))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// uncompressedP256Fixture returns a deterministic 65-byte uncompressed
// P-256 public key. The bytes do not need to be on the curve for the
// store layer (it stores raw blobs); on-curve validation belongs in
// the fabric package.
func uncompressedP256Fixture(seed byte) []byte {
	out := make([]byte, 65)
	out[0] = 0x04
	for i := 1; i < 65; i++ {
		out[i] = seed + byte(i)
	}
	return out
}
