// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// metadataKeyNextEndpointID is the KV key for the monotonically-advancing
// bridged-endpoint-number counter. Mirrors matter.js
// packages/node/src/storage/server/ServerEndpointStores.ts NEXT_NUMBER_KEY
// (the persisted form of #nextNumber).
//
// The row lives in matter_metadata (migration 013), a table the bridge
// module also uses for its own protocol-generic counter under a
// different key. The two never collide and the table is untouched by
// this split; only the row identified here belongs to endpoint identity.
const metadataKeyNextEndpointID = "next_endpoint_id"

// getNextEndpointIDFromMetadata reads the persisted next_endpoint_id counter.
// ok=false when the row is absent (a database migrated before migration 036)
// or holds a value outside the bridged range — both cases make the caller
// reseed from the endpoint table. matter.js validates the loaded number the
// same way, "for the off-chance it somehow gets corrupted"
// (ServerEndpointStores.ts load).
func getNextEndpointIDFromMetadata(ctx context.Context, tx *sql.Tx) (id uint16, ok bool, err error) {
	row := tx.QueryRowContext(ctx, `SELECT value FROM matter_metadata WHERE key = ?`, metadataKeyNextEndpointID)
	var v int64
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("matter endpoint store: read next_endpoint_id: %w", err)
	}
	if v < firstBridgedEndpointID || v > lastBridgedEndpointID {
		return 0, false, nil
	}
	return uint16(v), true, nil //nolint:gosec // bounded by the range check above
}

// setNextEndpointID persists the bridged-endpoint-number counter inside tx, so
// the allocation and its high-water mark commit together.
func setNextEndpointID(ctx context.Context, tx *sql.Tx, next uint16) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO matter_metadata (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metadataKeyNextEndpointID, int64(next),
	); err != nil {
		return fmt.Errorf("matter endpoint store: write next_endpoint_id: %w", err)
	}
	return nil
}
