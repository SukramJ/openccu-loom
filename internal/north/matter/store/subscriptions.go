// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// PersistentSubscriptionRecord is one row from matter_persistent_subscriptions.
// It carries the minimum state needed to re-arm a subscription after a
// daemon restart per Matter 1.4 §10.6.9.
type PersistentSubscriptionRecord struct {
	// ID is the auto-assigned row key; zero on insert (assigned by the DB).
	ID int64
	// FabricIndex is the operational fabric that owns the subscription.
	FabricIndex uint8
	// NodeID is the commissioner node ID.
	NodeID uint64
	// PathsJSON is the JSON-encoded list of attribute paths covered by
	// the subscription.  The encoding is opaque to the store layer —
	// the caller is responsible for marshalling / unmarshalling.
	PathsJSON string
	// IntervalsJSON is the JSON-encoded {min, max} cadence negotiated
	// during the original Subscribe handshake.
	IntervalsJSON string
}

// ErrPersistentSubscriptionNotFound is returned when a lookup misses.
var ErrPersistentSubscriptionNotFound = errors.New("matter store: persistent subscription not found")

// SavePersistentSubscription inserts one row and returns the assigned ID.
// Called when a new subscription is established so that a daemon restart
// can re-arm it.
func (s *Store) SavePersistentSubscription(ctx context.Context, rec PersistentSubscriptionRecord) (int64, error) {
	res, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_persistent_subscriptions
    (fabric_index, node_id, paths_json, intervals_json)
VALUES (?, ?, ?, ?)`,
		rec.FabricIndex,
		uint64ToBE(rec.NodeID),
		rec.PathsJSON,
		rec.IntervalsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("matter store: save persistent subscription: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("matter store: save persistent subscription: last insert id: %w", err)
	}
	return id, nil
}

// LoadPersistentSubscriptions returns every row from
// matter_persistent_subscriptions.  Called once at daemon start to
// re-arm subscriptions that survived a restart.  Rows are returned in
// ascending id order so the re-arm loop processes them deterministically.
func (s *Store) LoadPersistentSubscriptions(ctx context.Context) ([]PersistentSubscriptionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, fabric_index, node_id, paths_json, intervals_json
FROM matter_persistent_subscriptions
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("matter store: load persistent subscriptions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Close on a SELECT query is a cleanup-only call; the scan error is the actionable one

	var out []PersistentSubscriptionRecord
	for rows.Next() {
		var (
			rec      PersistentSubscriptionRecord
			nodeBlob []byte
		)
		if err := rows.Scan(
			&rec.ID,
			&rec.FabricIndex,
			&nodeBlob,
			&rec.PathsJSON,
			&rec.IntervalsJSON,
		); err != nil {
			return nil, fmt.Errorf("matter store: load persistent subscriptions: scan: %w", err)
		}
		if len(nodeBlob) != 8 {
			return nil, fmt.Errorf("matter store: load persistent subscriptions: node_id length=%d (want 8)", len(nodeBlob))
		}
		rec.NodeID = binary.BigEndian.Uint64(nodeBlob)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: load persistent subscriptions: rows: %w", err)
	}
	return out, nil
}

// DeletePersistentSubscription removes a single row by ID.  Called when
// a subscription is torn down so it is not re-armed on the next restart.
// Idempotent — no error if the row is already gone.
func (s *Store) DeletePersistentSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM matter_persistent_subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("matter store: delete persistent subscription: %w", err)
	}
	return nil
}

// DeletePersistentSubscriptionsByFabric removes all rows for fabricIndex.
// Called from the OnFabricRemoved hook so subscription records do not
// accumulate for fabrics that have been removed.
func (s *Store) DeletePersistentSubscriptionsByFabric(ctx context.Context, fabricIndex uint8) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM matter_persistent_subscriptions WHERE fabric_index = ?`, fabricIndex)
	if err != nil {
		return fmt.Errorf("matter store: delete persistent subscriptions by fabric: %w", err)
	}
	return nil
}

// GetPersistentSubscription returns a single row by ID.
// Returns ErrPersistentSubscriptionNotFound when the row does not exist.
func (s *Store) GetPersistentSubscription(ctx context.Context, id int64) (PersistentSubscriptionRecord, error) {
	var (
		rec      PersistentSubscriptionRecord
		nodeBlob []byte
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, fabric_index, node_id, paths_json, intervals_json
FROM matter_persistent_subscriptions WHERE id = ?`, id).
		Scan(&rec.ID, &rec.FabricIndex, &nodeBlob, &rec.PathsJSON, &rec.IntervalsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return PersistentSubscriptionRecord{}, ErrPersistentSubscriptionNotFound
	}
	if err != nil {
		return PersistentSubscriptionRecord{}, fmt.Errorf("matter store: get persistent subscription: %w", err)
	}
	if len(nodeBlob) != 8 {
		return PersistentSubscriptionRecord{}, fmt.Errorf("matter store: get persistent subscription: node_id length=%d (want 8)", len(nodeBlob))
	}
	rec.NodeID = binary.BigEndian.Uint64(nodeBlob)
	return rec, nil
}

// PersistentSubscriptionIntervals is the JSON shape of IntervalsJSON.
// Using a dedicated struct makes the marshal/unmarshal round-trip
// explicit and avoids map[string]any ambiguity.
type PersistentSubscriptionIntervals struct {
	// Min is the negotiated MinIntervalFloor in seconds.
	Min uint16 `json:"min"`
	// Max is the negotiated MaxIntervalCeiling in seconds.
	Max uint16 `json:"max"`
}

// MarshalIntervals serialises a cadence pair to the JSON string stored in
// IntervalsJSON.  Helper so callers don't have to import encoding/json.
func MarshalIntervals(minVal, maxVal uint16) (string, error) {
	b, err := json.Marshal(PersistentSubscriptionIntervals{Min: minVal, Max: maxVal})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalIntervals deserialises the string produced by MarshalIntervals.
func UnmarshalIntervals(s string) (PersistentSubscriptionIntervals, error) {
	var v PersistentSubscriptionIntervals
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return v, err
	}
	return v, nil
}
