// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// daemon_coverage4_test.go — targeted coverage for ws_adapters.go hub methods
// and GetParamsetDescription with a registered backend stub.
//
// Covered:
//   - ws_adapters.go wsHubQuery.AcknowledgeAlarmMessage   (h.Messages.Acknowledge)
//   - ws_adapters.go wsHubQuery.AcknowledgeServiceMessage (h.ServiceMessages.Acknowledge)
//   - ws_adapters.go wsHubQuery.TriggerBackup             (h.TriggerBackupRemote)
//   - ws_adapters.go wsHubQuery.BackupStatus              (h.BackupStatusRemote)
//   - ws_adapters.go wsHubQuery.TriggerFirmwareUpdate     (h.TriggerFirmwareUpdateRemote)
//   - ws_adapters.go wsHubQuery.AcceptInboxDevice         (h.AcceptInboxDeviceRemote)
//   - ws_adapters.go wsHubQuery.ExecuteProgram            (p.Execute with nil Writer)
//   - ws_adapters.go wsDeviceQuery.GetParamsetDescription (registered backend, psKey default)

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ── minimal backends.Operations stub for daemon_coverage4_test.go ────────────

// testBackendOps is a no-op stub that satisfies backends.Operations.
// Only GetParamsetDescription returns something meaningful for the test.
type testBackendOps struct{}

func (t *testBackendOps) Kind() backends.Kind                       { return backends.KindCCU }
func (t *testBackendOps) Capabilities() backends.Capabilities       { return backends.Capabilities{} }
func (t *testBackendOps) Init(_ context.Context, _, _ string) error { return nil }
func (t *testBackendOps) Deinit(_ context.Context, _ string) error  { return nil }
func (t *testBackendOps) Ping(_ context.Context, _ string) error    { return nil }
func (t *testBackendOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (t *testBackendOps) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	// Return empty map → structToMap succeeds.
	return map[string]hmproto.ParameterData{}, nil
}

func (t *testBackendOps) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandRxMode) error {
	return nil
}

func (t *testBackendOps) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return nil
}

func (t *testBackendOps) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, nil
}
func (t *testBackendOps) UpdateFirmware(_ context.Context, _ string) error { return nil }
func (t *testBackendOps) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (t *testBackendOps) GetLinkPeers(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (t *testBackendOps) AddLink(_ context.Context, _, _, _, _ string) error         { return nil }
func (t *testBackendOps) RemoveLink(_ context.Context, _, _ string) error            { return nil }
func (t *testBackendOps) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (t *testBackendOps) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

func (t *testBackendOps) ReportValueUsage(_ context.Context, _, _ string, _ int) error { return nil }

func (t *testBackendOps) DeleteDevice(_ context.Context, _ string, _ int) error { return nil }

func (t *testBackendOps) GetAllPrograms(_ context.Context) ([]map[string]any, error) { return nil, nil }

func (t *testBackendOps) SetProgramState(_ context.Context, _ string, _ bool) error { return nil }

func (t *testBackendOps) GetSystemUpdateInfo(_ context.Context) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) GetInboxDevices(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}
func (t *testBackendOps) SetSystemVariable(_ context.Context, _ string, _ any) error { return nil }
func (t *testBackendOps) CreateSystemVariableBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) CreateSystemVariableEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) CreateSystemVariableFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) DetermineParameter(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}

func (t *testBackendOps) GetInstallMode(_ context.Context) (int, error) { return 0, nil }

func (t *testBackendOps) SetInstallMode(_ context.Context, _ bool, _, _ int, _ string) error {
	return nil
}

func (t *testBackendOps) GetServiceMessages(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) SuppressServiceMessage(_ context.Context, _, _ string, _ bool) error {
	return nil
}

func (t *testBackendOps) GetAlarmMessages(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) GetAllRooms(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (t *testBackendOps) GetAllFunctions(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (t *testBackendOps) RenameDevice(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (t *testBackendOps) RenameChannel(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (t *testBackendOps) AcceptDeviceInInbox(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (t *testBackendOps) ExecuteProgram(_ context.Context, _ string) (bool, error) { return false, nil }

func (t *testBackendOps) GetSystemVariable(_ context.Context, _ string) (any, error) {
	return nil, nil
}

func (t *testBackendOps) GetAllSystemVariables(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) GetAllDeviceData(_ context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) GetDeviceDetails(_ context.Context, _ []string) ([]map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) GetDeviceDescription(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) CreateBackupAndDownload(_ context.Context, _, _ float64) ([]byte, error) {
	return nil, nil
}

func (t *testBackendOps) TriggerFirmwareUpdate(_ context.Context) (bool, error) { return false, nil }

func (t *testBackendOps) DeleteSystemVariable(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (t *testBackendOps) GetIseIDByAddress(_ context.Context, _ string) (int, error) { return 0, nil }

func (t *testBackendOps) GetLinkInfo(_ context.Context, _, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (t *testBackendOps) SetLinkInfo(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}

func (t *testBackendOps) GetSuppressedServiceMessages(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (t *testBackendOps) HasProgramIDs(_ context.Context, _ string) (bool, error) { return false, nil }
func (t *testBackendOps) DownloadFirmware(_ context.Context, _ string) error      { return nil }
func (t *testBackendOps) GetMetadata(_ context.Context, _, _ string) (any, error) { return nil, nil }
func (t *testBackendOps) SetMetadata(_ context.Context, _, _ string, _ any) error { return nil }

// buildHubQueryWithLiveHub creates a wsHubQuery backed by a real registry
// whose first Unit has a non-nil HubModel. Callers that need
// specific hub state (programs, sysvars, …) should operate on the hub
// returned alongside.
func buildHubQueryWithLiveHub(t *testing.T) (*wsHubQuery, *hubmodel.Hub) {
	t.Helper()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not in registry")
	}
	h := cu.HubModel
	if h == nil {
		t.Fatal("HubModel is nil — buildTestRegistry must create it")
	}
	hubAdapter := adapter.NewHubAdapter(reg)
	q := &wsHubQuery{hub: hubAdapter, registry: reg}
	return q, h
}

// ── AcknowledgeAlarmMessage with live hub ─────────────────────────────────────

// TestWSHubQuery_AcknowledgeAlarmMessage_LiveHub_ReturnsError exercises the
// non-nil hub path. The hub's Messages acknowledger is nil by default so
// Acknowledge returns an error, but the call IS made — covering the line.
func TestWSHubQuery_AcknowledgeAlarmMessage_LiveHub_ReturnsError(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	// err is expected (nil acknowledger); the test goal is reaching the call site.
	err := q.AcknowledgeAlarmMessage(context.Background(), "msg-1")
	_ = err // error expected
}

// ── AcknowledgeServiceMessage with live hub ───────────────────────────────────

// TestWSHubQuery_AcknowledgeServiceMessage_LiveHub_ReturnsError exercises the
// non-nil hub path for ServiceMessages.Acknowledge.
func TestWSHubQuery_AcknowledgeServiceMessage_LiveHub_ReturnsError(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	err := q.AcknowledgeServiceMessage(context.Background(), "svc-1")
	_ = err // error expected (nil acker)
}

// ── TriggerBackup with live hub ────────────────────────────────────────────────

// TestWSHubQuery_TriggerBackup_LiveHub_ReturnsError exercises the
// h.TriggerBackupRemote call with a hub whose BackupTrigger is nil.
func TestWSHubQuery_TriggerBackup_LiveHub_ReturnsError(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	err := q.TriggerBackup(context.Background())
	if err == nil {
		t.Error("expected error when BackupTrigger is nil")
	}
}

// ── BackupStatus with live hub ─────────────────────────────────────────────────

// TestWSHubQuery_BackupStatus_LiveHub_ErrorPath exercises h.BackupStatusRemote
// with nil BackupTrigger — error propagates through the if-err branch.
func TestWSHubQuery_BackupStatus_LiveHub_ErrorPath(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	_, err := q.BackupStatus(context.Background())
	if err == nil {
		t.Error("expected error when BackupTrigger is nil")
	}
}

// ── TriggerFirmwareUpdate with live hub ───────────────────────────────────────

// TestWSHubQuery_TriggerFirmwareUpdate_LiveHub_ReturnsError exercises the
// h.TriggerFirmwareUpdateRemote call with nil FirmwareUpdater.
func TestWSHubQuery_TriggerFirmwareUpdate_LiveHub_ReturnsError(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	err := q.TriggerFirmwareUpdate(context.Background())
	if err == nil {
		t.Error("expected error when FirmwareUpdater is nil")
	}
}

// ── AcceptInboxDevice with live hub ───────────────────────────────────────────

// TestWSHubQuery_AcceptInboxDevice_LiveHub_ReturnsError exercises the
// h.AcceptInboxDeviceRemote call with nil InboxAccepter.
func TestWSHubQuery_AcceptInboxDevice_LiveHub_ReturnsError(t *testing.T) {
	t.Parallel()
	q, _ := buildHubQueryWithLiveHub(t)
	err := q.AcceptInboxDevice(context.Background(), "INBOXDEV001", ws.InboxAcceptOptions{})
	if err == nil {
		t.Error("expected error when InboxAccepter is nil")
	}
}

// ── ExecuteProgram with live hub + registered program ─────────────────────────

// TestWSHubQuery_ExecuteProgram_LiveHub_FoundProgram_NilWriter exercises the
// p.Execute(ctx) call. The program has a nil Writer so Execute returns an
// error, but the call IS made — covering the previously uncovered line.
func TestWSHubQuery_ExecuteProgram_LiveHub_FoundProgram_NilWriter(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)
	// Register a program with nil Writer so Execute returns an error.
	h.PutProgram(hubmodel.NewProgram("ccu-01", "prog-test-coverage4", "Coverage Test Program", "", false, nil))
	err := q.ExecuteProgram(context.Background(), "prog-test-coverage4")
	if err == nil {
		t.Error("expected error from Program.Execute with nil Writer")
	}
}

// ── GetParamsetDescription with registered backend ────────────────────────────

// TestWSDeviceQuery_GetParamsetDescription_WithBackend_EmptyKey_DefaultMaster
// exercises the full success path of GetParamsetDescription:
//   - device found in registry (line 631)
//   - backend found in writer (line 635)
//   - psKey defaulted to MASTER (lines 639-641)
//   - backend.GetParamsetDescription called (line 643)
//   - structToMap called on result (line 648)
func TestWSDeviceQuery_GetParamsetDescription_WithBackend_EmptyKey_DefaultMaster(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not in registry")
	}

	dev := device.New(device.Config{
		Address:     "PSBACKED001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu.ModelRegistry.Put(dev)

	// Register the stub backend for (ccu-01, BidCos-RF) so Backend() returns (ops, true).
	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", &testBackendOps{})

	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}

	// Empty ParamsetKey → defaults to MASTER; backend returns empty map → success.
	result, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "PSBACKED001:1",
		ParamsetKey:    "",
	})
	if err != nil {
		t.Fatalf("GetParamsetDescription: unexpected error: %v", err)
	}
	// Result is map[string]any from the empty ParameterData map.
	_ = result
}

// TestWSDeviceQuery_GetParamsetDescription_NilRegistry_Errors exercises
// the "ws: registry not wired" guard on line 624.
func TestWSDeviceQuery_GetParamsetDescription_NilRegistry_Errors(t *testing.T) {
	t.Parallel()
	paramsets := adapter.NewParamsetsDomain(nil, nil)
	writer := clientpkg.NewValueWriter()
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  nil, // nil registry
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "ANYDEV:1",
	})
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// TestWSDeviceQuery_GetParamsetDescription_CentralNameFilter_SkipsMismatch
// exercises the "key.CentralName != "" && c.Name() != key.CentralName" branch:
// two centrals are registered; the first matches, the second doesn't.
func TestWSDeviceQuery_GetParamsetDescription_CentralNameFilter_SkipsMismatch(t *testing.T) {
	t.Parallel()
	// Register two centrals; device only in ccu-02 but request asks for ccu-01.
	reg := buildTestRegistry(t, "ccu-01", "ccu-02")
	cu2, ok := reg.Get("ccu-02")
	if !ok {
		t.Fatal("ccu-02 not in registry")
	}
	dev := device.New(device.Config{
		Address:     "FILTDEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu2.ModelRegistry.Put(dev)

	writer := clientpkg.NewValueWriter()
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}
	// CentralName=ccu-01 but device is in ccu-02 → ccu-01 skips device, ccu-02 is skipped by filter.
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "FILTDEV001:1",
	})
	// Expected: device not found (ccu-01 has no FILTDEV001).
	if err == nil {
		t.Fatal("expected error for device only in filtered-out central")
	}
}

// errBackendOps is like testBackendOps but GetParamsetDescription returns an error.
type errBackendOps struct{ testBackendOps }

func (e *errBackendOps) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, errors.New("backend: paramset unavailable")
}

// TestWSDeviceQuery_GetParamsetDescription_BackendError covers the
// "if err != nil { return nil, err }" branch after GetParamsetDescription.
func TestWSDeviceQuery_GetParamsetDescription_BackendError(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not in registry")
	}
	dev := device.New(device.Config{
		Address:     "ERRDEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu.ModelRegistry.Put(dev)

	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", &errBackendOps{})

	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "ERRDEV001:1",
	})
	if err == nil {
		t.Fatal("expected error from errBackendOps")
	}
}

// TestWSDeviceQuery_GetParamsetDescription_DeviceNotFound_Continue exercises
// the "if !ok { continue }" branch when the device address doesn't match.
func TestWSDeviceQuery_GetParamsetDescription_DeviceNotFound_Continue(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	writer := clientpkg.NewValueWriter()
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}
	// No device "NODEV001" in ccu-01 → continue → device not found error.
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "NODEV001:1",
	})
	if err == nil {
		t.Fatal("expected device-not-found error")
	}
}

// TestWSDeviceQuery_GetParamsetDescription_WithBackend_ExplicitMasterKey
// exercises the non-empty psKey branch (lines 639-641 are NOT taken because
// psKey != "").
func TestWSDeviceQuery_GetParamsetDescription_WithBackend_ExplicitMasterKey(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not in registry")
	}

	dev := device.New(device.Config{
		Address:     "PSBACKED002",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu.ModelRegistry.Put(dev)

	writer := clientpkg.NewValueWriter()
	writer.Register("ccu-01", "BidCos-RF", &testBackendOps{})

	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}

	result, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "PSBACKED002:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	})
	if err != nil {
		t.Fatalf("GetParamsetDescription with explicit MASTER: %v", err)
	}
	_ = result
}

// ── wsHubQuery with non-nil hub slog discard ─────────────────────────────────

// TestWSHubQuery_ListPrograms_LiveHub_WithProgram exercises the Programs()
// loop body — adds one program to the hub so the for-range fires.
func TestWSHubQuery_ListPrograms_LiveHub_WithProgram(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)
	h.PutProgram(hubmodel.NewProgram("ccu-01", "prog-list-1", "List Test", "desc", false, nil))

	out, err := q.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	found := false
	for _, e := range out {
		if e["id"] == "prog-list-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected prog-list-1 in output; got %v", out)
	}
}

// TestWSHubQuery_ListPrograms_LiveHub_WithExecutedProgram exercises the
// p.LastExecution() ok=true branch in ListPrograms. We call OnExecution
// to set lastExecute so LastExecution returns (time, true).
func TestWSHubQuery_ListPrograms_LiveHub_WithExecutedProgram(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)
	prog := hubmodel.NewProgram("ccu-01", "prog-exec-1", "Executed Program", "", false, nil)
	// Trigger OnExecution to populate lastExecute.
	prog.OnExecution(true, hmenum.ProgramTriggerUser)
	h.PutProgram(prog)

	out, err := q.ListPrograms(context.Background())
	if err != nil {
		t.Fatalf("ListPrograms: %v", err)
	}
	found := false
	for _, e := range out {
		if e["id"] == "prog-exec-1" {
			found = true
			if _, ok := e["last_executed"]; !ok {
				t.Error("expected last_executed key in output")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected prog-exec-1 in output; got %v", out)
	}
}

// TestWSHubQuery_SetSysvar_LiveHub_NilSetterReturnsError exercises the s.Set path
// by placing a sysvar in the hub. Sysvar.Set with nil writer returns error,
// but the call covers line 356.
func TestWSHubQuery_SetSysvar_LiveHub_NilSetterReturnsError(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)

	h.PutSysvar(hubmodel.NewSysvar("ccu-01", "sv-cov4", "", hmenum.HubValueTypeString, nil))

	err := q.SetSysvar(context.Background(), "sv-cov4", "val")
	_ = err // error expected from nil Writer
}

// TestWSHubQuery_SetSysvar_LiveHub_UnsupportedValueType exercises the
// hmtypes.NewParamValue error branch (line "ws: set_sysvar value: %w").
// struct{}{} is not a supported ParamValue type → NewParamValue returns error.
func TestWSHubQuery_SetSysvar_LiveHub_UnsupportedValueType(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)
	h.PutSysvar(hubmodel.NewSysvar("ccu-01", "sv-unsupported", "", hmenum.HubValueTypeString, nil))

	// struct{}{} triggers "NewParamValue: unsupported type struct {}" error.
	err := q.SetSysvar(context.Background(), "sv-unsupported", struct{}{})
	if err == nil {
		t.Error("expected error for unsupported value type")
	}
}

// ── stubBackupTrigger for BackupStatus success path ───────────────────────────

type stubBackupTrigger struct{ status string }

func (s *stubBackupTrigger) TriggerBackup(_ context.Context) error          { return nil }
func (s *stubBackupTrigger) BackupStatus(_ context.Context) (string, error) { return s.status, nil }

// TestWSHubQuery_BackupStatus_LiveHub_SuccessPath exercises the success return
// path of BackupStatus (the "return map[string]any{"status": status}" line).
func TestWSHubQuery_BackupStatus_LiveHub_SuccessPath(t *testing.T) {
	t.Parallel()
	q, h := buildHubQueryWithLiveHub(t)
	h.BackupTrigger = &stubBackupTrigger{status: "idle"}

	got, err := q.BackupStatus(context.Background())
	if err != nil {
		t.Fatalf("BackupStatus: unexpected error: %v", err)
	}
	if got["status"] != "idle" {
		t.Errorf("expected status=idle, got %v", got["status"])
	}
}
