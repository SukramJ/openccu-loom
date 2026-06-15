// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// TestAuditStorePurge_DeletesOldKeepsRecent verifies that Purge removes rows
// whose timestamp is older than the retention window and leaves newer rows
// intact. The returned count must match the number of deleted rows.
func TestAuditStorePurge_DeletesOldKeepsRecent(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-200 * 24 * time.Hour) // 200 days ago — past the 90-day cutoff
	recent := time.Now().UTC()

	// Insert two old entries and one recent entry.
	for range 2 {
		if err := s.Append(ctx, audit.Entry{
			Timestamp: old,
			Action:    audit.ActionParamsetWrite,
		}); err != nil {
			t.Fatalf("append old: %v", err)
		}
	}
	if err := s.Append(ctx, audit.Entry{
		Timestamp: recent,
		Action:    audit.ActionLinkAdd,
	}); err != nil {
		t.Fatalf("append recent: %v", err)
	}

	n, err := s.Purge(ctx, 90)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 2 {
		t.Errorf("Purge returned %d, want 2", n)
	}

	remaining, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List after Purge: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining row, got %d", len(remaining))
	}
	if remaining[0].Action != audit.ActionLinkAdd {
		t.Errorf("unexpected remaining action: %v", remaining[0].Action)
	}
}

// TestAuditStorePurge_EmptyStoreReturnsZero verifies that calling Purge on an
// empty table returns 0 deleted rows and no error.
func TestAuditStorePurge_EmptyStoreReturnsZero(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	n, err := s.Purge(ctx, 90)
	if err != nil {
		t.Fatalf("Purge on empty store: %v", err)
	}
	if n != 0 {
		t.Errorf("Purge on empty store returned %d, want 0", n)
	}
}

// TestAuditStoreOpportunisticPurge verifies that inserting auditPurgeEveryNAppends
// entries after seeding one old entry triggers the opportunistic retention purge
// and removes the old row.
func TestAuditStoreOpportunisticPurge_TriggersOnThreshold(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-200 * 24 * time.Hour)

	// Seed one old entry directly.
	if err := s.Append(ctx, audit.Entry{
		Timestamp: old,
		Action:    audit.ActionParamsetWrite,
	}); err != nil {
		t.Fatalf("append old: %v", err)
	}

	// Reset the counter so we start from 0 (the seed above incremented it to 1).
	s.appendsSincePurge.Store(0)

	// Append exactly auditPurgeEveryNAppends recent entries.
	// The last append crosses the threshold and triggers Purge(ctx, auditRetentionDays).
	recent := time.Now().UTC()
	for range auditPurgeEveryNAppends {
		if err := s.Append(ctx, audit.Entry{
			Timestamp: recent,
			Action:    audit.ActionLinkAdd,
		}); err != nil {
			t.Fatalf("append recent: %v", err)
		}
	}

	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List after opportunistic purge: %v", err)
	}

	for _, e := range all {
		if e.Action == audit.ActionParamsetWrite {
			t.Errorf("old entry survived opportunistic purge: %+v", e)
		}
	}

	// All recent entries must still be present.
	if len(all) != auditPurgeEveryNAppends {
		t.Errorf("expected %d recent entries, got %d", auditPurgeEveryNAppends, len(all))
	}
}
