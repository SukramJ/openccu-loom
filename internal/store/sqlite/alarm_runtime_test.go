// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

func freshAlarmRuntimeStore(t *testing.T) *AlarmRuntimeStore {
	t.Helper()
	return NewAlarmRuntimeStore(openTestDB(t, "alarm_runtime.db"))
}

// TestAlarmRuntimeStoreBootCountStartsAtZero verifies that a freshly
// migrated database has the seeded singleton row with boot_count == 0.
func TestAlarmRuntimeStoreBootCountStartsAtZero(t *testing.T) {
	s := freshAlarmRuntimeStore(t)
	ctx := context.Background()

	n, err := s.BootCount(ctx)
	if err != nil {
		t.Fatalf("BootCount: %v", err)
	}
	if n != 0 {
		t.Errorf("BootCount=%d want 0 on a fresh database", n)
	}
}

// TestAlarmRuntimeStoreIncrementBootCountSequence verifies
// IncrementBootCount returns 1 on the first call and 2 on the second, and
// that BootCount reflects the same value afterwards.
func TestAlarmRuntimeStoreIncrementBootCountSequence(t *testing.T) {
	s := freshAlarmRuntimeStore(t)
	ctx := context.Background()

	n1, err := s.IncrementBootCount(ctx, 1000)
	if err != nil {
		t.Fatalf("IncrementBootCount 1: %v", err)
	}
	if n1 != 1 {
		t.Errorf("IncrementBootCount 1 returned %d want 1", n1)
	}

	n2, err := s.IncrementBootCount(ctx, 2000)
	if err != nil {
		t.Fatalf("IncrementBootCount 2: %v", err)
	}
	if n2 != 2 {
		t.Errorf("IncrementBootCount 2 returned %d want 2", n2)
	}

	got, err := s.BootCount(ctx)
	if err != nil {
		t.Fatalf("BootCount: %v", err)
	}
	if got != 2 {
		t.Errorf("BootCount after two increments=%d want 2", got)
	}
}

// TestAlarmRuntimeStoreIncrementBootCountBumpsUpdatedAt verifies that each
// IncrementBootCount call writes the supplied nowMS into updated_at_ms.
func TestAlarmRuntimeStoreIncrementBootCountBumpsUpdatedAt(t *testing.T) {
	db := openTestDB(t, "alarm_runtime_updated_at.db")
	s := NewAlarmRuntimeStore(db)
	ctx := context.Background()

	if _, err := s.IncrementBootCount(ctx, 12345); err != nil {
		t.Fatalf("IncrementBootCount 1: %v", err)
	}
	var updatedAtMS int64
	if err := db.QueryRowContext(ctx, `SELECT updated_at_ms FROM alarm_runtime WHERE id = 1`).Scan(&updatedAtMS); err != nil {
		t.Fatalf("select updated_at_ms 1: %v", err)
	}
	if updatedAtMS != 12345 {
		t.Errorf("updated_at_ms=%d want 12345", updatedAtMS)
	}

	if _, err := s.IncrementBootCount(ctx, 67890); err != nil {
		t.Fatalf("IncrementBootCount 2: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT updated_at_ms FROM alarm_runtime WHERE id = 1`).Scan(&updatedAtMS); err != nil {
		t.Fatalf("select updated_at_ms 2: %v", err)
	}
	if updatedAtMS != 67890 {
		t.Errorf("updated_at_ms=%d want 67890 (must be bumped by the second call)", updatedAtMS)
	}
}
