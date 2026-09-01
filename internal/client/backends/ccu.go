// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// firmwareDownloadTimeout is the per-request timeout for firmware
// download operations, which may transfer large images over slow CCU
// links.
const firmwareDownloadTimeout = 10 * time.Minute

// CcuBackend talks to a classic CCU via XML-RPC + optional JSON-RPC
// (for program/sysvar enumeration and firmware updates). Adapters
// supply the [Caller] wrappers; this backend holds no transport
// state of its own.
type CcuBackend struct {
	xml  Caller
	json Caller // may be nil; disables JSON-RPC-only calls
	ann  Announcer
	// rega, when non-nil, routes operations through the CCU's ReGa
	// script engine rather than direct JSON-RPC. Set via
	// [CcuBackend.SetScriptRunner] after construction.
	rega ScriptRunner
	// baseURL is the CCU's HTTP root (e.g. "http://192.168.1.10"), used
	// for direct HTTP POST calls that bypass the JSON-RPC surface (e.g.
	// DownloadFirmware). Empty disables those calls.
	baseURL string
	// httpClient is shared for direct HTTP POSTs; if nil a default is used.
	httpClient *http.Client
	// sessionIDFn is a callback that returns the active JSON-RPC session ID.
	// Required for DownloadFirmware; nil disables that method.
	sessionIDFn func() string
	// sessionRenewFn forces a fresh JSON-RPC login and returns the new
	// session ID. The backup download uses it to guarantee a valid session
	// for the CCU's cp_security.cgi, which answers an unauthenticated GET
	// with a 200 + login page rather than a 401, so the download must
	// log in or renew first. Nil falls back to sessionIDFn (the current,
	// possibly-stale, session ID).
	sessionRenewFn func(ctx context.Context) (string, error)

	// probedCaps caches the capabilities discovered by [Initialize].
	// Stored atomically because Initialize is re-run on every reconnect
	// (InterfaceClient.ReinitProxy) on the connection-recovery goroutine,
	// while Capabilities() is read concurrently from the device-ingest
	// pipeline and from REST/WS/MQTT handler goroutines. An unordered
	// publish lets a reader see the pointer before the struct behind it
	// and reject a supported operation as unsupported.
	probedCaps atomic.Pointer[Capabilities]

	// ifaceType is the interface this backend serves. Required to pick the
	// correct install-mode wire call: HmIP-RF uses Interface.setInstallModeHMIP
	// while all other interfaces use Interface.setInstallMode.
	ifaceType hmenum.Interface
}

// NewCcuBackend constructs a backend. `ann` announces the callback
// URL; pass nil to skip Init/Deinit.
//
// Prefer [NewCcuBackendForInterface] for production use; this variant
// omits the interface-type parameter and cannot dispatch SetInstallMode
// correctly for HmIP-RF interfaces.
//
// loom:reachable:reason="test convenience constructor without interface-type dispatch; production wiring always uses NewCcuBackendForInterface"
func NewCcuBackend(xml, json Caller, ann Announcer) *CcuBackend {
	return &CcuBackend{xml: xml, json: json, ann: ann}
}

// NewCcuBackendForInterface constructs a backend that knows which CCU
// interface it serves. The interface type is needed by SetInstallMode to
// dispatch to the correct JSON-RPC method.
func NewCcuBackendForInterface(iface hmenum.Interface, xml, json Caller, ann Announcer) *CcuBackend {
	return &CcuBackend{xml: xml, json: json, ann: ann, ifaceType: iface}
}

// SetDownloadFirmwareTransport wires the CCU base URL, an optional HTTP
// client, and a session-ID provider into the backend so that
// [CcuBackend.DownloadFirmware] can reach the maintenance CGI. Call this once
// after construction; it is not required for any other backend operation.
func (b *CcuBackend) SetDownloadFirmwareTransport(baseURL string, hc *http.Client, sessionIDFn func() string) {
	b.baseURL = baseURL
	b.httpClient = hc
	b.sessionIDFn = sessionIDFn
}

// SetSessionRenewer wires a callback that forces a fresh JSON-RPC login and
// returns the new session ID. The backup download calls it so the
// cp_security.cgi request always carries a valid session. Optional; without
// it the download falls back to the current session ID from sessionIDFn.
func (b *CcuBackend) SetSessionRenewer(fn func(ctx context.Context) (string, error)) {
	b.sessionRenewFn = fn
}

// SetScriptRunner wires a [ScriptRunner] into the backend so that certain
// operations can be routed through the CCU's ReGa script engine.
// Call this once after construction; it is not required for operations
// that go directly through XML-RPC or JSON-RPC.
func (b *CcuBackend) SetScriptRunner(r ScriptRunner) {
	b.rega = r
}

// Kind implements Operations.
func (b *CcuBackend) Kind() Kind { return KindCCU }

// Capabilities implements Operations. Returns probed capabilities when
// [Initialize] has been called; falls back to the static KindCCU profile.
func (b *CcuBackend) Capabilities() Capabilities {
	caps := CapabilityFor(KindCCU)
	if probed := b.probedCaps.Load(); probed != nil {
		caps = *probed
	}
	// Backup and HasSystemUpdate both route through the ReGa script runner,
	// which production wires AFTER Initialize() runs (see ccu_wiring.go). If
	// these were frozen at probe time they would be stuck false, so derive
	// them from the current runner at call time instead.
	caps.Backup = b.rega != nil
	caps.HasSystemUpdate = b.rega != nil
	return caps
}

// Initialize implements [Initializer]. Backup and system-update capability
// are NOT set here: they depend on the ReGa script runner, which is wired
// after Initialize, so [CcuBackend.Capabilities] derives them live from the
// current runner instead.
func (b *CcuBackend) Initialize(_ context.Context) error {
	caps := CapabilityFor(KindCCU)
	b.probedCaps.Store(&caps)
	return nil
}

// Init implements Operations.
func (b *CcuBackend) Init(ctx context.Context, interfaceID, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Init(ctx, interfaceID, callbackURL)
}

// Deinit implements Operations.
func (b *CcuBackend) Deinit(ctx context.Context, callbackURL string) error {
	if b.ann == nil {
		return nil
	}
	return b.ann.Deinit(ctx, callbackURL)
}

// Ping implements Operations.
func (b *CcuBackend) Ping(ctx context.Context, interfaceID string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "ping", interfaceID)
	return err
}

// ListDevices implements Operations.
func (b *CcuBackend) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	return listDevicesViaCaller(ctx, b.xml, "ccu")
}

// GetParamsetDescription implements Operations.
func (b *CcuBackend) GetParamsetDescription(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return getParamsetDescriptionViaCaller(ctx, b.xml, "ccu", address, key)
}

// GetParamset implements Operations.
func (b *CcuBackend) GetParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	return getParamsetViaCaller(ctx, b.xml, "ccu", address, key)
}

// PutParamset implements Operations. When rxMode is non-empty it is
// appended as a 4th wire argument.
func (b *CcuBackend) PutParamset(
	ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any,
	priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return putParamsetViaCaller(ctx, b.xml, address, key, values, priority, rxMode, true)
}

// SetValue implements Operations. Priority is advisory and dropped
// here; the caller's command throttle is the effective scheduler.
// When rxMode is non-empty it is appended as a 4th wire argument.
func (b *CcuBackend) SetValue(
	ctx context.Context, address string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode,
) error {
	return setValueViaCaller(ctx, b.xml, address, parameter, value, priority, rxMode, true)
}

// GetValue implements Operations.
func (b *CcuBackend) GetValue(
	ctx context.Context, address string, parameter hmenum.Parameter,
) (any, error) {
	return getValueViaCaller(ctx, b.xml, address, parameter)
}

// UpdateFirmware implements Operations. Triggers a firmware update for
// the device at address via XML-RPC.
//
// HmIP and HmIP-Wired devices require installFirmware; BidCos devices
// use updateFirmware. The CCU returns a method-not-found fault when the
// first variant is unsupported, so we try installFirmware first and fall
// back to updateFirmware on any error.
func (b *CcuBackend) UpdateFirmware(ctx context.Context, address string) error {
	if b.xml == nil {
		return ErrUnsupported
	}
	_, callErr := b.xml.Call(ctx, "installFirmware", address)
	if callErr == nil {
		return nil
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(callErr, &fault) {
		return callErr
	}
	_, err := b.xml.Call(ctx, "updateFirmware", address)
	return err
}

// RestoreConfigToDevice re-transmits the stored configuration to the
// device via the XML-RPC `restoreConfigToDevice(address)` call. The
// method name is identical on rfd (BidCos-RF) and HMIPServer (HmIP-RF);
// the per-interface support gate lives in the adapter.
func (b *CcuBackend) RestoreConfigToDevice(ctx context.Context, address string) error {
	if b.xml == nil {
		return ErrUnsupported
	}
	_, err := b.xml.Call(ctx, "restoreConfigToDevice", address)
	return err
}

// ListReplaceableDevices returns the devices the new device may replace
// via the XML-RPC `listReplaceableDevices(newDeviceAddress)` call
// (rfd / hs485d). The per-interface support gate lives in the adapter.
func (b *CcuBackend) ListReplaceableDevices(ctx context.Context, newDeviceAddress string) ([]hmproto.DeviceDescription, error) {
	if b.xml == nil {
		return nil, ErrUnsupported
	}
	return listReplaceableDevicesViaCaller(ctx, b.xml, "ccu", newDeviceAddress)
}

// ReplaceDevice swaps oldDeviceAddress for newDeviceAddress via the
// XML-RPC `replaceDevice(old, new)` call (rfd / hs485d). The
// per-interface support gate lives in the adapter.
func (b *CcuBackend) ReplaceDevice(ctx context.Context, oldDeviceAddress, newDeviceAddress string) error {
	if b.xml == nil {
		return ErrUnsupported
	}
	_, err := b.xml.Call(ctx, "replaceDevice", oldDeviceAddress, newDeviceAddress)
	return err
}

// --- direct links --------------------------------------------------

// GetLinks implements Operations. CCU returns the link descriptors
// for the given channel regardless of direction (sender + receiver).
// Flags bit 0 toggles whether link metadata (names + descriptions)
// is included — we always request the full detail (flags = 0).
func (b *CcuBackend) GetLinks(ctx context.Context, channelAddress string) ([]hmproto.LinkDescription, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getLinks", channelAddress, 0)
	if err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetLinks: unexpected type %T", raw)
	}
	out := make([]hmproto.LinkDescription, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		ld := hmproto.LinkDescription{
			Sender:      asString(m["SENDER"]),
			Receiver:    asString(m["RECEIVER"]),
			Name:        asString(m["NAME"]),
			Description: asString(m["DESCRIPTION"]),
		}
		if f, ok := m["FLAGS"].(int); ok {
			ld.Flags = f
		}
		if ld.Sender == "" || ld.Receiver == "" {
			continue
		}
		out = append(out, ld)
	}
	return out, nil
}

// GetLinkPeers implements Operations. Returns the bare peer-address
// list; cheaper than GetLinks when only the peer enumeration is
// needed (e.g. to iterate LINK paramsets for a channel).
func (b *CcuBackend) GetLinkPeers(ctx context.Context, channelAddress string) ([]string, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getLinkPeers", channelAddress)
	if err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetLinkPeers: unexpected type %T", raw)
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if s, ok := entry.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// AddLink implements Operations.
func (b *CcuBackend) AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "addLink", senderAddress, receiverAddress, name, description)
	return err
}

// RemoveLink implements Operations.
func (b *CcuBackend) RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "removeLink", senderAddress, receiverAddress)
	return err
}

// GetLinkParamsetDescription implements Operations. The CCU uses the literal
// string "LINK" as the paramset key for descriptions — the schema is
// identical across peers. Only per-peer *values* (handled by GetLinkParamset
// / PutLinkParamset) key on the peer address.
func (b *CcuBackend) GetLinkParamsetDescription(ctx context.Context, channelAddress, _ string) (map[string]hmproto.ParameterData, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getParamsetDescription", channelAddress, "LINK")
	if err != nil {
		return nil, err
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetLinkParamsetDescription: unexpected type %T", raw)
	}
	out := make(map[string]hmproto.ParameterData, len(outer))
	for param, inner := range outer {
		m, ok := inner.(map[string]any)
		if !ok {
			continue
		}
		pd, err := toParameterData(m)
		if err != nil {
			return nil, fmt.Errorf("ccu.GetLinkParamsetDescription[%s]: %w", param, err)
		}
		out[param] = pd
	}
	return out, nil
}

// GetLinkParamset implements Operations.
func (b *CcuBackend) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	raw, err := b.xml.Call(ctx, "getParamset", channelAddress, peerAddress)
	if err != nil {
		return nil, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ccu.GetLinkParamset: unexpected type %T", raw)
	}
	return m, nil
}

// PutLinkParamset implements Operations.
func (b *CcuBackend) PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "putParamset", channelAddress, peerAddress, values)
	return err
}

// ActivateLinkParamset implements Operations. Maps to the CCU XML-RPC
// `activateLinkParamset(receiver, sender, longPress)` — it triggers the
// receiver as if the sender fired (the config dialog's "test link" /
// simulate-keypress probe). Physically actuates the receiver.
func (b *CcuBackend) ActivateLinkParamset(ctx context.Context, receiverAddress, senderAddress string, longPress bool) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "activateLinkParamset", receiverAddress, senderAddress, longPress)
	return err
}

// ReportValueUsage implements Operations. Maps to the CCU XML-RPC
// `reportValueUsage(channel_address, value_id, ref_counter)`.
func (b *CcuBackend) ReportValueUsage(ctx context.Context, channelAddress, valueID string, refCounter int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "reportValueUsage", channelAddress, valueID, refCounter)
	return err
}

// SetTeam implements Operations. Assigns a channel to a team via the
// XML-RPC `setTeam(address, team)` call; an empty team resets the
// channel to its own default team.
func (b *CcuBackend) SetTeam(ctx context.Context, channelAddress, teamChannelAddress string) error {
	if b.xml == nil {
		return ErrUnsupported
	}
	_, err := b.xml.Call(ctx, "setTeam", channelAddress, teamChannelAddress)
	return err
}

// ListTeams implements Operations. Returns the team-channel descriptions
// via the XML-RPC `listTeams()` call, decoded like the replaceable-device
// list (both are struct arrays of device descriptions).
func (b *CcuBackend) ListTeams(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	if b.xml == nil {
		return nil, ErrUnsupported
	}
	return listStructArrayViaCaller(ctx, b.xml, "ccu", "listTeams")
}

// SearchDevices implements Operations. Triggers the hs485d wired-bus
// scan via the XML-RPC `searchDevices()` call (no args) and returns the
// count of devices found. Only the BidCos-Wired interface exposes it.
func (b *CcuBackend) SearchDevices(ctx context.Context) (int, error) {
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

// DeleteDevice implements Operations. Maps to the CCU's XML-RPC
// `deleteDevice(address, flags)`. flags is the CCU delete bitmask
// ([DeleteFlagReset], [DeleteFlagForce]); 0 lets the CCU run the regular
// un-pair handshake.
func (b *CcuBackend) DeleteDevice(ctx context.Context, address string, flags int) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "deleteDevice", address, flags)
	return err
}

// --- JSON-RPC extended operations ---

// GetAllPrograms implements Operations via JSON-RPC.
// Returns ErrUnsupported when no JSON-RPC layer is wired.
func (b *CcuBackend) GetAllPrograms(ctx context.Context) ([]map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "Program.getAll")
	if err != nil {
		return nil, err
	}
	return toSliceOfMaps(raw, "GetAllPrograms")
}

// SetProgramState implements Operations via JSON-RPC.
func (b *CcuBackend) SetProgramState(ctx context.Context, iseID string, state bool) error {
	if b.json == nil {
		return ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Program.setActive", map[string]any{
		"id":     iseID,
		"active": state,
	})
	return err
}

// GetSystemUpdateInfo implements Operations via the ReGa script engine.
// Returns ErrUnsupported when no ScriptRunner has been wired in.
func (b *CcuBackend) GetSystemUpdateInfo(ctx context.Context) (map[string]any, error) {
	if b.rega == nil {
		return nil, ErrUnsupported
	}
	var result map[string]any
	if err := b.rega.RunJSON(ctx, hmenum.RegaScriptGetSystemUpdateInfo, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetInboxDevices implements Operations via the get_inbox_devices ReGa
// script. The pairing inbox is a central-wide ReGa query; the reference
// stack reads it the same way (there is no JSON-RPC inbox method on the
// CCU). When iface is non-empty the result is filtered to that interface.
// Without a ScriptRunner the inbox is unavailable.
func (b *CcuBackend) GetInboxDevices(ctx context.Context, iface string) ([]map[string]any, error) {
	if b.rega == nil {
		return nil, ErrUnsupported
	}
	type inboxDevice struct {
		DeviceID   string `json:"id"`
		Address    string `json:"address"`
		Name       string `json:"name"`
		DeviceType string `json:"type"`
		Interface  string `json:"interface"`
	}
	var devices []inboxDevice
	if err := b.rega.RunJSON(ctx, hmenum.RegaScriptGetInboxDevices, nil, &devices); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(devices))
	for i := range devices {
		d := &devices[i]
		if iface != "" && d.Interface != iface {
			continue
		}
		out = append(out, map[string]any{
			"id":        d.DeviceID,
			"address":   d.Address,
			"name":      d.Name,
			"type":      d.DeviceType,
			"interface": d.Interface,
		})
	}
	return out, nil
}

// SetSystemVariable implements Operations via JSON-RPC. Dispatches to
// the appropriate wire method based on value type; string sysvars
// require the ReGa layer and return ErrUnsupported.
func (b *CcuBackend) SetSystemVariable(ctx context.Context, name string, value any) error {
	if b.json == nil {
		return ErrUnsupported
	}
	switch v := value.(type) {
	case bool:
		iv := 0
		if v {
			iv = 1
		}
		_, err := b.json.Call(ctx, "SysVar.setBool", map[string]any{
			"name":  name,
			"value": iv,
		})
		return err
	case float64:
		_, err := b.json.Call(ctx, "SysVar.setFloat", map[string]any{
			"name":  name,
			"value": v,
		})
		return err
	case float32:
		_, err := b.json.Call(ctx, "SysVar.setFloat", map[string]any{
			"name":  name,
			"value": float64(v),
		})
		return err
	case int, int32, int64:
		var fv float64
		switch n := v.(type) {
		case int:
			fv = float64(n)
		case int32:
			fv = float64(n)
		case int64:
			fv = float64(n)
		}
		_, err := b.json.Call(ctx, "SysVar.setFloat", map[string]any{
			"name":  name,
			"value": fv,
		})
		return err
	default:
		return fmt.Errorf("ccu.SetSystemVariable: unsupported type %T for %q: %w", value, name, ErrUnsupported)
	}
}

// CreateSystemVariableBool implements Operations via JSON-RPC.
func (b *CcuBackend) CreateSystemVariableBool(ctx context.Context, name string, initVal bool) (map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	iv := 0
	if initVal {
		iv = 1
	}
	raw, err := b.json.Call(ctx, "SysVar.createBool", map[string]any{
		"name":     name,
		"init_val": iv,
		"internal": 0,
		"chnID":    -1,
	})
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	return m, nil
}

// CreateSystemVariableEnum implements Operations via JSON-RPC.
func (b *CcuBackend) CreateSystemVariableEnum(ctx context.Context, name string, valueList []string) (map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	// Join as semicolon-separated CCU wire format.
	var joined strings.Builder
	for i, v := range valueList {
		if i > 0 {
			joined.WriteString(";")
		}
		joined.WriteString(v)
	}
	raw, err := b.json.Call(ctx, "SysVar.createEnum", map[string]any{
		"name":     name,
		"valList":  joined.String(),
		"internal": 0,
		"chnID":    -1,
	})
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	return m, nil
}

// CreateSystemVariableFloat implements Operations via JSON-RPC.
func (b *CcuBackend) CreateSystemVariableFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error) {
	if b.json == nil {
		return nil, ErrUnsupported
	}
	raw, err := b.json.Call(ctx, "SysVar.createFloat", map[string]any{
		"name":     name,
		"minValue": minValue,
		"maxValue": maxValue,
		"internal": 0,
		"chnID":    -1,
	})
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	return m, nil
}

// DetermineParameter implements Operations. Calls the CCU's XML-RPC
// `determineParameter(address, parameter)` which returns the current value of
// parameter on channelAddress, auto-selecting the paramset (MASTER vs
// VALUES). Returns ErrNotWired when the XML caller is not configured.
func (b *CcuBackend) DetermineParameter(ctx context.Context, channelAddress, parameter string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "determineParameter", channelAddress, parameter)
}

// --- message acknowledgement (JSON-RPC fallback) ----------------------------

// AcknowledgeMessage acknowledges a CCU service or alarm message
// identified by messageID (the ReGa ISE-ID). Implements the optional
// [client.MessageAcknowledger] interface so [InterfaceClient] can fall
// back to the JSON-RPC layer when no ReGa runner is wired.
func (b *CcuBackend) AcknowledgeMessage(ctx context.Context, messageID string) (bool, error) {
	if b.json == nil {
		return false, ErrUnsupported
	}
	_, err := b.json.Call(ctx, "Message.acknowledge", map[string]any{
		"id": messageID,
	})
	return err == nil, err
}

// --- device metadata --------------------------------------------------------

// GetMetadata implements Operations via XML-RPC `getMetadata(address, dataID)`.
// The CCU exposes this call for device-level key-value storage; it is the same
// wire method Homegear uses.
func (b *CcuBackend) GetMetadata(ctx context.Context, address, dataID string) (any, error) {
	if b.xml == nil {
		return nil, ErrNotWired
	}
	return b.xml.Call(ctx, "getMetadata", address, dataID)
}

// SetMetadata implements Operations via XML-RPC `setMetadata(address, dataID, value)`.
func (b *CcuBackend) SetMetadata(ctx context.Context, address, dataID string, value any) error {
	if b.xml == nil {
		return ErrNotWired
	}
	_, err := b.xml.Call(ctx, "setMetadata", address, dataID, value)
	return err
}

// toSliceOfMaps narrows a raw any to []map[string]any. Used by the
// JSON-RPC methods above.
func toSliceOfMaps(raw any, method string) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("ccu.%s: unexpected type %T", method, raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		if m, ok := entry.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// asString is a defensive narrowing used by the link decoder. The
// CCU sometimes returns nil for optional string fields; we normalise
// to an empty string so downstream iteration stays linear.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
