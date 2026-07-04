// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// TestMigrate_CrossProcessLockBlocksSecondOpener is the regression guard for
// the second audit finding: migrateMu only serialises callers inside one
// process, so two daemons (or hmcli + the daemon) opening the same data dir
// could both run a pending migration and the second would abort. Migrate must
// therefore block on a cross-process advisory file lock. Simulating the "other
// process" by holding the lock file directly, a concurrent Migrate must not
// proceed until the lock is released.
func TestMigrate_CrossProcessLockBlocksSecondOpener(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "openccu-loom.db")

	// First open runs the migrations and leaves a fully-migrated database.
	openMu.Lock()
	db, err := Open(ctx, FileDSN(dbPath))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Stand in for a second process by grabbing the same advisory lock the
	// migration run acquires.
	unlockOther, err := lockedfile.MutexAt(dbPath + migrationLockSuffix).Lock()
	if err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}

	// A concurrent Migrate (idempotent, but still takes the lock) must block
	// while the lock is held.
	done := make(chan error, 1)
	go func() { done <- Migrate(ctx, db) }()

	select {
	case err := <-done:
		unlockOther()
		t.Fatalf("Migrate returned (%v) while the cross-process lock was held; it did not block", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: Migrate is blocked on the lock.
	}

	// Release the "other process" lock; Migrate must now complete.
	unlockOther()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Migrate after unlock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Migrate did not proceed after the cross-process lock was released")
	}
}

// TestMainDatabaseFile reports the backing file for a file database and the
// empty string for an in-memory one, so [withMigrationLock] can skip the file
// lock for databases that cannot be shared across processes.
func TestMainDatabaseFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "main_db_file.db")
	openMu.Lock()
	fileDB, err := Open(ctx, FileDSN(dbPath))
	openMu.Unlock()
	if err != nil {
		t.Fatalf("Open file db: %v", err)
	}
	t.Cleanup(func() { _ = fileDB.Close() })

	got, err := mainDatabaseFile(ctx, fileDB)
	if err != nil {
		t.Fatalf("mainDatabaseFile(file): %v", err)
	}
	// SQLite reports the fully symlink-resolved absolute path (on macOS
	// t.TempDir()'s /var/... resolves to /private/var/...). Comparing the
	// resolved forms both confirms the path and documents that two openers
	// spelling the same database differently converge on one lock file.
	wantResolved, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(dbPath): %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got=%q): %v", got, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("mainDatabaseFile(file)=%q (resolved %q), want %q", got, gotResolved, wantResolved)
	}

	memDB, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { _ = memDB.Close() })
	memFile, err := mainDatabaseFile(ctx, memDB)
	if err != nil {
		t.Fatalf("mainDatabaseFile(memory): %v", err)
	}
	if memFile != "" {
		t.Fatalf("mainDatabaseFile(memory)=%q, want empty string", memFile)
	}
}
