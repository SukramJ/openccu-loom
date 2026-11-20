// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// InstanceID returns this installation's identity, creating it on first use.
//
// The id is 32 lower-case hex digits from crypto/rand. The insert is
// INSERT OR IGNORE into a single-row table, followed by a read of that row,
// so a concurrent first call and every later one return the stored id; a
// freshly drawn id that lost the race is discarded.
//
// The id names the installation; it is not a secret and proves nothing. It
// is announced over mDNS and served on GET /api/v1/info so a client can tell
// one daemon from another serving the same CCU.
func InstanceID(ctx context.Context, db *sql.DB) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("instance_identity: draw id: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO instance_identity (singleton, instance_id, created_at) VALUES (1, ?, ?)`,
		hex.EncodeToString(raw[:]), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("instance_identity: create: %w", err)
	}
	var id string
	if err := db.QueryRowContext(ctx,
		`SELECT instance_id FROM instance_identity WHERE singleton = 1`).Scan(&id); err != nil {
		return "", fmt.Errorf("instance_identity: read: %w", err)
	}
	return id, nil
}
