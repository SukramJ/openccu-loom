// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// metadataKey is the KV key for the monotonically-advancing fabric-index
// counter. Mirrors matter.js FabricManager.ts #nextFabricIndex.
const metadataKeyNextFabricIndex = "next_fabric_index"

// getNextFabricIndexFromMetadata reads the persisted next_fabric_index
// counter from matter_metadata, or returns 1 as the default when the
// table/row is absent (supports databases migrated before migration 013).
func getNextFabricIndexFromMetadata(ctx context.Context, tx *sql.Tx) (uint8, error) {
	row := tx.QueryRowContext(ctx, `SELECT value FROM matter_metadata WHERE key = ?`, metadataKeyNextFabricIndex)
	var v int64
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 1, nil
		}
		return 0, fmt.Errorf("matter store: read next_fabric_index: %w", err)
	}
	if v < 1 || v > 254 {
		return 0, fmt.Errorf("matter store: next_fabric_index out of range: %d", v)
	}
	return uint8(v), nil //nolint:gosec // bounded by range check above
}

// bumpNextFabricIndex advances the next_fabric_index counter to the next
// free slot past candidate, wrapping 255→1, and persists the result in
// matter_metadata. The table row must already exist (inserted by
// migration 013).
//
// If the matter_metadata table is absent (pre-013 schema) the update
// is silently skipped — the scan-based fallback in nextFreeFabricIndex
// then takes over on the next call. This ensures backwards compatibility
// with databases that have not been migrated yet.
func bumpNextFabricIndex(ctx context.Context, tx *sql.Tx, occupied []uint8) error {
	// Build lookup set of occupied indices.
	occupiedSet := make(map[uint8]struct{}, len(occupied))
	for _, idx := range occupied {
		occupiedSet[idx] = struct{}{}
	}

	current, err := getNextFabricIndexFromMetadata(ctx, tx)
	if err != nil {
		return err
	}

	// Advance past the just-used slot + find the next free one,
	// wrapping at 255→1. Stop after a full cycle to detect full table.
	next := current
	for range 254 {
		// Advance.
		if next == 254 {
			next = 1
		} else {
			next++
		}
		if _, taken := occupiedSet[next]; !taken {
			break
		}
	}

	// If we wrapped all the way around, fall back — caller already
	// checked capacity.
	if _, taken := occupiedSet[next]; taken {
		// Table full; do not update the counter. AddFabric's capacity
		// check will have already returned ErrFabricExhausted.
		return nil
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO matter_metadata (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metadataKeyNextFabricIndex, int64(next),
	); err != nil {
		// If the table does not exist (pre-013 schema) treat as
		// non-fatal so old databases still work.
		return nil //nolint:nilerr // intentional: pre-013 compat
	}
	return nil
}
