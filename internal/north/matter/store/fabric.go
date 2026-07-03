// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
)

// FabricRecord mirrors the Fabric Descriptor from Matter Core Spec
// §11.18.5. fabric_index is stack-assigned (1..254); the remaining
// fields are commissioner-supplied or derived.
type FabricRecord struct {
	// FabricIndex is the stack-assigned identifier in [1, 254]. The
	// special value 0 means "not yet assigned" — callers passing a
	// zero index to [Store.AddFabric] receive a freshly allocated one.
	FabricIndex uint8
	// FabricID is the 64-bit fabric identifier set by the commissioner.
	FabricID uint64
	// NodeID is the 64-bit operational node identifier of this bridge
	// inside the fabric.
	NodeID uint64
	// RootPublicKey is the trust anchor — the issuing root CA public
	// key in uncompressed P-256 form (65 bytes, 0x04 prefix). Cached
	// alongside RootCert so the CASE sigma key lookup +
	// CompressedFabricID HKDF (Matter §4.13.2.4) can skip the cert
	// decode on every Sigma1.
	RootPublicKey []byte
	// RootCert is the full Matter Certificate TLV envelope received via
	// AddTrustedRootCertificate (Matter §11.18.7.12). Mirrors matter.js
	// `Fabric.rootCert` (Fabric.ts:68); served verbatim as each entry
	// of OperationalCredentials.TrustedRootCertificates
	// (matter.js OperationalCredentialsServer.ts:457-459). Apple Home
	// validates every entry as a Matter Certificate TLV and silently
	// drops the entire ReportData stream on schema mismatch.
	//
	// May be nil on legacy fabric rows persisted before the 012
	// migration; those fabrics are omitted from TrustedRootCertificates
	// rather than re-served as the old EC-pubkey-as-cert (which
	// triggers Bug I again).
	RootCert []byte
	// VendorID is the 16-bit IANA-assigned vendor identifier embedded
	// in the NOC.
	VendorID uint16
	// Label is the user-visible fabric name (max 32 utf-8 chars per
	// Matter spec; not enforced here — that's a cluster-server concern).
	Label string
	// CompressedID is the 8-byte compressed fabric identifier
	// (HKDF over RootPublicKey + FabricID per §4.13.2.4).
	CompressedID [8]byte
}

// AddFabric inserts a new fabric. If rec.FabricIndex is 0 the next
// free 1..254 slot is allocated; otherwise the caller's value is used
// (and the insert fails on collision).
//
// Returns the assigned fabric_index. Returns [ErrFabricExhausted] when
// no free slot exists.
//
// Index allocation follows the matter.js FabricManager #nextFabricIndex
// monotonic-counter strategy (FabricManager.ts:163-164,186-188): a persisted
// counter advances after each successful AddFabric so
// a freshly-removed index is not immediately reused — Apple Home caches
// fabric metadata by fabricIndex and may conflate old and new entries on
// rapid re-use. Falls back to the lowest-free-slot scan when the
// matter_metadata table is absent (pre-013 schema).
func (s *Store) AddFabric(ctx context.Context, rec FabricRecord) (uint8, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter store: add fabric: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	idx := rec.FabricIndex
	if idx == 0 {
		// Prefer the persisted counter; fall back to the lowest-free scan
		// when it returns the same index as an occupied slot (first call
		// after migration-013, or if the counter points at an occupied
		// slot due to manual inserts).
		candidate, cErr := nextFabricIndexFromCounter(ctx, tx)
		if cErr != nil {
			// Counter read failed — fall back to scan.
			candidate, cErr = nextFreeFabricIndex(ctx, tx)
			if cErr != nil {
				return 0, cErr
			}
		}
		idx = candidate
	}

	if _, err := tx.ExecContext(
		ctx, `
INSERT INTO matter_fabrics
    (fabric_index, fabric_id, node_id, root_public_key, root_cert, vendor_id, label, compressed_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		idx,
		uint64ToBE(rec.FabricID),
		uint64ToBE(rec.NodeID),
		rec.RootPublicKey,
		rec.RootCert,
		rec.VendorID,
		rec.Label,
		rec.CompressedID[:],
	); err != nil {
		return 0, fmt.Errorf("matter store: add fabric: insert: %w", err)
	}

	// Advance the persistent counter past the just-allocated index.
	// Best-effort: a failure here does not roll back the fabric insert
	// because the scan-based fallback still produces correct results.
	if rec.FabricIndex == 0 {
		// Collect currently-occupied indices (including the just-inserted
		// one) so bumpNextFabricIndex can skip them all.
		rows, _ := tx.QueryContext(ctx, `SELECT fabric_index FROM matter_fabrics ORDER BY fabric_index ASC`)
		var occupied []uint8
		if rows != nil {
			for rows.Next() {
				var fi int
				if scanErr := rows.Scan(&fi); scanErr == nil && fi >= 1 && fi <= 254 {
					occupied = append(occupied, uint8(fi)) //nolint:gosec // bounded by check; see #20
				}
			}
			_ = rows.Close()
		}
		_ = bumpNextFabricIndex(ctx, tx, occupied)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter store: add fabric: commit: %w", err)
	}
	return idx, nil
}

// GetFabric returns the fabric record for fabricIndex. Returns
// [ErrFabricNotFound] when no row exists.
func (s *Store) GetFabric(ctx context.Context, fabricIndex uint8) (FabricRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT fabric_index, fabric_id, node_id, root_public_key, root_cert, vendor_id, label, compressed_id
FROM matter_fabrics WHERE fabric_index = ?`, fabricIndex)
	rec, err := scanFabric(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FabricRecord{}, ErrFabricNotFound
	}
	if err != nil {
		return FabricRecord{}, fmt.Errorf("matter store: get fabric: %w", err)
	}
	return rec, nil
}

// ListFabrics returns every fabric ordered by fabric_index ascending.
func (s *Store) ListFabrics(ctx context.Context) ([]FabricRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT fabric_index, fabric_id, node_id, root_public_key, root_cert, vendor_id, label, compressed_id
FROM matter_fabrics ORDER BY fabric_index ASC`)
	if err != nil {
		return nil, fmt.Errorf("matter store: list fabrics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FabricRecord
	for rows.Next() {
		rec, err := scanFabric(rows)
		if err != nil {
			return nil, fmt.Errorf("matter store: list fabrics: scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list fabrics: rows: %w", err)
	}
	return out, nil
}

// UpdateFabricLabel rewrites the label for fabricIndex. Returns
// [ErrFabricNotFound] when no row exists.
func (s *Store) UpdateFabricLabel(ctx context.Context, fabricIndex uint8, label string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE matter_fabrics SET label = ?, updated_at = CURRENT_TIMESTAMP
WHERE fabric_index = ?`, label, fabricIndex)
	if err != nil {
		return fmt.Errorf("matter store: update fabric label: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("matter store: update fabric label: rows affected: %w", err)
	}
	if n == 0 {
		return ErrFabricNotFound
	}
	return nil
}

// UpdateFabricNodeID rewrites the operational NodeID for fabricIndex.
// UpdateNOC installs a NOC that may carry a new NodeID for the same
// fabric; the stored row must follow so destinationID resolution and
// the operational mDNS instance name (<compressedID>-<nodeID>) match
// the new certificate. Mirrors matter.js Fabric.ts:543 lifting nodeId
// from the new NOC into the rebuilt fabric. Returns
// [ErrFabricNotFound] when no row exists.
func (s *Store) UpdateFabricNodeID(ctx context.Context, fabricIndex uint8, nodeID uint64) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE matter_fabrics SET node_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE fabric_index = ?`, uint64ToBE(nodeID), fabricIndex)
	if err != nil {
		return fmt.Errorf("matter store: update fabric node id: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("matter store: update fabric node id: rows affected: %w", err)
	}
	if n == 0 {
		return ErrFabricNotFound
	}
	return nil
}

// RemoveFabric deletes the fabric and (via FK CASCADE) its
// node_identity, group_keys, group_key_map and acl_entries. Returns
// [ErrFabricNotFound] when no row exists.
func (s *Store) RemoveFabric(ctx context.Context, fabricIndex uint8) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM matter_fabrics WHERE fabric_index = ?`, fabricIndex)
	if err != nil {
		return fmt.Errorf("matter store: remove fabric: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("matter store: remove fabric: rows affected: %w", err)
	}
	if n == 0 {
		return ErrFabricNotFound
	}
	return nil
}

// nextFabricIndexFromCounter reads the persisted next_fabric_index counter
// from matter_metadata and verifies the candidate is actually free. If the
// counter points at an occupied slot it advances until it finds a free one.
// Returns [ErrFabricExhausted] when all 254 slots are taken.
// Mirrors matter.js FabricManager.ts:163-164 — allocate from #nextFabricIndex
// then bump.
func nextFabricIndexFromCounter(ctx context.Context, tx *sql.Tx) (uint8, error) {
	candidate, err := getNextFabricIndexFromMetadata(ctx, tx)
	if err != nil {
		return 0, err
	}

	// Collect occupied indices so we can skip them.
	rows, err := tx.QueryContext(ctx, `SELECT fabric_index FROM matter_fabrics ORDER BY fabric_index ASC`)
	if err != nil {
		return 0, fmt.Errorf("matter store: next fabric index (counter): list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	occupied := make(map[uint8]struct{}, 16)
	for rows.Next() {
		var got int
		if scanErr := rows.Scan(&got); scanErr != nil {
			return 0, fmt.Errorf("matter store: next fabric index (counter): scan: %w", scanErr)
		}
		if got >= 1 && got <= 254 {
			occupied[uint8(got)] = struct{}{} //nolint:gosec // bounded by check; see #20
		}
	}
	if rErr := rows.Err(); rErr != nil {
		return 0, fmt.Errorf("matter store: next fabric index (counter): rows: %w", rErr)
	}

	if len(occupied) >= 254 {
		return 0, ErrFabricExhausted
	}

	// Walk forward from candidate until we find a free slot, wrapping at 255→1.
	for range 254 {
		if _, taken := occupied[candidate]; !taken {
			return candidate, nil
		}
		if candidate == 254 {
			candidate = 1
		} else {
			candidate++
		}
	}
	return 0, ErrFabricExhausted
}

// nextFreeFabricIndex scans 1..254 for the lowest unused index in tx.
// Returns [ErrFabricExhausted] when all slots are taken.
func nextFreeFabricIndex(ctx context.Context, tx *sql.Tx) (uint8, error) {
	rows, err := tx.QueryContext(ctx, `SELECT fabric_index FROM matter_fabrics ORDER BY fabric_index ASC`)
	if err != nil {
		return 0, fmt.Errorf("matter store: next fabric index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	want := uint8(1)
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return 0, fmt.Errorf("matter store: next fabric index: scan: %w", err)
		}
		// CHECK constraint on fabric_index narrows got to [1, 254];
		// the cast is therefore safe.
		if got < 1 || got > 254 {
			return 0, fmt.Errorf("matter store: next fabric index: stored value %d out of range", got)
		}
		if uint8(got) != want { //nolint:gosec // bounded by the range check above; see #20
			return want, nil
		}
		if want == 254 {
			// occupied slot 254 — every smaller slot was contiguous
			// up to here. No room left.
			return 0, ErrFabricExhausted
		}
		want++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("matter store: next fabric index: rows: %w", err)
	}
	if want > 254 {
		return 0, ErrFabricExhausted
	}
	return want, nil
}

// scanRow is satisfied by both *sql.Row and *sql.Rows.
type scanRow interface {
	Scan(dest ...any) error
}

func scanFabric(r scanRow) (FabricRecord, error) {
	var (
		rec      FabricRecord
		fabricID []byte
		nodeID   []byte
		compID   []byte
	)
	if err := r.Scan(&rec.FabricIndex, &fabricID, &nodeID, &rec.RootPublicKey, &rec.RootCert, &rec.VendorID, &rec.Label, &compID); err != nil {
		return FabricRecord{}, err
	}
	if len(fabricID) != 8 {
		return FabricRecord{}, fmt.Errorf("matter store: fabric_id length=%d, want 8", len(fabricID))
	}
	if len(nodeID) != 8 {
		return FabricRecord{}, fmt.Errorf("matter store: node_id length=%d, want 8", len(nodeID))
	}
	if len(compID) != 8 {
		return FabricRecord{}, fmt.Errorf("matter store: compressed_id length=%d, want 8", len(compID))
	}
	rec.FabricID = binary.BigEndian.Uint64(fabricID)
	rec.NodeID = binary.BigEndian.Uint64(nodeID)
	copy(rec.CompressedID[:], compID)
	return rec, nil
}

func uint64ToBE(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
