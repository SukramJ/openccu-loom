// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// Initializer is an optional extension to [Operations] that backends may
// implement to perform a one-shot capability probe at start-up. Callers that
// hold an [Operations] reference can type-assert to [Initializer] and call
// Initialize before the first normal operation.
//
// Implementations should cache the probed result and return it via
// [Operations.Capabilities] after the call.
type Initializer interface {
	// Initialize probes the backend's runtime capabilities and caches the
	// result. Returns nil when the probe succeeded or when the backend
	// does not support the probed features. Errors are soft: a failure
	// means the cached capabilities stay at their conservative defaults.
	Initialize(ctx context.Context) error
}

// LifecycleOps covers backend identity, capability reporting, and the
// connection lifecycle: bind, unbind, and keepalive.
type LifecycleOps interface {
	// Kind identifies the backend flavour.
	Kind() Kind

	// Capabilities returns the profile for this backend. The profile
	// is static per Kind but re-exposed so consumers don't need a
	// separate lookup.
	Capabilities() Capabilities

	// Init binds the daemon callback URL to this backend. interfaceID
	// is the central-scoped identifier the CCU echoes back on every
	// event push.
	Init(ctx context.Context, interfaceID, callbackURL string) error

	// Deinit severs the callback channel.
	Deinit(ctx context.Context, interfaceID string) error

	// Ping is the keepalive. Returns nil when the CCU responds with
	// the matching PONG payload.
	Ping(ctx context.Context, interfaceID string) error
}

// Device-delete flags form the bitmask passed to [DeviceOps.DeleteDevice].
// They mirror the CCU's XML-RPC `deleteDevice` flag constants and may be
// OR-combined.
const (
	// DeleteFlagReset resets the device to factory defaults as part of the
	// unpair (CCU DELETE_FLAG_RESET = 1).
	DeleteFlagReset = 1
	// DeleteFlagForce forces the delete even when the device is unreachable,
	// so the CCU drops it without the bidirectional handshake
	// (CCU DELETE_FLAG_FORCE = 2).
	DeleteFlagForce = 2
)

// DeviceOps covers device enumeration, discovery helpers, firmware
// management, pairing lifecycle, and bulk device-data retrieval.
type DeviceOps interface {
	// ListDevices enumerates device descriptions. Backends without
	// [Capabilities.ListDevices] return [ErrUnsupported].
	ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error)

	// UpdateFirmware requests a firmware update. Returns
	// [ErrUnsupported] on backends without [Capabilities.FirmwareUpdate].
	UpdateFirmware(ctx context.Context, address string) error

	// RestoreConfigToDevice re-transmits the centrally stored
	// configuration (MASTER paramsets of every channel plus link
	// peerings) to the device — the recovery path after a device
	// factory reset. Maps to the CCU `restoreConfigToDevice(address)`
	// XML-RPC call. The call returns promptly; the radio transfer
	// continues asynchronously (CONFIG_PENDING). Returns
	// [ErrUnsupported] on backends without [Capabilities.ConfigRestore]
	// (CUxD, Homegear); the caller additionally gates on the interface
	// (only rfd / HMIPServer expose the method).
	RestoreConfigToDevice(ctx context.Context, address string) error

	// ListReplaceableDevices returns the already-paired devices the new
	// device (newDeviceAddress) may replace — the interface daemon
	// computes type / channel compatibility. Maps to the XML-RPC
	// `listReplaceableDevices(newDeviceAddress)` call. Returns
	// [ErrUnsupported] on backends without [Capabilities.ReplaceDevice];
	// the caller additionally gates on the interface (rfd / hs485d
	// only). A wrong-interface serial produces an upstream fault the
	// caller tolerates.
	ListReplaceableDevices(ctx context.Context, newDeviceAddress string) ([]hmproto.DeviceDescription, error)

	// ReplaceDevice swaps oldDeviceAddress for newDeviceAddress on the
	// interface daemon: the daemon migrates the direct links, teams and
	// link paramsets, and ReGa re-binds the existing object in place
	// (same ise-ID → programs, names, rooms survive). Maps to the
	// XML-RPC `replaceDevice(oldDeviceAddress, newDeviceAddress)` call.
	// Returns [ErrUnsupported] on backends without
	// [Capabilities.ReplaceDevice]; an incompatible pair surfaces as an
	// upstream fault.
	ReplaceDevice(ctx context.Context, oldDeviceAddress, newDeviceAddress string) error

	// SearchDevices triggers a wired-bus scan for new devices via the
	// XML-RPC `searchDevices()` call (no arguments) and returns the count
	// of devices found. The daemon additionally pushes newDevices
	// callbacks; the found devices surface in the inbox (ReadyConfig
	// false) for the operator to accept. Only hs485d (BidCos-Wired)
	// implements it — every other backend/interface returns
	// [ErrUnsupported]; the caller additionally gates on
	// [hmenum.Interface.SupportsDeviceSearch].
	SearchDevices(ctx context.Context) (int, error)

	// SetTeam assigns channelAddress to teamChannelAddress via the
	// XML-RPC `setTeam(address, team)` call (e.g. smoke-detector teams).
	// An empty teamChannelAddress resets the channel to its own default
	// team. Returns [ErrUnsupported] on backends without
	// [Capabilities.TeamAssignment]; the caller additionally gates on the
	// interface (BidCos-RF / HmIP-RF).
	SetTeam(ctx context.Context, channelAddress, teamChannelAddress string) error

	// ListTeams returns the team-channel descriptions used to build the
	// assignment candidate list via the XML-RPC `listTeams()` call. Each
	// entry carries ADDRESS / PARENT / TEAM_TAG / PARENT_TYPE. Returns
	// [ErrUnsupported] on backends without [Capabilities.TeamAssignment].
	ListTeams(ctx context.Context) ([]hmproto.DeviceDescription, error)

	// TestDevice runs the CCU's per-device communication/function test
	// (ReGa DevStartComTest) and polls for completion up to maxWaitSecs
	// (poll every pollIntervalSecs). The CCU sends a radio test frame and
	// waits for the device's ACK; a pass means the device answered.
	// Requires the ReGa runner — returns [ErrUnsupported] on backends
	// without [Capabilities.CommunicationTest]; the caller additionally
	// gates on the interface.
	TestDevice(ctx context.Context, address string, maxWaitSecs, pollIntervalSecs float64) (hmapi.CommunicationTestResult, error)

	// DeleteDevice unpairs the device from the CCU. Maps to the CCU's
	// `deleteDevice(address, flags)` XML-RPC call. flags is the CCU delete
	// bitmask ([DeleteFlagReset] resets the device to factory defaults,
	// [DeleteFlagForce] forces removal of an unreachable device); pass 0 for a
	// plain unpair that keeps the bidirectional handshake clean. Backends
	// without a pairing concept (CUxD virtual devices) return [ErrUnsupported].
	DeleteDevice(ctx context.Context, address string, flags int) error

	// GetAllDeviceData returns all current parameter values for all devices on
	// the interface in one call (where supported). Used during discovery to
	// pre-populate the value cache. Returns [ErrUnsupported] for backends
	// without this capability.
	GetAllDeviceData(ctx context.Context) (map[string]map[string]any, error)

	// GetDeviceDetails returns name / ISE-ID / interface details for all known
	// device addresses. Used during discovery. Returns [ErrUnsupported] for
	// backends without this capability.
	GetDeviceDetails(ctx context.Context, addresses []string) ([]map[string]any, error)

	// GetDeviceDescription returns the raw device description for a single
	// address. Returns [ErrUnsupported] for backends without this capability.
	GetDeviceDescription(ctx context.Context, address string) (map[string]any, error)

	// RenameDevice renames the CCU device identified by its ISE-ID. Returns true
	// on success. Returns [ErrUnsupported] when [Capabilities.Rename] is false.
	RenameDevice(ctx context.Context, iseID int, newName string) (bool, error)

	// RenameChannel renames the CCU channel identified by its ISE-ID. Returns
	// true on success. Returns [ErrUnsupported] when [Capabilities.Rename] is
	// false.
	RenameChannel(ctx context.Context, iseID int, newName string) (bool, error)

	// AcceptDeviceInInbox accepts a device from the CCU pairing inbox. Returns
	// true when the CCU confirmed acceptance. Returns [ErrUnsupported] when
	// [Capabilities.InboxDevices] is false.
	AcceptDeviceInInbox(ctx context.Context, deviceAddress string) (bool, error)

	// GetInboxDevices returns devices in the CCU pairing inbox (devices that
	// have announced but not yet been accepted). iface is the interface to
	// query.
	GetInboxDevices(ctx context.Context, iface string) ([]map[string]any, error)

	// GetIseIDByAddress resolves a device or channel address to its ReGa ISE-ID.
	// Returns 0 when the address is not found. Returns [ErrUnsupported] when
	// [Capabilities.IseIDLookup] is false.
	GetIseIDByAddress(ctx context.Context, address string) (int, error)

	// GetMetadata reads a metadata blob attached to a device. On Homegear this
	// maps to the XML-RPC `getMetadata(address, dataID)` call; device names are
	// stored under dataID "NAME". Other backends return [ErrUnsupported].
	GetMetadata(ctx context.Context, address, dataID string) (any, error)

	// SetMetadata writes a metadata blob for a device. Only supported on
	// Homegear; other backends return [ErrUnsupported].
	SetMetadata(ctx context.Context, address, dataID string, value any) error

	// TriggerFirmwareUpdate triggers a CCU firmware update. Returns
	// [ErrUnsupported] when [Capabilities.FirmwareUpdate] is false.
	TriggerFirmwareUpdate(ctx context.Context) (bool, error)

	// DownloadFirmware instructs the CCU to fetch firmware from the given URL
	// via an HTTP POST to the CCU's maintenance CGI. Only "http://" and
	// "https://" scheme URLs are accepted; others return [ErrUnsupported].
	// Backends without a JSON-RPC session layer (CUxD, Homegear, CCU-Jack)
	// return [ErrUnsupported].
	DownloadFirmware(ctx context.Context, firmwareURL string) error

	// CreateBackupAndDownload triggers a CCU config backup and
	// downloads the resulting archive. maxWaitTime and pollInterval
	// control how long to wait for the archive to become ready.
	// Returns [ErrUnsupported] when [Capabilities.Backup] is false.
	CreateBackupAndDownload(ctx context.Context, maxWaitTime, pollInterval float64) ([]byte, error)
}

// ParamsetOps covers reading and writing paramset descriptors and values
// (MASTER, VALUES, and LINK paramsets by standard key).
type ParamsetOps interface {
	// GetParamsetDescription reads the descriptor for one paramset.
	GetParamsetDescription(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error)

	// GetParamset reads the current values of one paramset.
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)

	// PutParamset writes values to one paramset atomically. rxMode is
	// appended to the wire call when non-empty ([hmenum.CommandRxModeUnset]
	// omits the argument). Backends that do not support rx_mode on the wire
	// (e.g. BIN-RPC) silently ignore the parameter.
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any, rxMode hmenum.CommandRxMode) error

	// DetermineParameter reads the current value of a named parameter from the
	// CCU for the given channel address. The CCU auto-selects the most
	// appropriate paramset (MASTER or VALUES) for the parameter. Returns nil
	// when the backend does not support this operation (e.g. CUxD, which has no
	// determineParameter XML-RPC method).
	DetermineParameter(ctx context.Context, channelAddress, parameter string) (any, error)
}

// ValueOps covers single-parameter read and write operations on the VALUES
// paramset, plus the click-event usage counter that routes press events
// without requiring a real direct link.
type ValueOps interface {
	// SetValue sets one parameter's value. The priority is advisory
	// backends that don't honor priority ignore the hint. rxMode is appended
	// to the wire call when non-empty; use [hmenum.CommandRxModeUnset] for
	// the default 3-argument form.
	SetValue(ctx context.Context, address string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority, rxMode hmenum.CommandRxMode) error

	// GetValue reads one parameter directly from the CCU (bypassing
	// the event-coordinator cache). Used for refresh + initial load.
	GetValue(ctx context.Context, address string, parameter hmenum.Parameter) (any, error)

	// ReportValueUsage tells the CCU that an event-parameter on the
	// given channel is consumed by a logic peer. The CCU uses this
	// counter to decide whether to deliver press events to the
	// central; a non-zero refCounter means "deliver", zero means
	// "stop". This is the wire-level primitive
	// implement central links — the daemon need not register a
	// real direct link, the metadata flag is sufficient. Maps to the
	// CCU's `reportValueUsage(channel, valueID, refCounter)` XML-RPC
	// call. Backends without click-event routing return ErrUnsupported.
	ReportValueUsage(ctx context.Context, channelAddress, valueID string, refCounter int) error
}

// LinkOps covers direct-link ("Direktverknüpfungen") management: listing,
// creating, removing, and reading or writing the per-link LINK paramset.
// Direct links couple a sender channel to a receiver channel so button
// presses, motion events, etc. propagate without involving the CCU logic
// layer. Backends without link support return [ErrUnsupported].
type LinkOps interface {
	// GetLinks returns every link connected to channelAddress — both
	// outgoing (this channel as sender) and incoming (as receiver).
	GetLinks(ctx context.Context, channelAddress string) ([]hmproto.LinkDescription, error)

	// GetLinkPeers returns the peer channel addresses linked to
	// channelAddress without the full link metadata — a cheaper
	// lookup when only the peer list is needed.
	GetLinkPeers(ctx context.Context, channelAddress string) ([]string, error)

	// AddLink creates a new direct link. name and description are
	// human-readable and may be empty.
	AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error

	// RemoveLink deletes the direct link between the two channels.
	RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error

	// GetLinkParamsetDescription reads the LINK paramset descriptor
	// for the (channel, peer) pair. Unlike MASTER/VALUES the key is
	// the peer channel address, so the method takes it explicitly.
	GetLinkParamsetDescription(ctx context.Context, channelAddress, peerAddress string) (map[string]hmproto.ParameterData, error)

	// GetLinkParamset reads the current LINK paramset values for
	// (channel, peer).
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)

	// PutLinkParamset writes LINK paramset values atomically.
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error

	// GetLinkInfo returns the name and description of the direct link between
	// senderAddress and receiverAddress on iface. Returns [ErrUnsupported] when
	// [Capabilities.LinkOperations] is false.
	GetLinkInfo(ctx context.Context, iface, senderAddress, receiverAddress string) (map[string]any, error)

	// SetLinkInfo sets the name and description of the direct link between
	// senderAddress and receiverAddress on iface. Returns [ErrUnsupported] when
	// [Capabilities.LinkOperations] is false.
	SetLinkInfo(ctx context.Context, iface, senderAddress, receiverAddress, name, description string) (bool, error)
}

// SystemOps covers CCU-level system state: install (pairing) mode, service
// and alarm messages, rooms, functions, system variables, programs, and the
// system update check. These are mostly JSON-RPC or ReGa operations; CUxD
// and Homegear return [ErrUnsupported] for the majority of this group.
type SystemOps interface {
	// --- install mode (pairing) -------------------------------------------

	// GetInstallMode returns the remaining seconds the CCU stays in install
	// (pairing) mode, or 0 when pairing is inactive. Returns [ErrUnsupported]
	// when [Capabilities.InstallMode] is false.
	GetInstallMode(ctx context.Context) (int, error)

	// SetInstallMode enables or disables CCU install (pairing) mode. on=true
	// enables pairing for the given duration (seconds). mode selects the pairing
	// protocol (1 = default). deviceAddress, when non-empty, restricts pairing
	// to that address. Returns [ErrUnsupported] when [Capabilities.InstallMode]
	// is false.
	SetInstallMode(ctx context.Context, on bool, duration, mode int, deviceAddress string) error

	// SetInstallModeLocal opens an HmIP pairing window restricted to a
	// single device identified by SGTIN + device key — the keyserver-less
	// LOCAL teach-in. sgtin and keyHex must be pre-normalised (24 / 32
	// uppercase hex characters, see pkg/hmproto). Returns
	// [ErrUnsupported] on backends or interfaces without the HmIP
	// JSON-RPC surface ([Capabilities.InstallModeLocal]).
	SetInstallModeLocal(ctx context.Context, duration int, sgtin, keyHex string) error

	// --- service / alarm messages -----------------------------------------

	// GetServiceMessages returns all active service messages. An optional
	// message-type filter string may be passed (empty = all types). Returns
	// [ErrUnsupported] when [Capabilities.ServiceMessages] is false.
	GetServiceMessages(ctx context.Context, messageType string) ([]map[string]any, error)

	// SuppressServiceMessage suppresses or unsuppresses the service message
	// identified by (channelAddress, parameterID). Returns [ErrUnsupported] when
	// [Capabilities.SuppressServiceMessage] is false.
	SuppressServiceMessage(ctx context.Context, channelAddress, parameterID string, suppress bool) error

	// GetAlarmMessages returns all active alarm messages. Returns
	// [ErrUnsupported] when [Capabilities.AlarmMessages] is false.
	GetAlarmMessages(ctx context.Context) ([]map[string]any, error)

	// GetSuppressedServiceMessages returns the list of currently suppressed
	// service message parameter IDs for channelAddress on iface. Returns
	// [ErrUnsupported] when [Capabilities.SuppressServiceMessage] is false.
	GetSuppressedServiceMessages(ctx context.Context, iface, channelAddress string) ([]string, error)

	// --- rooms / functions -------------------------------------------------

	// GetAllRooms returns all CCU rooms as a map of roomName →
	// set{channelAddress, …}. Returns [ErrUnsupported] when [Capabilities.Rooms]
	// is false.
	GetAllRooms(ctx context.Context) (map[string][]string, error)

	// GetAllFunctions returns all CCU functions (Gewerke) as a map of
	// functionName → set{channelAddress, …}. Returns [ErrUnsupported] when
	// [Capabilities.Functions] is false.
	GetAllFunctions(ctx context.Context) (map[string][]string, error)

	// --- programs ---------------------------------------------------------

	// GetAllPrograms returns all CCU automation programs as raw maps.
	// Marker-based filtering is the caller's responsibility.
	GetAllPrograms(ctx context.Context) ([]map[string]any, error)

	// SetProgramState enables or disables the CCU automation program identified
	// by iseID.
	SetProgramState(ctx context.Context, iseID string, state bool) error

	// ExecuteProgram triggers the CCU automation program identified by its
	// ISE-ID. Returns true on success. Returns [ErrUnsupported] when
	// [Capabilities.ExecuteProgram] is false.
	ExecuteProgram(ctx context.Context, iseID string) (bool, error)

	// HasProgramIDs reports whether the CCU program identified by iseID exists.
	// Returns [ErrUnsupported] when [Capabilities.GetAllPrograms] is false.
	HasProgramIDs(ctx context.Context, iseID string) (bool, error)

	// --- system variables -------------------------------------------------

	// GetSystemVariable returns the value of a single CCU system variable
	// identified by name. Returns [ErrUnsupported] when
	// [Capabilities.GetAllSysvars] is false.
	GetSystemVariable(ctx context.Context, name string) (any, error)

	// GetAllSystemVariables returns all CCU system variables as raw maps.
	GetAllSystemVariables(ctx context.Context) ([]map[string]any, error)

	// SetSystemVariable sets a CCU system variable. Delegates to the appropriate
	// wire method based on the value type (bool → setBool, numeric → setFloat;
	// string requires the ReGa layer and returns ErrUnsupported).
	SetSystemVariable(ctx context.Context, name string, value any) error

	// CreateSystemVariableBool creates a new boolean sysvar on the CCU.
	CreateSystemVariableBool(ctx context.Context, name string, initVal bool) (map[string]any, error)

	// CreateSystemVariableEnum creates a new enum sysvar on the CCU.
	CreateSystemVariableEnum(ctx context.Context, name string, valueList []string) (map[string]any, error)

	// CreateSystemVariableFloat creates a new float sysvar on the CCU.
	CreateSystemVariableFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error)

	// DeleteSystemVariable deletes the system variable identified by name from
	// the CCU. Returns true on success. Returns [ErrUnsupported] when
	// [Capabilities.DeleteSystemVariable] is false.
	DeleteSystemVariable(ctx context.Context, name string) (bool, error)

	// --- system info ------------------------------------------------------

	// GetSystemUpdateInfo returns the CCU's current firmware update state and
	// any available new version.
	GetSystemUpdateInfo(ctx context.Context) (map[string]any, error)
}

// Operations is the common contract every backend implements. It
// maps cleanly to the calls the InterfaceClient and coordinators
// make — backends specialise where the wire differs.
//
// The interface is composed from capability sub-interfaces so that
// call sites can depend on the narrowest slice they need while the
// embedding keeps the full surface identical: every existing
// [Operations] implementation satisfies all sub-interfaces without
// any code change.
type Operations interface {
	LifecycleOps
	DeviceOps
	ParamsetOps
	ValueOps
	LinkOps
	SystemOps
}
