// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"fmt"
	"time"
)

// busyTimeout is how long SQLite holds a lock-blocked statement open before
// it gives up with SQLITE_BUSY. It is the single source for that value across
// the daemon's connection pool: [connectionPragmas] formats it into the DSN
// every pooled connection carries, applyPragmas formats it into the priming
// statement, and the health probe's own query deadline is derived from it
// (health_probe.go) so a lock wait the database is configured to tolerate is
// never reported as a probe failure.
//
// The one-shot read-only opens ([SchemaVersionOfFile], the backup tool's
// vacuum open) deliberately do not read this constant: they open an archived
// or being-backed-up file outside the pool, so their lock tolerance is an
// independent knob that happens to carry the same number today.
const busyTimeout = 5 * time.Second

// connectionPragmas are the modernc.org/sqlite `_pragma` query parameters the
// driver applies to *every* new connection it opens for a pool — not just the
// first. Connection-scoped pragmas must ride on the DSN because setting them
// once via ExecContext primes a single pooled connection and leaves the rest
// on their defaults.
//
// foreign_keys is the load-bearing one: SQLite defaults it to OFF and resets
// it per connection, so ON DELETE CASCADE silently no-ops on any connection
// that never ran the pragma. With a 16-connection pool that means orphaned
// child rows (matter node identities, group keys, ACL entries) survive a
// parent-fabric delete on ~15 of 16 connections. Pinning it on the DSN makes
// the whole pool enforce it. journal_mode and busy_timeout are carried here
// too so a file-backed database is fully configured the moment any connection
// opens, independent of the single-connection applyPragmas priming pass.
//
// synchronous, cache_size and temp_store are also connection-scoped and were
// missing here even though applyPragmas sets them: that primes only the one
// connection sql.DB happens to hand ExecContext, so 15 of a 16-connection
// pool ran on SQLite's compiled defaults (synchronous=FULL, a 2 MB cache,
// file-backed temp storage) instead of the SPECIFICATION §13.2 tuning —
// nearly every write paid an extra fsync and reads got a tenth of the
// intended page cache.
var connectionPragmas = fmt.Sprintf(
	"_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"+
		"&_pragma=synchronous(NORMAL)&_pragma=cache_size(-20000)&_pragma=temp_store(MEMORY)",
	busyTimeout.Milliseconds(),
)

// FileDSN builds the canonical modernc.org/sqlite DSN for a file-backed
// database at path. Every caller that opens a daemon database must build its
// DSN through this helper so the connection pragmas — foreign_keys above all —
// apply uniformly across the whole connection pool rather than to whichever
// single connection happened to be primed at open time.
func FileDSN(path string) string {
	return "file:" + path + "?" + connectionPragmas
}
