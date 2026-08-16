// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// connection_recovery_stage_duration_test.go covers:
// RecoveryStageChangedEvent.DurationInOldStageMs (stage timing),
// per-interface CurrentStage tracking, CacheCoordinator.InvalidateParamsetDescriptions,
// HubCoordinator.SuppressServiceMessage, and DeviceCoordinator.GetVirtualRemotes
// / IdentifyChannel.

package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ──────────────────────────────────────────────────────────────────────────────
// C-RECOV-4: RecoveryStageChangedEvent.DurationInOldStageMs
// ──────────────────────────────────────────────────────────────────────────────

// TestStageChangedEventCarriesDuration pins that
// RecoveryStageChangedEvent.DurationInOldStageMs is set to a non-negative
// value for every stage transition.
func TestStageChangedEventCarriesDuration(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	coord := NewConnectionRecoveryCoordinator("c1", bus)

	var durations []int64
	events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
		durations = append(durations, e.DurationInOldStageMs)
	})

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageCooldown, Run: func(_ context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return nil }},
	}
	result := coord.Run(context.Background(), "iface-1", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("expected success, got %v", result)
	}
	if len(durations) != 2 {
		t.Fatalf("expected 2 stage-changed events, got %d", len(durations))
	}
	for i, d := range durations {
		if d < 0 {
			t.Errorf("stage %d: duration must be non-negative, got %d", i, d)
		}
	}
}

// TestStageChangedDurationOnFailedStageTransition verifies the duration
// field is also set when a stage step returns an error.
func TestStageChangedDurationOnFailedStageTransition(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	coord := NewConnectionRecoveryCoordinator("c1", bus)

	var captured int64 = -1
	events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
		if e.To == hmenum.RecoveryStageReconnecting {
			captured = e.DurationInOldStageMs
		}
	})

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageCooldown, Run: func(_ context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(_ context.Context) error { return errors.New("tcp refused") }},
	}
	_ = coord.Run(context.Background(), "iface-2", pipeline)

	if captured < 0 {
		t.Fatalf("DurationInOldStageMs not set on failed transition: got %d", captured)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C-RECOV-8: per-interface CurrentStage tracking
// ──────────────────────────────────────────────────────────────────────────────

// TestCurrentStageIdleBeforeRun verifies unknown interfaces return Idle.
func TestCurrentStageIdleBeforeRun(t *testing.T) {
	t.Parallel()
	coord := NewConnectionRecoveryCoordinator("c1", events.NewBus())
	if got := coord.CurrentStage("never-seen"); got != hmenum.RecoveryStageIdle {
		t.Fatalf("expected Idle for unknown interface, got %v", got)
	}
}

// TestCurrentStageTrackedDuringRun verifies that CurrentStage reflects
// the active stage while the pipeline is executing and resets to Idle
// after a successful run.
func TestCurrentStageTrackedDuringRun(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	coord := NewConnectionRecoveryCoordinator("c1", bus)

	const iface = "HmIP-RF"

	// A barrier so we can observe the stage while the step is in
	// progress.
	reached := make(chan struct{})
	proceed := make(chan struct{})

	pipeline := []Pipeline{
		{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(_ context.Context) error {
				close(reached) // signal: stage entered
				<-proceed      // wait until we've read CurrentStage
				return nil
			},
		},
	}

	done := make(chan hmenum.RecoveryResult, 1)
	go func() {
		done <- coord.Run(context.Background(), iface, pipeline)
	}()

	// Wait until the step is executing.
	select {
	case <-reached:
	case <-time.After(eventWaitTimeout):
		t.Fatal("pipeline step not reached")
	}

	got := coord.CurrentStage(iface)
	if got != hmenum.RecoveryStageReconnecting {
		t.Errorf("during run: expected Reconnecting, got %v", got)
	}

	close(proceed) // allow step to complete

	res := <-done
	if res != hmenum.RecoveryResultSuccess {
		t.Fatalf("run result: expected success, got %v", res)
	}

	// After a successful run, stage should be back to Idle.
	if got := coord.CurrentStage(iface); got != hmenum.RecoveryStageIdle {
		t.Errorf("after run: expected Idle, got %v", got)
	}
}

// TestCurrentStageFailedOnError verifies that CurrentStage transitions to
// Failed when a pipeline step returns an error.
func TestCurrentStageFailedOnError(t *testing.T) {
	t.Parallel()
	coord := NewConnectionRecoveryCoordinator("c1", events.NewBus())

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageRPCChecking, Run: func(_ context.Context) error { return errors.New("rpc down") }},
	}
	result := coord.Run(context.Background(), "iface-x", pipeline)
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("expected failed, got %v", result)
	}
	// After failure, stage is marked as Failed.
	if got := coord.CurrentStage("iface-x"); got != hmenum.RecoveryStageFailed {
		t.Errorf("expected Failed after error, got %v", got)
	}
}

// TestCurrentStageReflectedInStateSnapshot verifies that
// InterfaceRecoveryState.CurrentStage mirrors CurrentStage().
func TestCurrentStageReflectedInStateSnapshot(t *testing.T) {
	t.Parallel()
	coord := NewConnectionRecoveryCoordinator("c1", events.NewBus())

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageRPCChecking, Run: func(_ context.Context) error { return errors.New("down") }},
	}
	_ = coord.Run(context.Background(), "iface-snap", pipeline)

	snap := coord.State("iface-snap")
	if snap.CurrentStage != hmenum.RecoveryStageFailed {
		t.Errorf("State.CurrentStage=%v, want Failed", snap.CurrentStage)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// P2 — CacheCoordinator.InvalidateParamsetDescriptions
// ──────────────────────────────────────────────────────────────────────────────

type stubParamsetInvalidator struct {
	called []string
}

func (s *stubParamsetInvalidator) InvalidateByInterface(iface string) {
	s.called = append(s.called, iface)
}

// TestCacheInvalidateNoopWhenNotWired verifies no panic when no
// invalidator is wired.
func TestCacheInvalidateNoopWhenNotWired(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	// Must not panic.
	c.InvalidateParamsetDescriptions("HmIP-RF")
}

// TestCacheInvalidateDelegates verifies that InvalidateParamsetDescriptions
// delegates to the wired ParamsetInvalidator.
func TestCacheInvalidateDelegates(t *testing.T) {
	t.Parallel()
	c := NewCacheCoordinator()
	stub := &stubParamsetInvalidator{}
	c.SetParamsetInvalidator(stub)
	c.InvalidateParamsetDescriptions("HmIP-RF")
	c.InvalidateParamsetDescriptions("")

	if len(stub.called) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(stub.called), stub.called)
	}
	if stub.called[0] != "HmIP-RF" {
		t.Errorf("first call iface=%q, want HmIP-RF", stub.called[0])
	}
	if stub.called[1] != "" {
		t.Errorf("second call iface=%q, want empty (clear all)", stub.called[1])
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// P2 — HubCoordinator.SuppressServiceMessage
// ──────────────────────────────────────────────────────────────────────────────

type stubSuppressor struct {
	called []struct {
		iface, ch, param string
		suppress         bool
	}
	err error
}

func (s *stubSuppressor) SuppressServiceMessage(_ context.Context, iface, ch, param string, suppress bool) error {
	s.called = append(s.called, struct {
		iface, ch, param string
		suppress         bool
	}{iface, ch, param, suppress})
	return s.err
}

// TestSuppressServiceMessageNoopWhenNotWired verifies that
// SuppressServiceMessage returns nil without a suppressor wired.
func TestSuppressServiceMessageNoopWhenNotWired(t *testing.T) {
	t.Parallel()
	hub := NewHubCoordinator("c1", events.NewBus())
	if err := hub.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC0001234:1", "STICKY_UNREACH", true); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestSuppressServiceMessageDelegates verifies the coordinator
// delegates to the wired suppressor with all parameters.
func TestSuppressServiceMessageDelegates(t *testing.T) {
	t.Parallel()
	hub := NewHubCoordinator("c1", events.NewBus())
	stub := &stubSuppressor{}
	hub.SetServiceMessageSuppressor(stub)

	if err := hub.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC0001234:1", "STICKY_UNREACH", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.called) != 1 {
		t.Fatalf("expected 1 delegate call, got %d", len(stub.called))
	}
	got := stub.called[0]
	if got.iface != "HmIP-RF" || got.ch != "ABC0001234:1" || got.param != "STICKY_UNREACH" || !got.suppress {
		t.Errorf("unexpected args: %+v", got)
	}
}

// TestSuppressServiceMessagePropagatesError verifies error propagation.
func TestSuppressServiceMessagePropagatesError(t *testing.T) {
	t.Parallel()
	hub := NewHubCoordinator("c1", events.NewBus())
	want := errors.New("rpc failure")
	stub := &stubSuppressor{err: want}
	hub.SetServiceMessageSuppressor(stub)

	if got := hub.SuppressServiceMessage(context.Background(), "HmIP-RF", "ch", "param", false); !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// P2 — DeviceCoordinator.GetVirtualRemotes / IdentifyChannel
// ──────────────────────────────────────────────────────────────────────────────

func newTestDevCoord(t *testing.T) *DeviceCoordinator {
	t.Helper()
	bus := events.NewBus()
	devReg := registry.NewDeviceRegistry()
	descReg := registry.NewDeviceDescriptionRegistry()
	paramReg := registry.NewParamsetRegistry()
	return NewDeviceCoordinator("c1", bus, devReg, descReg, paramReg, nil, nil)
}

func seedDeviceDescs(t *testing.T, coord *DeviceCoordinator, iface hmtypes.WireInterfaceID, descs []hmproto.DeviceDescription) {
	t.Helper()
	coord.HandleNewDevices(context.Background(), iface, descs)
}

// TestGetVirtualRemotesEmpty verifies empty slice for no virtual remotes
// (both the address-only and enriched variants).
func TestGetVirtualRemotesEmpty(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	if got := coord.GetVirtualRemoteAddresses(wireKey(hmenum.InterfaceBidCosRF)); len(got) != 0 {
		t.Fatalf("GetVirtualRemoteAddresses: expected empty, got %v", got)
	}
	if got := coord.GetVirtualRemotes(wireKey(hmenum.InterfaceBidCosRF)); len(got) != 0 {
		t.Fatalf("GetVirtualRemotes: expected empty, got %v", got)
	}
}

// TestGetVirtualRemoteAddressesReturnsVirtualTypes verifies that virtual
// remote device types are detected and returned as plain addresses.
// Preserved from the original test of the old GetVirtualRemotes.
func TestGetVirtualRemoteAddressesReturnsVirtualTypes(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	iface := wireKey(hmenum.InterfaceBidCosRF)
	descs := []hmproto.DeviceDescription{
		{Address: "VRT0001", Type: "HM-RCV-50", Children: []string{"VRT0001:1"}},
		{Address: "VRT0001:1", Type: "HM-RCV-50", Parent: "VRT0001"},
		{Address: "REG0001", Type: "HmIP-BWTH", Children: []string{"REG0001:1"}},
		{Address: "REG0001:1", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER", Parent: "REG0001"},
	}
	seedDeviceDescs(t, coord, iface, descs)
	got := coord.GetVirtualRemoteAddresses(iface)
	if len(got) != 1 || got[0] != "VRT0001" {
		t.Fatalf("expected [VRT0001], got %v", got)
	}
}

// TestGetVirtualRemotesReturnsVirtualTypes verifies that virtual remote
// device types are detected and returned as enriched entries.
func TestGetVirtualRemotesReturnsVirtualTypes(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	iface := wireKey(hmenum.InterfaceBidCosRF)
	descs := []hmproto.DeviceDescription{
		{Address: "VRT0001", Type: "HM-RCV-50", Children: []string{"VRT0001:1"}},
		{Address: "VRT0001:1", Type: "HM-RCV-50", Parent: "VRT0001"},
		{Address: "REG0001", Type: "HmIP-BWTH", Children: []string{"REG0001:1"}},
		{Address: "REG0001:1", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER", Parent: "REG0001"},
	}
	seedDeviceDescs(t, coord, iface, descs)
	got := coord.GetVirtualRemotes(iface)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got[0].Address != "VRT0001" {
		t.Errorf("Address = %q, want VRT0001", got[0].Address)
	}
	if got[0].DeviceType != "HM-RCV-50" {
		t.Errorf("DeviceType = %q, want HM-RCV-50", got[0].DeviceType)
	}
	if len(got[0].ChannelAddresses) != 1 || got[0].ChannelAddresses[0] != "VRT0001:1" {
		t.Errorf("ChannelAddresses = %v, want [VRT0001:1]", got[0].ChannelAddresses)
	}
}

// TestIdentifyChannelFindsMatch verifies channel address lookup by substring.
func TestIdentifyChannelFindsMatch(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	iface := wireKey(hmenum.InterfaceBidCosRF)
	descs := []hmproto.DeviceDescription{
		{Address: "ABC0001234", Type: "HM-ES-TX-WM", Children: []string{"ABC0001234:1"}},
		{Address: "ABC0001234:1", Type: "POWERMETER_SUBMITTER", Parent: "ABC0001234"},
	}
	seedDeviceDescs(t, coord, iface, descs)
	addr, found := coord.IdentifyChannel(iface, "ABC0001234:1")
	if !found {
		t.Fatal("expected match, got not found")
	}
	if addr != "ABC0001234:1" {
		t.Errorf("addr=%q, want ABC0001234:1", addr)
	}
}

// TestIdentifyChannelNoMatchReturnsNotFound verifies not-found case.
func TestIdentifyChannelNoMatchReturnsNotFound(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	_, found := coord.IdentifyChannel(wireKey(hmenum.InterfaceBidCosRF), "NOTHERE")
	if found {
		t.Fatal("expected not found")
	}
}

// TestIdentifyChannelEmptyTextReturnsFalse verifies empty text guard.
func TestIdentifyChannelEmptyTextReturnsFalse(t *testing.T) {
	t.Parallel()
	coord := newTestDevCoord(t)
	_, found := coord.IdentifyChannel(wireKey(hmenum.InterfaceBidCosRF), "")
	if found {
		t.Fatal("empty text must return not found")
	}
}
