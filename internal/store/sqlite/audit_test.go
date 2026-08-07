// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

func freshAuditStore(t *testing.T) *AuditStore {
	t.Helper()
	// One file-backed DB per test. Use openTestDB (which holds openMu
	// around Open) so that concurrent parallel tests do not race on
	// goose's package-level globals (SetBaseFS / SetDialect).
	return NewAuditStore(openTestDB(t, "audit.db"))
}

func TestAuditStoreAppendAndList(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	entries := []audit.Entry{
		{
			Action:        audit.ActionParamsetWrite,
			DeviceAddress: "0001ABCD",
			ChannelNo:     1,
			Paramset:      "VALUES",
			Changes: []audit.Change{
				{Parameter: "STATE", Before: false, After: true},
			},
			User: "alice",
		},
		{
			Action:        audit.ActionLinkAdd,
			DeviceAddress: "0001ABCD",
			ChannelNo:     2,
			Peer:          "0002BEEF:1",
		},
	}
	for _, e := range entries {
		if err := s.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	// Newest first.
	if got[0].Action != audit.ActionLinkAdd {
		t.Fatalf("ordering broken: %+v", got)
	}
	if got[1].Action != audit.ActionParamsetWrite || len(got[1].Changes) != 1 {
		t.Fatalf("changes did not round-trip: %+v", got[1])
	}
	if got[1].Changes[0].After != true {
		t.Fatalf("change.After=%v", got[1].Changes[0].After)
	}
}

func TestAuditStoreFilterByDevice(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()
	for _, e := range []audit.Entry{
		{Action: audit.ActionParamsetWrite, DeviceAddress: "AA"},
		{Action: audit.ActionParamsetWrite, DeviceAddress: "BB"},
		{Action: audit.ActionParamsetWrite, DeviceAddress: "AA"},
	} {
		_ = s.Append(ctx, e)
	}
	got, err := s.List(ctx, "AA", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 AA entries, got %d", len(got))
	}
	for _, e := range got {
		if e.DeviceAddress != "AA" {
			t.Fatalf("filter leaked: %+v", e)
		}
	}
}

func TestAuditStoreLimit(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()
	for range 5 {
		_ = s.Append(ctx, audit.Entry{Action: audit.ActionParamsetWrite, DeviceAddress: "A"})
	}
	got, err := s.List(ctx, "", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not honored: %d", len(got))
	}
}

func TestAuditStorePreservesExplicitTimestamp(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.Append(ctx, audit.Entry{
		Timestamp: want,
		Action:    audit.ActionParamsetWrite,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, _ := s.List(ctx, "", 0)
	if !got[0].Timestamp.Equal(want) {
		t.Fatalf("ts=%v want %v", got[0].Timestamp, want)
	}
}

func TestAuditStoreNilSafe(t *testing.T) {
	t.Parallel()
	var s *AuditStore
	if err := s.Append(context.Background(), audit.Entry{}); err != nil {
		t.Fatalf("nil store must be a no-op, got %v", err)
	}
	out, err := s.List(context.Background(), "", 0)
	if err != nil || out != nil {
		t.Fatalf("nil store List=%v err=%v", out, err)
	}
}

func TestAuditStoreQueryDevicePrefixFilter(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	for _, addr := range []string{"ABC0001:1", "ABC0002:0", "XYZ9999:0"} {
		_ = s.Append(ctx, audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: addr})
	}

	got, err := s.Query(ctx, audit.Query{Device: "ABC"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 ABC entries, got %d", len(got))
	}
	for _, e := range got {
		if len(e.DeviceAddress) < 3 || e.DeviceAddress[:3] != "ABC" {
			t.Fatalf("filter leaked non-ABC entry: %q", e.DeviceAddress)
		}
	}
}

func TestAuditStoreQuerySinceUntilWindow(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i := range 5 {
		_ = s.Append(ctx, audit.Entry{
			Action:    audit.ActionDataPointWrite,
			Timestamp: t0.Add(time.Duration(i) * time.Hour),
		})
	}

	// Since=t0+1h (inclusive), Until=t0+3h (exclusive) → t0+1h and t0+2h.
	got, err := s.Query(ctx, audit.Query{
		Since: t0.Add(1 * time.Hour),
		Until: t0.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries in [t0+1h, t0+3h), got %d", len(got))
	}
}

func TestAuditStoreQueryPaginationNewestFirst(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for i := range 6 {
		_ = s.Append(ctx, audit.Entry{
			Action:    audit.ActionDataPointWrite,
			Timestamp: t0.Add(time.Duration(i) * time.Hour),
		})
	}

	page1, err := s.Query(ctx, audit.Query{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("page1 query: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: expected 3, got %d", len(page1))
	}

	page2, err := s.Query(ctx, audit.Query{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("page2 query: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2: expected 3, got %d", len(page2))
	}

	// Pages must not overlap: page1 holds the 3 newest, page2 the 3 oldest.
	// Results are ordered id DESC so page1[0] is newer than page2[2].
	if !page1[0].Timestamp.After(page2[2].Timestamp) {
		t.Fatalf("pages overlap or wrong order: page1[0]=%v page2[2]=%v",
			page1[0].Timestamp, page2[2].Timestamp)
	}
}

func TestAuditStoreQueryEscapesLikeMetachars(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	// Entries with literal '%' and '_' in address.
	_ = s.Append(ctx, audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: "DEV%001"})
	_ = s.Append(ctx, audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: "DEV_001"})
	_ = s.Append(ctx, audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: "DEV0001"})

	// Literal '%' must not act as wildcard — matches only "DEV%001".
	gotPct, err := s.Query(ctx, audit.Query{Device: "DEV%001"})
	if err != nil {
		t.Fatalf("query %%: %v", err)
	}
	if len(gotPct) != 1 || gotPct[0].DeviceAddress != "DEV%001" {
		t.Fatalf("percent escape: expected [DEV%%001], got %v", gotPct)
	}

	// Literal '_' must not act as single-char wildcard — matches only "DEV_001".
	gotUnd, err := s.Query(ctx, audit.Query{Device: "DEV_001"})
	if err != nil {
		t.Fatalf("query _: %v", err)
	}
	if len(gotUnd) != 1 || gotUnd[0].DeviceAddress != "DEV_001" {
		t.Fatalf("underscore escape: expected [DEV_001], got %v", gotUnd)
	}
}

func TestAuditStoreQueryEmptyReturnsAll(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	for range 3 {
		_ = s.Append(ctx, audit.Entry{Action: audit.ActionDataPointWrite})
	}

	got, err := s.Query(ctx, audit.Query{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("empty query: expected 3, got %d", len(got))
	}
}

func TestAuditStoreQueryNilSafe(t *testing.T) {
	t.Parallel()
	var s *AuditStore
	out, err := s.Query(context.Background(), audit.Query{})
	if err != nil || out != nil {
		t.Fatalf("nil store Query=%v err=%v", out, err)
	}
}

func TestAuditStoreReadPathsCarryTheRowID(t *testing.T) {
	t.Parallel()
	s := freshAuditStore(t)
	ctx := context.Background()

	// Three rows that agree on every column a client renders — the same
	// operator action emitting several entries within one second. Only the
	// table's primary key tells them apart.
	ts := time.Date(2026, 7, 28, 10, 40, 24, 0, time.UTC)
	for _, note := range []string{"area_create", "sensors_replace", "outputs_replace"} {
		if err := s.Append(ctx, audit.Entry{
			Timestamp: ts,
			User:      "markus",
			Action:    audit.Action("alarm_config_change"),
			Note:      note,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	for name, read := range map[string]func() ([]audit.Entry, error){
		"Query": func() ([]audit.Entry, error) { return s.Query(ctx, audit.Query{Limit: 10}) },
		"List":  func() ([]audit.Entry, error) { return s.List(ctx, "", 10) },
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 3 {
			t.Fatalf("%s: want 3 entries, got %d", name, len(got))
		}
		seen := make(map[int64]struct{}, len(got))
		for _, e := range got {
			if e.ID == 0 {
				t.Fatalf("%s: entry served without an ID: %+v", name, e)
			}
			if _, dup := seen[e.ID]; dup {
				t.Fatalf("%s: duplicate ID %d across entries", name, e.ID)
			}
			seen[e.ID] = struct{}{}
		}
	}
}
