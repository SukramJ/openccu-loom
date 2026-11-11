// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
)

// upgradeStepRecordingOverrides names the one-time copy of the recording
// overrides out of the history database.
const upgradeStepRecordingOverrides = "recording_overrides_from_history_db"

// TakeOverRecordingOverrides copies the per-data-point recording overrides
// from the history database at historyPath into db, once.
//
// The overrides used to live beside the measurements they steered. The
// daemon no longer opens that database, so the operator's decisions are
// carried over on the first start that finds it. The step is recorded in
// the same transaction as the copy: a later start must not repeat it, or an
// override the operator has cleared since would come back.
//
// The history database is attached read-only and left untouched. A missing
// file, or one without the overrides table, records the step with nothing
// copied. It returns the number of rows taken over.
func TakeOverRecordingOverrides(ctx context.Context, db *sql.DB, historyPath string) (int64, error) {
	done, err := UpgradeStepDone(ctx, db, upgradeStepRecordingOverrides)
	if err != nil || done {
		return 0, err
	}
	_, statErr := os.Stat(historyPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return 0, fmt.Errorf("recording_overrides.takeover stat: %w", statErr)
	}
	haveFile := statErr == nil

	// ATTACH is per connection, so the whole step runs on one.
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("recording_overrides.takeover conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if haveFile {
		uri := (&url.URL{Scheme: "file", Path: historyPath, RawQuery: "mode=ro"}).String()
		if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS history_src`, uri); err != nil {
			return 0, fmt.Errorf("recording_overrides.takeover attach: %w", err)
		}
		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE history_src`) }()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("recording_overrides.takeover begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var copied int64
	if haveFile {
		var tables int
		if err := tx.QueryRowContext(ctx, `
            SELECT COUNT(*) FROM history_src.sqlite_master
             WHERE type = 'table' AND name = 'measurement_recording_overrides'
        `).Scan(&tables); err != nil {
			return 0, fmt.Errorf("recording_overrides.takeover probe: %w", err)
		}
		if tables > 0 {
			res, err := tx.ExecContext(ctx, `
                INSERT OR IGNORE INTO main.measurement_recording_overrides
                    (central_name, interface_id, channel_address, parameter, record, updated_by, updated_at)
                SELECT central_name, interface_id, channel_address, parameter, record, updated_by, updated_at
                  FROM history_src.measurement_recording_overrides
            `)
			if err != nil {
				return 0, fmt.Errorf("recording_overrides.takeover copy: %w", err)
			}
			copied, _ = res.RowsAffected()
		}
	}
	if err := markUpgradeStep(ctx, tx, upgradeStepRecordingOverrides); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("recording_overrides.takeover commit: %w", err)
	}
	return copied, nil
}
