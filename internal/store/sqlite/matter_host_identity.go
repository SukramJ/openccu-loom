// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MatterHostIdentity is what the daemon states about its Matter node and
// keeps across restarts (migration 056): the pinned root SerialNumber and
// the node label it last applied from north.matter.node_label.
type MatterHostIdentity struct {
	RootSerial       string
	AppliedNodeLabel string
}

// LoadMatterHostIdentity returns the stored identity, seeding it with seed
// on first use.
//
// The insert is INSERT OR IGNORE into a single-row table followed by a read
// of that row, so the first call fixes the values and every later one
// returns them unchanged, whatever seed it passes. The daemon seeds with the
// serial it derived before the value was pinned and with the label
// configured at that start, so an installation that upgrades keeps both.
func LoadMatterHostIdentity(ctx context.Context, db *sql.DB, seed MatterHostIdentity) (MatterHostIdentity, error) {
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO matter_host_identity (singleton, root_serial, applied_node_label) VALUES (1, ?, ?)`,
		seed.RootSerial, seed.AppliedNodeLabel); err != nil {
		return MatterHostIdentity{}, fmt.Errorf("matter_host_identity: seed: %w", err)
	}
	var id MatterHostIdentity
	if err := db.QueryRowContext(ctx,
		`SELECT root_serial, applied_node_label FROM matter_host_identity WHERE singleton = 1`).
		Scan(&id.RootSerial, &id.AppliedNodeLabel); err != nil {
		return MatterHostIdentity{}, fmt.Errorf("matter_host_identity: read: %w", err)
	}
	return id, nil
}

// SetMatterAppliedNodeLabel records label as the configured node label last
// applied to the node. The row must exist ([LoadMatterHostIdentity] seeds
// it); a missing row is an error rather than a silent no-op, because the
// next start would then re-apply a label the node already serves.
func SetMatterAppliedNodeLabel(ctx context.Context, db *sql.DB, label string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE matter_host_identity SET applied_node_label = ? WHERE singleton = 1`, label)
	if err != nil {
		return fmt.Errorf("matter_host_identity: set applied node label: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("matter_host_identity: set applied node label: %w", err)
	} else if n != 1 {
		return errors.New("matter_host_identity: set applied node label: no identity row (seed it first)")
	}
	return nil
}
