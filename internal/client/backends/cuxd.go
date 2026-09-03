// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// CuxdBackend talks to the CUxD bridge via BIN-RPC — never JSON-RPC,
// which is the reference implementation's workaround and not ours.
// TestCUxDUsesBINRPCBackend in tests/contract enforces it.
type CuxdBackend struct {
	bin Caller
	ann Announcer
}

// NewCuxdBackend constructs a backend.
func NewCuxdBackend(bin Caller, ann Announcer) *CuxdBackend {
	return &CuxdBackend{bin: bin, ann: ann}
}

// Kind implements Operations.
func (b *CuxdBackend) Kind() Kind { return KindCUxD }

// Capabilities implements Operations.
func (b *CuxdBackend) Capabilities() Capabilities { return CapabilityFor(KindCUxD) }

// Initialize implements [Initializer]. CUxD is BIN-RPC only with a fixed
// capability set; no probing is required.
func (b *CuxdBackend) Initialize(_ context.Context) error { return nil }

// Init / Deinit / Ping / ListDevices follow the same BIN-RPC shape
// as the CCU backend.
func (b *CuxdBackend) Init(ctx context.Context, interfaceID, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Init(ctx, interfaceID, callbackURL)
}

// Deinit implements Operations.
func (b *CuxdBackend) Deinit(ctx context.Context, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Deinit(ctx, callbackURL)
}

// Ping implements Operations.
func (b *CuxdBackend) Ping(ctx context.Context, interfaceID string) error {
	if b.bin == nil {
		return ErrNotWired
	}
	_, err := b.bin.Call(ctx, "ping", interfaceID)
	return err
}

// ListDevices implements Operations.
func (b *CuxdBackend) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	return listDevicesViaCaller(ctx, b.bin, "cuxd")
}

// GetParamsetDescription implements Operations.
func (b *CuxdBackend) GetParamsetDescription(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return getParamsetDescriptionViaCaller(ctx, b.bin, "cuxd", address, key)
}

// GetParamset implements Operations.
func (b *CuxdBackend) GetParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	return getParamsetViaCaller(ctx, b.bin, "cuxd", address, key)
}

// PutParamset implements Operations. rxMode is silently ignored —
// BIN-RPC has no rx_mode argument slot.
func (b *CuxdBackend) PutParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any,
	priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return putParamsetViaCaller(ctx, b.bin, address, key, values, priority, rxMode, false)
}

// SetValue implements Operations. rxMode is silently ignored —
// BIN-RPC has no rx_mode argument slot.
func (b *CuxdBackend) SetValue(
	ctx context.Context, address string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return setValueViaCaller(ctx, b.bin, address, parameter, value, priority, rxMode, false)
}

// GetValue implements Operations.
func (b *CuxdBackend) GetValue(
	ctx context.Context, address string, parameter hmenum.Parameter,
) (any, error) {
	return getValueViaCaller(ctx, b.bin, address, parameter)
}

// UpdateFirmware implements Operations. CUxD virtual devices are
// firmware-less; always returns [ErrUnsupported].
func (b *CuxdBackend) UpdateFirmware(context.Context, string) error {
	return ErrUnsupported
}

// RestoreConfigToDevice implements Operations. CUxD has no stored-config
// re-transmit; always returns [ErrUnsupported].
func (b *CuxdBackend) RestoreConfigToDevice(context.Context, string) error {
	return ErrUnsupported
}

// SearchDevices implements Operations. CUxD has no wired bus; always
// returns [ErrUnsupported].
func (b *CuxdBackend) SearchDevices(context.Context) (int, error) {
	return 0, ErrUnsupported
}

// SetTeam implements Operations. CUxD has no team concept; always
// returns [ErrUnsupported].
func (b *CuxdBackend) SetTeam(context.Context, string, string) error {
	return ErrUnsupported
}

// ListTeams implements Operations. CUxD has no team concept; always
// returns [ErrUnsupported].
func (b *CuxdBackend) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, ErrUnsupported
}

// TestDevice implements Operations. CUxD has no radio com-test; always
// returns [ErrUnsupported].
func (b *CuxdBackend) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, ErrUnsupported
}

// ListReplaceableDevices implements Operations. CUxD has no
// device-replacement concept; always returns [ErrUnsupported].
func (b *CuxdBackend) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, ErrUnsupported
}

// ReplaceDevice implements Operations. CUxD has no device-replacement
// concept; always returns [ErrUnsupported].
func (b *CuxdBackend) ReplaceDevice(context.Context, string, string) error {
	return ErrUnsupported
}

// --- direct links (not modelled by CUxD) --------------------------
//
// CUxD's virtual devices are composed at runtime and have no peer-to-
// peer link concept; every link method returns [ErrUnsupported] so
// the domain adapter can surface a clear 501 to the caller.

// GetLinks implements [Operations].
func (*CuxdBackend) GetLinks(context.Context, string) ([]hmproto.LinkDescription, error) {
	return nil, ErrUnsupported
}

// GetLinkPeers implements [Operations].
func (*CuxdBackend) GetLinkPeers(context.Context, string) ([]string, error) {
	return nil, ErrUnsupported
}

// AddLink implements [Operations].
func (*CuxdBackend) AddLink(context.Context, string, string, string, string) error {
	return ErrUnsupported
}

// RemoveLink implements [Operations].
func (*CuxdBackend) RemoveLink(context.Context, string, string) error {
	return ErrUnsupported
}

// GetLinkParamsetDescription implements [Operations].
func (*CuxdBackend) GetLinkParamsetDescription(context.Context, string, string) (map[string]hmproto.ParameterData, error) {
	return nil, ErrUnsupported
}

// GetLinkParamset implements [Operations].
func (*CuxdBackend) GetLinkParamset(context.Context, string, string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// PutLinkParamset implements [Operations].
func (*CuxdBackend) PutLinkParamset(context.Context, string, string, map[string]any) error {
	return ErrUnsupported
}

// ActivateLinkParamset implements [Operations]. CUxD has no central-link
// concept.
func (*CuxdBackend) ActivateLinkParamset(context.Context, string, string, bool) error {
	return ErrUnsupported
}

// ReportValueUsage implements [Operations]. CUxD has no central-link
// concept — its devices are virtual proxies, and click events flow
// through the BIN-RPC channel directly.
func (*CuxdBackend) ReportValueUsage(context.Context, string, string, int) error {
	return ErrUnsupported
}

// DeleteDevice implements [Operations]. CUxD virtual devices are
// owned by the CUxD daemon; openccu-loom does not pair / unpair them.
// Returning ErrUnsupported is the right answer — the SPA renders a
// 422 / 501 instead of trying.
func (*CuxdBackend) DeleteDevice(context.Context, string, int) error {
	return ErrUnsupported
}

// --- JSON-RPC extended operations ---
// CUxD has no JSON-RPC layer; all extended operations return ErrUnsupported.

// GetAllPrograms returns ErrUnsupported on CUxD; programs are CCU-only.
func (*CuxdBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// SetProgramState returns ErrUnsupported on CUxD; programs are CCU-only.
func (*CuxdBackend) SetProgramState(context.Context, string, bool) error {
	return ErrUnsupported
}

// GetSystemUpdateInfo returns ErrUnsupported on CUxD; system update info is CCU-only.
func (*CuxdBackend) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, ErrUnsupported
}

// GetInboxDevices returns ErrUnsupported on CUxD; inbox is CCU-only.
func (*CuxdBackend) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// SetSystemVariable returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) SetSystemVariable(context.Context, string, any) error {
	return ErrUnsupported
}

// CreateSystemVariableBool returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateSystemVariableEnum returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateSystemVariableFloat returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, ErrUnsupported
}

// DetermineParameter implements Operations. CUxD has no determineParameter
// XML-RPC method; returns ErrUnsupported.
func (*CuxdBackend) DetermineParameter(context.Context, string, string) (any, error) {
	return nil, ErrUnsupported
}

// --- Extended Operations: all unsupported on CUxD ----------------------

// GetInstallMode returns ErrUnsupported on CUxD; install mode is CCU-only.
func (*CuxdBackend) GetInstallMode(context.Context) (int, error) { return 0, ErrUnsupported }

// SetInstallMode returns ErrUnsupported on CUxD; install mode is CCU-only.
func (*CuxdBackend) SetInstallMode(context.Context, bool, int, int, string) error {
	return ErrUnsupported
}

// SetInstallModeLocal returns ErrUnsupported on CUxD; the HmIP LOCAL
// teach-in is CCU-only.
func (*CuxdBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return ErrUnsupported
}

// GetServiceMessages returns ErrUnsupported on CUxD; service messages are CCU-only.
func (*CuxdBackend) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// SuppressServiceMessage returns ErrUnsupported on CUxD; service messages are CCU-only.
func (*CuxdBackend) SuppressServiceMessage(context.Context, string, string, bool) error {
	return ErrUnsupported
}

// GetAlarmMessages returns ErrUnsupported on CUxD; alarm messages are CCU-only.
func (*CuxdBackend) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetAllRooms returns ErrUnsupported on CUxD; room enumeration is CCU-only.
func (*CuxdBackend) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, ErrUnsupported
}

// GetAllFunctions returns ErrUnsupported on CUxD; function enumeration is CCU-only.
func (*CuxdBackend) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, ErrUnsupported
}

// RenameDevice returns ErrUnsupported on CUxD; device renaming requires JSON-RPC.
func (*CuxdBackend) RenameDevice(context.Context, int, string) (bool, error) {
	return false, ErrUnsupported
}

// RenameChannel returns ErrUnsupported on CUxD; channel renaming requires JSON-RPC.
func (*CuxdBackend) RenameChannel(context.Context, int, string) (bool, error) {
	return false, ErrUnsupported
}

// AcceptDeviceInInbox returns ErrUnsupported on CUxD; inbox is CCU-only.
func (*CuxdBackend) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// ExecuteProgram returns ErrUnsupported on CUxD; programs are CCU-only.
func (*CuxdBackend) ExecuteProgram(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// GetSystemVariable returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) GetSystemVariable(context.Context, string) (any, error) {
	return nil, ErrUnsupported
}

// GetAllSystemVariables returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetAllDeviceData returns ErrUnsupported on CUxD; bulk device data requires JSON-RPC.
func (*CuxdBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetDeviceDetails returns ErrUnsupported on CUxD; device details require JSON-RPC.
func (*CuxdBackend) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetDeviceDescription returns ErrUnsupported on CUxD; device description requires JSON-RPC.
func (*CuxdBackend) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateBackupAndDownload returns ErrUnsupported on CUxD; backup is CCU-only.
func (*CuxdBackend) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, ErrUnsupported
}

// TriggerFirmwareUpdate returns ErrUnsupported on CUxD; firmware update is CCU-only.
func (*CuxdBackend) TriggerFirmwareUpdate(context.Context) (bool, error) {
	return false, ErrUnsupported
}

// DeleteSystemVariable returns ErrUnsupported on CUxD; system variables are CCU-only.
func (*CuxdBackend) DeleteSystemVariable(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// GetIseIDByAddress returns ErrUnsupported on CUxD; ISE-ID lookup requires JSON-RPC.
func (*CuxdBackend) GetIseIDByAddress(context.Context, string) (int, error) {
	return 0, ErrUnsupported
}

// GetLinkInfo returns ErrUnsupported on CUxD; link info requires JSON-RPC.
func (*CuxdBackend) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// SetLinkInfo returns ErrUnsupported on CUxD; link info requires JSON-RPC.
func (*CuxdBackend) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, ErrUnsupported
}

// GetSuppressedServiceMessages returns ErrUnsupported on CUxD; suppressed messages are CCU-only.
func (*CuxdBackend) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, ErrUnsupported
}

// HasProgramIDs returns ErrUnsupported on CUxD; program IDs are CCU-only.
func (*CuxdBackend) HasProgramIDs(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// DownloadFirmware implements Operations. CUxD is a CCU add-on, not a
// CCU: it has no firmware of its own to fetch. Returns [ErrUnsupported].
func (*CuxdBackend) DownloadFirmware(context.Context) error { return ErrUnsupported }

// GetMetadata implements Operations. Not available on CUxD; returns [ErrUnsupported].
func (*CuxdBackend) GetMetadata(context.Context, string, string) (any, error) {
	return nil, ErrUnsupported
}

// SetMetadata implements Operations. Not available on CUxD; returns [ErrUnsupported].
func (*CuxdBackend) SetMetadata(context.Context, string, string, any) error { return ErrUnsupported }
