// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// ResumptionRecord is a CASE resumption-id row (Matter §4.13.2.4).
// One per (fabric, peer_node) pair; reused on every successful
// resumption.
//
// CASEAuthTags carries the set of CASE Authenticated Tags extracted
// from the peer's operational certificate (Matter §6.6.5.1 / Core
// §4.13.2.2). Tags allow the responder to grant fabric-scoped ACL
// privilege to resumed sessions without re-validating the full NOC.
// Persisted as JSON in the `case_authenticated_tags` column (migration
// 010). An empty or nil slice means no CATs were present.
// matter.js ref: packages/protocol/src/session/case/
type ResumptionRecord struct {
	FabricIndex  uint8
	PeerNodeID   uint64
	ResumptionID []byte   // 16 bytes
	SharedSecret []byte   // 32 bytes (HKDF input for resumed sessions)
	CASEAuthTags []uint32 // optional; nil == no CATs (Matter §6.6.5.1)
}

// ErrResumptionNotFound is returned when a lookup misses.
var ErrResumptionNotFound = errors.New("matter store: resumption record not found")

// UpsertResumption inserts or replaces the resumption row for
// (fabric_index, peer_node_id). CASEAuthTags is serialised as JSON;
// nil and empty slice both persist as '[]'.
func (s *Store) UpsertResumption(ctx context.Context, rec ResumptionRecord) error {
	if len(rec.ResumptionID) != 16 {
		return fmt.Errorf("matter store: resumption_id length=%d (want 16)", len(rec.ResumptionID))
	}
	if len(rec.SharedSecret) != 32 {
		return fmt.Errorf("matter store: shared_secret length=%d (want 32)", len(rec.SharedSecret))
	}
	cats := rec.CASEAuthTags
	if cats == nil {
		cats = []uint32{}
	}
	catsJSON, err := json.Marshal(cats)
	if err != nil {
		return fmt.Errorf("matter store: marshal case_authenticated_tags: %w", err)
	}
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_resumption
    (fabric_index, peer_node_id, resumption_id, shared_secret, case_authenticated_tags)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(fabric_index, peer_node_id) DO UPDATE SET
    resumption_id              = excluded.resumption_id,
    shared_secret              = excluded.shared_secret,
    case_authenticated_tags    = excluded.case_authenticated_tags,
    last_used_at               = CURRENT_TIMESTAMP`,
		rec.FabricIndex, uint64ToBE(rec.PeerNodeID), rec.ResumptionID, rec.SharedSecret, catsJSON,
	); err != nil {
		return fmt.Errorf("matter store: upsert resumption: %w", err)
	}
	return nil
}

// GetResumptionByID looks up a row by ResumptionID alone (the Sigma1
// path, where the responder receives the ID before it knows which
// fabric/peer it points at).
func (s *Store) GetResumptionByID(ctx context.Context, resumptionID []byte) (ResumptionRecord, error) {
	if len(resumptionID) != 16 {
		return ResumptionRecord{}, fmt.Errorf("matter store: resumption_id length=%d (want 16)", len(resumptionID))
	}
	var (
		rec      ResumptionRecord
		nodeBlob []byte
		catsJSON []byte
	)
	err := s.db.QueryRowContext(ctx, `
SELECT fabric_index, peer_node_id, resumption_id, shared_secret, case_authenticated_tags
FROM matter_resumption WHERE resumption_id = ?`, resumptionID).
		Scan(&rec.FabricIndex, &nodeBlob, &rec.ResumptionID, &rec.SharedSecret, &catsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ResumptionRecord{}, ErrResumptionNotFound
	}
	if err != nil {
		return ResumptionRecord{}, fmt.Errorf("matter store: get resumption: %w", err)
	}
	if len(nodeBlob) != 8 {
		return ResumptionRecord{}, fmt.Errorf("matter store: peer_node_id length=%d (want 8)", len(nodeBlob))
	}
	rec.PeerNodeID = binary.BigEndian.Uint64(nodeBlob)
	if err := json.Unmarshal(catsJSON, &rec.CASEAuthTags); err != nil {
		rec.CASEAuthTags = nil // tolerate corrupt rows
	}
	return rec, nil
}

// GetResumptionByPeer looks up by (fabric_index, peer_node_id).
func (s *Store) GetResumptionByPeer(ctx context.Context, fabricIndex uint8, peerNodeID uint64) (ResumptionRecord, error) {
	var (
		rec      ResumptionRecord
		nodeBlob []byte
		catsJSON []byte
	)
	err := s.db.QueryRowContext(ctx, `
SELECT fabric_index, peer_node_id, resumption_id, shared_secret, case_authenticated_tags
FROM matter_resumption WHERE fabric_index = ? AND peer_node_id = ?`,
		fabricIndex, uint64ToBE(peerNodeID)).
		Scan(&rec.FabricIndex, &nodeBlob, &rec.ResumptionID, &rec.SharedSecret, &catsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ResumptionRecord{}, ErrResumptionNotFound
	}
	if err != nil {
		return ResumptionRecord{}, fmt.Errorf("matter store: get resumption: %w", err)
	}
	if len(nodeBlob) != 8 {
		return ResumptionRecord{}, fmt.Errorf("matter store: peer_node_id length=%d (want 8)", len(nodeBlob))
	}
	rec.PeerNodeID = binary.BigEndian.Uint64(nodeBlob)
	if err := json.Unmarshal(catsJSON, &rec.CASEAuthTags); err != nil {
		rec.CASEAuthTags = nil // tolerate corrupt rows
	}
	return rec, nil
}

// RemoveResumption deletes a row. Idempotent.
func (s *Store) RemoveResumption(ctx context.Context, fabricIndex uint8, peerNodeID uint64) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM matter_resumption WHERE fabric_index = ? AND peer_node_id = ?`,
		fabricIndex, uint64ToBE(peerNodeID)); err != nil {
		return fmt.Errorf("matter store: remove resumption: %w", err)
	}
	return nil
}

// RemoveResumptionsByFabric deletes every resumption record bound to
// fabricIndex. Called from the OnFabricRemoved hook to clean up
// resumption state for a fabric that was just removed via RemoveFabric;
// mirrors chip src/credentials/FabricTable.cpp Delete() resumption
// teardown. Independent of the matter_fabrics FK so callers don't have
// to rely on SQLite's CASCADE behaviour for defense-in-depth.
func (s *Store) RemoveResumptionsByFabric(ctx context.Context, fabricIndex uint8) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM matter_resumption WHERE fabric_index = ?`, fabricIndex); err != nil {
		return fmt.Errorf("matter store: remove resumptions by fabric: %w", err)
	}
	return nil
}
