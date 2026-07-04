// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

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
const connectionPragmas = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"

// FileDSN builds the canonical modernc.org/sqlite DSN for a file-backed
// database at path. Every caller that opens a daemon database must build its
// DSN through this helper so the connection pragmas — foreign_keys above all —
// apply uniformly across the whole connection pool rather than to whichever
// single connection happened to be primed at open time.
func FileDSN(path string) string {
	return "file:" + path + "?" + connectionPragmas
}
