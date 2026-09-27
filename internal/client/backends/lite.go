// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// LiteBackend talks to one interface process of an openccu-lite box
// through the box's authenticated XML-RPC proxy
// (`/api/rpc/v1/xmlrpc/<interface>`). The proxy forwards to the same
// interface processes a CCU runs, so every pure XML-RPC operation keeps
// the CCU backend's wire shape. What a CCU serves over JSON-RPC, ReGa or
// the WebUI has no counterpart on the box: the XML-RPC equivalent is used
// where the interface process offers one (install mode, link info,
// service-message suppression, gateway list), and every other operation
// refuses with a [*hmerr.FeatureUnavailableError] that wraps
// [ErrUnsupported], so existing [errors.Is] branches keep working.
//
// Every call passes through the lite refusal mapping (see
// [liteFaultCaller]).
type LiteBackend struct {
	xml       Caller // fault-mapping wrapper around the proxy caller; nil when unwired
	ann       Announcer
	ifaceType hmenum.Interface
}

// NewLiteBackend constructs a backend for the interface iface. xml is the
// caller bound to that interface's proxy URL; ann replaces the callback
// registration (the lite announcer subscribes to the event stream instead
// of calling `init`) — pass nil to make Init/Deinit no-ops. There is no
// JSON-RPC caller: the box has no JSON-RPC surface.
func NewLiteBackend(iface hmenum.Interface, xml Caller, ann Announcer) *LiteBackend {
	b := &LiteBackend{ann: ann, ifaceType: iface}
	if xml != nil {
		b.xml = &liteFaultCaller{next: xml}
	}
	return b
}

// liteFaultCaller applies [occulited.ClassifyFault] to every call it
// forwards, so the shared wire helpers get the lite refusal mapping without
// knowing it: a tier refusal becomes a [*hmerr.ScopeMissingError], the
// `init` refusal wraps [occulited.ErrInitRefused] (a Loom bug if it ever
// fires). The classification is idempotent, so a transport that already
// classified below the reliability stack costs nothing here.
type liteFaultCaller struct{ next Caller }

// Call implements [Caller].
func (c *liteFaultCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	v, err := c.next.Call(ctx, method, args...)
	return v, occulited.ClassifyFault(err)
}

// CallAt implements [Caller].
func (c *liteFaultCaller) CallAt(
	ctx context.Context, priority hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	v, err := c.next.CallAt(ctx, priority, method, args...)
	return v, occulited.ClassifyFault(err)
}

// liteAbsent is the refusal every operation without a lite counterpart
// returns: the feature is not offered by the system, and the legacy
// sentinel stays matchable.
func liteAbsent(feature hmenum.Feature) error {
	return &hmerr.FeatureUnavailableError{
		Feature: feature,
		Reason:  hmenum.FeatureReasonNotSupported,
		Legacy:  ErrUnsupported,
	}
}

// Kind implements Operations.
func (b *LiteBackend) Kind() Kind { return KindOpenCCULite }

// Capabilities implements Operations. The profile is static.
func (b *LiteBackend) Capabilities() Capabilities { return CapabilityFor(KindOpenCCULite) }

// Init implements Operations by delegating to the announcer.
func (b *LiteBackend) Init(ctx context.Context, interfaceID, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Init(ctx, interfaceID, callbackURL)
}

// Deinit implements Operations by delegating to the announcer.
func (b *LiteBackend) Deinit(ctx context.Context, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Deinit(ctx, callbackURL)
}

// Ping implements Operations via XML-RPC `ping(interfaceID)`.
func (b *LiteBackend) Ping(ctx context.Context, interfaceID string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "ping", interfaceID)
	return err
}

// --- class a: pure XML-RPC, the CCU wire shapes ----------------------

// ListDevices implements Operations.
func (b *LiteBackend) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	return listDevicesViaCaller(ctx, b.xml, "lite")
}

// GetDeviceDescription implements Operations.
func (b *LiteBackend) GetDeviceDescription(ctx context.Context, address string) (map[string]any, error) {
	return getDeviceDescriptionViaCaller(ctx, b.xml, "lite", address)
}

// GetParamsetDescription implements Operations.
func (b *LiteBackend) GetParamsetDescription(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return getParamsetDescriptionViaCaller(ctx, b.xml, "lite", address, key)
}

// GetParamset implements Operations.
func (b *LiteBackend) GetParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	return getParamsetViaCaller(ctx, b.xml, "lite", address, key)
}

// PutParamset implements Operations. The interface processes behind the
// proxy are the CCU's, so rxMode travels as the 4th argument when set.
func (b *LiteBackend) PutParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any,
	priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return putParamsetViaCaller(ctx, b.xml, address, key, values, priority, rxMode, true)
}

// SetValue implements Operations, forwarding the command's priority.
func (b *LiteBackend) SetValue(
	ctx context.Context, address string, parameter hmenum.Parameter, value any,
	priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return setValueViaCaller(ctx, b.xml, address, parameter, value, priority, rxMode, true)
}

// GetValue implements Operations.
func (b *LiteBackend) GetValue(ctx context.Context, address string, parameter hmenum.Parameter) (any, error) {
	return getValueViaCaller(ctx, b.xml, address, parameter)
}

// ReportValueUsage implements Operations via XML-RPC
// `reportValueUsage(channel, valueID, refCounter)`.
func (b *LiteBackend) ReportValueUsage(ctx context.Context, channelAddress, valueID string, refCounter int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "reportValueUsage", channelAddress, valueID, refCounter)
	return err
}

// DetermineParameter implements Operations via XML-RPC
// `determineParameter(address, parameter)`.
func (b *LiteBackend) DetermineParameter(ctx context.Context, channelAddress, parameter string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "determineParameter", channelAddress, parameter)
}

// GetLinks implements Operations. The flag word is the CCU backend's;
// see [CcuBackend.GetLinks] for what it selects.
func (b *LiteBackend) GetLinks(ctx context.Context, channelAddress string) ([]hmproto.LinkDescription, error) {
	return getLinksViaCaller(ctx, b.xml, "lite", channelAddress)
}

// GetLinkPeers implements Operations.
func (b *LiteBackend) GetLinkPeers(ctx context.Context, channelAddress string) ([]string, error) {
	return getLinkPeersViaCaller(ctx, b.xml, "lite", channelAddress)
}

// AddLink implements Operations via XML-RPC
// `addLink(sender, receiver, name, description)`.
func (b *LiteBackend) AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "addLink", senderAddress, receiverAddress, name, description)
	return err
}

// RemoveLink implements Operations via XML-RPC `removeLink(sender, receiver)`.
func (b *LiteBackend) RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "removeLink", senderAddress, receiverAddress)
	return err
}

// GetLinkParamsetDescription implements Operations. The key is the
// literal LINK, as on the CCU.
func (b *LiteBackend) GetLinkParamsetDescription(ctx context.Context, channelAddress, _ string) (map[string]hmproto.ParameterData, error) {
	return getLinkParamsetDescriptionViaCaller(ctx, b.xml, "lite", channelAddress)
}

// GetLinkParamset implements Operations.
func (b *LiteBackend) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	return getLinkParamsetViaCaller(ctx, b.xml, "lite", channelAddress, peerAddress)
}

// PutLinkParamset implements Operations.
func (b *LiteBackend) PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error {
	return putLinkParamsetViaCaller(ctx, b.xml, channelAddress, peerAddress, values)
}

// ActivateLinkParamset implements Operations via XML-RPC
// `activateLinkParamset(receiver, sender, longPress)`. Physically
// actuates the receiver.
func (b *LiteBackend) ActivateLinkParamset(ctx context.Context, receiverAddress, senderAddress string, longPress bool) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "activateLinkParamset", receiverAddress, senderAddress, longPress)
	return err
}

// UpdateFirmware implements Operations: `installFirmware` first (HmIP),
// falling back to `updateFirmware` (BidCos) on a fault, as the CCU
// backend does. A tier refusal is not a fault any more once mapped, so
// it is returned instead of triggering the fallback.
func (b *LiteBackend) UpdateFirmware(ctx context.Context, address string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, callErr := b.xml.Call(ctx, "installFirmware", address)
	if callErr == nil {
		return nil
	}
	// A refusal of the box is final: the fallback would only be refused
	// the same way. The classified refusal keeps the fault in its chain,
	// so it is tested before the fault.
	if errors.Is(callErr, hmerr.ErrScopeMissing) || errors.Is(callErr, occulited.ErrInitRefused) {
		return callErr
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(callErr, &fault) {
		return callErr
	}
	_, err := b.xml.Call(ctx, "updateFirmware", address)
	return err
}

// RestoreConfigToDevice implements Operations via XML-RPC
// `restoreConfigToDevice(address)`.
func (b *LiteBackend) RestoreConfigToDevice(ctx context.Context, address string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "restoreConfigToDevice", address)
	return err
}

// ListReplaceableDevices implements Operations via XML-RPC
// `listReplaceableDevices(newAddress)`.
func (b *LiteBackend) ListReplaceableDevices(ctx context.Context, newDeviceAddress string) ([]hmproto.DeviceDescription, error) {
	return listReplaceableDevicesViaCaller(ctx, b.xml, "lite", newDeviceAddress)
}

// ReplaceDevice implements Operations via XML-RPC `replaceDevice(old, new)`.
func (b *LiteBackend) ReplaceDevice(ctx context.Context, oldDeviceAddress, newDeviceAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "replaceDevice", oldDeviceAddress, newDeviceAddress)
	return err
}

// SearchDevices implements Operations via XML-RPC `searchDevices()`. Only
// the wired bus scans; every other interface returns [ErrUnsupported]
// without a wire call, as on the CCU.
func (b *LiteBackend) SearchDevices(ctx context.Context) (int, error) {
	if !b.ifaceType.SupportsDeviceSearch() {
		return 0, ErrUnsupported
	}
	if b.xml == nil {
		return 0, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "searchDevices")
	if err != nil {
		return 0, err
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	}
	return 0, nil
}

// SetTeam implements Operations via XML-RPC `setTeam(address, team)`.
func (b *LiteBackend) SetTeam(ctx context.Context, channelAddress, teamChannelAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setTeam", channelAddress, teamChannelAddress)
	return err
}

// ListTeams implements Operations via XML-RPC `listTeams()`.
func (b *LiteBackend) ListTeams(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	return listStructArrayViaCaller(ctx, b.xml, "lite", "listTeams")
}

// DeleteDevice implements Operations via XML-RPC `deleteDevice(address, flags)`.
func (b *LiteBackend) DeleteDevice(ctx context.Context, address string, flags int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "deleteDevice", address, flags)
	return err
}

// GetMetadata implements Operations via XML-RPC `getMetadata(address, dataID)`.
func (b *LiteBackend) GetMetadata(ctx context.Context, address, dataID string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "getMetadata", address, dataID)
}

// SetMetadata implements Operations via XML-RPC
// `setMetadata(address, dataID, value)`.
func (b *LiteBackend) SetMetadata(ctx context.Context, address, dataID string, value any) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setMetadata", address, dataID, value)
	return err
}

// --- class b: the XML-RPC equivalent of a CCU JSON-RPC method ----------
//
// On a CCU these reach the interface process through a WebUI JSON-RPC
// wrapper (OpenCCU-Base www/api/methods/interface/*.tcl) that calls the
// XML-RPC method named below and reshapes the answer. The backend is
// bound to one interface's proxy URL, so the iface arguments the CCU
// wrapper needs are not used here. Where the wrapper reshapes the answer,
// the same reshaping happens here so callers see one result shape.

// GetLinkInfo implements Operations via XML-RPC
// `getLinkInfo(sender, receiver)`. The interface process answers
// {NAME, DESCRIPTION}; the CCU wrapper (getlinkinfo.tcl) hands callers
// {name, description}, so the answer is renamed to that spelling.
func (b *LiteBackend) GetLinkInfo(ctx context.Context, _, senderAddress, receiverAddress string) (map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getLinkInfo", senderAddress, receiverAddress)
	if err != nil {
		return nil, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("lite.GetLinkInfo: unexpected type %T", raw)
	}
	return map[string]any{
		"name":        asString(m["NAME"]),
		"description": asString(m["DESCRIPTION"]),
	}, nil
}

// SetLinkInfo implements Operations via XML-RPC
// `setLinkInfo(sender, receiver, name, description)` (setlinkinfo.tcl).
func (b *LiteBackend) SetLinkInfo(ctx context.Context, _, senderAddress, receiverAddress, name, description string) (bool, error) {
	if b.xml == nil {
		return false, ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setLinkInfo", senderAddress, receiverAddress, name, description)
	return err == nil, err
}

// GetInstallMode implements Operations via XML-RPC `getInstallMode()`,
// which answers the remaining seconds (getinstallmode.tcl).
func (b *LiteBackend) GetInstallMode(ctx context.Context) (int, error) {
	if b.xml == nil {
		return 0, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getInstallMode")
	if err != nil {
		return 0, err
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	}
	return 0, nil
}

// SetInstallMode implements Operations via XML-RPC `setInstallMode`.
//
// HmIP-RF gets `setInstallMode(on, time)`: that is the call the CCU's
// HmIP wrapper makes for the keyserver teach-in (setinstallmodehmip.tcl,
// installMode ALL), and it passes no device address either. Every other
// interface gets the CCU backend's positional form: (on, time, address)
// when a device address restricts pairing, (on, time, mode) otherwise.
func (b *LiteBackend) SetInstallMode(ctx context.Context, on bool, duration, mode int, deviceAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	if b.ifaceType == hmenum.InterfaceHmIPRF {
		_, err := b.xml.Call(ctx, "setInstallMode", on, duration)
		return err
	}
	if deviceAddress != "" {
		_, err := b.xml.Call(ctx, "setInstallMode", on, duration, deviceAddress)
		return err
	}
	_, err := b.xml.Call(ctx, "setInstallMode", on, duration, mode)
	return err
}

// SetInstallModeLocal implements Operations via XML-RPC
// `setInstallModeWithWhitelist(on, time, [{ADDRESS, KEY, KEY_MODE}])`,
// the call the CCU's HmIP wrapper makes for installMode LOCAL
// (setinstallmodehmip.tcl). HmIP-RF only; other interfaces return
// [ErrUnsupported] without a wire call.
func (b *LiteBackend) SetInstallModeLocal(ctx context.Context, duration int, sgtin, keyHex string) error {
	if b.ifaceType != hmenum.InterfaceHmIPRF {
		return ErrUnsupported
	}
	if b.xml == nil {
		return ErrNotWired
	}
	whitelist := []any{map[string]any{
		"ADDRESS":  sgtin,
		"KEY":      keyHex,
		"KEY_MODE": "LOCAL",
	}}
	_, err := b.xml.Call(ctx, "setInstallModeWithWhitelist", true, duration, whitelist)
	return err
}

// SuppressServiceMessage implements Operations via XML-RPC
// `suppressServiceMessages(channel, parameter, suppress)`
// (suppressservicemessages.tcl).
func (b *LiteBackend) SuppressServiceMessage(ctx context.Context, channelAddress, parameterID string, suppress bool) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "suppressServiceMessages", channelAddress, parameterID, suppress)
	return err
}

// GetSuppressedServiceMessages implements Operations via XML-RPC
// `getSuppressedServiceMessages(channel)`. The interface process answers
// a plain array of parameter names; the double JSON encoding a CCU
// answer carries is the WebUI wrapper's doing and does not occur here.
// A shape other than an array is an error, never an empty list: the
// suppression reconciler reads an empty list as "nothing suppressed".
func (b *LiteBackend) GetSuppressedServiceMessages(ctx context.Context, _, channelAddress string) ([]string, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getSuppressedServiceMessages", channelAddress)
	if err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("lite.GetSuppressedServiceMessages(%s): unexpected type %T", channelAddress, raw)
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if s, ok := entry.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// ListBidcosInterfaces returns the radio gateways of this interface via
// XML-RPC `listBidcosInterfaces()`, renamed to the keys the CCU wrapper
// (listbidcosinterfaces.tcl) hands callers: address, description,
// dutyCycle, isConnected, isDefault, fwVersion, type. Values keep their
// XML-RPC types.
func (b *LiteBackend) ListBidcosInterfaces(ctx context.Context, _ string) ([]map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "listBidcosInterfaces")
	if err != nil {
		return nil, err
	}
	gateways, err := toSliceOfMaps(raw, "ListBidcosInterfaces")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(gateways))
	for _, gw := range gateways {
		out = append(out, map[string]any{
			"address":     gw["ADDRESS"],
			"description": gw["DESCRIPTION"],
			"dutyCycle":   gw["DUTY_CYCLE"],
			"isConnected": gw["CONNECTED"],
			"isDefault":   gw["DEFAULT"],
			"fwVersion":   gw["FIRMWARE_VERSION"],
			"type":        gw["TYPE"],
		})
	}
	return out, nil
}

// --- class c: no counterpart on openccu-lite ---------------------------

// TestDevice implements Operations. The com-test is a ReGa function.
func (b *LiteBackend) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, liteAbsent(hmenum.FeatureDeviceCommunicationTest)
}

// GetAllDeviceData implements Operations. The bulk value read is a ReGa
// script; value seeding on a box reads the lite state endpoint instead.
// No feature key: an internal operation with no north-bound surface.
func (b *LiteBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, ErrUnsupported
}

// GetDeviceDetails implements Operations. Names come from the metadata
// API on a box, not from this backend.
func (b *LiteBackend) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureTaxonomyRead)
}

// RenameDevice implements Operations. Renames go through the metadata API.
func (b *LiteBackend) RenameDevice(context.Context, int, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureDeviceRename)
}

// RenameChannel implements Operations. Renames go through the metadata API.
func (b *LiteBackend) RenameChannel(context.Context, int, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureDeviceRename)
}

// AcceptDeviceInInbox implements Operations. A box has no CCU inbox.
func (b *LiteBackend) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureHubInbox)
}

// GetInboxDevices implements Operations. A box has no CCU inbox.
func (b *LiteBackend) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubInbox)
}

// GetIseIDByAddress implements Operations. A box has no ReGa ids.
// No feature key: an internal operation with no north-bound surface.
func (b *LiteBackend) GetIseIDByAddress(context.Context, string) (int, error) {
	return 0, ErrUnsupported
}

// TriggerFirmwareUpdate implements Operations. The system update runs
// through the box's system API.
func (b *LiteBackend) TriggerFirmwareUpdate(context.Context) (bool, error) {
	return false, liteAbsent(hmenum.FeatureHubSystemUpdateInstall)
}

// DownloadFirmware implements Operations. The system update runs
// through the box's system API.
func (b *LiteBackend) DownloadFirmware(context.Context) error {
	return liteAbsent(hmenum.FeatureHubSystemUpdateInstall)
}

// CreateBackupAndDownload implements Operations. Backups run through the
// box's system API.
func (b *LiteBackend) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, liteAbsent(hmenum.FeatureSystemBackupCreate)
}

// GetServiceMessages implements Operations. The list comes from the
// box's system API, not from this backend.
func (b *LiteBackend) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubServiceMessages)
}

// GetAlarmMessages implements Operations. Alarm messages are ReGa objects.
func (b *LiteBackend) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubAlarmMessages)
}

// GetAllRooms implements Operations. Rooms come from the metadata API.
func (b *LiteBackend) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, liteAbsent(hmenum.FeatureTaxonomyRead)
}

// GetAllFunctions implements Operations. Functions come from the
// metadata API.
func (b *LiteBackend) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, liteAbsent(hmenum.FeatureTaxonomyRead)
}

// GetAllPrograms implements Operations. Programs are ReGa objects.
func (b *LiteBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubPrograms)
}

// SetProgramState implements Operations. Programs are ReGa objects.
func (b *LiteBackend) SetProgramState(context.Context, string, bool) error {
	return liteAbsent(hmenum.FeatureHubPrograms)
}

// ExecuteProgram implements Operations. Programs are ReGa objects.
func (b *LiteBackend) ExecuteProgram(context.Context, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureHubPrograms)
}

// HasProgramIDs implements Operations. Programs are ReGa objects.
func (b *LiteBackend) HasProgramIDs(context.Context, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureHubPrograms)
}

// GetSystemVariable implements Operations. System variables are ReGa
// objects.
func (b *LiteBackend) GetSystemVariable(context.Context, string) (any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSysvars)
}

// GetAllSystemVariables implements Operations. System variables are
// ReGa objects.
func (b *LiteBackend) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSysvars)
}

// SetSystemVariable implements Operations. System variables are ReGa
// objects.
func (b *LiteBackend) SetSystemVariable(context.Context, string, any) error {
	return liteAbsent(hmenum.FeatureHubSysvars)
}

// CreateSystemVariableBool implements Operations. System variables are
// ReGa objects.
func (b *LiteBackend) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSysvars)
}

// CreateSystemVariableEnum implements Operations. System variables are
// ReGa objects.
func (b *LiteBackend) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSysvars)
}

// CreateSystemVariableFloat implements Operations. System variables are
// ReGa objects.
func (b *LiteBackend) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSysvars)
}

// DeleteSystemVariable implements Operations. System variables are ReGa
// objects.
func (b *LiteBackend) DeleteSystemVariable(context.Context, string) (bool, error) {
	return false, liteAbsent(hmenum.FeatureHubSysvars)
}

// GetSystemUpdateInfo implements Operations. The update state comes from
// the box's system API.
func (b *LiteBackend) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, liteAbsent(hmenum.FeatureHubSystemUpdate)
}
