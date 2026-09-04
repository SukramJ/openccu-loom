// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// Model labels emitted by [HomegearBackend.Model] depending on the probed
// software version.
const (
	HomegearModelHomegear = "Homegear"
	HomegearModelPyDevCCU = "pydevccu"
)

// HomegearBackend talks to a Homegear daemon (or
// Homegear-compat mode). Homegear speaks **only XML-RPC** — there is
// no JSON-RPC / ReGa surface. System variables, metadata and device
// names are therefore reached via dedicated XML-RPC methods
// (`getSystemVariable`, `setSystemVariable`, `deleteSystemVariable`,
// `getAllSystemVariables`, `getMetadata`, `setMetadata`,
// `deleteMetadata`, `clientServerInitialized`). Firmware updates,
// programs, rooms, functions, install-mode and CCU-side backups have
// no Homegear analog and surface as [ErrUnsupported].
type HomegearBackend struct {
	xml     Caller
	ann     Announcer
	version string
}

// NewHomegearBackend constructs a backend. `ann` announces the
// callback URL; pass nil to skip Init/Deinit.
func NewHomegearBackend(xml Caller, ann Announcer) *HomegearBackend {
	return &HomegearBackend{xml: xml, ann: ann}
}

// SetVersion records the probed Homegear /
// so [HomegearBackend.Model] can distinguish the two flavours.
func (b *HomegearBackend) SetVersion(version string) { b.version = version }

// Version returns the recorded software version string ("" when
// [SetVersion] was never called).
func (b *HomegearBackend) Version() string { return b.version }

// Model returns the backend model label.
func (b *HomegearBackend) Model() string {
	if strings.Contains(strings.ToLower(b.version), strings.ToLower(HomegearModelPyDevCCU)) {
		return HomegearModelPyDevCCU
	}
	return HomegearModelHomegear
}

// Kind implements Operations.
func (b *HomegearBackend) Kind() Kind { return KindHomegear }

// Capabilities implements Operations.
func (b *HomegearBackend) Capabilities() Capabilities { return CapabilityFor(KindHomegear) }

// Initialize implements [Initializer]. Homegear is XML-RPC only with a fixed
// capability set; no probing is required.
func (b *HomegearBackend) Initialize(_ context.Context) error { return nil }

// Init implements Operations.
func (b *HomegearBackend) Init(ctx context.Context, interfaceID, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Init(ctx, interfaceID, callbackURL)
}

// Deinit implements Operations.
func (b *HomegearBackend) Deinit(ctx context.Context, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Deinit(ctx, callbackURL)
}

// Ping implements Operations. Homegear answers `clientServerInitialized`
// where the CCU answers `ping`. 90).
func (b *HomegearBackend) Ping(ctx context.Context, interfaceID string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "clientServerInitialized", interfaceID)
	return err
}

// ListDevices implements Operations.
func (b *HomegearBackend) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	return listDevicesViaCaller(ctx, b.xml, "homegear")
}

// GetParamsetDescription implements Operations.
func (b *HomegearBackend) GetParamsetDescription(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return getParamsetDescriptionViaCaller(ctx, b.xml, "homegear", address, key)
}

// GetParamset implements Operations.
func (b *HomegearBackend) GetParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	return getParamsetViaCaller(ctx, b.xml, "homegear", address, key)
}

// PutParamset implements Operations. rxMode is silently ignored —
// Homegear's XML-RPC surface does not define a rx_mode argument.
func (b *HomegearBackend) PutParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any,
	priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return putParamsetViaCaller(ctx, b.xml, address, key, values, priority, rxMode, false)
}

// SetValue implements Operations. Priority is advisory and dropped
// here; the caller's command throttle is the effective scheduler.
// rxMode is silently ignored — Homegear's XML-RPC surface does not
// define a rx_mode argument.
func (b *HomegearBackend) SetValue(
	ctx context.Context, address string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return setValueViaCaller(ctx, b.xml, address, parameter, value, priority, rxMode, false)
}

// GetValue implements Operations.
func (b *HomegearBackend) GetValue(
	ctx context.Context, address string, parameter hmenum.Parameter,
) (any, error) {
	return getValueViaCaller(ctx, b.xml, address, parameter)
}

// UpdateFirmware implements Operations. Homegear has no native
// firmware-update RPC analog to the CCU's `Device.updateFirmware`
// JSON call — Homegear is XML-RPC-only and does not expose a
// firmware-update method. Always returns [ErrUnsupported].
func (b *HomegearBackend) UpdateFirmware(_ context.Context, _ string) error {
	return ErrUnsupported
}

// RestoreConfigToDevice implements Operations. Homegear has no
// CCU-style stored-config re-transmit; always returns [ErrUnsupported].
func (b *HomegearBackend) RestoreConfigToDevice(_ context.Context, _ string) error {
	return ErrUnsupported
}

// SearchDevices implements Operations. Homegear has no wired-bus scan;
// always returns [ErrUnsupported].
func (b *HomegearBackend) SearchDevices(_ context.Context) (int, error) {
	return 0, ErrUnsupported
}

// SetTeam implements Operations. Homegear has no team concept; always
// returns [ErrUnsupported].
func (b *HomegearBackend) SetTeam(_ context.Context, _, _ string) error {
	return ErrUnsupported
}

// ListTeams implements Operations. Homegear has no team concept; always
// returns [ErrUnsupported].
func (b *HomegearBackend) ListTeams(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, ErrUnsupported
}

// TestDevice implements Operations. Homegear has no CCU com-test;
// always returns [ErrUnsupported].
func (b *HomegearBackend) TestDevice(_ context.Context, _ string, _, _ float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, ErrUnsupported
}

// ListReplaceableDevices implements Operations. Homegear has no
// device-replacement concept; always returns [ErrUnsupported].
func (b *HomegearBackend) ListReplaceableDevices(_ context.Context, _ string) ([]hmproto.DeviceDescription, error) {
	return nil, ErrUnsupported
}

// ReplaceDevice implements Operations. Homegear has no
// device-replacement concept; always returns [ErrUnsupported].
func (b *HomegearBackend) ReplaceDevice(_ context.Context, _, _ string) error {
	return ErrUnsupported
}

// --- direct links --------------------------------------------------

// GetLinks implements Operations.
//
// The flag word 0 is carried over from [CcuBackend.GetLinks], where it is
// grounded in the CCU firmware. Homegear is a re-implementation, not that
// firmware, so what its getLinks does with the flag word is unverified here:
// no source in the reference set states it. What would settle it is
// Homegear's own getLinks implementation. Until then, treat 0 as "the
// default", which is the only claim both sides support, and do not assume
// the CCU bit meanings apply.
func (b *HomegearBackend) GetLinks(ctx context.Context, channelAddress string) ([]hmproto.LinkDescription, error) {
	return getLinksViaCaller(ctx, b.xml, "homegear", channelAddress)
}

// GetLinkPeers implements Operations.
func (b *HomegearBackend) GetLinkPeers(ctx context.Context, channelAddress string) ([]string, error) {
	return getLinkPeersViaCaller(ctx, b.xml, "homegear", channelAddress)
}

// AddLink implements Operations.
func (b *HomegearBackend) AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "addLink", senderAddress, receiverAddress, name, description)
	return err
}

// RemoveLink implements Operations.
func (b *HomegearBackend) RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "removeLink", senderAddress, receiverAddress)
	return err
}

// GetLinkParamsetDescription implements Operations.
func (b *HomegearBackend) GetLinkParamsetDescription(ctx context.Context, channelAddress, _ string) (map[string]hmproto.ParameterData, error) {
	return getLinkParamsetDescriptionViaCaller(ctx, b.xml, "homegear", channelAddress)
}

// GetLinkParamset implements Operations.
func (b *HomegearBackend) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	return getLinkParamsetViaCaller(ctx, b.xml, "homegear", channelAddress, peerAddress)
}

// PutLinkParamset implements Operations.
func (b *HomegearBackend) PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error {
	return putLinkParamsetViaCaller(ctx, b.xml, channelAddress, peerAddress, values)
}

// ActivateLinkParamset implements Operations. Homegear's XML-RPC does not
// document activateLinkParamset; return ErrUnsupported until validated
// against a real Homegear instance rather than risk a spurious fault.
func (*HomegearBackend) ActivateLinkParamset(context.Context, string, string, bool) error {
	return ErrUnsupported
}

// ReportValueUsage implements Operations.
func (b *HomegearBackend) ReportValueUsage(ctx context.Context, channelAddress, valueID string, refCounter int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "reportValueUsage", channelAddress, valueID, refCounter)
	return err
}

// DeleteDevice implements Operations. Homegear's wire surface mirrors
// the CCU's `deleteDevice(address, flags)`. flags is the CCU delete bitmask
// ([DeleteFlagReset], [DeleteFlagForce]); 0 keeps the bidirectional unpair
// handshake.
func (b *HomegearBackend) DeleteDevice(ctx context.Context, address string, flags int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "deleteDevice", address, flags)
	return err
}

// --- Homegear-spezifische Erweiterungen --------------------------------

// GetSystemVariable returns a single system variable. Homegear-only,
// not part of the [Operations] interface — accessible via type
// assertion when the caller knows it has a HomegearBackend.
func (b *HomegearBackend) GetSystemVariable(ctx context.Context, name string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "getSystemVariable", name)
}

// GetAllSystemVariablesRaw returns the full sysvar map as a plain
// name→value map. This Homegear-specific helper is used internally
// (e.g. by the coordinator). The Operations interface method
// GetAllSystemVariables (below) wraps this into the standard
// []map[string]any shape.
func (b *HomegearBackend) GetAllSystemVariablesRaw(ctx context.Context) (map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getAllSystemVariables")
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("homegear.GetAllSystemVariablesRaw: unexpected type %T", raw)
	}
	return m, nil
}

// SetSystemVariable writes a single sysvar.
func (b *HomegearBackend) SetSystemVariable(ctx context.Context, name string, value any) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setSystemVariable", name, value)
	return err
}

// DeleteSystemVariable implements Operations. Removes a sysvar by name.
// Returns true on success. 103).
func (b *HomegearBackend) DeleteSystemVariable(ctx context.Context, name string) (bool, error) {
	if b.xml == nil {
		return false, ErrNotWired
	}
	_, err := b.xml.Call(ctx, "deleteSystemVariable", name)
	return err == nil, err
}

// GetMetadata reads a metadata blob attached to a device. Homegear
// stores device names under the data ID `"NAME"`.
func (b *HomegearBackend) GetMetadata(ctx context.Context, address, dataID string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "getMetadata", address, dataID)
}

// SetMetadata writes a metadata blob for a device.
func (b *HomegearBackend) SetMetadata(ctx context.Context, address, dataID string, value any) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setMetadata", address, dataID, value)
	return err
}

// DeleteMetadata removes a metadata blob.
func (b *HomegearBackend) DeleteMetadata(ctx context.Context, address, dataID string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "deleteMetadata", address, dataID)
	return err
}

// GetDeviceName fetches the device name via Metadata("NAME"). Returns an
// empty string when the device has no name set.
//
// 133) for the single-address case.
func (b *HomegearBackend) GetDeviceName(ctx context.Context, address string) (string, error) {
	raw, err := b.GetMetadata(ctx, address, "NAME")
	if err != nil {
		return "", err
	}
	if raw == nil {
		return "", nil
	}
	if s, ok := raw.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", raw), nil
}

// --- JSON-RPC extended operations ---
// Homegear has no CCU JSON-RPC layer; programs, inbox and update-info
// operations return ErrUnsupported. System variables are reachable via
// Homegear's own XML-RPC methods.

// GetAllPrograms returns ErrUnsupported on Homegear; programs are CCU-only.
func (*HomegearBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// SetProgramState returns ErrUnsupported on Homegear; programs are CCU-only.
func (*HomegearBackend) SetProgramState(context.Context, string, bool) error {
	return ErrUnsupported
}

// GetSystemUpdateInfo returns ErrUnsupported on Homegear; system update info is CCU-only.
func (*HomegearBackend) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, ErrUnsupported
}

// GetInboxDevices returns ErrUnsupported on Homegear; inbox is CCU-only.
func (*HomegearBackend) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateSystemVariableBool returns ErrUnsupported on Homegear; Homegear uses its own sysvar XML-RPC methods.
func (*HomegearBackend) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateSystemVariableEnum returns ErrUnsupported on Homegear; Homegear uses its own sysvar XML-RPC methods.
func (*HomegearBackend) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// CreateSystemVariableFloat returns ErrUnsupported on Homegear; Homegear uses its own sysvar XML-RPC methods.
func (*HomegearBackend) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, ErrUnsupported
}

// DetermineParameter implements Operations. Homegear supports the
// `determineParameter` XML-RPC method analogously to the CCU. Returns
// ErrNotWired when the XML caller is not configured.
func (b *HomegearBackend) DetermineParameter(ctx context.Context, channelAddress, parameter string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "determineParameter", channelAddress, parameter)
}

// --- Extended Operations: Operations interface conformance ----------------
// GetSystemVariable and GetAllSystemVariables are already defined above as
// non-interface Homegear-specific methods. They need to be present on the
// Operations interface but the Homegear versions have a different signature
// (GetAllSystemVariables returns map[string]any, not []map[string]any).
// We satisfy Operations with thin wrappers.

// GetInstallMode implements Operations. Homegear has no CCU-style
// install-mode RPC; returns ErrUnsupported.
func (*HomegearBackend) GetInstallMode(context.Context) (int, error) { return 0, ErrUnsupported }

// SetInstallMode implements Operations. Homegear has no CCU-style
// install-mode RPC; returns ErrUnsupported.
func (*HomegearBackend) SetInstallMode(context.Context, bool, int, int, string) error {
	return ErrUnsupported
}

// SetInstallModeLocal implements Operations. Homegear has no HmIP
// LOCAL teach-in; returns ErrUnsupported.
func (*HomegearBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return ErrUnsupported
}

// GetServiceMessages implements Operations. Not available on Homegear.
func (*HomegearBackend) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// SuppressServiceMessage implements Operations. Not available on Homegear.
func (*HomegearBackend) SuppressServiceMessage(context.Context, string, string, bool) error {
	return ErrUnsupported
}

// GetAlarmMessages implements Operations. Not available on Homegear.
func (*HomegearBackend) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetAllRooms implements Operations. Not available on Homegear.
func (*HomegearBackend) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, ErrUnsupported
}

// GetAllFunctions implements Operations. Not available on Homegear.
func (*HomegearBackend) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, ErrUnsupported
}

// RenameDevice implements Operations. Not available on Homegear.
func (*HomegearBackend) RenameDevice(context.Context, int, string) (bool, error) {
	return false, ErrUnsupported
}

// RenameChannel implements Operations. Not available on Homegear.
func (*HomegearBackend) RenameChannel(context.Context, int, string) (bool, error) {
	return false, ErrUnsupported
}

// AcceptDeviceInInbox implements Operations. Not available on Homegear.
func (*HomegearBackend) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// ExecuteProgram implements Operations. Not available on Homegear.
func (*HomegearBackend) ExecuteProgram(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// GetSystemVariable is already declared above (line ~348) as part of the
// Homegear-native API and satisfies Operations.GetSystemVariable.

// GetAllSystemVariables implements Operations.GetAllSystemVariables.
// Wraps GetAllSystemVariablesRaw (which returns map[string]any) into
// the []map[string]any shape the Operations interface demands.
func (b *HomegearBackend) GetAllSystemVariables(ctx context.Context) ([]map[string]any, error) {
	flat, err := b.GetAllSystemVariablesRaw(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(flat))
	for k, v := range flat {
		out = append(out, map[string]any{"name": k, "value": v})
	}
	return out, nil
}

// GetAllDeviceData implements Operations. Not available on Homegear.
func (*HomegearBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetDeviceDetails implements Operations. Returns device names by
// iterating known addresses and calling GetMetadata("NAME") for each.
func (b *HomegearBackend) GetDeviceDetails(ctx context.Context, addresses []string) ([]map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	out := make([]map[string]any, 0, len(addresses))
	for _, addr := range addresses {
		name, _ := b.GetDeviceName(ctx, addr)
		out = append(out, map[string]any{
			"address":  addr,
			"name":     name,
			"id":       0,
			"channels": []any{},
		})
	}
	return out, nil
}

// GetDeviceDescription implements Operations. Returns the raw device
// description for a single address via XML-RPC.
func (b *HomegearBackend) GetDeviceDescription(ctx context.Context, address string) (map[string]any, error) {
	return getDeviceDescriptionViaCaller(ctx, b.xml, "homegear", address)
}

// CreateBackupAndDownload implements Operations. Not available on Homegear.
func (*HomegearBackend) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, ErrUnsupported
}

// TriggerFirmwareUpdate implements Operations. Not available on Homegear.
func (*HomegearBackend) TriggerFirmwareUpdate(context.Context) (bool, error) {
	return false, ErrUnsupported
}

// GetIseIDByAddress implements Operations. Not available on Homegear.
func (*HomegearBackend) GetIseIDByAddress(context.Context, string) (int, error) {
	return 0, ErrUnsupported
}

// GetLinkInfo implements Operations. Not available on Homegear.
func (*HomegearBackend) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, ErrUnsupported
}

// SetLinkInfo implements Operations. Not available on Homegear.
func (*HomegearBackend) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, ErrUnsupported
}

// GetSuppressedServiceMessages implements Operations. Not available on Homegear.
func (*HomegearBackend) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, ErrUnsupported
}

// HasProgramIDs implements Operations. Not available on Homegear.
func (*HomegearBackend) HasProgramIDs(context.Context, string) (bool, error) {
	return false, ErrUnsupported
}

// DownloadFirmware implements Operations. Homegear is not a CCU and
// exposes no equivalent self-update call. Returns [ErrUnsupported].
func (*HomegearBackend) DownloadFirmware(context.Context) error { return ErrUnsupported }
