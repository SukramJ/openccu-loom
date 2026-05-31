// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// incidents_a4_l17_test.go — end-to-end tests for the parity item:
// IncidentStore.RecordIncident satisfies reliability.IncidentRecorder and
// performs BumpIfRecent deduplication + retention-limit enforcement.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshIncidentStoreA4(t *testing.T) *IncidentStore {
	t.Helper()
	return NewIncidentStore(openTestDB(t, "inc_a4.db"))
}

// TestIncidentStoreRecordIncidentImplementsInterface verifies at
// compile-time that *IncidentStore satisfies reliability.IncidentRecorder.
// This is the contract that enables c.Cache.SetIncidentRecorder(recorder).
var _ reliability.IncidentRecorder = (*IncidentStore)(nil)

// TestIncidentStoreRecordIncidentRoundtrip verifies the full
// Record → Lookup roundtrip via the reliability.IncidentRecord shape.
func TestIncidentStoreRecordIncidentRoundtrip(t *testing.T) {
	t.Parallel()

	s := freshIncidentStoreA4(t)
	ctx := context.Background()

	inc := reliability.IncidentRecord{
		CentralName: "ccu1",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypeCircuitBreakerTripped,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "circuit-breaker tripped: closed → open",
		Details:     "rpc: timeout",
	}

	// First call must insert a new row.
	if err := s.RecordIncident(ctx, inc); err != nil {
		t.Fatalf("RecordIncident: %v", err)
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	got := list[0]
	if got.CentralName != "ccu1" {
		t.Errorf("CentralName=%q want ccu1", got.CentralName)
	}
	if got.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q want HmIP-RF", got.InterfaceID)
	}
	if got.Type != hmenum.IncidentTypeCircuitBreakerTripped {
		t.Errorf("Type=%q want %q", got.Type, hmenum.IncidentTypeCircuitBreakerTripped)
	}
	if got.Severity != hmenum.IncidentSeverityError {
		t.Errorf("Severity=%q want %q", got.Severity, hmenum.IncidentSeverityError)
	}
	if got.Message != inc.Message {
		t.Errorf("Message=%q want %q", got.Message, inc.Message)
	}
	if got.Details != inc.Details {
		t.Errorf("Details=%q want %q", got.Details, inc.Details)
	}
}

// TestIncidentStoreRecordIncidentDeduplicatesWithinWindow verifies that
// calling RecordIncident twice for the same (central, interface, type,
// severity, message) within the 5-minute dedup window produces exactly
// one row with count=2 rather than two separate rows.
func TestIncidentStoreRecordIncidentDeduplicatesWithinWindow(t *testing.T) {
	t.Parallel()

	s := freshIncidentStoreA4(t)
	ctx := context.Background()

	inc := reliability.IncidentRecord{
		CentralName: "ccu1",
		InterfaceID: "BidCos-RF",
		Type:        hmenum.IncidentTypePingPongMismatch,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "ping/pong: pending TTL exceeded",
	}

	for i := range 3 {
		if err := s.RecordIncident(ctx, inc); err != nil {
			t.Fatalf("RecordIncident call %d: %v", i, err)
		}
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("row count=%d want 1 (dedup must merge repeats)", len(list))
	}
	if list[0].Count != 3 {
		t.Errorf("count=%d want 3 (1 insert + 2 bumps)", list[0].Count)
	}
}

// TestIncidentStoreRecordIncidentDifferentMessageCreatesNewRow verifies
// that two incidents with distinct messages both produce their own rows
// even within the 5-minute dedup window.
func TestIncidentStoreRecordIncidentDifferentMessageCreatesNewRow(t *testing.T) {
	t.Parallel()

	s := freshIncidentStoreA4(t)
	ctx := context.Background()

	base := reliability.IncidentRecord{
		CentralName: "ccu1",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypeConnectionLost,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "first message",
	}
	second := base
	second.Message = "second message"

	if err := s.RecordIncident(ctx, base); err != nil {
		t.Fatalf("RecordIncident base: %v", err)
	}
	if err := s.RecordIncident(ctx, second); err != nil {
		t.Fatalf("RecordIncident second: %v", err)
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("row count=%d want 2 (different messages → different rows)", len(list))
	}
}

// TestIncidentStoreRecordIncidentMultiCCUIsolation verifies that incidents
// recorded for different centrals remain isolated and do not cross-contaminate.
func TestIncidentStoreRecordIncidentMultiCCUIsolation(t *testing.T) {
	t.Parallel()

	s := freshIncidentStoreA4(t)
	ctx := context.Background()

	for _, ccu := range []string{"alpha", "beta"} {
		if err := s.RecordIncident(ctx, reliability.IncidentRecord{
			CentralName: ccu,
			InterfaceID: "HmIP-RF",
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     ccu + " fault",
		}); err != nil {
			t.Fatalf("RecordIncident %s: %v", ccu, err)
		}
	}

	for _, ccu := range []string{"alpha", "beta"} {
		list, err := s.Recent(ctx, ccu, 10)
		if err != nil {
			t.Fatalf("Recent %s: %v", ccu, err)
		}
		if len(list) != 1 {
			t.Fatalf("%s: len=%d want 1", ccu, len(list))
		}
		if list[0].Message != ccu+" fault" {
			t.Errorf("%s: message=%q, cross-contamination?", ccu, list[0].Message)
		}
	}
}
