// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/httpx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// backupDownloadTimeout is the per-request HTTP timeout for the CCU backup
// download. The CCU must create the archive before serving it, which can take
// up to five minutes on a loaded unit.
const backupDownloadTimeout = 5 * time.Minute

// The methods in this file are typed wrappers around [Client.Call] for the
// most frequently used CCU JSON-RPC operations. Wire-method names and
// Parameter keys mirror py (lines cited per method).
//
// Each wrapper:
// - Takes exactly the parameters the CCU expects on the wire.
// - Returns a typed result (or just an error for void operations).
// - Passes a nil out pointer when the CCU result is not needed.
//
// Adding a wrapper here is preferred over calling [Client.Call] directly from
// higher-level code because it keeps the wire coupling in one place.

// DeviceDetail holds a single entry returned by [Client.GetDeviceDetails].
// Wire shape from Device.listAllDetail: map of device address → detail map.
// We decode the outer result as a map[string]any for flexibility.
type DeviceDetail = map[string]any

// DeleteSystemVariable removes the system variable identified by name.
//
// Wire: SysVar.deleteSysVarByName, params: {name: name}.
func (c *Client) DeleteSystemVariable(ctx context.Context, name string) error {
	return c.Call(ctx, "SysVar.deleteSysVarByName", map[string]any{
		"name": name,
	}, nil)
}

// RenameChannel sets the display name of the channel identified by its
// ISE-ID.
//
// Wire: Channel.setName, params: {id: iseID, name: newName}.
func (c *Client) RenameChannel(ctx context.Context, iseID, newName string) error {
	return c.Call(ctx, "Channel.setName", map[string]any{
		"id":   iseID,
		"name": newName,
	}, nil)
}

// RenameDevice sets the display name of the device identified by its ISE-ID.
//
// Wire: Device.setName, params: {id: iseID, name: newName}.
func (c *Client) RenameDevice(ctx context.Context, iseID, newName string) error {
	return c.Call(ctx, "Device.setName", map[string]any{
		"id":   iseID,
		"name": newName,
	}, nil)
}

// SetLinkInfo updates the name and description of a direct-link between two
// channels on the given interface.
//
// Wire: Interface.setLinkInfo, params: {interface, sender, receiver, name,
// description}.
//
// The two address keys are the SHORT form, and deliberately not the ones
// [Client.GetLinkInfo] uses. The CCU's own method registry is asymmetric here
// (www/api/methods.conf):
//
//	Interface.setLinkInfo ARGUMENTS {_session_id_ interface sender receiver name description}
//	Interface.getLinkInfo ARGUMENTS {_session_id_ interface senderAddress receiverAddress}
//
// and interface/setlinkinfo.tcl reads $args(sender) / $args(receiver).
// Sending senderAddress/receiverAddress leaves a declared argument unset,
// checkArguments rejects the call, and a rename fails upstream — which it did.
//
// This is a deliberate divergence from the reference, which sends the long
// form for both (json_rpc.py `_JsonKey.SENDER_ADDRESS = "senderAddress"`).
// The firmware is the origin for a wire parameter name.
func (c *Client) SetLinkInfo(ctx context.Context, iface, sender, receiver, name, description string) error {
	return c.Call(ctx, "Interface.setLinkInfo", map[string]any{
		"interface":   iface,
		"sender":      sender,
		"receiver":    receiver,
		"name":        name,
		"description": description,
	}, nil)
}

// SetInstallModeHMIP enters or leaves HmIP pairing mode on the given
// interface. duration is the pairing window in seconds (0 to exit
// immediately). deviceAddress limits pairing to one SGTIN (pass "" for all
// devices). on must be the string "true" or "false" as required by the CCU
// wire protocol; installMode is the mode selector ("ALL" for all devices).
//
// Wire: Interface.setInstallModeHMIP,
//
// params: {interface, on, time, installMode, address, key, keymode}.
func (c *Client) SetInstallModeHMIP(ctx context.Context, iface string, on bool, duration int, deviceAddress string) error {
	onStr := "false"
	if on {
		onStr = "true"
	}
	return c.Call(ctx, "Interface.setInstallModeHMIP", map[string]any{
		"interface":   iface,
		"on":          onStr,
		"time":        duration,
		"installMode": "ALL",
		"address":     deviceAddress,
		"key":         "",
		"keymode":     "",
	}, nil)
}

// GetDeviceDetails returns the full detail map for all known devices. The
// returned slice contains one map per device; keys are CCU-defined (e.g.
// "ADDRESS", "TYPE", "FIRMWARE_VERSION").
//
// Wire: Device.listAllDetail (no params).
func (c *Client) GetDeviceDetails(ctx context.Context) ([]DeviceDetail, error) {
	var result []DeviceDetail
	if err := c.Call(ctx, "Device.listAllDetail", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SuppressServiceMessage sets or clears the suppression flag on a service
// message for the given channel parameter.
//
// Wire: Interface.suppressServiceMessages,
//
// params: {interface, channelAddress, parameterId, suppress}.
func (c *Client) SuppressServiceMessage(ctx context.Context, iface, channelAddress, parameterID string, suppress bool) error {
	return c.Call(ctx, "Interface.suppressServiceMessages", map[string]any{
		"interface":      iface,
		"channelAddress": channelAddress,
		"parameterId":    parameterID,
		"suppress":       suppress,
	}, nil)
}

// SetInstallModeBidCos enters or leaves BidCos pairing mode on the given
// interface. duration is the pairing window in seconds (0 to exit
// immediately). mode selects the learning mode (0 = normal, 1 = set, 2 =
// unset).
//
// Wire: Interface.setInstallMode,
//
// params: {interface, on, duration, mode}.
func (c *Client) SetInstallModeBidCos(ctx context.Context, iface string, on bool, duration, mode int) error {
	return c.Call(ctx, "Interface.setInstallMode", map[string]any{
		"interface": iface,
		"on":        on,
		"duration":  duration,
		"mode":      mode,
	}, nil)
}

// AssignProgramIDs assigns one or more program ISE-IDs to a channel.
//
// Wire: Program.assignProgramIDs, params: {id: iseID, channelId: channelID}.
func (c *Client) AssignProgramIDs(ctx context.Context, iseID, channelID string) error {
	return c.Call(ctx, "Program.assignProgramIDs", map[string]any{
		"id":        iseID,
		"channelId": channelID,
	}, nil)
}

// DeleteProgramID removes the CCU program identified by iseID.
//
// Wire: Program.deleteProgramID, params: {id: iseID}.
func (c *Client) DeleteProgramID(ctx context.Context, iseID string) error {
	return c.Call(ctx, "Program.deleteProgramID", map[string]any{
		"id": iseID,
	}, nil)
}

// ReadProgram reads the script/logic body of the CCU program identified by
// iseID. The raw JSON result is returned as-is for the caller to decode.
//
// Wire: Program.readProgram, params: {id: iseID}.
func (c *Client) ReadProgram(ctx context.Context, iseID string) (map[string]any, error) {
	var result map[string]any
	if err := c.Call(ctx, "Program.readProgram", map[string]any{
		"id": iseID,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateProgram updates the script/logic body of the CCU program identified
// by iseID. body contains the program fields to overwrite.
//
// Wire: Program.updateProgram, params: body ∪ {id: iseID}.
func (c *Client) UpdateProgram(ctx context.Context, iseID string, body map[string]any) error {
	params := make(map[string]any, len(body)+1)
	maps.Copy(params, body)
	params["id"] = iseID
	return c.Call(ctx, "Program.updateProgram", params, nil)
}

// SetMetadata stores an arbitrary metadata value for the object identified by
// objectID.
//
// Wire: Metadata.setMetadata, params: {objectId, dataId, value}.
func (c *Client) SetMetadata(ctx context.Context, objectID, dataID string, value any) error {
	return c.Call(ctx, "Metadata.setMetadata", map[string]any{
		"objectId": objectID,
		"dataId":   dataID,
		"value":    value,
	}, nil)
}

// GetMetadata retrieves the metadata value stored under dataID for objectID.
// The raw value is returned for the caller to type-assert.
//
// Wire: Metadata.getMetadata, params: {objectId, dataId}.
func (c *Client) GetMetadata(ctx context.Context, objectID, dataID string) (any, error) {
	var result any
	if err := c.Call(ctx, "Metadata.getMetadata", map[string]any{
		"objectId": objectID,
		"dataId":   dataID,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteMetadata removes the metadata entry stored under dataID for objectID.
//
// Wire: Metadata.deleteMetadata, params: {objectId, dataId}.
func (c *Client) DeleteMetadata(ctx context.Context, objectID, dataID string) error {
	return c.Call(ctx, "Metadata.deleteMetadata", map[string]any{
		"objectId": objectID,
		"dataId":   dataID,
	}, nil)
}

// InterfaceGetLinks returns the direct-link list for the channel identified
// by channelAddress on the given interface.
//
// Wire: Interface.getLinks,
//
// params: {interface, address, flags}.
func (c *Client) InterfaceGetLinks(ctx context.Context, iface, channelAddress string, flags int) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.Call(ctx, "Interface.getLinks", map[string]any{
		"interface": iface,
		"address":   channelAddress,
		"flags":     flags,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------

// ExecuteProgram triggers the CCU automation program identified by its
// ISE-ID. The CCU executes the program asynchronously.
//
// Wire: Program.execute, params: {id: iseID}.
func (c *Client) ExecuteProgram(ctx context.Context, iseID string) error {
	return c.Call(ctx, "Program.execute", map[string]any{
		"id": iseID,
	}, nil)
}

// GetAllChannelISEIDsRoom returns a map from room ISE-ID to the list of
// channel ISE-IDs assigned to that room.
//
// Wire: Room.getChannelIDs (no params).
func (c *Client) GetAllChannelISEIDsRoom(ctx context.Context) (map[string][]string, error) {
	var result map[string][]string
	if err := c.Call(ctx, "Room.getChannelIDs", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetAllChannelISEIDsFunction returns a map from function (trade-group)
// ISE-ID to the list of channel ISE-IDs assigned to that function.
//
// Wire: Function.getChannelIDs (no params).
func (c *Client) GetAllChannelISEIDsFunction(ctx context.Context) (map[string][]string, error) {
	var result map[string][]string
	if err := c.Call(ctx, "Function.getChannelIDs", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// RoomEntry is the typed shape returned by [Client.GetAllRoomsRaw].
// Mirrors the CCU's `Room.getAll` response: each entry carries the
// room's ISE-ID, the operator-assigned name, and the list of channel
// ISE-IDs assigned to it.
type RoomEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channelIds"`
}

// SubsectionEntry is the same envelope as [RoomEntry] but for
// "Gewerke" / functions. The CCU treats rooms and functions as
// parallel taxonomies.
type SubsectionEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channelIds"`
}

// GetAllRoomsRaw returns every room with its name and member channels.
// The DeviceDetailsCache loader uses this to populate per-channel
// room sets — `GetAllChannelISEIDsRoom` only returns ISE-ID maps and
// would need a second `Room.getName` round-trip per entry.
//
// Wire: Room.getAll (no params).
func (c *Client) GetAllRoomsRaw(ctx context.Context) ([]RoomEntry, error) {
	var result []RoomEntry
	if err := c.Call(ctx, "Room.getAll", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetAllFunctionsRaw returns every function (Subsection) with its name and
// member channels.
//
// Wire: Subsection.getAll (no params).
func (c *Client) GetAllFunctionsRaw(ctx context.Context) ([]SubsectionEntry, error) {
	var result []SubsectionEntry
	if err := c.Call(ctx, "Subsection.getAll", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetIseIDByAddress resolves a device or channel address to its internal CCU
// ISE-ID. Returns the ISE-ID as a string.
//
// Wire: Device.getIseIDByAddress, params: {address: address}.
func (c *Client) GetIseIDByAddress(ctx context.Context, address string) (string, error) {
	var result string
	if err := c.Call(ctx, "Device.getIseIDByAddress", map[string]any{
		"address": address,
	}, &result); err != nil {
		return "", err
	}
	return result, nil
}

// IsInterfacePresent reports whether the given interface is currently
// reachable / registered on the CCU.
//
// Wire: Interface.isPresent, params: {interface: iface}.
func (c *Client) IsInterfacePresent(ctx context.Context, iface string) (bool, error) {
	var result bool
	if err := c.Call(ctx, "Interface.isPresent", map[string]any{
		"interface": iface,
	}, &result); err != nil {
		return false, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Batch 2: JSON-RPC typed write-wrappers
// ---------------------------------------------------------------------------

// SetSystemVariableBool sets a boolean system variable on the CCU. The CCU
// requires the value as an integer (0/1), not a native bool.
//
// Wire: SysVar.setBool, params: {name, value}.
func (c *Client) SetSystemVariableBool(ctx context.Context, name string, value bool) error {
	intVal := 0
	if value {
		intVal = 1
	}
	return c.Call(ctx, "SysVar.setBool", map[string]any{
		"name":  name,
		"value": intVal,
	}, nil)
}

// SetSystemVariableFloat sets a numeric (float or enum-index) system
// variable. Both float and integer/enum sysvars use the same SysVar.setFloat
// wire call.
//
// Wire: SysVar.setFloat, params: {name, value}.
func (c *Client) SetSystemVariableFloat(ctx context.Context, name string, value float64) error {
	return c.Call(ctx, "SysVar.setFloat", map[string]any{
		"name":  name,
		"value": value,
	}, nil)
}

// CreateSystemVariableBool creates a new boolean system variable on the CCU.
// initVal is the initial state (false = 0, true = 1).
//
// Wire: SysVar.createBool, params: {name, init_val, internal, chnID}.
func (c *Client) CreateSystemVariableBool(ctx context.Context, name string, initVal bool) (map[string]any, error) {
	iv := 0
	if initVal {
		iv = 1
	}
	var result map[string]any
	if err := c.Call(ctx, "SysVar.createBool", map[string]any{
		"name":     name,
		"init_val": iv,
		"internal": 0,
		"chnID":    -1,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// CreateSystemVariableFloat creates a new float system variable on the CCU.
// minValue and maxValue define the allowed range (CCU defaults: 0–65000).
//
// Wire: SysVar.createFloat, params: {name, minValue, maxValue, internal,
// chnID}.
func (c *Client) CreateSystemVariableFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error) {
	var result map[string]any
	if err := c.Call(ctx, "SysVar.createFloat", map[string]any{
		"name":     name,
		"minValue": minValue,
		"maxValue": maxValue,
		"internal": 0,
		"chnID":    -1,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetAllSystemVariables fetches the raw list of all system variables from the
// CCU. Marker-based filtering is the caller's responsibility.
//
// Wire: SysVar.getAll (no params).
func (c *Client) GetAllSystemVariables(ctx context.Context) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.Call(ctx, "SysVar.getAll", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetAllPrograms fetches the raw list of all automation programs from the
// CCU. Marker-based filtering is the caller's responsibility.
//
// Wire: Program.getAll (no params).
func (c *Client) GetAllPrograms(ctx context.Context) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.Call(ctx, "Program.getAll", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// PutParamset writes a set of key/value pairs to a paramset on the CCU.
// values is the slice of parameter maps to apply (CCU "set" field).
//
// Wire: Interface.putParamset, params: {interface, address, paramsetKey,
// set}.
func (c *Client) PutParamset(ctx context.Context, iface, address, paramsetKey string, values []map[string]any) error {
	return c.Call(ctx, "Interface.putParamset", map[string]any{
		"interface":   iface,
		"address":     address,
		"paramsetKey": paramsetKey,
		"set":         values,
	}, nil)
}

// SetValue writes a single parameter to the CCU. valueType is the CCU type
// string (e.g. "FLOAT", "BOOL", "INTEGER"). This lets the CCU coerce the
// value correctly when the raw JSON type is ambiguous.
//
// Wire: Interface.setValue, params: {interface, address, valueKey, type,
// value}.
func (c *Client) SetValue(ctx context.Context, iface, address, parameter, valueType string, value any) error {
	return c.Call(ctx, "Interface.setValue", map[string]any{
		"interface": iface,
		"address":   address,
		"valueKey":  parameter,
		"type":      valueType,
		"value":     value,
	}, nil)
}

// SetSystemVariable sets a system variable on the CCU with automatic type
// dispatch. The CCU requires different wire methods for boolean, float/enum,
// and string types: - bool → [Client.SetSystemVariableBool] (SysVar.setBool)
// - float64 → [Client.SetSystemVariableFloat] (SysVar.setFloat) - string →
// ReGa script (not handled here; use the rega.Runner)
//
// Returns [hmerr.ErrUnsupported] for string values because string sysvars
// require the ReGa layer.
func (c *Client) SetSystemVariable(ctx context.Context, name string, value any) error {
	switch v := value.(type) {
	case bool:
		return c.SetSystemVariableBool(ctx, name, v)
	case float64:
		return c.SetSystemVariableFloat(ctx, name, v)
	case float32:
		return c.SetSystemVariableFloat(ctx, name, float64(v))
	case int:
		return c.SetSystemVariableFloat(ctx, name, float64(v))
	case int32:
		return c.SetSystemVariableFloat(ctx, name, float64(v))
	case int64:
		return c.SetSystemVariableFloat(ctx, name, float64(v))
	default:
		// String sysvars use the ReGa layer; callers that need them
		// should go through rega.Runner.SetSystemVariable instead.
		return fmt.Errorf("jsonrpc.SetSystemVariable: unsupported value type %T for sysvar %q: %w", value, name, hmerr.ErrUnsupported)
	}
}

// SetProgramState enables or disables the CCU automation program identified
// by iseID. state=true activates the program; state=false deactivates it.
//
// Wire: Program.setActive, params: {id: iseID, active: state}.
func (c *Client) SetProgramState(ctx context.Context, iseID string, state bool) error {
	return c.Call(ctx, "Program.setActive", map[string]any{
		"id":     iseID,
		"active": state,
	}, nil)
}

// AcceptDeviceInInbox accepts a pairing-inbox device into the CCU.
//
// Wire: Interface.acceptNewDevice, params: {interface, address}.
func (c *Client) AcceptDeviceInInbox(ctx context.Context, iface, address string) error {
	return c.Call(ctx, "Interface.acceptNewDevice", map[string]any{
		"interface": iface,
		"address":   address,
	}, nil)
}

// AcknowledgeMessage acknowledges an alarm or service message by its CCU
// message-ID. After acknowledgement the CCU marks the message as read and
// removes it from the active-alarms list.
//
// Wire: Alarm.acknowledge, params: {id: messageID}.
func (c *Client) AcknowledgeMessage(ctx context.Context, messageID string) error {
	return c.Call(ctx, "Alarm.acknowledge", map[string]any{
		"id": messageID,
	}, nil)
}

// TriggerFirmwareUpdate triggers a CCU-initiated firmware update for all
// devices that have a pending update.
//
// Wire: Interface.triggerFirmwareUpdate (no params).
func (c *Client) TriggerFirmwareUpdate(ctx context.Context) error {
	return c.Call(ctx, "Interface.triggerFirmwareUpdate", nil, nil)
}

// GetInstallMode returns the current pairing mode for the given interface (0
// = off, >0 = remaining seconds).
//
// Wire: Interface.getInstallMode, params: {interface: iface}.
func (c *Client) GetInstallMode(ctx context.Context, iface string) (int, error) {
	var result int
	if err := c.Call(ctx, "Interface.getInstallMode", map[string]any{
		"interface": iface,
	}, &result); err != nil {
		return 0, err
	}
	return result, nil
}

// BidcosInterface is one gateway entry returned by
// [Client.ListBidcosInterfaces]. It carries the per-gateway radio
// utilisation the CCU exposes for a BidCos interface (the CCU's own
// antenna plus any LAN gateways). DutyCycle and CarrierSense are
// percentages in the 0..100 range, or -1 when the CCU did not report
// the value.
type BidcosInterface struct {
	// Address is the gateway serial number (e.g. "OEQ1234567").
	Address string
	// Description is the human-readable gateway description.
	Description string
	// Type is the gateway type string (e.g. "CCU2", "HM-LGW").
	Type string
	// DutyCycle is the transmit duty cycle in percent (0..100), or -1
	// when unknown.
	DutyCycle int
	// CarrierSense is the receive carrier-sense load in percent (0..100),
	// or -1 when unknown. Most CCU firmwares do not report this over the
	// JSON-RPC surface, so it is commonly -1.
	CarrierSense int
	// Connected reports whether the gateway is currently reachable.
	Connected bool
	// Default reports whether this gateway is the interface's default
	// (primary) gateway.
	Default bool
}

// ListBidcosInterfaces returns the BidCos gateways attached to the named
// interface together with their radio utilisation. Reads-only: this is a
// pure JSON-RPC query that generates no radio traffic.
//
// Wire: Interface.listBidcosInterfaces, params: {interface: iface}. The
// CCU emits per gateway: address, description, dutyCycle, isConnected,
// isDefault, fwVersion, type (see the CCU WebUI
// listbidcosinterfaces.tcl handler). Numeric fields arrive as JSON
// strings, so decoding goes through a permissive map coercion.
func (c *Client) ListBidcosInterfaces(ctx context.Context, iface string) ([]BidcosInterface, error) {
	var raw []map[string]any
	if err := c.Call(ctx, "Interface.listBidcosInterfaces", map[string]any{
		"interface": iface,
	}, &raw); err != nil {
		return nil, err
	}
	out := make([]BidcosInterface, 0, len(raw))
	for _, m := range raw {
		out = append(out, BidcosInterface{
			Address:      bidcosString(m, "address"),
			Description:  bidcosString(m, "description"),
			Type:         bidcosString(m, "type"),
			DutyCycle:    bidcosPercent(m, "dutyCycle"),
			CarrierSense: bidcosPercent(m, "carrierSense"),
			Connected:    bidcosBool(m, "isConnected"),
			Default:      bidcosBool(m, "isDefault"),
		})
	}
	return out, nil
}

// bidcosString extracts a string value from a decoded JSON map, returning
// "" when the key is absent or not a string.
func bidcosString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// bidcosBool extracts a boolean value from a decoded JSON map. It accepts
// a native JSON bool as well as the string forms "true"/"1" the CCU may
// emit; anything else is false.
func bidcosBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	default:
		return false
	}
}

// bidcosPercent coerces a duty-cycle / carrier-sense field into a 0..100
// integer percentage. The CCU emits these as quoted JSON strings, so both
// the string and native-number shapes are handled. Returns -1 when the
// key is absent or cannot be parsed, which the north-bound DTO renders as
// "unknown".
func bidcosPercent(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if v == "" {
			return -1
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return -1
}

// GetSystemVariable reads the current value of the named CCU system variable.
// The return type is CCU-typed: bool, float64, or string depending on the
// sysvar type. Returns nil when the variable is not set or has no value.
//
// Wire: SysVar.getValueByName, params: {name: name}.
func (c *Client) GetSystemVariable(ctx context.Context, name string) (any, error) {
	var result any
	if err := c.Call(ctx, "SysVar.getValueByName", map[string]any{
		"name": name,
	}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// DownloadBackup downloads a fresh CCU config backup from the CCU's
// cp_security.cgi endpoint. The CCU creates the archive on demand and streams
// it back. Returns the raw archive bytes (typically a few MB).
//
// The call requires an active JSON-RPC session because the CCU's CGI uses the
// session ID for authentication, and the @…@ delimiters around it are not a
// convention: the CGI's own extractor captures them inclusively
// (`regexp "$sidname=(@[A-Za-z0-9]*@)"` in OpenCCU-Base
// www/tcl/eq3_old/session.tcl, session_urlsid) and a companion proc strips
// them again for the ReGa lookup. The WebUI builds byte-for-byte this URL
// (www/config/cp_security.cgi), and `action=create_backup` is a real proc
// there that streams the .sbk archive (www/config/backup.tcl). The JSON-RPC
// login hands back the INNER value, so re-wrapping it here is right; the
// url.QueryEscape is a provable no-op over the [A-Za-z0-9] session alphabet
// and is kept only as a defensive measure.
//
// Wire: GET
// {baseURL}/config/cp_security.cgi?sid=@{session_id}@&action=create_backup
// (5-minute timeout).
func (c *Client) DownloadBackup(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid == "" {
		return nil, c.wrap("download_backup", fmt.Errorf("no active JSON-RPC session: %w", hmerr.ErrAuthFailure))
	}

	base := c.backupBaseURL()
	if base == "" {
		return nil, c.wrap("download_backup", fmt.Errorf("cannot derive base URL from endpoint %q: %w", c.cfg.Endpoint, hmerr.ErrUnsupported))
	}

	downloadURL := base + "/config/cp_security.cgi?sid=@" + url.QueryEscape(sid) + "@&action=create_backup"

	hc := c.httpClient
	if hc == nil {
		hc = httpx.NewClient(backupDownloadTimeout)
	} else {
		// Override timeout for this large download regardless of the shared client.
		hc = &http.Client{
			Transport: hc.Transport,
			Timeout:   backupDownloadTimeout,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, http.NoBody)
	if err != nil {
		return nil, c.wrap("download_backup", fmt.Errorf("build request: %w", err))
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, c.wrap("download_backup", fmt.Errorf("%w: %w", hmerr.ErrNoConnection, err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, c.wrap("download_backup", fmt.Errorf("CCU returned HTTP %d: %w", resp.StatusCode, hmerr.ErrInternalBackendException))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, c.wrap("download_backup", fmt.Errorf("read response body: %w", err))
	}
	return data, nil
}

// GetHTTPSRedirectEnabled queries the CCU for its current HTTPS-redirect flag.
// Returns (false, nil) when the CCU responds with a nil result (feature not
// configured).
//
// Wire: CCU.getHttpsRedirectEnabled (no params).
func (c *Client) GetHTTPSRedirectEnabled(ctx context.Context) (bool, error) {
	var result any
	if err := c.Call(ctx, "CCU.getHttpsRedirectEnabled", nil, &result); err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}
	if b, ok := result.(bool); ok {
		return b, nil
	}
	return false, nil
}

// GetAuthEnabled queries the CCU for whether authentication is required.
// Returns (false, nil) when the CCU responds with a nil result (auth not
// configured or the method is unavailable on this firmware).
//
// Wire: CCU.getAuthEnabled (no params).
func (c *Client) GetAuthEnabled(ctx context.Context) (bool, error) {
	var result any
	if err := c.Call(ctx, "CCU.getAuthEnabled", nil, &result); err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}
	if b, ok := result.(bool); ok {
		return b, nil
	}
	return false, nil
}

// InterfaceEntry is one entry returned by [Client.ListInterfaces].
type InterfaceEntry struct {
	// Type is the CCU interface type string (e.g. "HmIP-RF", "BidCos-RF").
	Type string `json:"type"`
	// Address is the CCU interface identifier (same as the interface ID
	// the CCU uses in XML-RPC callbacks, e.g. "HmIP-RF").
	Address string `json:"address"`
	// Port is the XML-RPC port on which the interface listens.
	Port int `json:"port"`
	// URL is the full XML-RPC endpoint URL reported by the CCU.
	URL string `json:"url"`
}

// ListInterfaces enumerates all CCU interfaces currently registered on
// the CCU. The return value includes both RF and wired interface adapters.
//
// Wire: Interface.listInterfaces (no params).
func (c *Client) ListInterfaces(ctx context.Context) ([]InterfaceEntry, error) {
	var raw []map[string]any
	if err := c.Call(ctx, "Interface.listInterfaces", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]InterfaceEntry, 0, len(raw))
	for _, m := range raw {
		entry := InterfaceEntry{}
		// Real firmware answers with name/port/info and reports neither
		// `type` nor `address` nor `url` (verified against OpenCCU
		// 3.89.8, which returns e.g. {"name":"HmIP-RF","port":32010,
		// "info":"HmIP-RF"}). `name` is the interface identifier - the
		// same token configured_interfaces is keyed on - so it seeds both
		// fields, and an explicit type/address still wins where a
		// firmware does supply them.
		if v, ok := m["name"].(string); ok {
			entry.Type = v
			entry.Address = v
		}
		if v, ok := m["type"].(string); ok && v != "" {
			entry.Type = v
		}
		if v, ok := m["address"].(string); ok && v != "" {
			entry.Address = v
		}
		if v, ok := m["port"].(float64); ok {
			entry.Port = int(v)
		}
		if v, ok := m["url"].(string); ok {
			entry.URL = v
		}
		out = append(out, entry)
	}
	return out, nil
}

// backupBaseURL derives the CCU base URL from the configured JSON-RPC endpoint
// by stripping the "/api/homematic.cgi" suffix. The suffix is not a borrowed
// constant: the firmware's document root makes the JSON-RPC entry point and
// the config CGIs siblings under /www/ (OpenCCU-Base www/api/homematic.cgi
// sets DOCUMENT_ROOT to /www/, with www/config/cp_*.cgi alongside it), so the
// trimmed prefix plus "/config/…" addresses the same server. Returns an empty
// string when the endpoint does not contain the expected suffix and cannot be
// safely trimmed.
func (c *Client) backupBaseURL() string {
	const jsonRPCPath = "/api/homematic.cgi"
	ep := strings.TrimRight(c.cfg.Endpoint, "/")
	if before, ok := strings.CutSuffix(ep, jsonRPCPath); ok {
		return before
	}
	// Fallback: try to use the URL up to the path root.
	u, err := url.Parse(ep)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
