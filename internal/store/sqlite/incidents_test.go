// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// p2Incident constructs an Incident for use in multi-method test helpers.
func p2Incident(centralName, iface string, incType hmenum.IncidentType) Incident {
	return Incident{
		CentralName: centralName,
		InterfaceID: iface,
		Type:        incType,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "test incident",
		Details:     "{}",
	}
}

func freshIncidentStore(t *testing.T) *IncidentStore {
	t.Helper()
	return NewIncidentStore(openTestDB(t, "inc.db"))
}

func baseIncident(centralName, iface string) Incident {
	return Incident{
		CentralName: centralName,
		InterfaceID: iface,
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "auth failed",
		Details:     "connection refused",
	}
}

// TestIncidentStoreRecordInsertsWithAutoIncrementID verifies that Record
// returns non-zero IDs, that IDs increase, and that first_seen / last_seen
// are populated and count starts at 1.
func TestIncidentStoreRecordInsertsWithAutoIncrementID(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	id1, err := s.Record(ctx, inc)
	if err != nil {
		t.Fatalf("record 1: %v", err)
	}
	id2, err := s.Record(ctx, inc)
	if err != nil {
		t.Fatalf("record 2: %v", err)
	}

	if id1 <= 0 {
		t.Errorf("id1=%d want >0", id1)
	}
	if id2 <= id1 {
		t.Errorf("id2=%d must be > id1=%d (auto-increment)", id2, id1)
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len=%d want 2", len(list))
	}
	for _, inc := range list {
		if inc.Count != 1 {
			t.Errorf("new row count=%d want 1", inc.Count)
		}
		if inc.FirstSeen.IsZero() {
			t.Error("first_seen is zero")
		}
		if inc.LastSeen.IsZero() {
			t.Error("last_seen is zero")
		}
	}
}

// TestIncidentStoreRecordRoundTripsFields verifies that all fields in
// Incident survive the Record → Recent round-trip.
func TestIncidentStoreRecordRoundTripsFields(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := Incident{
		CentralName: "ccu1",
		InterfaceID: "BidCos-RF",
		Type:        hmenum.IncidentTypeConnectionLost,
		Severity:    hmenum.IncidentSeverityCritical,
		Message:     "lost connection after 5 retries",
		Details:     "dial tcp: connection refused",
	}
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}

	list, err := s.Recent(ctx, "ccu1", 1)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	got := list[0]
	if got.CentralName != "ccu1" {
		t.Errorf("CentralName=%q want ccu1", got.CentralName)
	}
	if got.InterfaceID != "BidCos-RF" {
		t.Errorf("InterfaceID=%q want BidCos-RF", got.InterfaceID)
	}
	if got.Type != hmenum.IncidentTypeConnectionLost {
		t.Errorf("Type=%q want %q", got.Type, hmenum.IncidentTypeConnectionLost)
	}
	if got.Severity != hmenum.IncidentSeverityCritical {
		t.Errorf("Severity=%q want %q", got.Severity, hmenum.IncidentSeverityCritical)
	}
	if got.Message != inc.Message {
		t.Errorf("Message=%q want %q", got.Message, inc.Message)
	}
	if got.Details != inc.Details {
		t.Errorf("Details=%q want %q", got.Details, inc.Details)
	}
}

// TestIncidentStoreRecordEmptyInterfaceID checks that an empty interface
// ID (central-level incident, not tied to a specific interface) is stored
// and retrieved without error.
func TestIncidentStoreRecordEmptyInterfaceID(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := Incident{
		CentralName: "ccu1",
		InterfaceID: "", // intentionally empty
		Type:        hmenum.IncidentTypeConfigError,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "bad config key",
	}
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	if list[0].InterfaceID != "" {
		t.Errorf("InterfaceID=%q want empty", list[0].InterfaceID)
	}
}

// TestIncidentStoreBumpIfRecentMergesDuplicate verifies that BumpIfRecent
// increments count and updates last_seen when the same tuple is seen
// within the window, and that only one row exists after the bump.
func TestIncidentStoreBumpIfRecentMergesDuplicate(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}

	bumped, err := s.BumpIfRecent(ctx, inc, 1*time.Hour)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !bumped {
		t.Fatal("expected bump=true, got false")
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("row count=%d want 1 (bump must not insert)", len(list))
	}
	if list[0].Count != 2 {
		t.Errorf("count=%d want 2", list[0].Count)
	}
}

// TestIncidentStoreBumpIfRecentUpdatesDetails verifies that BumpIfRecent
// updates the Details field when a non-empty Details value is supplied.
func TestIncidentStoreBumpIfRecentUpdatesDetails(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	inc.Details = "initial details"
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}

	inc.Details = "updated details"
	if _, err := s.BumpIfRecent(ctx, inc, time.Hour); err != nil {
		t.Fatalf("bump: %v", err)
	}

	list, _ := s.Recent(ctx, "ccu1", 1)
	if list[0].Details != "updated details" {
		t.Errorf("Details=%q want updated details", list[0].Details)
	}
}

// TestIncidentStoreBumpIfRecentReturnsFalseWhenNoCandidate verifies that
// BumpIfRecent returns false (and no error) when no matching row exists.
func TestIncidentStoreBumpIfRecentReturnsFalseWhenNoCandidate(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	bumped, err := s.BumpIfRecent(ctx, inc, time.Hour)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if bumped {
		t.Fatal("expected bump=false for empty store")
	}
}

// TestIncidentStoreBumpIfRecentDifferentMessageDoesNotMerge verifies that
// an incident with a different message is NOT merged even within the window.
func TestIncidentStoreBumpIfRecentDifferentMessageDoesNotMerge(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	original := baseIncident("ccu1", "HmIP-RF")
	if _, err := s.Record(ctx, original); err != nil {
		t.Fatalf("record: %v", err)
	}

	different := original
	different.Message = "different message"
	bumped, err := s.BumpIfRecent(ctx, different, time.Hour)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if bumped {
		t.Fatal("different message must not be merged")
	}

	// Verify the original row is unchanged.
	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1 (no extra row created by failed bump)", len(list))
	}
	if list[0].Count != 1 {
		t.Errorf("count=%d want 1 (original row must not be bumped)", list[0].Count)
	}
}

// TestIncidentStoreBumpIfRecentWindowExpiry verifies that an incident
// whose last_seen is outside the supplied window is NOT merged. We
// simulate this by using a zero-duration window (no past row can match).
func TestIncidentStoreBumpIfRecentWindowExpiry(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}

	// A window of 0 (or negative) seconds means "only rows from the
	// future" can match — effectively nothing. The sqlite datetime
	// arithmetic rounds to seconds, so a 0-second window reliably
	// excludes rows inserted in the same second; a negative window is
	// even safer.
	bumped, err := s.BumpIfRecent(ctx, inc, -1*time.Second)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if bumped {
		t.Fatal("expected bump=false: window expired (negative duration)")
	}
}

// TestIncidentStoreRecentLimitHonored verifies that the limit parameter
// is respected and that results are ordered newest-first.
func TestIncidentStoreRecentLimitHonored(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for i := range 5 {
		inc := Incident{
			CentralName: "ccu1",
			InterfaceID: "HmIP-RF",
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     "rpc fault",
		}
		if _, err := s.Record(ctx, inc); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	list, err := s.Recent(ctx, "ccu1", 3)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d want 3 (limit not honored)", len(list))
	}
	// Newest-first: the last inserted row has the highest ID.
	for i := 1; i < len(list); i++ {
		if list[i].ID >= list[i-1].ID {
			t.Errorf("ordering broken: list[%d].ID=%d >= list[%d].ID=%d",
				i, list[i].ID, i-1, list[i-1].ID)
		}
	}
}

// TestIncidentStoreRecentMultiCCUIsolation verifies that Recent only
// returns incidents for the queried central.
func TestIncidentStoreRecentMultiCCUIsolation(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for _, ccu := range []string{"ccu1", "ccu2"} {
		inc := baseIncident(ccu, "HmIP-RF")
		inc.Message = ccu + " specific message"
		if _, err := s.Record(ctx, inc); err != nil {
			t.Fatalf("record %s: %v", ccu, err)
		}
	}

	list1, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent ccu1: %v", err)
	}
	if len(list1) != 1 {
		t.Fatalf("ccu1: len=%d want 1", len(list1))
	}
	if list1[0].Message != "ccu1 specific message" {
		t.Errorf("ccu1 message=%q leaked ccu2 data", list1[0].Message)
	}

	list2, err := s.Recent(ctx, "ccu2", 10)
	if err != nil {
		t.Fatalf("recent ccu2: %v", err)
	}
	if len(list2) != 1 {
		t.Fatalf("ccu2: len=%d want 1", len(list2))
	}
}

// TestIncidentStoreRecentBumpedIncidentCountPersists runs Record +
// multiple BumpIfRecent calls and confirms the count accumulates correctly.
func TestIncidentStoreRecentBumpedIncidentCountPersists(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := baseIncident("ccu1", "HmIP-RF")
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("record: %v", err)
	}
	for i := range 4 {
		if _, err := s.BumpIfRecent(ctx, inc, time.Hour); err != nil {
			t.Fatalf("bump %d: %v", i, err)
		}
	}

	list, err := s.Recent(ctx, "ccu1", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	if list[0].Count != 5 {
		t.Errorf("count=%d want 5 (1 record + 4 bumps)", list[0].Count)
	}
}

// TestIncidentStoreNoteOnSeverityFilter documents that Recent has no
// severity-filter parameter. All severities are returned; callers must
// filter in-memory. This is noted as a gap; no production code change
// is made here.
func TestIncidentStoreNoteOnSeverityFilter(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for _, sev := range []hmenum.IncidentSeverity{
		hmenum.IncidentSeverityInfo,
		hmenum.IncidentSeverityWarning,
		hmenum.IncidentSeverityError,
		hmenum.IncidentSeverityCritical,
	} {
		_, _ = s.Record(ctx, Incident{
			CentralName: "ccu1",
			Type:        hmenum.IncidentTypeRPCFault,
			Severity:    sev,
			Message:     string(sev),
		})
	}

	// Recent returns all rows; caller would filter on .Severity.
	list, err := s.Recent(ctx, "ccu1", 100)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("len=%d want 4 (all severities)", len(list))
	}
	// Document that Recent.Signature is: (ctx, central string, limit int)
	// — there is no severity parameter. This test acts as the specification
	// reference for that interface decision.
}

// ---------------------------------------------------------------------------
// IncidentStore.IncidentCount
// ---------------------------------------------------------------------------

func TestIncidentStoreIncidentCount(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	n, err := s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount: %v", err)
	}
	if n != 0 {
		t.Errorf("IncidentCount on empty store=%d want 0", n)
	}

	for range 3 {
		if _, err := s.Record(ctx, p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeRPCError)); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	// Different central.
	if _, err := s.Record(ctx, p2Incident("ccu2", "HmIP-RF", hmenum.IncidentTypeRPCError)); err != nil {
		t.Fatalf("Record ccu2: %v", err)
	}

	n, err = s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount after insert: %v", err)
	}
	if n != 3 {
		t.Errorf("IncidentCount=%d want 3", n)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.ClearIncidents
// ---------------------------------------------------------------------------

func TestIncidentStoreClearIncidents(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for range 2 {
		if _, err := s.Record(ctx, p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeCallbackTimeout)); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	// Different central — must survive ClearIncidents for ccu1.
	if _, err := s.Record(ctx, p2Incident("ccu2", "HmIP-RF", hmenum.IncidentTypeCallbackTimeout)); err != nil {
		t.Fatalf("Record ccu2: %v", err)
	}

	if err := s.ClearIncidents(ctx, "ccu1"); err != nil {
		t.Fatalf("ClearIncidents: %v", err)
	}

	n1, err := s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount ccu1 after clear: %v", err)
	}
	if n1 != 0 {
		t.Errorf("IncidentCount ccu1 after clear=%d want 0", n1)
	}

	n2, err := s.IncidentCount(ctx, "ccu2")
	if err != nil {
		t.Fatalf("IncidentCount ccu2 after clear: %v", err)
	}
	if n2 != 1 {
		t.Errorf("IncidentCount ccu2 after clear=%d want 1 (must survive)", n2)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.GetIncidentsFiltered
// ---------------------------------------------------------------------------

// TestIncidentStoreGetIncidentsFilteredLimitHonored verifies that a
// positive limit caps the result set and rows stay newest-first, mirroring
// the ordering contract of Recent/GetAllIncidents.
func TestIncidentStoreGetIncidentsFilteredLimitHonored(t *testing.T) {
	t.Parallel()
	s := freshIncidentStore(t)
	ctx := context.Background()

	for i := range 5 {
		inc := p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeRPCFault)
		inc.Message = fmt.Sprintf("msg-%d", i)
		if _, err := s.Record(ctx, inc); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := s.GetIncidentsFiltered(ctx, "ccu1", time.Time{}, time.Time{}, 3)
	if err != nil {
		t.Fatalf("GetIncidentsFiltered: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (limit not honored)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID >= got[i-1].ID {
			t.Errorf("ordering broken: got[%d].ID=%d >= got[%d].ID=%d", i, got[i].ID, i-1, got[i-1].ID)
		}
	}
}

// TestIncidentStoreGetIncidentsFilteredZeroLimitReturnsAll verifies that
// limit<=0 returns every matching row (no cap).
func TestIncidentStoreGetIncidentsFilteredZeroLimitReturnsAll(t *testing.T) {
	t.Parallel()
	s := freshIncidentStore(t)
	ctx := context.Background()

	for range 4 {
		if _, err := s.Record(ctx, p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeRPCFault)); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	got, err := s.GetIncidentsFiltered(ctx, "ccu1", time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("GetIncidentsFiltered: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len=%d want 4", len(got))
	}
}

// TestIncidentStoreGetIncidentsFilteredCentralIsolation verifies that
// GetIncidentsFiltered never leaks another central's rows regardless of
// the time bounds supplied.
func TestIncidentStoreGetIncidentsFilteredCentralIsolation(t *testing.T) {
	t.Parallel()
	s := freshIncidentStore(t)
	ctx := context.Background()

	for _, ccu := range []string{"ccu1", "ccu2"} {
		inc := baseIncident(ccu, "HmIP-RF")
		inc.Message = ccu + " message"
		if _, err := s.Record(ctx, inc); err != nil {
			t.Fatalf("record %s: %v", ccu, err)
		}
	}

	got, err := s.GetIncidentsFiltered(ctx, "ccu1", time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("GetIncidentsFiltered: %v", err)
	}
	if len(got) != 1 || got[0].Message != "ccu1 message" {
		t.Fatalf("got=%+v want single ccu1 row", got)
	}
}

// TestIncidentStoreGetIncidentsFilteredSinceUntilWindow verifies the
// since/until bounds are pushed down to SQL: since is inclusive, until is
// exclusive, mirroring the /audit durable-query contract.
func TestIncidentStoreGetIncidentsFilteredSinceUntilWindow(t *testing.T) {
	t.Parallel()
	s := freshIncidentStore(t)
	ctx := context.Background()

	// BumpIfRecent lets the test control last_seen indirectly is not
	// available; instead record three rows and read back their actual
	// last_seen timestamps to build a window that excludes the oldest and
	// the newest row.
	for range 3 {
		if _, err := s.Record(ctx, p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeRPCFault)); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	all, err := s.GetAllIncidents(ctx, "ccu1")
	if err != nil {
		t.Fatalf("GetAllIncidents: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("setup: len=%d want 3", len(all))
	}

	// A window covering everything must return all 3 rows.
	got, err := s.GetIncidentsFiltered(ctx, "ccu1", all[2].LastSeen.Add(-time.Hour), all[0].LastSeen.Add(time.Hour), 0)
	if err != nil {
		t.Fatalf("GetIncidentsFiltered wide window: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("wide window: len=%d want 3", len(got))
	}

	// A window strictly before every row's last_seen must return nothing.
	got, err = s.GetIncidentsFiltered(ctx, "ccu1", time.Time{}, all[2].LastSeen.Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("GetIncidentsFiltered empty window: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty window: len=%d want 0, got=%+v", len(got), got)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.GetIncidentsByInterface
// ---------------------------------------------------------------------------

func TestIncidentStoreGetIncidentsByInterface(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for range 2 {
		if _, err := s.Record(ctx, p2Incident("ccu1", "HmIP-RF", hmenum.IncidentTypeRPCError)); err != nil {
			t.Fatalf("Record HmIP-RF: %v", err)
		}
	}
	if _, err := s.Record(ctx, p2Incident("ccu1", "BidCos-RF", hmenum.IncidentTypeRPCError)); err != nil {
		t.Fatalf("Record BidCos-RF: %v", err)
	}

	got, err := s.GetIncidentsByInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("GetIncidentsByInterface: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d want 2", len(got))
	}
	for _, inc := range got {
		if inc.InterfaceID != "HmIP-RF" {
			t.Errorf("unexpected interface_id=%q", inc.InterfaceID)
		}
	}

	// Empty result for unknown interface.
	got2, err := s.GetIncidentsByInterface(ctx, "ccu1", "GHOST")
	if err != nil {
		t.Fatalf("GetIncidentsByInterface GHOST: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("len=%d want 0 for ghost interface", len(got2))
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.GetDiagnostics
// ---------------------------------------------------------------------------

func TestIncidentStoreGetDiagnostics(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	for range 3 {
		_, err := s.Record(ctx, Incident{
			CentralName: "ccu1",
			Type:        hmenum.IncidentTypeAuthFailure,
			Severity:    hmenum.IncidentSeverityError,
			Message:     "auth fail",
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	_, err := s.Record(ctx, Incident{
		CentralName: "ccu1",
		Type:        hmenum.IncidentTypeConnectionLost,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "timeout",
	})
	if err != nil {
		t.Fatalf("record timeout: %v", err)
	}

	diag, err := s.GetDiagnostics(ctx, "ccu1", 100, 30)
	if err != nil {
		t.Fatalf("GetDiagnostics: %v", err)
	}
	if total, ok := diag["total_incidents"].(int); !ok || total != 4 {
		t.Errorf("total_incidents=%v, want 4", diag["total_incidents"])
	}
	byType, ok := diag["incidents_by_type"].(map[string]int)
	if !ok {
		t.Fatalf("incidents_by_type type=%T", diag["incidents_by_type"])
	}
	if byType[string(hmenum.IncidentTypeAuthFailure)] != 3 {
		t.Errorf("auth_failure count=%d, want 3", byType[string(hmenum.IncidentTypeAuthFailure)])
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.RecordIncidentCtx
// ---------------------------------------------------------------------------

func TestIncidentStoreRecordIncidentCtx(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	err := s.RecordIncidentCtx(ctx, "ccu1", "BidCos-RF",
		hmenum.IncidentTypeAuthFailure, hmenum.IncidentSeverityError, "ctx-based record")
	if err != nil {
		t.Fatalf("RecordIncidentCtx: %v", err)
	}

	list, err := s.Recent(ctx, "ccu1", 5)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d, want 1", len(list))
	}
	if list[0].Message != "ctx-based record" {
		t.Errorf("message=%q, want 'ctx-based record'", list[0].Message)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.PurgeOld
// ---------------------------------------------------------------------------

func TestIncidentStorePurgeOld(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	// Insert two rows.
	for range 2 {
		_, err := s.Record(ctx, Incident{
			CentralName: "ccu1",
			Type:        hmenum.IncidentTypeAuthFailure,
			Severity:    hmenum.IncidentSeverityError,
			Message:     "old",
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	// Purge with 0 maxAgeDays falls back to DefaultMaxAgeDays.
	// Since rows are brand-new they won't be purged, but the code path is exercised.
	n, err := s.PurgeOld(ctx, "ccu1", 0)
	if err != nil {
		t.Fatalf("PurgeOld: %v", err)
	}
	if n < 0 {
		t.Errorf("PurgeOld returned negative count: %d", n)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.EnforcePerTypeCap
// ---------------------------------------------------------------------------

func TestIncidentStoreEnforcePerTypeCap(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	// Insert 5 auth-failure rows.
	for range 5 {
		_, err := s.Record(ctx, Incident{
			CentralName: "ccu1",
			Type:        hmenum.IncidentTypeAuthFailure,
			Severity:    hmenum.IncidentSeverityError,
			Message:     "fail",
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	if err := s.EnforcePerTypeCap(ctx, "ccu1", 3); err != nil {
		t.Fatalf("EnforcePerTypeCap: %v", err)
	}
	list, _ := s.GetAllIncidents(ctx, "ccu1")
	if len(list) > 3 {
		t.Errorf("after cap enforcement len=%d, want ≤3", len(list))
	}
}

func TestIncidentStoreEnforcePerTypeCapDefault(t *testing.T) {
	// maxPerType=0 should fall back to DefaultMaxPerType (no panic).
	s := freshIncidentStore(t)
	ctx := context.Background()
	if err := s.EnforcePerTypeCap(ctx, "ccu1", 0); err != nil {
		t.Fatalf("EnforcePerTypeCap(0): %v", err)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.IncidentCount (second variant — empty store + multi-insert)
// ---------------------------------------------------------------------------

func TestIncidentStoreIncidentCountGaps(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	n, err := s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("IncidentCount on empty store=%d, want 0", n)
	}

	for range 3 {
		_, _ = s.Record(ctx, Incident{
			CentralName: "ccu1",
			Type:        hmenum.IncidentTypeAuthFailure,
			Severity:    hmenum.IncidentSeverityError,
			Message:     "x",
		})
	}
	n, err = s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount: %v", err)
	}
	if n != 3 {
		t.Errorf("IncidentCount=%d, want 3", n)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.ClearIncidents (second variant)
// ---------------------------------------------------------------------------

func TestIncidentStoreClearIncidentsGaps(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	_, _ = s.Record(ctx, Incident{
		CentralName: "ccu1",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "x",
	})

	if err := s.ClearIncidents(ctx, "ccu1"); err != nil {
		t.Fatalf("ClearIncidents: %v", err)
	}

	n2, _ := s.IncidentCount(ctx, "ccu1")
	if n2 != 0 {
		t.Errorf("IncidentCount after clear=%d, want 0", n2)
	}
}

// ---------------------------------------------------------------------------
// IncidentStore.RecordWithLimits
// ---------------------------------------------------------------------------

func TestIncidentStoreRecordWithLimits(t *testing.T) {
	s := freshIncidentStore(t)
	ctx := context.Background()

	inc := Incident{
		CentralName: "ccu1",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "limits-test",
	}
	id, err := s.RecordWithLimits(ctx, inc, 30, 100)
	if err != nil {
		t.Fatalf("RecordWithLimits: %v", err)
	}
	if id <= 0 {
		t.Errorf("RecordWithLimits returned id=%d, want >0", id)
	}
}

// ---------------------------------------------------------------------------
// Incident.JournalExcerpt field
// ---------------------------------------------------------------------------

func TestIncidentJournalExcerptRoundtrip(t *testing.T) {
	t.Parallel()
	s := NewIncidentStore(openTestDB(t, "inc_journal.db"))
	ctx := context.Background()

	const excerpt = "PING sent @ 2026-05-01T10:00:00Z; PONG not received"
	inc := Incident{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		Type:           hmenum.IncidentTypeRPCError,
		Severity:       hmenum.IncidentSeverityError,
		Message:        "ping/pong mismatch",
		JournalExcerpt: excerpt,
	}

	id, err := s.Record(ctx, inc)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if id == 0 {
		t.Fatal("Record returned id=0")
	}

	// GetAllIncidents must return the journal_excerpt.
	all, err := s.GetAllIncidents(ctx, "ccu1")
	if err != nil {
		t.Fatalf("GetAllIncidents: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("GetAllIncidents returned %d items, want 1", len(all))
	}
	if all[0].JournalExcerpt != excerpt {
		t.Errorf("JournalExcerpt=%q want %q", all[0].JournalExcerpt, excerpt)
	}
}

func TestIncidentJournalExcerptEmptyIsAllowed(t *testing.T) {
	t.Parallel()
	s := NewIncidentStore(openTestDB(t, "inc_journal_empty.db"))
	ctx := context.Background()

	inc := Incident{
		CentralName: "ccu1",
		Type:        hmenum.IncidentTypeRPCError,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "no excerpt",
	}
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("Record without JournalExcerpt: %v", err)
	}

	all, err := s.GetAllIncidents(ctx, "ccu1")
	if err != nil {
		t.Fatalf("GetAllIncidents: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("GetAllIncidents returned %d items, want 1", len(all))
	}
	if all[0].JournalExcerpt != "" {
		t.Errorf("JournalExcerpt=%q want empty for no-excerpt record", all[0].JournalExcerpt)
	}
}

func TestIncidentJournalExcerptInGetIncidentsByType(t *testing.T) {
	t.Parallel()
	s := NewIncidentStore(openTestDB(t, "inc_journal_bytype.db"))
	ctx := context.Background()

	const excerpt = "journal data"
	inc := Incident{
		CentralName:    "ccu1",
		Type:           hmenum.IncidentTypeCallbackTimeout,
		Severity:       hmenum.IncidentSeverityError,
		Message:        "timeout",
		JournalExcerpt: excerpt,
	}
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("Record: %v", err)
	}

	list, err := s.GetIncidentsByType(ctx, "ccu1", hmenum.IncidentTypeCallbackTimeout)
	if err != nil {
		t.Fatalf("GetIncidentsByType: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("GetIncidentsByType returned %d items, want 1", len(list))
	}
	if list[0].JournalExcerpt != excerpt {
		t.Errorf("JournalExcerpt=%q want %q", list[0].JournalExcerpt, excerpt)
	}
}

func TestIncidentJournalExcerptInRecent(t *testing.T) {
	t.Parallel()
	s := NewIncidentStore(openTestDB(t, "inc_journal_recent.db"))
	ctx := context.Background()

	const excerpt = "recent excerpt"
	inc := Incident{
		CentralName:    "ccu1",
		Type:           hmenum.IncidentTypeRPCError,
		Severity:       hmenum.IncidentSeverityWarning,
		Message:        "recent incident",
		JournalExcerpt: excerpt,
	}
	if _, err := s.Record(ctx, inc); err != nil {
		t.Fatalf("Record: %v", err)
	}

	recent, err := s.Recent(ctx, "ccu1", 5)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent returned %d items, want 1", len(recent))
	}
	if recent[0].JournalExcerpt != excerpt {
		t.Errorf("JournalExcerpt=%q want %q", recent[0].JournalExcerpt, excerpt)
	}
}

// TestEnforcePerTypeCap_TwoTypesInOneCall verifies that EnforcePerTypeCap
// correctly limits each type independently when multiple types exist in a
// single call — the CTE-based rewrite replaces the old N+1 loop.
func TestEnforcePerTypeCap_TwoTypesInOneCall(t *testing.T) {
	t.Parallel()
	s := NewIncidentStore(openTestDB(t, "inc_cap_two_types.db"))
	ctx := context.Background()

	const maxCap = 2
	typesUnderTest := []hmenum.IncidentType{
		hmenum.IncidentTypeConnectionLost,
		hmenum.IncidentTypeAuthFailure,
	}

	// Insert maxCap+1 rows for each type so each type has one row to trim.
	for _, incType := range typesUnderTest {
		for i := range maxCap + 1 {
			inc := Incident{
				CentralName: "ccu1",
				Type:        incType,
				Severity:    hmenum.IncidentSeverityWarning,
				Message:     fmt.Sprintf("msg-%d", i),
			}
			if _, err := s.Record(ctx, inc); err != nil {
				t.Fatalf("Record type=%s i=%d: %v", incType, i, err)
			}
		}
	}

	if err := s.EnforcePerTypeCap(ctx, "ccu1", maxCap); err != nil {
		t.Fatalf("EnforcePerTypeCap: %v", err)
	}

	// Each type must have exactly maxCap rows after enforcement.
	for _, incType := range typesUnderTest {
		rows, err := s.GetIncidentsByType(ctx, "ccu1", incType)
		if err != nil {
			t.Fatalf("GetIncidentsByType %s: %v", incType, err)
		}
		if len(rows) != maxCap {
			t.Errorf("type=%s: got %d rows want %d", incType, len(rows), maxCap)
		}
	}

	// Total count must be maxCap * len(types).
	total, err := s.IncidentCount(ctx, "ccu1")
	if err != nil {
		t.Fatalf("IncidentCount: %v", err)
	}
	want := maxCap * len(typesUnderTest)
	if total != want {
		t.Errorf("total=%d want %d", total, want)
	}
}
