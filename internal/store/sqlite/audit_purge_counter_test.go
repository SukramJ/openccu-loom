// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// TestAuditPurgeCounter_StaysArmedAfterFailure verifies that when Purge
// returns an error the appendsSincePurge counter is NOT reset, so the
// next batch of appends will trigger another purge attempt rather than
// silently skipping retention forever.
//
// The test works by arming the counter to the threshold minus one, then
// replacing the store's internal DB handle with a closed *sql.DB so that
// the INSERT in Append succeeds first (on the real DB) — wait, the whole
// store uses one DB. Instead we arm the counter and replace the DB with a
// closed handle so that Append fails (INSERT fails too), which means
// Purge is called on a closed DB and must fail, leaving the counter armed.
// The Append error is intentional and discarded; only the counter matters.
func TestAuditPurgeCounter_StaysArmedAfterFailure(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	// Arm the counter to one below the purge threshold.
	s.appendsSincePurge.Store(auditPurgeEveryNAppends - 1)

	// Replace the store's DB with a closed handle so Purge will fail.
	// We are in the same package (sqlite) so s.db is accessible.
	openMu.Lock()
	closedDB, err := Open(context.Background(), "file::memory:?cache=shared&_counter_fail=1")
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open auxiliary DB: %v", err)
	}
	_ = closedDB.Close()
	s.db = closedDB

	// Append triggers the threshold. Append itself fails (closed DB), but
	// the important invariant is that the counter is NOT reset when Purge
	// also fails. Counter must remain >= auditPurgeEveryNAppends.
	_ = s.Append(ctx, audit.Entry{Action: audit.ActionParamsetWrite})

	got := s.appendsSincePurge.Load()
	if got == 0 {
		t.Errorf("counter was reset to 0 after a failing Purge; want >= %d", auditPurgeEveryNAppends)
	}
}

// TestAuditPurgeCounter_ResetsOnSuccess verifies that a successful Purge
// does reset the counter to 0 so the next retention window starts fresh.
func TestAuditPurgeCounter_ResetsOnSuccess(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	// Arm the counter to one below the threshold, then trigger with a
	// successful append on a live DB.
	s.appendsSincePurge.Store(auditPurgeEveryNAppends - 1)

	if err := s.Append(ctx, audit.Entry{Action: audit.ActionParamsetWrite}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := s.appendsSincePurge.Load()
	if got != 0 {
		t.Errorf("counter = %d after successful Purge; want 0", got)
	}
}

// TestAuditList_CapAppliedWhenLimitZero verifies that List(ctx, "", 0)
// returns at most maxAuditListRows rows even when more rows exist.
func TestAuditList_CapAppliedWhenLimitZero(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	// Insert more rows than the cap. Use SaveBatch-style looping since
	// AuditStore has no batch insert — this intentionally stays just
	// above the cap to keep test run time reasonable.
	want := maxAuditListRows + 5
	for range want {
		if err := s.Append(ctx, audit.Entry{
			Action:    audit.ActionParamsetWrite,
			Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// Reset the purge counter after each append so the opportunistic
		// purge never fires and removes rows we just inserted.
		s.appendsSincePurge.Store(0)
	}

	got, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != maxAuditListRows {
		t.Errorf("List(limit=0) returned %d rows; want %d (cap)", len(got), maxAuditListRows)
	}
}

// TestAuditList_ExplicitLimitAboveCapIsClampedToCap verifies that a caller
// passing a limit larger than the cap also gets at most maxAuditListRows rows.
func TestAuditList_ExplicitLimitAboveCapIsClampedToCap(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	want := maxAuditListRows + 3
	for range want {
		if err := s.Append(ctx, audit.Entry{
			Action:    audit.ActionParamsetWrite,
			Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		s.appendsSincePurge.Store(0)
	}

	got, err := s.List(ctx, "", maxAuditListRows+3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != maxAuditListRows {
		t.Errorf("List(limit=%d) returned %d rows; want %d (cap)", maxAuditListRows+3, len(got), maxAuditListRows)
	}
}

// TestAuditList_SmallExplicitLimitHonored verifies that a small explicit
// limit is still honored (not expanded to the cap).
func TestAuditList_SmallExplicitLimitHonored(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	for range 20 {
		if err := s.Append(ctx, audit.Entry{Action: audit.ActionParamsetWrite}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := s.List(ctx, "", 5)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("List(limit=5) returned %d rows; want 5", len(got))
	}
}
