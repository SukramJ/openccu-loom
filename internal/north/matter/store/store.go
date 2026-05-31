// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"database/sql"
	"errors"
)

// Store is the typed access surface over the matter_* tables defined
// in migration 006. The DB connection is supplied by the shared
// sqlite layer; see internal/store/sqlite for the open path.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db. The DB must already have the
// matter_* tables — typically because [sqlite.Open] applied migration
// 006 at startup.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Sentinel errors. Callers use [errors.Is] to detect them.
var (
	// ErrFabricNotFound is returned when a fabric_index lookup misses.
	ErrFabricNotFound = errors.New("matter store: fabric not found")
	// ErrFabricExhausted is returned when [Store.AddFabric] runs out
	// of free fabric_index slots (1..254 are all taken).
	ErrFabricExhausted = errors.New("matter store: fabric index exhausted")
	// ErrIdentityNotFound is returned when [Store.GetIdentity] misses.
	ErrIdentityNotFound = errors.New("matter store: identity not found")
	// ErrGroupKeySetNotFound is returned when a (fabric, group_key_set_id)
	// lookup misses.
	ErrGroupKeySetNotFound = errors.New("matter store: group key set not found")
)
