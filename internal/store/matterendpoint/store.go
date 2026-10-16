// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"database/sql"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/endpoint/sqlitestore"
)

// Store is the typed access surface over matter_endpoints and
// matter_exposures. The DB connection is supplied by the shared sqlite
// layer the daemon owns; this package never opens a database.
//
// Endpoint identity is go-fabric's sqlitestore under this daemon's key
// type ([DecodeSourceKey]); the exposure allowlist is this package's own,
// because it is keyed by the 5-tuple the operator edits through REST.
type Store struct {
	db        *sql.DB
	endpoints *sqlitestore.Store
}

// New returns a Store backed by db. The DB must already carry the
// matter_* tables — in production because [sqlite.Open] applied the
// migrations at startup, 049 among them for sqlitestore's schema.
func New(db *sql.DB) *Store {
	return &Store{
		db:        db,
		endpoints: sqlitestore.New(db, sqlitestore.WithKeyDecoder(DecodeSourceKey)),
	}
}

// Store implements the bridge module's endpoint-identity port; the
// module never learns what a channel is.
var _ fabricendpoint.Store = (*Store)(nil)
