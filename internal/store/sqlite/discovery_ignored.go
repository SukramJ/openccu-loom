// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ErrDiscoveryStoreUnavailable is returned by mutating methods when the store
// has no database handle (defensive — the daemon always wires a real one).
var ErrDiscoveryStoreUnavailable = errors.New("sqlite: discovery ignore store unavailable")

// DiscoveryIgnoreStore persists the SSDP-discovered central units the operator
// chose to hide. Keyed by the discovery serial. The discovery surface filters
// these out so an unwanted CCU stops reappearing on every scan; an "ignored"
// management view can list them and the operator can un-ignore one to bring it
// back.
type DiscoveryIgnoreStore struct {
	db *sql.DB
}

// NewDiscoveryIgnoreStore returns a store backed by db.
func NewDiscoveryIgnoreStore(db *sql.DB) *DiscoveryIgnoreStore {
	return &DiscoveryIgnoreStore{db: db}
}

// Close releases the underlying database handle. Safe on a nil store / handle.
func (s *DiscoveryIgnoreStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// IgnoredCCU is one persisted ignore row.
type IgnoredCCU struct {
	Serial    string    `json:"serial"`
	Name      string    `json:"name,omitempty"`
	Host      string    `json:"host,omitempty"`
	IgnoredAt time.Time `json:"ignored_at"`
	IgnoredBy string    `json:"ignored_by,omitempty"`
}

// Add records (or refreshes) an ignore decision for one CCU. Upsert on serial.
func (s *DiscoveryIgnoreStore) Add(ctx context.Context, e IgnoredCCU) error {
	if s == nil || s.db == nil {
		return ErrDiscoveryStoreUnavailable
	}
	if e.IgnoredAt.IsZero() {
		e.IgnoredAt = time.Now().UTC()
	}
	const q = `INSERT INTO discovery_ignored_ccus (serial, name, host, ignored_at, ignored_by)
               VALUES (?, ?, ?, ?, ?)
               ON CONFLICT(serial) DO UPDATE SET
                 name = excluded.name, host = excluded.host,
                 ignored_at = excluded.ignored_at, ignored_by = excluded.ignored_by`
	if _, err := s.db.ExecContext(ctx, q, e.Serial, e.Name, e.Host,
		e.IgnoredAt.UTC().Format(time.RFC3339Nano), e.IgnoredBy); err != nil {
		return fmt.Errorf("sqlite: add discovery_ignored_ccus: %w", err)
	}
	return nil
}

// Remove un-ignores a CCU. Returns true when a row was deleted.
func (s *DiscoveryIgnoreStore) Remove(ctx context.Context, serial string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrDiscoveryStoreUnavailable
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM discovery_ignored_ccus WHERE serial = ?`, serial)
	if err != nil {
		return false, fmt.Errorf("sqlite: remove discovery_ignored_ccus: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// List returns every ignored CCU, sorted by name then serial.
func (s *DiscoveryIgnoreStore) List(ctx context.Context) ([]IgnoredCCU, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	const q = `SELECT serial, name, host, ignored_at, ignored_by FROM discovery_ignored_ccus`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list discovery_ignored_ccus: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []IgnoredCCU
	for rows.Next() {
		var e IgnoredCCU
		var tsRaw string
		if err := rows.Scan(&e.Serial, &e.Name, &e.Host, &tsRaw, &e.IgnoredBy); err != nil {
			return nil, fmt.Errorf("sqlite: scan discovery_ignored_ccus: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, tsRaw); err == nil {
			e.IgnoredAt = t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate discovery_ignored_ccus: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Serial < out[j].Serial
	})
	return out, nil
}

// IgnoredSerials returns the set of ignored serials for fast filtering of the
// discovery surface. Returns an empty (non-nil) set when nothing is ignored.
func (s *DiscoveryIgnoreStore) IgnoredSerials(ctx context.Context) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if s == nil || s.db == nil {
		return set, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT serial FROM discovery_ignored_ccus`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list ignored serials: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, fmt.Errorf("sqlite: scan ignored serial: %w", err)
		}
		set[serial] = struct{}{}
	}
	return set, rows.Err()
}
