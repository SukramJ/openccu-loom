// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// nilHubQuery returns a wsHubQuery backed by an empty registry so hub.Hub()
// returns nil — all methods should return the "hub not available" error.
func nilHubQuery() *wsHubQuery {
	emptyReg := central.NewRegistry()
	return &wsHubQuery{
		hub:      adapter.NewHubAdapter(emptyReg),
		registry: emptyReg,
	}
}

// liveHubQuery returns a wsHubQuery backed by a real hub.Hub so we can
// exercise the non-nil hub path.
func liveHubQuery(t *testing.T) (*wsHubQuery, *hub.Hub) {
	t.Helper()
	h := hub.NewHub("test-ccu")
	hubAdapter, reg := buildHubAdapter(h)
	return &wsHubQuery{hub: hubAdapter, registry: reg}, h
}

// ── wsHubMessageCounts ────────────────────────────────────────────────────────

func TestWSHubMessageCounts_NilHub_ReturnsTwoNils(t *testing.T) {
	t.Parallel()
	emptyReg := central.NewRegistry()
	w := &wsHubMessageCounts{hub: adapter.NewHubAdapter(emptyReg)}
	svc, alarm := w.HubMessageCounts()
	if svc != nil || alarm != nil {
		t.Errorf("expected nil, nil; got svc=%v alarm=%v", svc, alarm)
	}
}

func TestWSHubMessageCounts_NilAdapter_ReturnsTwoNils(t *testing.T) {
	t.Parallel()
	w := &wsHubMessageCounts{hub: nil}
	svc, alarm := w.HubMessageCounts()
	if svc != nil || alarm != nil {
		t.Errorf("expected nil, nil; got svc=%v alarm=%v", svc, alarm)
	}
}

func TestWSHubMessageCounts_LiveHub_ReturnsCounts(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	hubAdapter, _ := buildHubAdapter(h)
	w := &wsHubMessageCounts{hub: hubAdapter}
	svc, alarm := w.HubMessageCounts()
	if svc == nil || alarm == nil {
		t.Fatal("expected non-nil counts from live hub")
	}
	if *svc != 0 || *alarm != 0 {
		t.Errorf("fresh hub: expected svc=0 alarm=0; got svc=%d alarm=%d", *svc, *alarm)
	}
}

// ── wsHubQuery nil-hub paths ─────────────────────────────────────────────────

func TestWSHubQuery_ListPrograms_NilHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 programs, got %d", len(got))
	}
}

func TestWSHubQuery_ListPrograms_LiveHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	got, err := q.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil slice, got nil")
	}
}

func TestWSHubQuery_ExecuteProgram_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.ExecuteProgram(context.Background(), "prog-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_ExecuteProgram_LiveHub_UnknownID_Errors(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	err := q.ExecuteProgram(context.Background(), "nonexistent-prog")
	if err == nil {
		t.Fatal("expected error for unknown program, got nil")
	}
}

func TestWSHubQuery_ListSysvars_NilHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 sysvars, got %d", len(got))
	}
}

func TestWSHubQuery_ListSysvars_LiveHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil slice")
	}
}

func TestWSHubQuery_SetSysvar_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.SetSysvar(context.Background(), "foo", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_SetSysvar_LiveHub_UnknownName_Errors(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	err := q.SetSysvar(context.Background(), "nonexistent-sysvar", "val")
	if err == nil {
		t.Fatal("expected error for unknown sysvar, got nil")
	}
}

func TestWSHubQuery_ListAlarmMessages_NilHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.ListAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 alarm messages, got %d", len(got))
	}
}

func TestWSHubQuery_ListAlarmMessages_LiveHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	got, err := q.ListAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil slice")
	}
}

func TestWSHubQuery_AcknowledgeAlarmMessage_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.AcknowledgeAlarmMessage(context.Background(), "msg-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_ListServiceMessages_NilHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.ListServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 service messages, got %d", len(got))
	}
}

func TestWSHubQuery_ListServiceMessages_LiveHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	got, err := q.ListServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil slice")
	}
}

func TestWSHubQuery_AcknowledgeServiceMessage_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.AcknowledgeServiceMessage(context.Background(), "svc-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_TriggerBackup_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.TriggerBackup(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_BackupStatus_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	_, err := q.BackupStatus(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_TriggerFirmwareUpdate_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.TriggerFirmwareUpdate(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_InboxDevices_NilHub_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.InboxDevices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 inbox devices, got %d", len(got))
	}
}

func TestWSHubQuery_InboxDevices_LiveHub_NilInbox_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	// Inbox is nil by default on a fresh hub.
	if h.Inbox != nil {
		t.Skip("Inbox is already non-nil; skipping nil-inbox path")
	}
	got, err := q.InboxDevices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 inbox devices, got %d", len(got))
	}
}

func TestWSHubQuery_AcceptInboxDevice_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	err := q.AcceptInboxDevice(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_FirmwareInfo_NilHub_ReturnsUnobserved(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	got, err := q.FirmwareInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["observed"] != false {
		t.Errorf("expected observed=false, got %v", got["observed"])
	}
}

func TestWSHubQuery_FirmwareInfo_LiveHub_NilUpdate_ReturnsUnobserved(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	// Fresh hub has h.Update == nil.
	if h.Update != nil {
		t.Skip("Update is non-nil on fresh hub; skipping nil-update path")
	}
	got, err := q.FirmwareInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["observed"] != false {
		t.Errorf("expected observed=false, got %v", got["observed"])
	}
}

// ── wsDeviceQuery nil-guard paths ────────────────────────────────────────────

func TestWSDeviceQuery_NilAdapter_ListDevices_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	q := &wsDeviceQuery{devs: nil}
	got, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestWSDeviceQuery_NilAdapter_GetDevice_Errors(t *testing.T) {
	t.Parallel()
	q := &wsDeviceQuery{devs: nil}
	_, err := q.GetDevice(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("expected error when adapter is nil, got nil")
	}
}

func TestWSDeviceQuery_NilParamsets_GetParamset_Errors(t *testing.T) {
	t.Parallel()
	q := &wsDeviceQuery{paramsets: nil}
	_, err := q.GetParamset(context.Background(), configui.SessionKey{ChannelAddress: "A:0"})
	if err == nil {
		t.Fatal("expected error when paramsets is nil, got nil")
	}
}
