// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// AlarmRuntimeStore persists the alarm engine's singleton runtime row
// (boot counter). Persisted timer tuples reference the boot counter so
// a restore can tell same-boot re-reads from genuine restarts.
type AlarmRuntimeStore struct {
	db *sql.DB
}

// NewAlarmRuntimeStore returns a store backed by db.
func NewAlarmRuntimeStore(db *sql.DB) *AlarmRuntimeStore { return &AlarmRuntimeStore{db: db} }

// IncrementBootCount bumps the boot counter once per engine start and
// returns the new value.
func (s *AlarmRuntimeStore) IncrementBootCount(ctx context.Context, nowMS int64) (int64, error) {
	const q = `UPDATE alarm_runtime SET boot_count = boot_count + 1, updated_at_ms = ? WHERE id = 1`
	if _, err := s.db.ExecContext(ctx, q, nowMS); err != nil {
		return 0, fmt.Errorf("sqlite: increment alarm boot count: %w", err)
	}
	return s.BootCount(ctx)
}

// BootCount returns the current boot counter.
func (s *AlarmRuntimeStore) BootCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT boot_count FROM alarm_runtime WHERE id = 1`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: get alarm boot count: %w", err)
	}
	return n, nil
}
