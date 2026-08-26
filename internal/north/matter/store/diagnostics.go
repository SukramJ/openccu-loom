// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store

import (
	"context"
	"fmt"
	"time"
)

// DiagnosticsRecord is the persisted counter pair the daemon seeds
// into the GeneralDiagnostics cluster on every boot. Mirrors
// migration 011_matter_diagnostics.sql.
type DiagnosticsRecord struct {
	// RebootCount is the number of fresh daemon starts since first
	// install. The daemon reads it, increments by 1, persists, and
	// then seeds it into the cluster — matching matter.js's
	// `Behavior.initialize` semantics.
	RebootCount uint16
	// BaseOperationalHours is the cumulative TotalOperationalHours
	// from prior process lifetimes. The cluster adds the current
	// process's uptime on every read; the daemon's shutdown hook
	// updates the row with `base + uptime_at_shutdown`.
	BaseOperationalHours uint32
	// UpdatedAt is set to time.Now() on every write.
	UpdatedAt time.Time
}

// LoadDiagnostics returns the singleton diagnostics row. Returns the
// zero record if the table is empty (first boot before migration
// seeded the row, or test fixtures that wipe the table).
func (s *Store) LoadDiagnostics(ctx context.Context) (DiagnosticsRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT reboot_count, base_operational_hours, updated_at FROM matter_diagnostics WHERE id = 1`)
	var (
		rebootCount uint16
		baseHours   uint32
		updatedAt   int64
	)
	if err := row.Scan(&rebootCount, &baseHours, &updatedAt); err != nil {
		return DiagnosticsRecord{}, fmt.Errorf("matter store: load diagnostics: %w", err)
	}
	return DiagnosticsRecord{
		RebootCount:          rebootCount,
		BaseOperationalHours: baseHours,
		UpdatedAt:            time.Unix(updatedAt, 0),
	}, nil
}

// SaveDiagnostics persists the current counter values into the
// singleton row. Daemon's shutdown hook calls this with the live
// rebootCount + (baseHours + uptime_seconds_at_shutdown / 3600).
func (s *Store) SaveDiagnostics(ctx context.Context, rec DiagnosticsRecord) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE matter_diagnostics
		 SET reboot_count = ?, base_operational_hours = ?, updated_at = ?
		 WHERE id = 1`,
		rec.RebootCount, rec.BaseOperationalHours, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("matter store: save diagnostics: %w", err)
	}
	return nil
}
