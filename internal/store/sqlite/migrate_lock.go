// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// migrationLockSuffix names the advisory lock file placed next to a database
// file. It sits alongside SQLite's own "-wal"/"-shm" sidecars and guards the
// migration run.
const migrationLockSuffix = "-migrate.lock"

// withMigrationLock runs fn while holding an exclusive, cross-process advisory
// lock on a lock file beside the database file. [migrateMu] only serialises
// callers inside one process; two independent openers of the same data
// directory (two daemons, or hmcli and the daemon) would otherwise both run a
// pending migration and the second would abort on a duplicate-column error.
// The OS file lock makes the second opener block until the first releases,
// rather than re-applying.
//
// In-memory databases have no backing file and cannot be shared across
// processes, so they skip the file lock and rely on [migrateMu] alone.
func withMigrationLock(ctx context.Context, db *sql.DB, fn func() error) error {
	dbFile, err := mainDatabaseFile(ctx, db)
	if err != nil {
		return err
	}
	if dbFile == "" {
		// In-memory / temporary database: no cross-process contention.
		return fn()
	}
	unlock, err := lockedfile.MutexAt(dbFile + migrationLockSuffix).Lock()
	if err != nil {
		return fmt.Errorf("sqlite: acquire migration lock for %q: %w", dbFile, err)
	}
	defer unlock()
	return fn()
}

// mainDatabaseFile returns the filesystem path backing the "main" database of
// db, or the empty string when it is an in-memory or temporary database
// (SQLite reports an empty file for those). It reads the path via
// PRAGMA database_list so no DSN needs to be threaded through the caller.
func mainDatabaseFile(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("sqlite: read database_list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			seq  int
			name string
			file string
		)
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", fmt.Errorf("sqlite: scan database_list: %w", err)
		}
		if name == "main" {
			return file, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sqlite: iterate database_list: %w", err)
	}
	return "", nil
}
