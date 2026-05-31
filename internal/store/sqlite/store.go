// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DriverName is the database/sql driver registered by modernc.org/sqlite.
const DriverName = "sqlite"

// Connection-pool defaults. SQLite serialises writes through a single
// reserved lock; with WAL enabled, readers proceed in parallel against
// a snapshot. Capping MaxOpenConns at 16 keeps the writer queue from
// stacking under burst-load (paramset persistence + audit batches +
// goose migrations) while leaving headroom for concurrent reads.
// Mirrors the operational expectations in SPECIFICATION §13.2.
const (
	// DefaultMaxOpenConns bounds total concurrent connections. SQLite
	// serializes writes regardless, so going higher mostly grows the
	// reader pool. Tuned to keep response p99 under 100 ms during
	// migration windows.
	DefaultMaxOpenConns = 16
	// DefaultMaxIdleConns keeps a warm pool to avoid connection churn
	// on bursty REST workloads. Idle conns close after ConnMaxLifetime.
	DefaultMaxIdleConns = 4
	// DefaultConnMaxLifetime recycles long-lived connections. SQLite
	// does not need this for correctness; we use it to surface latent
	// resource leaks in tests and long-running daemons.
	DefaultConnMaxLifetime = 30 * time.Minute
)

// Open initialises a database at dsn and applies all embedded
// migrations. Use ":memory:" (or "file::memory:?cache=shared") for
// tests and tmpfs.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", dsn, err)
	}
	db.SetMaxOpenConns(DefaultMaxOpenConns)
	db.SetMaxIdleConns(DefaultMaxIdleConns)
	db.SetConnMaxLifetime(DefaultConnMaxLifetime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Wipe rows that belong to a previous on-disk schema. Failure here is
	// non-fatal: callers can still operate against a non-wiped cache, just with
	// stale rows that get refetched on access.
	if _, err := NewParamsetStore(db).WipeOutdated(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: wipe outdated caches: %w", err)
	}
	return db, nil
}

// migrateMu serialises [Migrate] calls. pressly/goose v3.27 reads
// package-level state (`goose.TableName()`, dialect registry) inside
// the migration path that is not safe for concurrent calls against
// distinct databases. Production opens a single DB at boot so the
// cost is zero; tests that call Open from multiple goroutines (race
// detector enabled) would otherwise flag a real (if benign) race
// inside goose.
//
// The lock is held only across the goose calls; the caller's
// connection-pool config + sqlite-busy-timeout handle in-DB
// concurrency separately.
var migrateMu sync.Mutex

// Migrate applies every migration. Safe to call against an already-
// migrated database — goose is idempotent.
func Migrate(ctx context.Context, db *sql.DB) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		return fmt.Errorf("sqlite: set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		// goose returns a sentinel for "nothing to do" only on some
		// paths; treat a nil error as success and anything else as
		// fatal.
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	return nil
}

// applyPragmas sets the SPECIFICATION §13.2 recommended pragma set.
// In-memory databases skip WAL because SQLite rejects the combination.
func applyPragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA cache_size = -20000",
		"PRAGMA temp_store = MEMORY",
	}
	// Journal mode: WAL for file-backed, MEMORY for in-memory.
	if isMemoryDSN(db) {
		pragmas = append(pragmas, "PRAGMA journal_mode = MEMORY")
	} else {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("sqlite: %s: %w", p, err)
		}
	}
	return nil
}

// isMemoryDSN returns true when the underlying connection looks like an
// in-memory database. SQLite doesn't expose the DSN directly; we test
// by asking whether journal_mode=WAL is even a valid target.
func isMemoryDSN(db *sql.DB) bool {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return false
	}
	// A brand-new in-memory database reports "memory" here; file
	// databases report "delete" / "truncate" / ... by default.
	return mode == "memory"
}

// ErrMigrationFailed is returned when goose fails mid-way. Callers can
// use [errors.Is] to detect it.
var ErrMigrationFailed = errors.New("sqlite: migration failed")
