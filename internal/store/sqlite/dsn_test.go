// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
