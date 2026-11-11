// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpgradeStepDone reports whether the named one-time upgrade step has been
// recorded. A step is work the daemon does once outside a migration because
// it needs something a migration cannot reach, such as a second database
// file.
func UpgradeStepDone(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM upgrade_steps WHERE name = ?`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("upgrade_steps.Done %q: %w", name, err)
	}
	return true, nil
}

// markUpgradeStep records the named step inside tx, so the record commits
// with the work it stands for or not at all.
func markUpgradeStep(ctx context.Context, tx *sql.Tx, name string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO upgrade_steps (name, done_at) VALUES (?, ?)`,
		name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upgrade_steps.Mark %q: %w", name, err)
	}
	return nil
}
