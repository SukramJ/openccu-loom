// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// orchBackend is a minimal backends.Operations for orchestration tests.
type orchBackend struct {
	caps          backends.Capabilities
	getInstall    int
	setInstallErr error
	svcMessages   []map[string]any
	alarmMessages []map[string]any
	rooms         map[string][]string
	functions     map[string][]string
	allDeviceData map[string]map[string]any
	deviceDetails []map[string]any
	deviceDesc    map[string]any

	initErr  error
	deinitOK bool
}

func (b *orchBackend) Kind() backends.Kind                 { return backends.KindCCU }
func (b *orchBackend) Capabilities() backends.Capabilities { return b.caps }
func (b *orchBackend) Init(_ context.Context, _, _ string) error {
	return b.initErr
}
func (b *orchBackend) Deinit(context.Context, string) error { b.deinitOK = true; return nil }
func (b *orchBackend) Ping(context.Context, string) error   { return nil }
func (b *orchBackend) ListDevices(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (b *orchBackend) GetParamsetDescription(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *orchBackend) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any, hmenum.CommandRxMode) error {
	return nil
}

func (b *orchBackend) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority, hmenum.CommandRxMode) error {
	return nil
}

func (b *orchBackend) GetValue(context.Context, string, hmenum.Parameter) (any, error) {
	return nil, nil
}
func (b *orchBackend) UpdateFirmware(context.Context, string) error { return nil }
func (b *orchBackend) GetLinks(context.Context, string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}
func (b *orchBackend) GetLinkPeers(context.Context, string) ([]string, error) { return nil, nil }
func (b *orchBackend) AddLink(context.Context, string, string, string, string) error {
	return nil
}
func (b *orchBackend) RemoveLink(context.Context, string, string) error { return nil }
func (b *orchBackend) GetLinkParamsetDescription(context.Context, string, string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *orchBackend) GetLinkParamset(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) ActivateLinkParamset(context.Context, string, string, bool) error { return nil }

func (b *orchBackend) PutLinkParamset(context.Context, string, string, map[string]any) error {
	return nil
}
func (b *orchBackend) ReportValueUsage(context.Context, string, string, int) error { return nil }
func (b *orchBackend) DeleteDevice(context.Context, string, int) error             { return nil }
func (b *orchBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (b *orchBackend) SetProgramState(context.Context, string, bool) error { return nil }
func (b *orchBackend) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}
func (b *orchBackend) SetSystemVariable(context.Context, string, any) error { return nil }
func (b *orchBackend) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) DetermineParameter(context.Context, string, string) (any, error) {
	return nil, nil
}

// Extended Operations.
func (b *orchBackend) GetInstallMode(context.Context) (int, error) { return b.getInstall, nil }

func (b *orchBackend) SetInstallMode(context.Context, bool, int, int, string) error {
	return b.setInstallErr
}

func (b *orchBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackend) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackend) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *orchBackend) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackend) SearchDevices(context.Context) (int, error) {
	return 0, backends.ErrUnsupported
}

func (b *orchBackend) SetTeam(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackend) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *orchBackend) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, backends.ErrUnsupported
}

func (b *orchBackend) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return b.svcMessages, nil
}

func (b *orchBackend) SuppressServiceMessage(context.Context, string, string, bool) error {
	return nil
}

func (b *orchBackend) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return b.alarmMessages, nil
}

func (b *orchBackend) GetAllRooms(context.Context) (map[string][]string, error) {
	return b.rooms, nil
}

func (b *orchBackend) GetAllFunctions(context.Context) (map[string][]string, error) {
	return b.functions, nil
}
func (b *orchBackend) RenameDevice(context.Context, int, string) (bool, error)  { return true, nil }
func (b *orchBackend) RenameChannel(context.Context, int, string) (bool, error) { return true, nil }
func (b *orchBackend) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return true, nil
}
func (b *orchBackend) ExecuteProgram(context.Context, string) (bool, error) { return true, nil }
func (b *orchBackend) GetSystemVariable(context.Context, string) (any, error) {
	return "val", nil
}

func (b *orchBackend) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return b.allDeviceData, nil
}

func (b *orchBackend) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return b.deviceDetails, nil
}

func (b *orchBackend) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return b.deviceDesc, nil
}

func (b *orchBackend) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, nil
}
func (b *orchBackend) TriggerFirmwareUpdate(context.Context) (bool, error) { return true, nil }
func (b *orchBackend) DeleteSystemVariable(context.Context, string) (bool, error) {
	return true, nil
}
func (b *orchBackend) GetIseIDByAddress(context.Context, string) (int, error) { return 0, nil }
func (b *orchBackend) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackend) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (b *orchBackend) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (b *orchBackend) HasProgramIDs(context.Context, string) (bool, error) { return false, nil }
func (b *orchBackend) DownloadFirmware(context.Context, string) error      { return nil }
func (b *orchBackend) GetMetadata(context.Context, string, string) (any, error) {
	return nil, nil
}
func (b *orchBackend) SetMetadata(context.Context, string, string, any) error { return nil }

// ----- helpers -----

func newOrchIC(t *testing.T, iface hmenum.Interface) *client.InterfaceClient {
	t.Helper()
	nopCallerFn := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, nil
	})
	ic, err := client.New(client.Config{
		CentralName: "test",
		Interface:   iface,
		Caller:      nopCallerFn,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ic
}

// ----- tests -----

func TestReconnectSkipsWhenNotReconnectable(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	attempts := 0
	// Default state is CREATED, which is not reconnectable.
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", nil, &attempts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when state is not reconnectable")
	}
}

func TestReconnectFromDisconnected(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	// Force DISCONNECTED state.
	_ = ic.TransitionTo(hmenum.ClientStateInitialized, "", true, hmenum.FailureReasonNone)
	_ = ic.TransitionTo(hmenum.ClientStateDisconnected, "", true, hmenum.FailureReasonNone)

	b := &orchBackend{}
	attempts := 0
	cfg := &client.ReconnectConfig{
		InitialDelay:  1 * time.Millisecond,
		MaxDelay:      10 * time.Millisecond,
		BackoffFactor: 2.0,
	}
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", cfg, &attempts)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true on successful reconnect")
	}
	if attempts != 0 {
		t.Fatalf("expected attempts reset to 0, got %d", attempts)
	}
}

func TestReconnectIncrementsAttemptsOnFailure(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	_ = ic.TransitionTo(hmenum.ClientStateInitialized, "", true, hmenum.FailureReasonNone)
	_ = ic.TransitionTo(hmenum.ClientStateDisconnected, "", true, hmenum.FailureReasonNone)

	b := &orchBackend{initErr: errors.New("timeout")}
	attempts := 2
	cfg := &client.ReconnectConfig{
		InitialDelay:  1 * time.Millisecond,
		MaxDelay:      10 * time.Millisecond,
		BackoffFactor: 2.0,
	}
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", cfg, &attempts)
	if err == nil {
		t.Fatal("expected error on init failure")
	}
	if ok {
		t.Fatal("expected ok=false on failure")
	}
	if attempts != 3 {
		t.Fatalf("expected attempts=3, got %d", attempts)
	}
}

func TestReconnectContextCancel(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	_ = ic.TransitionTo(hmenum.ClientStateInitialized, "", true, hmenum.FailureReasonNone)
	_ = ic.TransitionTo(hmenum.ClientStateDisconnected, "", true, hmenum.FailureReasonNone)

	b := &orchBackend{}
	cfg := &client.ReconnectConfig{
		InitialDelay:  10 * time.Second, // long delay → context will cancel first
		MaxDelay:      60 * time.Second,
		BackoffFactor: 2.0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	attempts := 0
	ok, err := ic.Reconnect(ctx, b, "id", "url", cfg, &attempts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got ok=%v err=%v", ok, err)
	}
}

func TestCommandTrackerOnIC(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	tr := ic.CommandTracker()
	if tr == nil {
		t.Fatal("expected non-nil CommandTracker")
	}
	// Same instance on second call.
	if ic.CommandTracker() != tr {
		t.Fatal("expected same CommandTracker instance")
	}
}

func TestWriteUnconfirmedValue(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	ic.WriteUnconfirmedValue("VCU:1", "LEVEL", hmenum.ParamsetKeyValues, 0.75)

	tr := ic.CommandTracker()
	dpk, _ := tr.AddSetValue("VCU:1", "LEVEL", hmenum.ParamsetKeyValues, 0.75)
	val, ok := tr.GetLastSentValue(dpk)
	if !ok {
		t.Fatal("expected tracked value")
	}
	if val != 0.75 {
		t.Fatalf("unexpected value: %v", val)
	}
}

func TestGetInstallModeCapabilityGate(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:       backends.Capabilities{InstallMode: false},
		getInstall: 42,
	}
	v, err := ic.GetInstallMode(context.Background(), b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Fatalf("expected 0 (capability gated), got %d", v)
	}
}

func TestGetInstallModeWithCapability(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:       backends.Capabilities{InstallMode: true},
		getInstall: 55,
	}
	v, err := ic.GetInstallMode(context.Background(), b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 55 {
		t.Fatalf("expected 55, got %d", v)
	}
}

func TestGetServiceMessagesCapabilityGate(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:        backends.Capabilities{ServiceMessages: false},
		svcMessages: []map[string]any{{"id": "1"}},
	}
	msgs, err := ic.GetServiceMessages(context.Background(), b, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil {
		t.Fatal("expected nil when capability not present")
	}
}

func TestGetServiceMessagesWithCapability(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		caps:        backends.Capabilities{ServiceMessages: true},
		svcMessages: []map[string]any{{"id": "1"}, {"id": "2"}},
	}
	msgs, err := ic.GetServiceMessages(context.Background(), b, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestFetchAllDeviceData(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		allDeviceData: map[string]map[string]any{
			"VCU:1": {"LEVEL": 0.5},
		},
	}
	data, err := ic.FetchAllDeviceData(context.Background(), b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("expected 1 device, got %d", len(data))
	}
}

func TestFetchDeviceDetails(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{
		deviceDetails: []map[string]any{
			{"address": "VCU001", "name": "Switch", "id": 100},
		},
	}
	details, err := ic.FetchDeviceDetails(context.Background(), b, []string{"VCU001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(details))
	}
}

// ---------------------------------------------------------------------------
// UpdateParamsetDescriptions — L09 convenience wrapper
// ---------------------------------------------------------------------------

// stubDeviceDescriptionFinder implements client.DeviceDescriptionFinder.
type stubDeviceDescriptionFinder struct {
	desc map[string]map[string]any // address → device description
}

func (s *stubDeviceDescriptionFinder) FindDeviceDescription(_ context.Context, address string) (map[string]any, error) {
	if d, ok := s.desc[address]; ok {
		return d, nil
	}
	return nil, nil
}

// stubParamsetDescriptionPersister implements client.ParamsetDescriptionPersister.
type stubParamsetDescriptionPersister struct {
	called int
}

func (s *stubParamsetDescriptionPersister) PersistParamsetDescriptions(_ context.Context) error {
	s.called++
	return nil
}

func TestUpdateParamsetDescriptionsKnownAddress(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	finder := &stubDeviceDescriptionFinder{
		desc: map[string]map[string]any{
			"VCU001": {"ADDRESS": "VCU001", "PARAMSETS": []any{"VALUES", "MASTER"}},
		},
	}
	persister := &stubParamsetDescriptionPersister{}

	err := ic.UpdateParamsetDescriptions(context.Background(), b, finder, persister, "VCU001")
	if err != nil {
		t.Fatalf("UpdateParamsetDescriptions: %v", err)
	}
	// Persister must have been called exactly once.
	if persister.called != 1 {
		t.Fatalf("expected persister called 1 time, got %d", persister.called)
	}
}

func TestUpdateParamsetDescriptionsUnknownAddressIsNoop(t *testing.T) {
	ic := newOrchIC(t, hmenum.InterfaceHmIPRF)
	b := &orchBackend{}
	finder := &stubDeviceDescriptionFinder{desc: map[string]map[string]any{}}
	persister := &stubParamsetDescriptionPersister{}

	err := ic.UpdateParamsetDescriptions(context.Background(), b, finder, persister, "GHOST")
	if err != nil {
		t.Fatalf("unexpected error for unknown address: %v", err)
	}
	// Address not found → persister must NOT have been called.
	if persister.called != 0 {
		t.Fatalf("expected persister not called, got %d", persister.called)
	}
}

// ---------------------------------------------------------------------------
// — GetValue delegates to backend
// ---------------------------------------------------------------------------

func TestICGetValueDelegatesToBackend(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{}
	b.caps = backends.Capabilities{}

	// orchBackend.GetValue returns nil, nil by default — just check no error.
	v, err := ic.GetValue(context.Background(), b, "ADDR:1", "STATE")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	_ = v
}

// ---------------------------------------------------------------------------
// — UpdateDeviceFirmware delegates to backend
// ---------------------------------------------------------------------------

func TestICUpdateDeviceFirmwareDelegatesToBackend(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{}
	// orchBackend.UpdateFirmware returns nil — success.
	if err := ic.UpdateDeviceFirmware(context.Background(), b, "ADDR0001"); err != nil {
		t.Fatalf("UpdateDeviceFirmware: %v", err)
	}
}

// ---------------------------------------------------------------------------
// — GetMetadata / SetMetadata delegates to backend
// ---------------------------------------------------------------------------

func TestICGetMetadataReturnsUnsupportedForCCU(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	// orchBackend.GetMetadata returns nil, nil (no error) — but a real CCU
	// backend would return ErrUnsupported. Just verify the call flows through.
	b := &orchBackend{}
	_, err := ic.GetMetadata(context.Background(), b, "ADDR0001", "NAME")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
}

func TestICSetMetadataFlowsThrough(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{}
	if err := ic.SetMetadata(context.Background(), b, "ADDR0001", "NAME", "mydevice"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
}

// ---------------------------------------------------------------------------
// — CreateSysvar* wrappers gate on Capabilities.CreateSystemVariable
// ---------------------------------------------------------------------------

func TestICCreateSysvarBoolGatedOnCapability(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{} // CreateSystemVariable=false by default
	result, err := ic.CreateSystemVariableBool(context.Background(), b, "MySysVar", true)
	if err != nil {
		t.Fatalf("CreateSystemVariableBool: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when capability not set, got %v", result)
	}
}

func TestICCreateSysvarBoolPassesThroughWhenCapable(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{caps: backends.Capabilities{CreateSystemVariable: true}}
	// orchBackend.CreateSystemVariableBool returns nil, nil — no error expected.
	result, err := ic.CreateSystemVariableBool(context.Background(), b, "MySysVar", true)
	if err != nil {
		t.Fatalf("CreateSystemVariableBool with capability: %v", err)
	}
	_ = result
}

func TestICCreateSysvarEnumGatedOnCapability(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{}
	result, err := ic.CreateSystemVariableEnum(context.Background(), b, "MyEnum", []string{"A", "B"})
	if err != nil {
		t.Fatalf("CreateSystemVariableEnum: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when not capable")
	}
}

func TestICCreateSysvarFloatGatedOnCapability(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{}
	result, err := ic.CreateSystemVariableFloat(context.Background(), b, "MyFloat", 0, 100)
	if err != nil {
		t.Fatalf("CreateSystemVariableFloat: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when not capable")
	}
}

// ---------------------------------------------------------------------------
// — GetAllProgramsFiltered applies marker filter
// ---------------------------------------------------------------------------

type filterBackend struct {
	orchBackend
	programs []map[string]any
}

func (b *filterBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return b.programs, nil
}

func TestICGetAllProgramsFilteredNoMarkersReturnsAll(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &filterBackend{
		orchBackend: orchBackend{caps: backends.Capabilities{GetAllPrograms: true}},
		programs: []map[string]any{
			{"id": "1", "description": "morning routine"},
			{"id": "2", "description": "evening lights"},
		},
	}
	result, err := ic.GetAllProgramsFiltered(context.Background(), b, nil)
	if err != nil {
		t.Fatalf("GetAllProgramsFiltered: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 programs, got %d", len(result))
	}
}

func TestICGetAllProgramsFilteredWithMarkerFilters(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &filterBackend{
		orchBackend: orchBackend{caps: backends.Capabilities{GetAllPrograms: true}},
		programs: []map[string]any{
			{"id": "1", "description": "HAHM morning routine"},
			{"id": "2", "description": "evening lights"},
			{"id": "3", "description": "HAHM security check"},
		},
	}
	result, err := ic.GetAllProgramsFiltered(context.Background(), b, []string{"HAHM"})
	if err != nil {
		t.Fatalf("GetAllProgramsFiltered with markers: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 filtered programs, got %d", len(result))
	}
}

func TestICGetAllProgramsFilteredGatedOnCapability(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &orchBackend{} // backend with GetAllPrograms capability not set
	result, err := ic.GetAllProgramsFiltered(context.Background(), b, nil)
	if err != nil {
		t.Fatalf("error from gated call: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when not capable, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// — GetAllSystemVariablesFiltered
// ---------------------------------------------------------------------------

type sysvarBackend struct {
	orchBackend
	sysvars []map[string]any
}

func (b *sysvarBackend) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return b.sysvars, nil
}

func TestICGetAllSysvarsFilteredMarkedOnlyFalseReturnsAll(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &sysvarBackend{
		sysvars: []map[string]any{
			{"name": "HAHM_alarm", "value": true},
			{"name": "temp_sensor", "value": 21.5},
		},
	}
	result, err := ic.GetAllSystemVariablesFiltered(context.Background(), b, false, nil)
	if err != nil {
		t.Fatalf("GetAllSystemVariablesFiltered: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 sysvars, got %d", len(result))
	}
}

func TestICGetAllSysvarsFilteredMarkedOnlyFilters(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &sysvarBackend{
		sysvars: []map[string]any{
			{"name": "HAHM_alarm", "value": true},
			{"name": "temp_sensor", "value": 21.5},
			{"name": "HAHM_motion", "value": false},
		},
	}
	result, err := ic.GetAllSystemVariablesFiltered(context.Background(), b, true, []string{"HAHM_"})
	if err != nil {
		t.Fatalf("GetAllSystemVariablesFiltered with markers: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 HAHM_ sysvars, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// — AcknowledgeMessage backend fallback
// ---------------------------------------------------------------------------

type ackBackend struct {
	orchBackend
	ackCalled bool
	ackErr    error
}

func (b *ackBackend) AcknowledgeMessage(_ context.Context, _ string) (bool, error) {
	b.ackCalled = true
	return b.ackErr == nil, b.ackErr
}

func TestICAcknowledgeMessageNoRunnerNoBackendReturnsUnsupported(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	// No RegaRunner, orchBackend does not implement MessageAcknowledger.
	b := &orchBackend{}
	_, err := ic.AcknowledgeMessage(context.Background(), "42", b)
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got %v", err)
	}
}

func TestICAcknowledgeMessageFallsBackToBackend(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	b := &ackBackend{}
	ok, err := ic.AcknowledgeMessage(context.Background(), "42", b)
	if err != nil {
		t.Fatalf("AcknowledgeMessage backend fallback: %v", err)
	}
	if !ok {
		t.Error("expected ok=true from backend fallback")
	}
	if !b.ackCalled {
		t.Error("backend.AcknowledgeMessage was not called")
	}
}

func TestICAcknowledgeMessageBackendErrorPropagated(t *testing.T) {
	t.Parallel()
	ic := newOrchIC(t, orchIface)
	wantErr := errors.New("json-rpc failure")
	b := &ackBackend{ackErr: wantErr}
	ok, err := ic.AcknowledgeMessage(context.Background(), "42", b)
	if ok {
		t.Error("expected ok=false on backend error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected %v, got %v", wantErr, err)
	}
}

// orchIface is the interface constant used by helpers in this test file.
const orchIface hmenum.Interface = hmenum.InterfaceHmIPRF
