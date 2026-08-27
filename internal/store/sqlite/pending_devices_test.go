// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

func freshPendingDeviceStore(t *testing.T) *PendingDeviceStore {
	t.Helper()
	return NewPendingDeviceStore(openTestDB(t, "pending_devices.db"))
}

func TestPendingDevices_RoundTripAndScoping(t *testing.T) {
	t.Parallel()
	s := freshPendingDeviceStore(t)
	ctx := context.Background()

	for _, p := range []PendingDevice{
		{CentralName: "otto", InterfaceID: "otto-HmIP-RF", Address: "AAA1", Model: "HmIP-STH"},
		{CentralName: "otto", InterfaceID: "otto-HmIP-RF", Address: "AAA2", Model: "HmIP-PS"},
		{CentralName: "otto", InterfaceID: "otto-VirtualDevices", Address: "VVV1", Model: "HM-RCV-50"},
		// A second CCU. Multi-CCU is first class here: one central's
		// held-back devices must never gate another's bring-up.
		{CentralName: "berta", InterfaceID: "berta-HmIP-RF", Address: "BBB1", Model: "HmIP-SWDO"},
	} {
		if err := s.Put(ctx, p); err != nil {
			t.Fatalf("Put %s: %v", p.Address, err)
		}
	}

	rows, err := s.ListByCentral(ctx, "otto")
	if err != nil {
		t.Fatalf("ListByCentral: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("otto has %d row(s), want 3 — got %+v", len(rows), rows)
	}
	if rows[0].Address != "AAA1" || rows[0].Model != "HmIP-STH" {
		t.Errorf("first row = %+v, want AAA1/HmIP-STH (ordered by interface then address)", rows[0])
	}
	if rows[0].FirstSeen == "" {
		t.Error("FirstSeen is empty — an operator cannot tell a decision postponed yesterday from one a minute old")
	}

	berta, err := s.ListByCentral(ctx, "berta")
	if err != nil {
		t.Fatalf("ListByCentral berta: %v", err)
	}
	if len(berta) != 1 {
		t.Errorf("berta has %d row(s), want 1 — centrals are leaking into each other", len(berta))
	}
}

// TestPendingDevices_PutKeepsTheOriginalFirstSeen pins the idempotence
// that matters: a CCU re-announcing its inventory must not reset the age
// of a decision the operator has been postponing.
func TestPendingDevices_PutKeepsTheOriginalFirstSeen(t *testing.T) {
	t.Parallel()
	s := freshPendingDeviceStore(t)
	ctx := context.Background()

	first := PendingDevice{
		CentralName: "otto", InterfaceID: "otto-HmIP-RF", Address: "AAA1",
		Model: "HmIP-STH", FirstSeen: "2026-01-01T09:00:00Z",
	}
	if err := s.Put(ctx, first); err != nil {
		t.Fatalf("Put: %v", err)
	}
	again := first
	again.FirstSeen = "2026-08-27T18:00:00Z"
	again.Model = "HmIP-STH-2"
	if err := s.Put(ctx, again); err != nil {
		t.Fatalf("Put again: %v", err)
	}

	rows, err := s.ListByCentral(ctx, "otto")
	if err != nil {
		t.Fatalf("ListByCentral: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("re-announcement stacked a second row: %+v", rows)
	}
	if rows[0].FirstSeen != "2026-01-01T09:00:00Z" {
		t.Errorf("FirstSeen = %q, want the original — a reconnect reset the decision's age", rows[0].FirstSeen)
	}
	if rows[0].Model != "HmIP-STH-2" {
		t.Errorf("Model = %q, want the refreshed value", rows[0].Model)
	}
}

func TestPendingDevices_DeleteAndDeleteByCentral(t *testing.T) {
	t.Parallel()
	s := freshPendingDeviceStore(t)
	ctx := context.Background()

	for _, a := range []string{"AAA1", "AAA2"} {
		if err := s.Put(ctx, PendingDevice{
			CentralName: "otto", InterfaceID: "otto-HmIP-RF", Address: a,
		}); err != nil {
			t.Fatalf("Put %s: %v", a, err)
		}
	}
	if err := s.Put(ctx, PendingDevice{
		CentralName: "berta", InterfaceID: "berta-HmIP-RF", Address: "BBB1",
	}); err != nil {
		t.Fatalf("Put berta: %v", err)
	}

	if err := s.Delete(ctx, "otto", "otto-HmIP-RF", "AAA1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rows, _ := s.ListByCentral(ctx, "otto")
	if len(rows) != 1 || rows[0].Address != "AAA2" {
		t.Fatalf("after Delete, otto = %+v, want only AAA2", rows)
	}

	if err := s.DeleteByCentral(ctx, "otto"); err != nil {
		t.Fatalf("DeleteByCentral: %v", err)
	}
	if rows, _ := s.ListByCentral(ctx, "otto"); len(rows) != 0 {
		t.Errorf("otto still has %d row(s) after DeleteByCentral", len(rows))
	}
	// The other central is untouched: turning the toggle off on one CCU
	// must not release another's held-back devices.
	if rows, _ := s.ListByCentral(ctx, "berta"); len(rows) != 1 {
		t.Errorf("berta has %d row(s), want 1 — DeleteByCentral crossed a central boundary", len(rows))
	}
}
