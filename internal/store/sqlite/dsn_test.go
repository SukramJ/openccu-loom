// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestFileDSN_EnablesForeignKeysRaw proves the DSN alone carries
// foreign_keys=ON, independent of the single-connection applyPragmas priming
// pass: a raw sql.Open (which never runs applyPragmas) must still report the
// pragma enabled on the connection it hands out.
func TestFileDSN_EnablesForeignKeysRaw(t *testing.T) {
	t.Parallel()
	dsn := FileDSN(filepath.Join(t.TempDir(), "fk_raw.db"))

	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var fk int
	if err := db.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d on a DSN-only connection, want 1 (the DSN must enforce it)", fk)
	}
}

// TestOpen_ForeignKeysOnEveryPooledConnection is the regression guard for the
// audit finding: foreign_keys is a per-connection setting, so priming a single
// pooled connection via applyPragmas left the other ~15 connections with FK
// OFF, silently no-oping ON DELETE CASCADE. Holding several connections open at
// once forces the pool to hand out distinct physical connections; every one of
// them — not just the primed one — must report foreign_keys=1.
func TestOpen_ForeignKeysOnEveryPooledConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	openMu.Lock()
	db, err := Open(ctx, FileDSN(filepath.Join(t.TempDir(), "fk_pool.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Hold N connections simultaneously so the pool must open N distinct
	// physical connections; at most one of them could have been primed by
	// applyPragmas, so if all N report FK on, the DSN is enforcing it.
	const n = 6
	conns := make([]*sql.Conn, 0, n)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for i := range n {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire conn %d: %v", i, err)
		}
		conns = append(conns, c)

		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("conn %d PRAGMA foreign_keys: %v", i, err)
		}
		if fk != 1 {
			t.Fatalf("conn %d reports foreign_keys=%d, want 1 (a non-primed pooled connection has FK off)", i, fk)
		}
	}
}

// TestOpen_TuningPragmasOnEveryPooledConnection is the regression guard for
// the audit finding covering the other three connection-scoped pragmas:
// synchronous, cache_size and temp_store were only ever applied through
// applyPragmas' single ExecContext call, which primes one pooled connection
// and leaves the rest on SQLite's compiled defaults (synchronous=FULL, a
// 2 MB cache, file-backed temp storage) instead of the SPECIFICATION §13.2
// tuning. Holding several connections open at once forces the pool to hand
// out distinct physical connections; every one of them must report the
// tuned values, not just the one applyPragmas happened to prime.
func TestOpen_TuningPragmasOnEveryPooledConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	openMu.Lock()
	db, err := Open(ctx, FileDSN(filepath.Join(t.TempDir(), "tuning_pool.db")))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const n = 6
	conns := make([]*sql.Conn, 0, n)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for i := range n {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire conn %d: %v", i, err)
		}
		conns = append(conns, c)

		var synchronous int
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatalf("conn %d PRAGMA synchronous: %v", i, err)
		}
		if synchronous != 1 { // 1 = NORMAL, SQLite's compiled default is 2 = FULL
			t.Errorf("conn %d reports synchronous=%d, want 1 (NORMAL)", i, synchronous)
		}

		var cacheSize int
		if err := c.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize); err != nil {
			t.Fatalf("conn %d PRAGMA cache_size: %v", i, err)
		}
		if cacheSize != -20000 {
			t.Errorf("conn %d reports cache_size=%d, want -20000 (20 MB)", i, cacheSize)
		}

		var tempStore int
		if err := c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore); err != nil {
			t.Fatalf("conn %d PRAGMA temp_store: %v", i, err)
		}
		if tempStore != 2 { // 2 = MEMORY, SQLite's compiled default is 0 = file-backed
			t.Errorf("conn %d reports temp_store=%d, want 2 (MEMORY)", i, tempStore)
		}
	}
}
