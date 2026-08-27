// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package interfaces

import (
	"context"
	"errors"
	"io"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// BackupService is the facade the backup endpoints need.
type BackupService interface {
	TriggerBackup(ctx context.Context) (string, error) // returns job id
	List(ctx context.Context) ([]hmapi.BackupEntry, error)
	Stream(ctx context.Context, id string, w io.Writer) error
	// Restore reinstalls a previously taken backup on the CCU. The
	// returned id is the (re-used) backup id so the caller can poll
	// for completion via the same job-tracking endpoints.
	Restore(ctx context.Context, id string) (string, error)
	// TriggerBackupForCentral backs up exactly one central by name and
	// returns the backup/job id. Used by the per-central scheduled-backup
	// job so a multi-CCU daemon backs up each CCU independently.
	TriggerBackupForCentral(ctx context.Context, centralName string) (string, error)
	// Prune deletes a central's oldest backups, keeping the newest keepLast.
	// keepLast <= 0 is a no-op (keep all).
	Prune(ctx context.Context, centralName string, keepLast int) error
	// StorageInfo reports where the archives are stored and how much is
	// there. The directory is not derivable from the configuration — see
	// [hmapi.BackupStorageInfo].
	StorageInfo(ctx context.Context) (hmapi.BackupStorageInfo, error)
	// Delete removes one stored archive. A missing archive is not an error:
	// the caller asked for it to be gone, and it is.
	Delete(ctx context.Context, id string) error
}

// SysvarRefreshService is the facade `POST /sysvars/fetch` depends on.
// It force re-pulls the CCU system-variable catalogue and refreshes the
// hub model. An empty centralName refreshes every registered central.
// Mirrors the Python reference's fetch_system_variables.
type SysvarRefreshService interface {
	FetchSystemVariables(ctx context.Context, centralName string) error
}

// CentralLinksService is the facade `/devices/{addr}/central-links` depends
// on. The implementation lives in central/adapter and routes the request to
// the per-CCU client backend.
type CentralLinksService interface {
	// CreateCentralLinks / RemoveCentralLinks toggle click-event routing.
	// An empty channelAddress scopes the call to the whole device (every
	// eligible channel); a non-empty channelAddress scopes it to that
	// single channel.
	CreateCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error)
	RemoveCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error)
	CentralLinksStatus(ctx context.Context, deviceAddress string) (hmapi.CentralLinksStatus, error)
}

// ConfigReader is the facade `GET /api/v1/config` depends on.
type ConfigReader interface {
	SanitizedConfig() hmapi.ConfigSnapshot
}

// CustomDPWriter is the mutating surface custom data point handlers use to
// invoke operations on custom data points. The daemon wires a concrete
// implementation that translates the abstract (device address, name,
// operation, params) tuple into the appropriate model method call and
// ultimately pushes a wire command.
//
// Implementations are responsible for audit-log entries — the handler
// layer passes Source ("rest:custom-dp:PUT") so the entry has provenance.
type CustomDPWriter interface {
	// InvokeCustomDP dispatches `operation` with `params` on the custom
	// data point identified by `deviceAddress` and `name`. Returns
	// hmapi.ErrUnknownOperation when the operation string is not in the
	// dispatch table for the DP's category, and hmapi.ErrBadParam when a
	// required param is missing or out of range.
	InvokeCustomDP(
		ctx context.Context,
		deviceAddress string,
		name string,
		operation string,
		params map[string]any,
		priority hmenum.CommandPriority,
		source string,
	) error
}

// DataPointWriter is how the REST surface asks the client layer to
// push a value to the CCU. It abstracts over the per-interface
// client so handlers never touch wire packages directly.
type DataPointWriter interface {
	SetValue(
		ctx context.Context,
		address string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// DeviceAdmin is the write-path facade for device-lifecycle
// operations. Separate from DeviceIndex so read-only deployments
// can leave it nil.
type DeviceAdmin interface {
	// UnpairDevice removes the device from the CCU. reset additionally
	// factory-resets the device during removal; force removes an unreachable
	// device even when the CCU cannot reach it for the handshake. Both map to
	// the CCU `deleteDevice` delete bitmask.
	UnpairDevice(ctx context.Context, address string, reset, force bool) error
	// RenameDevice persists the device name to the CCU. When
	// includeChannels is true every channel is renamed along with the
	// pattern "<name>:<channelNo>", matching the CCU WebUI behaviour.
	RenameDevice(ctx context.Context, address, name string, includeChannels bool) error
	// RenameChannel persists a single channel name to the CCU. The
	// channel address is resolved as deviceAddr + ":" + channelNo.
	RenameChannel(ctx context.Context, deviceAddr string, channelNo int, name string) error
	// AcceptInboxDevice promotes a pending inbox device into the active
	// registry. opts carries the optional first-time configuration
	// (name, rooms, functions) applied best-effort right after the
	// accept; a zero-value opts accepts only. When a follow-up step
	// fails the accept has already happened, so the returned error wraps
	// [ErrAcceptConfigIncomplete].
	AcceptInboxDevice(ctx context.Context, address string, opts AcceptInboxOptions) error
	// ReleaseDevice finishes onboarding: the device stops being withheld
	// from the ecosystems (MQTT / Home Assistant, Matter, the outbound
	// webhook) and is published to them.
	//
	// It is the wizard's last step, deliberately separate from the
	// accept. Between the two the device is materialised and
	// configurable — that is when its name, rooms and functions are set —
	// but invisible downstream, because an ecosystem that sees it first
	// and is corrected afterwards keeps the identity it saw: Home
	// Assistant its entity ids, a Matter controller its endpoint.
	//
	// Returns [ErrInboxDeviceNotFound] when no central withholds the
	// address, which covers both "already released" and "never held".
	ReleaseDevice(ctx context.Context, address string) error
	UpdateFirmware(ctx context.Context, address string) error
	// RestoreDeviceConfig re-transmits the centrally stored
	// configuration (all channels' MASTER paramsets + link peerings) to
	// the device after a factory reset. Supported on HmIP-RF and
	// BidCos-RF only; other interfaces answer with a
	// [backends.ErrUnsupported]-class error the handler maps to 422.
	RestoreDeviceConfig(ctx context.Context, address string) error
	// InterfaceDutyCycle returns the transmit duty cycle in percent
	// (0..100) of the radio interface the device identified by address is
	// paired to, sourced from the per-interface BidCos utilisation poll.
	// The bool is false when the value is unknown: the device is not
	// found, its interface carries no BidCos gateway (HmIP interfaces
	// report a device-level DUTY_CYCLE data point instead), or the poll
	// has not populated the cache yet. It is a cache read — no CCU round
	// trip — so the firmware-update gate can consult it inline.
	InterfaceDutyCycle(address string) (int, bool)
	SetRooms(ctx context.Context, address string, rooms []string) error
	SetFunctions(ctx context.Context, address string, functions []string) error
	// SetChannelRooms replaces a single channel's room assignments. The
	// channel address is resolved as deviceAddr + ":" + channelNo; an
	// explicit empty slice clears every assignment.
	SetChannelRooms(ctx context.Context, deviceAddr string, channelNo int, rooms []string) error
	// SetChannelFunctions replaces a single channel's function (Gewerk)
	// assignments, mirroring [DeviceAdmin.SetChannelRooms].
	SetChannelFunctions(ctx context.Context, deviceAddr string, channelNo int, functions []string) error
}

// AcceptInboxOptions carries the optional first-time configuration
// applied to an inbox device immediately after it is accepted. Every
// field is optional: an empty Name skips the rename, and nil Rooms /
// Functions leave those assignments untouched (an explicit empty slice
// clears them). IncludeChannels only matters together with Name and
// mirrors the device-rename cascade ("<name>:<channelNo>").
type AcceptInboxOptions struct {
	Name            string
	IncludeChannels bool
	Rooms           []string
	Functions       []string
}

// HasConfig reports whether any optional first-time configuration was
// requested. When false the accept is a plain promotion with no
// follow-up steps.
func (o AcceptInboxOptions) HasConfig() bool {
	return o.Name != "" || o.Rooms != nil || o.Functions != nil
}

// ErrAcceptConfigIncomplete signals that an inbox device was accepted
// successfully but one or more of the optional first-time configuration
// steps (rename, rooms, functions) failed afterwards. The accept itself
// is durable — callers should surface this so the operator re-applies
// only the configuration rather than re-accepting the device.
var ErrAcceptConfigIncomplete = errors.New("device accepted but initial configuration incomplete")

// ErrInboxDeviceNotFound signals that an accept targeted a device that is no
// longer in any central's pairing inbox — it settled or was removed on the CCU
// (a common case for the virtual backing device of a heating group). REST maps
// it to 404 so a stale inbox entry is distinguishable from an upstream failure
// (502); the daemon also drops the stale entry from its inbox view.
var ErrInboxDeviceNotFound = errors.New("inbox device not found")

// ErrChannelNotFound signals that a channel-scoped device-admin
// operation named a channel number the device does not have. REST maps
// it to 404 so a typo is distinguishable from an upstream failure.
var ErrChannelNotFound = errors.New("channel not found")

// DiagnosticsIntrospectService is the facade the live-introspection
// diagnostics endpoints depend on. It exposes read-only daemon internals
// that have no other machine-readable surface: per-interface reliability
// state and a live event-bus tap.
type DiagnosticsIntrospectService interface {
	// ReliabilitySnapshot returns per-(central, interface) reliability state,
	// filtered to centralName when non-empty.
	ReliabilitySnapshot(centralName string) []hmapi.ReliabilityState
	// ResolveCentral resolves the central for a tap: a named central must
	// exist; an empty name resolves to the sole central when exactly one is
	// configured (ADR 0002 — central is explicit otherwise). ok=false when
	// the name is unknown or ambiguous.
	ResolveCentral(centralName string) (string, bool)
	// TapEventBus subscribes to the resolved central's event bus and calls
	// emit for each event (optionally filtered by type name) until ctx is
	// done.
	TapEventBus(ctx context.Context, centralName string, types []string, emit func(hmapi.DiagnosticsEvent))
}

// IncidentsReader is the narrow facade `/incidents` depends on.
type IncidentsReader interface {
	Incidents() []hmapi.Incident
}

// LinksService is the narrow facade the /links endpoints depend on.
type LinksService interface {
	ListLinks(ctx context.Context, deviceAddress, locale string) ([]hmapi.Link, error)
	// ListAllLinks aggregates every direct link across all registered
	// centrals (or a single central when centralName is non-empty). Each
	// returned link carries its owning central_name + interface_id.
	ListAllLinks(ctx context.Context, centralName, locale string) ([]hmapi.Link, error)
	// ActivateLink triggers the receiver's LINK-paramset behaviour for the
	// given sender (the "test link at device" probe) — short or long
	// keypress. It physically actuates the receiver.
	ActivateLink(ctx context.Context, receiverAddress, senderAddress string, longPress bool) error
	AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error
	SetLinkInfo(ctx context.Context, senderAddress, receiverAddress, name, description string) error
	RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error
	LinkableChannels(
		ctx context.Context,
		interfaceID, sourceChannelAddress, role, locale string,
	) ([]hmapi.LinkableChannel, error)
}

// ParameterLabeler is the optional translator the data-point
// endpoints consult for a human-readable parameter name. It is
// locale-scoped: the concrete implementation captures the active
// locale so handlers stay language-agnostic.
type ParameterLabeler interface {
	ParameterLabel(parameter string) string
}

// ParamsetService is the narrow facade `GET/PUT /paramsets/{key}`
// depends on. The paramset key is one of VALUES / MASTER / LINK.
//
// LINK paramsets need the peer channel address (the CCU uses it as
// the paramset key on the wire) and therefore get their own method
// pair. The REST surface reflects this with a dedicated route —
// `/devices/{addr}/link-ps/{peer}` — to keep the `{key}` URL parameter
// free of ambiguity.
type ParamsetService interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error
}

// ParameterDeterminer backs `POST /devices/{addr}/channels/{no}/paramsets/{key}/determine`.
// It reads ("determines") the current live value of a single parameter
// straight from the device via the CCU's determineParameter operation,
// which auto-selects the paramset. This is a read, not a configuration
// write — the MASTER editor's "Determine" button uses it to pull the
// device's current value into an editable field.
//
// The interfaceID argument is resolved from the central registry by the
// implementation ([central/adapter.ParameterDeterminerAdapter], which
// also backs the WS `paramset.determine` command); the REST handler
// passes "" for it. Returns nil when the backend does not support the
// operation (e.g. CUxD).
type ParameterDeterminer interface {
	DetermineParameter(ctx context.Context, interfaceID, channelAddress, parameterID string) (any, error)
}

// RPCRecorderService is the facade the RPC-session-recording endpoints depend
// on. It activates/deactivates the per-central session recorder (which
// captures XML/JSON-RPC call→response pairs for deterministic golden replay)
// and exports the recorded trace.
type RPCRecorderService interface {
	// Start activates the recorder on the named centrals (empty = all),
	// bounded by durationSeconds (0 = open, clamped to a safety cap) and
	// optionally anonymising the exported trace, returning the status.
	Start(centrals []string, durationSeconds int, randomize bool) []hmapi.RPCRecordingStatus
	// Stop deactivates the recorder on the named centrals (empty = all).
	Stop(centrals []string) []hmapi.RPCRecordingStatus
	// Status returns the current recorder status for every central.
	Status() []hmapi.RPCRecordingStatus
	// Export serialises a central's recorded trace. format selects the shape
	// ("golden" = ordered replay slice, else the keyed map). Returns false
	// when the central is unknown.
	Export(centralName, format string) (any, bool)
}

// ScheduleService is the facade for climate-schedule endpoints.
//
// Two flavours: explicit-channel methods are kept for back-compat
// (older SPA versions, scripted clients); the *Auto variants resolve
// the schedule channel from the device automatically.
type ScheduleService interface {
	GetClimateSchedule(ctx context.Context, deviceAddress string, channelNo int) (*hmapi.ClimateSchedule, error)
	PutClimateSchedule(ctx context.Context, deviceAddress string, channelNo int, schedule *hmapi.ClimateSchedule) error
	SetActiveProfile(ctx context.Context, deviceAddress string, channelNo int, profile string) error

	GetClimateScheduleAuto(ctx context.Context, deviceAddress string) (*hmapi.ClimateSchedule, error)
	PutClimateScheduleAuto(ctx context.Context, deviceAddress string, schedule *hmapi.ClimateSchedule) error
	SetActiveProfileAuto(ctx context.Context, deviceAddress, profile string) error
	FindScheduleChannel(ctx context.Context, deviceAddress string) (int, error)

	// ListScheduleDevices returns every device that carries a week
	// schedule, for the fleet-wide overview. Type-derived and free of CCU
	// traffic — see hmapi.ScheduleDeviceSummary for why it does not
	// confirm against MASTER.
	ListScheduleDevices(ctx context.Context) ([]hmapi.ScheduleDeviceSummary, error)

	// CopySchedule copies the whole week schedule from one device to
	// another (channels auto-resolved on both sides).
	CopySchedule(ctx context.Context, srcDeviceAddress, dstDeviceAddress string) error
	// CopyClimateProfile copies a single climate profile from the source
	// channel/profile to the destination channel/profile.
	CopyClimateProfile(ctx context.Context, srcChannelAddress string, srcProfile int, dstChannelAddress string, dstProfile int) error
}

// UISchemaService produces a renderable UI schema for one channel /
// paramset pair. The implementation lives in the central/adapter
// layer and composes data points, easymode metadata, translations,
// and the receiver profile catalogue into a single payload the SPA
// can render.
//
// The paramset argument selects between VALUES (runtime state),
// MASTER (channel configuration), and LINK (per-peer direct-link
// configuration). For LINK the peer argument is the peer channel
// address; otherwise it is ignored.
type UISchemaService interface {
	UISchema(ctx context.Context, opts hmapi.UISchemaRequest) (*hmapi.UISchema, error)
}
