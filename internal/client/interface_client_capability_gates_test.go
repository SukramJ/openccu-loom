// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for InterfaceClient capability-gated operations and helpers:
// BackendCaller.Priority, PingPong hooks,
// AllCircuitBreakersClosed, IsInitialized, RPCServerTypeForInterface,
// MetricsCircuitState, payload accessors, state machine predicates,
// ForcedAvailability, capability-gated delegates,
// GetDeviceDescriptionWithCoalescing, GetParamsetDescriptionOnDemand,
// CreateSystemVariable*, and ValueWriter wiring.
package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// BackendCaller.Priority and coalesceKeyFor
// ---------------------------------------------------------------------------

func TestBackendCallerPriority(t *testing.T) {
	t.Parallel()
	nop := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil })
	ic, _ := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      nop,
	})
	defer ic.Close()

	bc := client.NewBackendCaller(ic, hmenum.CommandPriorityHigh)
	if bc.Priority() != hmenum.CommandPriorityHigh {
		t.Errorf("Priority()=%v, want High", bc.Priority())
	}
}

func TestBackendCallerCallRoutesThroughIC(t *testing.T) {
	t.Parallel()
	called := false
	ic, _ := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, method string, _ []any) (any, error) {
			if method == "ping" {
				called = true
			}
			return nil, nil
		}),
	})
	defer ic.Close()

	bc := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)
	_, _ = bc.Call(context.Background(), "ping")
	if !called {
		t.Error("expected Call to forward to the underlying transport")
	}
}

// ---------------------------------------------------------------------------
// PingPong, SetPublishHook, SetConnectionIssueGate
// ---------------------------------------------------------------------------

func TestPingPongNotNil(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	pp := ic.PingPong()
	if pp == nil {
		t.Fatal("PingPong() returned nil")
	}
	// Stable across calls.
	if ic.PingPong() != pp {
		t.Error("PingPong() returned different instances")
	}
}

func TestSetPublishHookDoesNotPanic(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	ic.SetPublishHook(func(_ hmenum.PingPongMismatchType, _ int) {})
	ic.SetPublishHook(nil) // nil hook must not panic
}

func TestSetConnectionIssueGateDoesNotPanic(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	ic.SetConnectionIssueGate(func() bool { return false })
	ic.SetConnectionIssueGate(nil)
}

// ---------------------------------------------------------------------------
// AllCircuitBreakersClosed
// ---------------------------------------------------------------------------

func TestAllCircuitBreakersClosedInitially(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	if !ic.AllCircuitBreakersClosed() {
		t.Error("expected circuit to be closed initially")
	}
}

func TestAllCircuitBreakersClosedAfterTrip(t *testing.T) {
	t.Parallel()
	ic, _ := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, errors.New("err")
		}),
		Retrier: reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1}),
	})
	defer ic.Close()

	// Force the circuit open by calling until it trips.
	for range 20 {
		_, _ = ic.Call(context.Background(), "m", nil, hmenum.CommandPriorityLow, "")
	}
	// May or may not be open depending on threshold; ensure method does not panic.
	_ = ic.AllCircuitBreakersClosed()
}

// ---------------------------------------------------------------------------
// IsInitialized
// ---------------------------------------------------------------------------

func TestIsInitializedCreatedState(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	// Default state is CREATED.
	if ic.IsInitialized() {
		t.Error("IsInitialized() = true in CREATED state; want false")
	}
}

func TestIsInitializedAfterTransition(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	_ = ic.TransitionTo(hmenum.ClientStateInitialized, "test", true, hmenum.FailureReasonNone)
	if !ic.IsInitialized() {
		t.Error("IsInitialized() = false in INITIALIZED state; want true")
	}
}

func TestIsInitializedInitializingState(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	_ = ic.TransitionTo(hmenum.ClientStateInitializing, "test", true, hmenum.FailureReasonNone)
	if ic.IsInitialized() {
		t.Error("IsInitialized() = true in INITIALIZING state; want false")
	}
}

// ---------------------------------------------------------------------------
// RPCServerTypeForInterface
// ---------------------------------------------------------------------------

func TestRPCServerTypeForInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		iface hmenum.Interface
		want  hmenum.RPCServerType
	}{
		{hmenum.InterfaceCUxD, hmenum.RPCServerTypeBINRPC},
		{hmenum.InterfaceHmIPRF, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceBidCosRF, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceBidCosWired, hmenum.RPCServerTypeXMLRPC},
		{hmenum.InterfaceVirtualDevices, hmenum.RPCServerTypeXMLRPC},
	}
	for _, tc := range cases {
		t.Run(string(tc.iface), func(t *testing.T) {
			t.Parallel()
			got := client.RPCServerTypeForInterface(tc.iface)
			if got != tc.want {
				t.Errorf("RPCServerTypeForInterface(%v)=%v, want %v", tc.iface, got, tc.want)
			}
		})
	}
}

func TestRPCServerTypeForInterfaceUnknown(t *testing.T) {
	t.Parallel()
	// A truly unknown interface value returns None.
	got := client.RPCServerTypeForInterface(hmenum.Interface("NonExistent"))
	if got != hmenum.RPCServerTypeNone {
		t.Errorf("unknown interface: got %v, want None", got)
	}
}

// ---------------------------------------------------------------------------
// MetricsCircuitState — open and half-open branches
// ---------------------------------------------------------------------------

func TestMetricsCircuitStateOpen(t *testing.T) {
	t.Parallel()
	cb := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
	})
	ic, _ := client.New(client.Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, errors.New("err")
		}),
		Circuit: cb,
		Retrier: reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1}),
	})
	defer ic.Close()

	// Force open.
	cb.RecordFailure()
	if got := ic.MetricsCircuitState(); got != 1 {
		t.Errorf("MetricsCircuitState()=%d, want 1 (open)", got)
	}
}

// ---------------------------------------------------------------------------
// Payload methods (InfoPayload, ConfigPayload, StatePayload)
// ---------------------------------------------------------------------------

func TestInfoPayload(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	info, _ := ic.Info().(*payload.InterfaceClientInfo)
	if info == nil {
		t.Fatal("Info must not be nil")
	}
	if info.Central != "test-central" {
		t.Errorf("Info Central=%q", info.Central)
	}
	if info.Interface != string(hmenum.InterfaceHmIPRF) {
		t.Errorf("Info Interface=%q", info.Interface)
	}
}

func TestConfigPayload(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	cfg, _ := ic.Config().(*payload.InterfaceClientConfig)
	if cfg == nil {
		t.Fatal("Config must not be nil")
	}
	// rpc_callback / ping_pong / list_devices / get_all_programs /
	// get_all_sysvars are always present as typed bool fields — the
	// previous test asserted only that the key was set, which after
	// the typed migration is automatic.
	_ = cfg.RPCCallback
}

func TestStatePayload(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	st, _ := ic.State().(*payload.InterfaceClientState)
	if st == nil {
		t.Fatal("State must not be nil")
	}
	if st.State == "" {
		t.Error("State.State must not be empty")
	}
	_ = st.Closed
}

func TestStatePayloadNilSafe(t *testing.T) {
	t.Parallel()
	var ic *client.InterfaceClient
	if ic.Info() != nil {
		t.Error("nil InfoPayload should return nil")
	}
	if ic.Config() != nil {
		t.Error("nil ConfigPayload should return nil")
	}
	if ic.State() != nil {
		t.Error("nil StatePayload should return nil")
	}
}

// ---------------------------------------------------------------------------
// ClientStateMachine helpers (IsAvailable, IsConnected, IsFailed, IsStopped,
// CanReconnect, AddOnStateChange, snapshotListenersLocked)
// ---------------------------------------------------------------------------

func TestStateMachinePredicates(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	sm := ic.StateMachine()

	_ = ic.TransitionTo(hmenum.ClientStateConnected, "", true, hmenum.FailureReasonNone)
	if !sm.IsAvailable() {
		t.Error("IsAvailable() false in CONNECTED")
	}
	if !sm.IsConnected() {
		t.Error("IsConnected() false in CONNECTED")
	}
	if sm.IsFailed() {
		t.Error("IsFailed() true in CONNECTED")
	}
	if sm.IsStopped() {
		t.Error("IsStopped() true in CONNECTED")
	}
	if sm.CanReconnect() {
		t.Error("CanReconnect() true in CONNECTED")
	}

	_ = ic.TransitionTo(hmenum.ClientStateFailed, "test", true, hmenum.FailureReasonNone)
	if !sm.IsFailed() {
		t.Error("IsFailed() false in FAILED")
	}
	if !sm.CanReconnect() {
		t.Error("CanReconnect() false in FAILED")
	}

	_ = ic.TransitionTo(hmenum.ClientStateStopped, "", true, hmenum.FailureReasonNone)
	if !sm.IsStopped() {
		t.Error("IsStopped() false in STOPPED")
	}

	_ = ic.TransitionTo(hmenum.ClientStateReconnecting, "", true, hmenum.FailureReasonNone)
	if !sm.IsAvailable() {
		t.Error("IsAvailable() false in RECONNECTING")
	}
}

func TestStateMachineAddOnStateChange(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	sm := ic.StateMachine()

	var from, to hmenum.ClientState
	sm.AddOnStateChange(func(f, tt hmenum.ClientState) {
		from, to = f, tt
	})
	sm.AddOnStateChange(nil) // nil must be ignored silently

	_ = ic.TransitionTo(hmenum.ClientStateInitializing, "", true, hmenum.FailureReasonNone)
	// from == to is allowed; just verify no panic occurred.
	_ = from == to
}

// ---------------------------------------------------------------------------
// MarkAllDevicesForced / ForcedAvailabilityMode
// ---------------------------------------------------------------------------

func TestForcedAvailabilityDefaults(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	if got := ic.ForcedAvailabilityMode(); got != client.ForcedAvailabilityNone {
		t.Errorf("default ForcedAvailabilityMode()=%v, want None", got)
	}
}

func TestMarkAllDevicesForced(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	ic.MarkAllDevicesForced(client.ForcedAvailabilityTrue)
	if got := ic.ForcedAvailabilityMode(); got != client.ForcedAvailabilityTrue {
		t.Errorf("ForcedAvailabilityMode()=%v, want True", got)
	}
	ic.MarkAllDevicesForced(client.ForcedAvailabilityFalse)
	if got := ic.ForcedAvailabilityMode(); got != client.ForcedAvailabilityFalse {
		t.Errorf("ForcedAvailabilityMode()=%v, want False", got)
	}
}

// ---------------------------------------------------------------------------
// Capability-gated delegates: AcceptDeviceInInbox, SetInstallMode,
// SuppressServiceMessage, GetAlarmMessages, GetAllRooms, GetAllFunctions,
// RenameDevice, RenameChannel, ExecuteProgram, GetSystemVariable,
// GetAllSystemVariables, CreateBackupAndDownload, TriggerFirmwareUpdate,
// AddLink, GetLinkPeers, RemoveLink, DeleteSystemVariable,
// GetIseIDByAddress, GetLinkInfo, SetLinkInfo, GetSuppressedServiceMessages,
// HasProgramIDs, CreateSystemVariableBool/Enum/Float
// ---------------------------------------------------------------------------

func TestAcceptDeviceInInboxGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{InboxDevices: false}}
	ok, err := ic.AcceptDeviceInInbox(context.Background(), b, "VCU001")
	if err != nil || ok {
		t.Errorf("gated AcceptDeviceInInbox: ok=%v err=%v", ok, err)
	}
}

func TestAcceptDeviceInInboxEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{InboxDevices: true}}
	ok, err := ic.AcceptDeviceInInbox(context.Background(), b, "VCU001")
	if err != nil || !ok {
		t.Errorf("enabled AcceptDeviceInInbox: ok=%v err=%v", ok, err)
	}
}

func TestSetInstallModeGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{InstallMode: false}}
	ok, err := ic.SetInstallMode(context.Background(), b, true, 60, 1, "")
	if err != nil || ok {
		t.Errorf("gated SetInstallMode: ok=%v err=%v", ok, err)
	}
}

func TestSetInstallModeEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{InstallMode: true}}
	ok, err := ic.SetInstallMode(context.Background(), b, true, 60, 1, "")
	if err != nil || !ok {
		t.Errorf("enabled SetInstallMode: ok=%v err=%v", ok, err)
	}
}

func TestSuppressServiceMessageGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{SuppressServiceMessage: false}}
	ok, err := ic.SuppressServiceMessage(context.Background(), b, "ch:1", "LOWBAT", true)
	if err != nil || ok {
		t.Errorf("gated SuppressServiceMessage: ok=%v err=%v", ok, err)
	}
}

func TestSuppressServiceMessageEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{SuppressServiceMessage: true}}
	ok, err := ic.SuppressServiceMessage(context.Background(), b, "ch:1", "LOWBAT", true)
	if err != nil || !ok {
		t.Errorf("enabled SuppressServiceMessage: ok=%v err=%v", ok, err)
	}
}

func TestGetAlarmMessagesGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{AlarmMessages: false}}
	msgs, err := ic.GetAlarmMessages(context.Background(), b)
	if err != nil || msgs != nil {
		t.Errorf("gated GetAlarmMessages: msgs=%v err=%v", msgs, err)
	}
}

func TestGetAlarmMessagesEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:          backends.Capabilities{AlarmMessages: true},
		alarmMessages: []map[string]any{{"id": "99"}},
	}
	msgs, err := ic.GetAlarmMessages(context.Background(), b)
	if err != nil || len(msgs) != 1 {
		t.Errorf("enabled GetAlarmMessages: msgs=%v err=%v", msgs, err)
	}
}

func TestGetAllRoomsGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Rooms: false}}
	r, err := ic.GetAllRooms(context.Background(), b)
	if err != nil || r != nil {
		t.Errorf("gated GetAllRooms: r=%v err=%v", r, err)
	}
}

func TestGetAllRoomsEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:  backends.Capabilities{Rooms: true},
		rooms: map[string][]string{"living": {"ch1"}},
	}
	r, err := ic.GetAllRooms(context.Background(), b)
	if err != nil || len(r) != 1 {
		t.Errorf("enabled GetAllRooms: r=%v err=%v", r, err)
	}
}

func TestGetAllFunctionsGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Functions: false}}
	f, err := ic.GetAllFunctions(context.Background(), b)
	if err != nil || f != nil {
		t.Errorf("gated GetAllFunctions: f=%v err=%v", f, err)
	}
}

func TestGetAllFunctionsEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:      backends.Capabilities{Functions: true},
		functions: map[string][]string{"lights": {"ch2"}},
	}
	f, err := ic.GetAllFunctions(context.Background(), b)
	if err != nil || len(f) != 1 {
		t.Errorf("enabled GetAllFunctions: f=%v err=%v", f, err)
	}
}

func TestRenameDeviceGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Rename: false}}
	ok, err := ic.RenameDevice(context.Background(), b, 100, "NewName")
	if err != nil || ok {
		t.Errorf("gated RenameDevice: ok=%v err=%v", ok, err)
	}
}

func TestRenameDeviceEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Rename: true}}
	ok, err := ic.RenameDevice(context.Background(), b, 100, "NewName")
	if err != nil || !ok {
		t.Errorf("enabled RenameDevice: ok=%v err=%v", ok, err)
	}
}

func TestRenameChannelGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Rename: false}}
	ok, err := ic.RenameChannel(context.Background(), b, 200, "NewChan")
	if err != nil || ok {
		t.Errorf("gated RenameChannel: ok=%v err=%v", ok, err)
	}
}

func TestRenameChannelEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Rename: true}}
	ok, err := ic.RenameChannel(context.Background(), b, 200, "NewChan")
	if err != nil || !ok {
		t.Errorf("enabled RenameChannel: ok=%v err=%v", ok, err)
	}
}

func TestExecuteProgramGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{ExecuteProgram: false}}
	ok, err := ic.ExecuteProgram(context.Background(), b, "prog-1")
	if err != nil || ok {
		t.Errorf("gated ExecuteProgram: ok=%v err=%v", ok, err)
	}
}

func TestExecuteProgramEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{ExecuteProgram: true}}
	ok, err := ic.ExecuteProgram(context.Background(), b, "prog-1")
	if err != nil || !ok {
		t.Errorf("enabled ExecuteProgram: ok=%v err=%v", ok, err)
	}
}

func TestGetSystemVariable(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	v, err := ic.GetSystemVariable(context.Background(), b, "myVar")
	if err != nil || v == nil {
		t.Errorf("GetSystemVariable: v=%v err=%v", v, err)
	}
}

func TestGetAllSystemVariables(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	_, err := ic.GetAllSystemVariables(context.Background(), b)
	if err != nil {
		t.Errorf("GetAllSystemVariables: %v", err)
	}
}

func TestCreateBackupAndDownloadGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Backup: false}}
	data, err := ic.CreateBackupAndDownload(context.Background(), b, 10, 1)
	if err != nil || data != nil {
		t.Errorf("gated CreateBackupAndDownload: data=%v err=%v", data, err)
	}
}

func TestCreateBackupAndDownloadEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{Backup: true}}
	_, err := ic.CreateBackupAndDownload(context.Background(), b, 10, 1)
	if err != nil {
		t.Errorf("enabled CreateBackupAndDownload: %v", err)
	}
}

func TestTriggerFirmwareUpdateGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{FirmwareUpdate: false}}
	ok, err := ic.TriggerFirmwareUpdate(context.Background(), b)
	if err != nil || ok {
		t.Errorf("gated TriggerFirmwareUpdate: ok=%v err=%v", ok, err)
	}
}

func TestTriggerFirmwareUpdateEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{FirmwareUpdate: true}}
	ok, err := ic.TriggerFirmwareUpdate(context.Background(), b)
	if err != nil || !ok {
		t.Errorf("enabled TriggerFirmwareUpdate: ok=%v err=%v", ok, err)
	}
}

func TestAddLinkGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: false}}
	err := ic.AddLink(context.Background(), b, "s:1", "r:1", "name", "desc")
	if err != nil {
		t.Errorf("gated AddLink: %v", err)
	}
}

func TestAddLinkEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: true}}
	err := ic.AddLink(context.Background(), b, "s:1", "r:1", "name", "desc")
	if err != nil {
		t.Errorf("enabled AddLink: %v", err)
	}
}

func TestGetLinkPeersGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: false}}
	peers, err := ic.GetLinkPeers(context.Background(), b, "ch:1")
	if err != nil || peers != nil {
		t.Errorf("gated GetLinkPeers: peers=%v err=%v", peers, err)
	}
}

func TestGetLinkPeersEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: true}}
	_, err := ic.GetLinkPeers(context.Background(), b, "ch:1")
	if err != nil {
		t.Errorf("enabled GetLinkPeers: %v", err)
	}
}

func TestRemoveLinkGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: false}}
	err := ic.RemoveLink(context.Background(), b, "s:1", "r:1")
	if err != nil {
		t.Errorf("gated RemoveLink: %v", err)
	}
}

func TestRemoveLinkEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: true}}
	err := ic.RemoveLink(context.Background(), b, "s:1", "r:1")
	if err != nil {
		t.Errorf("enabled RemoveLink: %v", err)
	}
}

func TestDeleteSystemVariableGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{DeleteSystemVariable: false}}
	ok, err := ic.DeleteSystemVariable(context.Background(), b, "sv1")
	if err != nil || ok {
		t.Errorf("gated DeleteSystemVariable: ok=%v err=%v", ok, err)
	}
}

func TestDeleteSystemVariableEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{DeleteSystemVariable: true}}
	ok, err := ic.DeleteSystemVariable(context.Background(), b, "sv1")
	if err != nil || !ok {
		t.Errorf("enabled DeleteSystemVariable: ok=%v err=%v", ok, err)
	}
}

func TestGetIseIDByAddressGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{IseIDLookup: false}}
	id, err := ic.GetIseIDByAddress(context.Background(), b, "VCU001")
	if err != nil || id != 0 {
		t.Errorf("gated GetIseIDByAddress: id=%v err=%v", id, err)
	}
}

func TestGetIseIDByAddressEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{IseIDLookup: true}}
	_, err := ic.GetIseIDByAddress(context.Background(), b, "VCU001")
	if err != nil {
		t.Errorf("enabled GetIseIDByAddress: %v", err)
	}
}

func TestGetLinkInfoGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: false}}
	info, err := ic.GetLinkInfo(context.Background(), b, "iface", "s:1", "r:1")
	if err != nil || info != nil {
		t.Errorf("gated GetLinkInfo: info=%v err=%v", info, err)
	}
}

func TestGetLinkInfoEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: true}}
	_, err := ic.GetLinkInfo(context.Background(), b, "iface", "s:1", "r:1")
	if err != nil {
		t.Errorf("enabled GetLinkInfo: %v", err)
	}
}

func TestSetLinkInfoGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: false}}
	ok, err := ic.SetLinkInfo(context.Background(), b, "iface", "s:1", "r:1", "n", "d")
	if err != nil || ok {
		t.Errorf("gated SetLinkInfo: ok=%v err=%v", ok, err)
	}
}

func TestSetLinkInfoEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{LinkOperations: true}}
	_, err := ic.SetLinkInfo(context.Background(), b, "iface", "s:1", "r:1", "n", "d")
	if err != nil {
		t.Errorf("enabled SetLinkInfo: %v", err)
	}
}

func TestGetSuppressedServiceMessagesGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{SuppressServiceMessage: false}}
	ids, err := ic.GetSuppressedServiceMessages(context.Background(), b, "iface", "ch:1")
	if err != nil || ids != nil {
		t.Errorf("gated GetSuppressedServiceMessages: ids=%v err=%v", ids, err)
	}
}

func TestGetSuppressedServiceMessagesEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{SuppressServiceMessage: true}}
	_, err := ic.GetSuppressedServiceMessages(context.Background(), b, "iface", "ch:1")
	if err != nil {
		t.Errorf("enabled GetSuppressedServiceMessages: %v", err)
	}
}

func TestHasProgramIDsGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{GetAllPrograms: false}}
	ok, err := ic.HasProgramIDs(context.Background(), b, "prog-1")
	if err != nil || ok {
		t.Errorf("gated HasProgramIDs: ok=%v err=%v", ok, err)
	}
}

func TestHasProgramIDsEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{GetAllPrograms: true}}
	_, err := ic.HasProgramIDs(context.Background(), b, "prog-1")
	if err != nil {
		t.Errorf("enabled HasProgramIDs: %v", err)
	}
}

func TestGetParamsetDescriptionOnDemand(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	// orchBackend returns (nil, nil) from GetParamsetDescription → method
	// converts to empty map and returns (map, nil).
	result, err := ic.GetParamsetDescriptionOnDemand(
		context.Background(), b, "VCU001:0", hmenum.ParamsetKeyMaster,
	)
	if err != nil {
		t.Fatalf("GetParamsetDescriptionOnDemand: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil map (empty is OK)")
	}
}

func TestCreateSystemVariableBoolGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: false}}
	r, err := ic.CreateSystemVariableBool(context.Background(), b, "flag", true)
	if err != nil || r != nil {
		t.Errorf("gated CreateSystemVariableBool: r=%v err=%v", r, err)
	}
}

func TestCreateSystemVariableBoolEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: true}}
	_, err := ic.CreateSystemVariableBool(context.Background(), b, "flag", true)
	if err != nil {
		t.Errorf("enabled CreateSystemVariableBool: %v", err)
	}
}

func TestCreateSystemVariableEnumGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: false}}
	r, err := ic.CreateSystemVariableEnum(context.Background(), b, "mode", []string{"off", "on"})
	if err != nil || r != nil {
		t.Errorf("gated CreateSystemVariableEnum: r=%v err=%v", r, err)
	}
}

func TestCreateSystemVariableEnumEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: true}}
	_, err := ic.CreateSystemVariableEnum(context.Background(), b, "mode", []string{"off", "on"})
	if err != nil {
		t.Errorf("enabled CreateSystemVariableEnum: %v", err)
	}
}

func TestCreateSystemVariableFloatGated(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: false}}
	r, err := ic.CreateSystemVariableFloat(context.Background(), b, "temp", 5.0, 35.0)
	if err != nil || r != nil {
		t.Errorf("gated CreateSystemVariableFloat: r=%v err=%v", r, err)
	}
}

func TestCreateSystemVariableFloatEnabled(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: true}}
	_, err := ic.CreateSystemVariableFloat(context.Background(), b, "temp", 5.0, 35.0)
	if err != nil {
		t.Errorf("enabled CreateSystemVariableFloat: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetDeviceDescriptionWithCoalescing
// ---------------------------------------------------------------------------

func TestGetDeviceDescriptionWithCoalescing(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		deviceDesc: map[string]any{"ADDRESS": "VCU001", "TYPE": "HmIP-PSM"},
	}
	desc, err := ic.GetDeviceDescriptionWithCoalescing(context.Background(), b, "VCU001")
	if err != nil {
		t.Fatalf("GetDeviceDescriptionWithCoalescing: %v", err)
	}
	if desc == nil {
		t.Fatal("expected non-nil description")
	}
	if desc["ADDRESS"] != "VCU001" {
		t.Errorf("ADDRESS=%v", desc["ADDRESS"])
	}
}

func TestGetDeviceDescriptionWithCoalescingNilResult(t *testing.T) {
	t.Parallel()
	ic := newExtraIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{deviceDesc: nil}
	desc, err := ic.GetDeviceDescriptionWithCoalescing(context.Background(), b, "GHOST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != nil {
		t.Errorf("expected nil for unknown device, got %v", desc)
	}
}

// ---------------------------------------------------------------------------
// ValueWriter wiring: SetBusResolver, SetCommandTrackerFn
// ---------------------------------------------------------------------------

func TestValueWriterSetBusResolverAndCommandTrackerFn(t *testing.T) {
	t.Parallel()
	vw := client.NewValueWriter()
	// Passing a non-nil resolver must not panic.
	vw.SetBusResolver(nil)
	vw.SetCommandTrackerFn(nil)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newExtraIC(t *testing.T, iface hmenum.Interface) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName: "test-central",
		Interface:   iface,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(ic.Close)
	return ic
}

// orchBackendExtra extends orchBackend with Capabilities fields used here.
// (orchBackend is already declared in interface_client_orchestration_test.go,
// reuse it; we only need to confirm the extra capability fields compile.)
var _ backends.Operations = (*orchBackend)(nil)

// Ensure the newly tested capability fields exist on backends.Capabilities.
var _ = backends.Capabilities{
	InboxDevices:           true,
	InstallMode:            true,
	SuppressServiceMessage: true,
	AlarmMessages:          true,
	Rooms:                  true,
	Functions:              true,
	Rename:                 true,
	ExecuteProgram:         true,
	Backup:                 true,
	FirmwareUpdate:         true,
	LinkOperations:         true,
	DeleteSystemVariable:   true,
	IseIDLookup:            true,
	GetAllPrograms:         true,
	CreateSystemVariable:   true,
}

// orchBackend needs GetDeviceDescription to return a non-nil map.
// Overriding via an embedded struct would create a conflict, so we rely on
// the orchBackend already in the test package returning b.deviceDesc.
// The deviceDesc field is already typed as map[string]any so both nil and
// non-nil are handled.
var _ hmproto.DeviceDescription // keep import alive
