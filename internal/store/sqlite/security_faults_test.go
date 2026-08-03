// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

func freshSecurityFaultStore(t *testing.T) *SecurityFaultStore {
	t.Helper()
	return NewSecurityFaultStore(openTestDB(t, "security_faults.db"))
}

// baseSecurityFault builds a fault row. id is derived from (ref, reason)
// the same way the production faultID helper in internal/security does,
// so the tests exercise the identity scheme actually in use.
func baseSecurityFault(ref, reason string, sinceMS int64) SecurityFault {
	return SecurityFault{
		ID:             ref + "|" + reason,
		Ref:            ref,
		Class:          "technical",
		Reason:         reason,
		Severity:       "info",
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		DeviceAddress:  "ABC123",
		ChannelAddress: "ABC123:1",
		Parameter:      "UNREACH",
		Name:           "Sensor 1",
		SinceMS:        sinceMS,
	}
}

// TestSecurityFaultStoreRaiseOpensNewFault verifies the first Raise of a
// (ref, reason) pair inserts the row and reports opened=true.
func TestSecurityFaultStoreRaiseOpensNewFault(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	row := baseSecurityFault("ref-1", "unreachable", 1000)
	got, opened, err := s.Raise(ctx, row)
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if !opened {
		t.Error("Raise: want opened=true for a brand new fault")
	}
	if got.SinceMS != 1000 {
		t.Errorf("SinceMS = %d, want 1000", got.SinceMS)
	}
}

// TestSecurityFaultStoreRaiseSameRefReasonKeepsOriginalSinceMS verifies
// that raising the same (ref, reason) pair again while it is still open
// reports opened=false and keeps the original since_ms — a device that
// re-reports every few minutes must not reset the "broken since" clock.
func TestSecurityFaultStoreRaiseSameRefReasonKeepsOriginalSinceMS(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	first := baseSecurityFault("ref-1", "unreachable", 1000)
	if _, opened, err := s.Raise(ctx, first); err != nil || !opened {
		t.Fatalf("first Raise: opened=%v err=%v", opened, err)
	}

	second := baseSecurityFault("ref-1", "unreachable", 999999)
	effective, opened, err := s.Raise(ctx, second)
	if err != nil {
		t.Fatalf("second Raise: %v", err)
	}
	if opened {
		t.Error("second Raise: want opened=false, fault is still standing")
	}
	if effective.SinceMS != 1000 {
		t.Errorf("SinceMS = %d, want 1000 (the original raise time must survive)", effective.SinceMS)
	}
}

// TestSecurityFaultStoreClearReportsWhetherStanding verifies Clear
// reports true the first time a standing fault is closed and false on a
// repeated clear of the same (ref, reason).
func TestSecurityFaultStoreClearReportsWhetherStanding(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	row := baseSecurityFault("ref-1", "unreachable", 1000)
	if _, _, err := s.Raise(ctx, row); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	cleared, err := s.Clear(ctx, "ref-1", "unreachable", 2000)
	if err != nil {
		t.Fatalf("Clear 1: %v", err)
	}
	if !cleared {
		t.Error("Clear 1: want true, a fault was standing")
	}

	clearedAgain, err := s.Clear(ctx, "ref-1", "unreachable", 3000)
	if err != nil {
		t.Fatalf("Clear 2: %v", err)
	}
	if clearedAgain {
		t.Error("Clear 2: want false, nothing was standing anymore")
	}
}

// TestSecurityFaultStoreRaiseAfterClearOpensNewRow covers the design the
// partial index exists for: security_faults_open_unique is scoped to
// cleared_at_ms = 0 so a cleared fault stays in history while a fresh one
// opens for the same source.
//
// That only holds if the new row gets a distinct id. The caller derives
// it from (ref, reason, since) — the timestamp is what keeps a flapping
// condition from colliding with its own cleared row, which would make the
// fault silently stop reopening for as long as retention kept the old row.
// This test uses distinct ids for that reason; reusing one is asserted to
// fail below, so the caller's obligation stays visible here.
func TestSecurityFaultStoreRaiseAfterClearOpensNewRow(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	first := baseSecurityFault("ref-1", "unreachable", 1000)
	first.ID = "ref-1|unreachable|1000"
	if _, opened, err := s.Raise(ctx, first); err != nil || !opened {
		t.Fatalf("first Raise: opened=%v err=%v", opened, err)
	}
	if _, err := s.Clear(ctx, "ref-1", "unreachable", 2000); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	second := baseSecurityFault("ref-1", "unreachable", 5000)
	second.ID = "ref-1|unreachable|5000"
	got, opened, err := s.Raise(ctx, second)
	if err != nil || !opened {
		t.Fatalf("Raise after clear: opened=%v err=%v", opened, err)
	}
	if got.SinceMS != 5000 {
		t.Errorf("since_ms = %d, want the new occurrence at 5000", got.SinceMS)
	}
	open, err := s.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(open) != 1 || open[0].ID != second.ID {
		t.Fatalf("ListOpen = %+v, want exactly the re-opened fault", open)
	}

	// Reusing the id of the cleared row still collides — the primary key
	// is unconditional. This is why the caller stamps the id with the
	// open time rather than deriving it from (ref, reason) alone.
	if _, err := s.Clear(ctx, "ref-1", "unreachable", 6000); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
	dup := baseSecurityFault("ref-1", "unreachable", 7000)
	dup.ID = first.ID
	if _, _, err := s.Raise(ctx, dup); err == nil {
		t.Error("re-using a cleared row's id must collide on the primary key")
	}
}

// TestSecurityFaultStoreAcknowledgeSetsFieldsWithoutClearing verifies
// Acknowledge records the actor and timestamp but leaves the fault
// open, and returns false for an unknown id and for an id already
// acknowledged.
func TestSecurityFaultStoreAcknowledgeSetsFieldsWithoutClearing(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	row := baseSecurityFault("ref-1", "unreachable", 1000)
	if _, _, err := s.Raise(ctx, row); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	ok, err := s.Acknowledge(ctx, row.ID, 4000, "operator-a")
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if !ok {
		t.Fatal("Acknowledge: want true")
	}

	standing, found, err := s.OpenByRefReason(ctx, "ref-1", "unreachable")
	if err != nil {
		t.Fatalf("OpenByRefReason: %v", err)
	}
	if !found {
		t.Fatal("fault must still be open after acknowledgement")
	}
	if standing.AcknowledgedAt != 4000 {
		t.Errorf("AcknowledgedAt = %d, want 4000", standing.AcknowledgedAt)
	}
	if standing.AcknowledgedBy != "operator-a" {
		t.Errorf("AcknowledgedBy = %q, want operator-a", standing.AcknowledgedBy)
	}
	if standing.ClearedAtMS != 0 {
		t.Errorf("ClearedAtMS = %d, want 0 (acknowledgement never clears)", standing.ClearedAtMS)
	}

	if ok, err := s.Acknowledge(ctx, "unknown-id", 5000, "operator-b"); err != nil || ok {
		t.Errorf("Acknowledge(unknown id) = %v, %v, want false, nil", ok, err)
	}

	if ok, err := s.Acknowledge(ctx, row.ID, 6000, "operator-b"); err != nil || ok {
		t.Errorf("Acknowledge(already acknowledged) = %v, %v, want false, nil", ok, err)
	}
}

// TestSecurityFaultStoreClearByCentralClearsOnlyThatCentral verifies
// ClearByCentral closes only the open faults of the named central,
// leaving another central's faults standing.
func TestSecurityFaultStoreClearByCentralClearsOnlyThatCentral(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	rowA := baseSecurityFault("ccu1|if|ADDR1:1|UNREACH", "unreachable", 1000)
	rowA.CentralName = "ccu1"
	rowB := baseSecurityFault("ccu2|if|ADDR2:1|UNREACH", "unreachable", 1000)
	rowB.CentralName = "ccu2"
	if _, _, err := s.Raise(ctx, rowA); err != nil {
		t.Fatalf("Raise A: %v", err)
	}
	if _, _, err := s.Raise(ctx, rowB); err != nil {
		t.Fatalf("Raise B: %v", err)
	}

	n, err := s.ClearByCentral(ctx, "ccu1", 2000)
	if err != nil {
		t.Fatalf("ClearByCentral: %v", err)
	}
	if n != 1 {
		t.Errorf("ClearByCentral returned %d, want 1", n)
	}

	_, foundA, err := s.OpenByRefReason(ctx, rowA.Ref, rowA.Reason)
	if err != nil {
		t.Fatalf("OpenByRefReason A: %v", err)
	}
	if foundA {
		t.Error("ccu1 fault must be cleared")
	}

	_, foundB, err := s.OpenByRefReason(ctx, rowB.Ref, rowB.Reason)
	if err != nil {
		t.Fatalf("OpenByRefReason B: %v", err)
	}
	if !foundB {
		t.Error("ccu2 fault must remain open")
	}
}

// TestSecurityFaultStoreListOpenExcludesCleared verifies ListOpen
// returns only standing faults, oldest first.
func TestSecurityFaultStoreListOpenExcludesCleared(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	older := baseSecurityFault("ref-older", "unreachable", 1000)
	newer := baseSecurityFault("ref-newer", "unreachable", 2000)
	toClose := baseSecurityFault("ref-closed", "unreachable", 1500)
	for _, row := range []SecurityFault{older, newer, toClose} {
		if _, _, err := s.Raise(ctx, row); err != nil {
			t.Fatalf("Raise %s: %v", row.Ref, err)
		}
	}
	if _, err := s.Clear(ctx, toClose.Ref, toClose.Reason, 9000); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	got, err := s.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListOpen returned %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Ref != older.Ref || got[1].Ref != newer.Ref {
		t.Errorf("ListOpen order = [%s, %s], want [%s, %s] (oldest first)",
			got[0].Ref, got[1].Ref, older.Ref, newer.Ref)
	}
	for _, f := range got {
		if f.Ref == toClose.Ref {
			t.Error("ListOpen must not include the cleared fault")
		}
	}
}

// TestSecurityFaultStorePurgeClearedBeforeDeletesOnlyOldClearedRows
// verifies PurgeClearedBefore deletes cleared rows older than the
// cutoff, leaves recently-cleared rows and never touches open rows.
func TestSecurityFaultStorePurgeClearedBeforeDeletesOnlyOldClearedRows(t *testing.T) {
	s := freshSecurityFaultStore(t)
	ctx := context.Background()

	oldClosed := baseSecurityFault("ref-old-closed", "unreachable", 1000)
	recentClosed := baseSecurityFault("ref-recent-closed", "unreachable", 1000)
	stillOpen := baseSecurityFault("ref-open", "unreachable", 1000)
	for _, row := range []SecurityFault{oldClosed, recentClosed, stillOpen} {
		if _, _, err := s.Raise(ctx, row); err != nil {
			t.Fatalf("Raise %s: %v", row.Ref, err)
		}
	}
	if _, err := s.Clear(ctx, oldClosed.Ref, oldClosed.Reason, 1500); err != nil {
		t.Fatalf("Clear oldClosed: %v", err)
	}
	if _, err := s.Clear(ctx, recentClosed.Ref, recentClosed.Reason, 5000); err != nil {
		t.Fatalf("Clear recentClosed: %v", err)
	}

	n, err := s.PurgeClearedBefore(ctx, 3000)
	if err != nil {
		t.Fatalf("PurgeClearedBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeClearedBefore returned %d, want 1", n)
	}

	_, foundOld, err := s.OpenByRefReason(ctx, oldClosed.Ref, oldClosed.Reason)
	if err != nil {
		t.Fatalf("OpenByRefReason oldClosed: %v", err)
	}
	if foundOld {
		t.Error("a cleared fault must never satisfy OpenByRefReason")
	}
	all, err := s.query(ctx, securityFaultSelect)
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range all {
		seen[f.Ref] = true
	}
	if seen[oldClosed.Ref] {
		t.Error("oldClosed row must be purged")
	}
	if !seen[recentClosed.Ref] {
		t.Error("recentClosed row must survive (cleared_at_ms=5000 >= cutoff=3000)")
	}
	if !seen[stillOpen.Ref] {
		t.Error("stillOpen row must never be purged regardless of cutoff")
	}
}
