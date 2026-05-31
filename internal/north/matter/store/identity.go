// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// IdentityRecord is the per-fabric node operational credential bundle.
// One identity per fabric: the bridge has exactly one operational node
// per fabric.
type IdentityRecord struct {
	// FabricIndex links this identity to matter_fabrics.fabric_index.
	FabricIndex uint8
	// NOC is the Node Operational Certificate bytes (Matter-cert TLV
	// per §11.18.5.2).
	NOC []byte
	// ICAC is the optional Intermediate CA certificate. nil means
	// the NOC is signed directly by the fabric root.
	ICAC []byte
	// PrivateKey is the 32-byte raw P-256 scalar matching the NOC's
	// public key. Persisted as-is; at-rest encryption is the
	// operator's responsibility for v1.1.
	PrivateKey []byte
	// IPK is the 16-byte Identity Protection Key for this fabric
	// (used in CASE Sigma).
	IPK []byte
}

// UpsertIdentity inserts or replaces the identity row for
// rec.FabricIndex. The fabric must already exist (FK constraint).
func (s *Store) UpsertIdentity(ctx context.Context, rec IdentityRecord) error {
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_node_identities
    (fabric_index, noc, icac, private_key, ipk)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(fabric_index) DO UPDATE SET
    noc         = excluded.noc,
    icac        = excluded.icac,
    private_key = excluded.private_key,
    ipk         = excluded.ipk`,
		rec.FabricIndex, rec.NOC, nullableBytes(rec.ICAC), rec.PrivateKey, rec.IPK,
	); err != nil {
		return fmt.Errorf("matter store: upsert identity: %w", err)
	}
	return nil
}

// GetIdentity returns the identity for fabricIndex. Returns
// [ErrIdentityNotFound] when no row exists.
func (s *Store) GetIdentity(ctx context.Context, fabricIndex uint8) (IdentityRecord, error) {
	var (
		rec  IdentityRecord
		icac sql.NullString
	)
	rec.FabricIndex = fabricIndex
	err := s.db.QueryRowContext(ctx, `
SELECT noc, icac, private_key, ipk FROM matter_node_identities
WHERE fabric_index = ?`, fabricIndex).Scan(&rec.NOC, &icac, &rec.PrivateKey, &rec.IPK)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentityRecord{}, ErrIdentityNotFound
	}
	if err != nil {
		return IdentityRecord{}, fmt.Errorf("matter store: get identity: %w", err)
	}
	if icac.Valid {
		rec.ICAC = []byte(icac.String)
	}
	return rec, nil
}

// nullableBytes turns a nil byte slice into a SQL NULL. modernc.org/sqlite
// stores []byte{} and NULL as distinct values, and we want NULL for
// "no ICAC".
func nullableBytes(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}
