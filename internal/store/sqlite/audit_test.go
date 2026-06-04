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
