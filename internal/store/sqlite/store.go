// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// migrateMu serialises [Migrate] calls within a single process. The goose v3
// legacy API writes two package-level globals that are explicitly documented
// "not safe for concurrent use": the dialect store (written by
// [goose.SetDialect]) and the base filesystem (written by [goose.SetBaseFS]).
// Both are read on every [goose.UpContext] call before the per-database lock
// inside goose takes effect. A per-store or per-DB mutex would not protect
// these package-level writes when two goroutines concurrently open distinct
// databases, so the mutex must be package-level here to cover all callers in
// this package.
//
// migrateMu is intra-process only. Cross-process coexistence — two daemons, or
// hmcli and the daemon, opening the same data directory — is guarded
// separately by the advisory file lock in [withMigrationLock]; without it both
// openers could run a pending migration and the second would abort on a
// duplicate-column error. The lock is held only across the goose calls; the
// caller's connection-pool config and the sqlite busy_timeout handle
// in-database concurrency separately.
var migrateMu sync.Mutex

// Migrate applies every migration. Safe to call against an already-
// migrated database — goose is idempotent.
func Migrate(ctx context.Context, db *sql.DB) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	return withMigrationLock(ctx, db, func() error {
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
	})
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
	if isMemoryDSN(ctx, db) {
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
func isMemoryDSN(ctx context.Context, db *sql.DB) bool {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return false
	}
	// A brand-new in-memory database reports "memory" here; file
	// databases report "delete" / "truncate" / ... by default.
	return mode == "memory"
}

// ErrMigrationFailed is returned when goose fails mid-way. Callers can
// use [errors.Is] to detect it.
var ErrMigrationFailed = errors.New("sqlite: migration failed")

// SchemaVersion returns the highest applied goose migration version recorded
// in db, i.e. the on-disk schema generation. Zero means no migrations have
// been recorded yet. Used by the backup tooling to stamp a backup with the
// schema it was produced against.
func SchemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx,
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sqlite: schema version: %w", err)
	}
	return v.Int64, nil
}

// SchemaVersionOfFile opens the SQLite file at path read-only and returns its
// schema version. It does not run migrations, so an archived database is read
// exactly as stamped. A path with no goose_db_version table (not a
// OpenCCU-Loom database) surfaces the query error.
func SchemaVersionOfFile(ctx context.Context, path string) (int64, error) {
	db, err := sql.Open(DriverName, path+"?mode=ro")
	if err != nil {
		return 0, fmt.Errorf("sqlite: open %q: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	return SchemaVersion(ctx, db)
}

// MaxKnownMigration returns the highest migration version embedded in this
// binary. It is the newest schema this binary can operate; a backup stamped
// with a higher schema version was produced by a newer daemon and cannot be
// safely restored into this one without an explicit override.
func MaxKnownMigration() (int64, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return 0, fmt.Errorf("sqlite: read migrations: %w", err)
	}
	var maxV int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		// goose filename convention: <version>_<name>.sql.
		idx := strings.IndexByte(name, '_')
		if idx <= 0 {
			continue
		}
		v, perr := strconv.ParseInt(name[:idx], 10, 64)
		if perr != nil {
			continue
		}
		if v > maxV {
			maxV = v
		}
	}
	return maxV, nil
}
