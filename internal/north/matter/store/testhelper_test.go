// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	storesqlite "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// openMu serialises sqlite.Open calls across parallel tests; goose
// uses a package-level global that races otherwise. Mirrors the
// helper in internal/store/sqlite/testhelper_test.go.
var openMu sync.Mutex

// openTestDB opens (and migrates) a fresh file-backed SQLite database
// in t's temp directory and registers a cleanup to close it.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "matter.db") + "?_pragma=journal_mode(WAL)"
	openMu.Lock()
	db, err := storesqlite.Open(context.Background(), dsn)
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
