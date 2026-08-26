// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package sqlite is the daemon's persistent store.
//
// It uses modernc.org/sqlite — a pure-Go SQLite implementation — so
// openccu-loom binaries stay CGO-free. goose-managed migrations live in
// the "migrations" subdirectory and are embedded into the binary, then
// applied at Open time.
//
// The store exposes one access type per table. Every access type takes
// a *sql.DB at construction and is safe for concurrent use. The
// schema is the canonical reference — see migrations/.
package sqlite
