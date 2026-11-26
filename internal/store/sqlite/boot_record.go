// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// BootRecord is what the daemon knows about why this process started
// (migration 057) and what an earlier process left it to finish
// (migration 058).
type BootRecord struct {
	// LastStopRequested reports that the previous process ended in a
	// graceful shutdown that followed an operator's restart request.
	LastStopRequested bool
	// MatterStartedVersion is the daemon version the Matter node last
	// started with; empty before the first such start.
	MatterStartedVersion string
	// ScrubPending reports that an erase of the Matter node removed its
	// rows but could not scrub their bytes out of the database file; the
	// boot runs the scrub.
	ScrubPending bool
}

// TakeStopRecordAtBoot moves the stop marker the previous process wrote into
// last_stop_requested and clears it. The daemon calls it once per boot,
// before anything reads the record, so a marker describes exactly one stop:
// a boot that does not read it (the Matter bridge off) still retires it.
func TakeStopRecordAtBoot(ctx context.Context, db *sql.DB) error {
	return updateBootRecord(ctx, db, "take stop record",
		`UPDATE boot_record SET last_stop_requested = stop_requested, stop_requested = 0 WHERE singleton = 1`)
}

// RecordStopRequested marks the shutdown in progress as one an operator
// requested. The daemon writes it at the end of a graceful shutdown, so a
// process that crashes or is killed leaves no marker.
func RecordStopRequested(ctx context.Context, db *sql.DB) error {
	return updateBootRecord(ctx, db, "record stop requested",
		`UPDATE boot_record SET stop_requested = 1 WHERE singleton = 1`)
}

// SetMatterStartedVersion records version as the one the Matter node
// started with.
func SetMatterStartedVersion(ctx context.Context, db *sql.DB, version string) error {
	return updateBootRecord(ctx, db, "set matter started version",
		`UPDATE boot_record SET matter_started_version = ? WHERE singleton = 1`, version)
}

// MarkScrubPending records that the database still has to be scrubbed: an
// erase of the Matter node removed its rows, but the scrub that removes
// their bytes from the file did not finish.
func MarkScrubPending(ctx context.Context, db *sql.DB) error {
	return updateBootRecord(ctx, db, "mark scrub pending",
		`UPDATE boot_record SET scrub_pending = 1 WHERE singleton = 1`)
}

// ClearScrubPending records that the pending scrub finished.
func ClearScrubPending(ctx context.Context, db *sql.DB) error {
	return updateBootRecord(ctx, db, "clear scrub pending",
		`UPDATE boot_record SET scrub_pending = 0 WHERE singleton = 1`)
}

// LoadBootRecord reads the record.
func LoadBootRecord(ctx context.Context, db *sql.DB) (BootRecord, error) {
	var (
		rec                BootRecord
		requested, pending int
	)
	if err := db.QueryRowContext(ctx,
		`SELECT last_stop_requested, matter_started_version, scrub_pending FROM boot_record WHERE singleton = 1`).
		Scan(&requested, &rec.MatterStartedVersion, &pending); err != nil {
		return BootRecord{}, fmt.Errorf("boot_record: read: %w", err)
	}
	rec.LastStopRequested = requested == 1
	rec.ScrubPending = pending == 1
	return rec, nil
}

// updateBootRecord runs one UPDATE on the single row. The migration inserts
// the row, so a statement that touches none is an error rather than a
// silent no-op. args are the statement's arguments, passed through to
// database/sql, whose API takes them as any.
func updateBootRecord(ctx context.Context, db *sql.DB, what, query string, args ...any) error {
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("boot_record: %s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("boot_record: %s: %w", what, err)
	}
	if n != 1 {
		return errors.New("boot_record: " + what + ": no record row")
	}
	return nil
}
