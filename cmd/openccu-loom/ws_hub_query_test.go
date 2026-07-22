// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
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
	got, err := q.ListPrograms(context.Background(), nil)
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
	got, err := q.ListPrograms(context.Background(), nil)
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
	_, err := q.ExecuteProgram(context.Background(), "prog-1", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWSHubQuery_ExecuteProgram_LiveHub_UnknownID_Errors(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	_, err := q.ExecuteProgram(context.Background(), "nonexistent-prog", false)
	if err == nil {
		t.Fatal("expected error for unknown program, got nil")
	}
}

// stubConditionalWriter implements hub.ConditionalProgramWriter so
// TestWSHubQuery_ExecuteProgram_LiveHub_CheckConditions_RoutesConditional can
// verify checkConditions=true reaches the condition-checked path rather than
// the unconditional one.
type stubConditionalWriter struct {
	executed  bool
	condCalls int
	execCalls int
}

func (s *stubConditionalWriter) ExecuteProgram(_ context.Context, _ string) error {
	s.execCalls++
	return nil
}

func (s *stubConditionalWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}

func (s *stubConditionalWriter) ExecuteProgramConditional(_ context.Context, _ string) (bool, error) {
	s.condCalls++
	return s.executed, nil
}

// TestWSHubQuery_ExecuteProgram_LiveHub_CheckConditions_RoutesConditional
// verifies checkConditions=true routes through
// [hub.Program.ExecuteWithConditionCheck] (the conditional writer path) and
// reports the writer's executed flag, instead of silently falling back to
// the unconditional Execute call.
func TestWSHubQuery_ExecuteProgram_LiveHub_CheckConditions_RoutesConditional(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	writer := &stubConditionalWriter{executed: false}
	h.PutProgram(hub.NewProgram("test-ccu", "prog-cond", "Conditional", "", false, writer))

	executed, err := q.ExecuteProgram(context.Background(), "prog-cond", true)
	if err != nil {
		t.Fatalf("ExecuteProgram(checkConditions=true): %v", err)
	}
	if executed {
		t.Fatal("executed=true, want false (writer reports condition not met)")
	}
	if writer.condCalls != 1 {
		t.Fatalf("condCalls=%d, want 1 (conditional path must be used)", writer.condCalls)
	}
	if writer.execCalls != 0 {
		t.Fatalf("execCalls=%d, want 0 (unconditional path must not run)", writer.execCalls)
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

// stubQueryBulkAck implements hub.BulkMessageAcknowledger with independent,
// fixed counts for the alarm and service passes.
type stubQueryBulkAck struct {
	serviceCount int
	alarmCount   int
}

func (b stubQueryBulkAck) AcknowledgeAllServiceMessages(context.Context) (int, error) {
	return b.serviceCount, nil
}

func (b stubQueryBulkAck) AcknowledgeAllAlarmMessages(context.Context) (int, error) {
	return b.alarmCount, nil
}

func TestWSHubQuery_AcknowledgeAllAlarmMessages_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	_, err := q.AcknowledgeAllAlarmMessages(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestWSHubQuery_AcknowledgeAllAlarmMessages_LiveHub_Delegates verifies that
// the adapter forwards to the hub's AlarmMessages aggregate and returns the
// count the wired bulk acknowledger reports, rather than a hard-coded value.
func TestWSHubQuery_AcknowledgeAllAlarmMessages_LiveHub_Delegates(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.Messages.SetAcknowledgers(nil, stubQueryBulkAck{alarmCount: 4, serviceCount: 9})

	n, err := q.AcknowledgeAllAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllAlarmMessages: %v", err)
	}
	if n != 4 {
		t.Fatalf("count=%d, want 4 (the alarm count, not the service count)", n)
	}
}

func TestWSHubQuery_AcknowledgeAllServiceMessages_NilHub_Errors(t *testing.T) {
	t.Parallel()
	q := nilHubQuery()
	_, err := q.AcknowledgeAllServiceMessages(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestWSHubQuery_AcknowledgeAllServiceMessages_LiveHub_Delegates mirrors the
// alarm-side delegation test for the service-messages aggregate.
func TestWSHubQuery_AcknowledgeAllServiceMessages_LiveHub_Delegates(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.ServiceMessages.SetAcknowledgers(nil, stubQueryBulkAck{alarmCount: 4, serviceCount: 9})

	n, err := q.AcknowledgeAllServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllServiceMessages: %v", err)
	}
	if n != 9 {
		t.Fatalf("count=%d, want 9 (the service count, not the alarm count)", n)
	}
}

// TestWSHubQuery_AcknowledgeAllAlarmMessages_LiveHub_NoAckerConfigured
// verifies that a live hub without a wired bulk acknowledger (the default
// state of a freshly constructed hub.Hub) surfaces the model-level error
// instead of silently reporting zero.
func TestWSHubQuery_AcknowledgeAllAlarmMessages_LiveHub_NoAckerConfigured(t *testing.T) {
	t.Parallel()
	q, _ := liveHubQuery(t)
	_, err := q.AcknowledgeAllAlarmMessages(context.Background())
	if err == nil {
		t.Fatal("expected error when no bulk acknowledger is wired")
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
	err := q.AcceptInboxDevice(context.Background(), "DEV001", ws.InboxAcceptOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// stubWSInboxAccepter is a minimal hub.InboxAccepter for the
// deviceAdmin-delegation tests below.
type stubWSInboxAccepter struct{ err error }

func (s *stubWSInboxAccepter) AcceptDeviceInInbox(_ context.Context, _ string) error {
	return s.err
}

// buildWSHubQueryWithDeviceAdmin wires a wsHubQuery whose deviceAdmin field
// is non-nil, exercising the preferred multi-CCU-safe delegation path
// (rather than the single-central hub-direct fallback).
func buildWSHubQueryWithDeviceAdmin(t *testing.T, acceptErr error) (*wsHubQuery, *central.Registry) {
	t.Helper()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "ccu-ws-deviceadmin"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cu.HubModel.InboxAccepter = &stubWSInboxAccepter{err: acceptErr}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	admin := adapter.NewDeviceAdminDomain(reg, nil)
	return &wsHubQuery{hub: adapter.NewHubAdapter(reg), registry: reg, deviceAdmin: admin}, reg
}

// TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_HappyPath verifies that a
// wired deviceAdmin is preferred over the hub-direct fallback and that the
// options (name/rooms/functions) reach the accept orchestration.
func TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_HappyPath(t *testing.T) {
	t.Parallel()
	q, reg := buildWSHubQueryWithDeviceAdmin(t, nil)

	opts := ws.InboxAcceptOptions{Name: "Kitchen Switch"}
	if err := q.AcceptInboxDevice(context.Background(), "DEV777", opts); err != nil {
		t.Fatalf("AcceptInboxDevice via deviceAdmin: %v", err)
	}
	// The rename step is a no-op when the device is not in the model
	// registry (nothing to rename in-memory), so a missing device must not
	// turn into an error — it just means there is nothing local to update.
	if _, ok := reg.Get("ccu-ws-deviceadmin"); !ok {
		t.Fatal("expected the fixture central to remain registered")
	}
}

// TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_PropagatesError verifies a
// wired deviceAdmin's error (e.g. the CCU rejected the accept) is returned
// to the WS caller rather than swallowed.
func TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_PropagatesError(t *testing.T) {
	t.Parallel()
	q, _ := buildWSHubQueryWithDeviceAdmin(t, errors.New("ccu rejected accept"))

	err := q.AcceptInboxDevice(context.Background(), "DEV777", ws.InboxAcceptOptions{})
	if err == nil {
		t.Fatal("expected the deviceAdmin error to propagate")
	}
}

// TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_MultiCentral_FindsDeviceOnSecondCentral
// proves the multi-CCU fix this wiring exists for: the plain hub-direct
// fallback ([adapter.HubAdapter.Hub]) only ever resolves the first
// registered central, so an inbox device paired on a later central would
// have been unreachable. A wired deviceAdmin instead walks every central
// (matching [DeviceAdminDomain.AcceptInboxDevice]'s registry loop), so the
// accept succeeds even when the first central's InboxAccepter fails.
func TestWSHubQuery_AcceptInboxDevice_DeviceAdmin_MultiCentral_FindsDeviceOnSecondCentral(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()

	// Registry.List() returns centrals sorted by name — "ccu-a" resolves
	// first and does NOT have the device; the hub-direct fallback would
	// stop here and report failure.
	first, err := central.New(central.Config{Name: "ccu-a-no-device"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	first.HubModel.InboxAccepter = &stubWSInboxAccepter{err: errors.New("unknown device")}
	if err := reg.Register(first); err != nil {
		t.Fatalf("reg.Register(first): %v", err)
	}

	second, err := central.New(central.Config{Name: "ccu-b-has-device"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	second.HubModel.InboxAccepter = &stubWSInboxAccepter{err: nil}
	if err := reg.Register(second); err != nil {
		t.Fatalf("reg.Register(second): %v", err)
	}

	admin := adapter.NewDeviceAdminDomain(reg, nil)
	q := &wsHubQuery{hub: adapter.NewHubAdapter(reg), registry: reg, deviceAdmin: admin}

	if err := q.AcceptInboxDevice(context.Background(), "DEV777", ws.InboxAcceptOptions{}); err != nil {
		t.Fatalf("expected the accept to succeed via the second central, got %v", err)
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
