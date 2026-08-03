// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// SecuritySource is an operator decision about one data point: an
// override of the automatic classification, an explicit inclusion of a
// source the classifier does not recognise, or an exclusion of one it
// recognises wrongly.
//
// A row exists only where the operator said something. Everything else
// is answered by the classifier, so the table stays small and its
// contents are all deliberate.
type SecuritySource struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	// Class overrides the classifier verdict; empty keeps it.
	Class string
	// Included = false removes the source from every aggregate.
	Included  bool
	Note      string
	UpdatedAt int64
}

// SecuritySourceStore persists the operator overrides.
type SecuritySourceStore struct {
	db *sql.DB
}

// NewSecuritySourceStore returns a store backed by db.
func NewSecuritySourceStore(db *sql.DB) *SecuritySourceStore { return &SecuritySourceStore{db: db} }

// Upsert writes an override, replacing any previous decision for the
// same data point.
func (s *SecuritySourceStore) Upsert(ctx context.Context, row SecuritySource) error {
	const q = `
INSERT INTO security_sources (central_name, interface_id, channel_address, parameter,
    class, included, note, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, interface_id, channel_address, parameter) DO UPDATE SET
    class = excluded.class,
    included = excluded.included,
    note = excluded.note,
    updated_at_ms = excluded.updated_at_ms`
	if _, err := s.db.ExecContext(ctx, q, row.CentralName, row.InterfaceID, row.ChannelAddress,
		row.Parameter, row.Class, boolToInt(row.Included), row.Note, row.UpdatedAt); err != nil {
		return fmt.Errorf("sqlite: upsert security source: %w", err)
	}
	return nil
}

// Delete drops an override, returning the data point to the
// classifier's verdict.
func (s *SecuritySourceStore) Delete(ctx context.Context, centralName, interfaceID, channelAddress, parameter string) error {
	const q = `
DELETE FROM security_sources
WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND parameter = ?`
	if _, err := s.db.ExecContext(ctx, q, centralName, interfaceID, channelAddress, parameter); err != nil {
		return fmt.Errorf("sqlite: delete security source: %w", err)
	}
	return nil
}

// GetAll returns every override. The set is small by construction, so
// the index builder loads it whole rather than querying per data point.
func (s *SecuritySourceStore) GetAll(ctx context.Context) ([]SecuritySource, error) {
	const q = `
SELECT central_name, interface_id, channel_address, parameter, class, included, note, updated_at_ms
FROM security_sources
ORDER BY central_name, channel_address, parameter`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list security sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SecuritySource
	for rows.Next() {
		var r SecuritySource
		var included int
		if err := rows.Scan(&r.CentralName, &r.InterfaceID, &r.ChannelAddress, &r.Parameter,
			&r.Class, &included, &r.Note, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan security source: %w", err)
		}
		r.Included = included != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
