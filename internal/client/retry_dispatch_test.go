// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// retry_dispatch_test.go — countingBackend, a backends.Operations stub that
// counts SetValue invocations, shared by the write-path tests.

package client

import (
	"context"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// countingBackend records the number of SetValue invocations.
// All other methods are no-ops that satisfy backends.Operations.
type countingBackend struct {
	setCallCount atomic.Int64
}

func (b *countingBackend) incSet() error {
	b.setCallCount.Add(1)
	return nil
}

func (b *countingBackend) SetCallCount() int { return int(b.setCallCount.Load()) }

// --- backends.Operations stub ---
func (b *countingBackend) Kind() backends.Kind { return backends.KindCCU }

func (b *countingBackend) Capabilities() backends.Capabilities       { return backends.Capabilities{} }
func (b *countingBackend) Init(_ context.Context, _, _ string) error { return nil }
func (b *countingBackend) Deinit(_ context.Context, _ string) error  { return nil }
func (b *countingBackend) Ping(_ context.Context, _ string) error    { return nil }
func (b *countingBackend) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (b *countingBackend) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *countingBackend) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return nil
}

func (b *countingBackend) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return b.incSet()
}

func (b *countingBackend) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, nil
}
func (b *countingBackend) UpdateFirmware(_ context.Context, _ string) error { return nil }
func (b *countingBackend) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (b *countingBackend) GetLinkPeers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (b *countingBackend) AddLink(_ context.Context, _, _, _, _ string) error { return nil }
func (b *countingBackend) RemoveLink(_ context.Context, _, _ string) error    { return nil }
func (b *countingBackend) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *countingBackend) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) ActivateLinkParamset(context.Context, string, string, bool) error {
	return nil
}

func (b *countingBackend) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

func (b *countingBackend) ReportValueUsage(_ context.Context, _, _ string, _ int) error { return nil }

func (b *countingBackend) DeleteDevice(_ context.Context, _ string, _ int) error { return nil }

func (b *countingBackend) GetAllPrograms(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (b *countingBackend) SetProgramState(_ context.Context, _ string, _ bool) error { return nil }
func (b *countingBackend) GetSystemUpdateInfo(_ context.Context) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetInboxDevices(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}
func (b *countingBackend) SetSystemVariable(_ context.Context, _ string, _ any) error { return nil }
func (b *countingBackend) CreateSystemVariableBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateSystemVariableEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateSystemVariableFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) DetermineParameter(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}
func (b *countingBackend) GetInstallMode(_ context.Context) (int, error) { return 0, nil }
func (b *countingBackend) SetInstallMode(_ context.Context, _ bool, _, _ int, _ string) error {
	return nil
}

func (b *countingBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) ClearConfigCache(context.Context, string) error {
	return backends.ErrUnsupported
}

func (*countingBackend) RSSIInfo(context.Context) (map[string]map[string][2]int, error) {
	return nil, backends.ErrUnsupported
}

func (*countingBackend) SetBidcosInterface(context.Context, string, string, bool) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *countingBackend) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) SearchDevices(context.Context) (int, error) {
	return 0, backends.ErrUnsupported
}

func (b *countingBackend) SetTeam(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *countingBackend) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, backends.ErrUnsupported
}

func (b *countingBackend) GetServiceMessages(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) SuppressServiceMessage(_ context.Context, _, _ string, _ bool) error {
	return nil
}

func (b *countingBackend) GetAlarmMessages(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllRooms(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *countingBackend) GetAllFunctions(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *countingBackend) RenameDevice(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) RenameChannel(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) AcceptDeviceInInbox(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) ExecuteProgram(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetSystemVariable(_ context.Context, _ string) (any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllSystemVariables(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllDeviceData(_ context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetDeviceDetails(_ context.Context, _ []string) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetDeviceDescription(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateBackupAndDownload(_ context.Context, _, _ float64) ([]byte, error) {
	return nil, nil
}

func (b *countingBackend) TriggerFirmwareUpdate(_ context.Context) (bool, error) { return false, nil }

func (b *countingBackend) DeleteSystemVariable(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetIseIDByAddress(_ context.Context, _ string) (int, error) { return 0, nil }

func (b *countingBackend) GetLinkInfo(_ context.Context, _, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) SetLinkInfo(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetSuppressedServiceMessages(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (b *countingBackend) HasProgramIDs(_ context.Context, _ string) (bool, error) { return false, nil }
func (b *countingBackend) DownloadFirmware(_ context.Context) error                { return nil }
func (b *countingBackend) GetMetadata(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}
func (b *countingBackend) SetMetadata(_ context.Context, _, _ string, _ any) error { return nil }

var _ backends.Operations = (*countingBackend)(nil)
