// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"database/sql"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
)

// Store is the typed access surface over matter_endpoints and
// matter_exposures. The DB connection is supplied by the shared sqlite
// layer the daemon owns; this package never opens a database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db. The DB must already carry the
// matter_* tables — in production because [sqlite.Open] applied
// migrations 007 / 009 / 013 / 036 at startup.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Store implements the bridge module's endpoint-identity port; the
// module never learns what a channel is.
var _ fabricendpoint.Store = (*Store)(nil)
