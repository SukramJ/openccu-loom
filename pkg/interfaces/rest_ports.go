// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package interfaces

import (
	"context"
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
	CreateCentralLinks(ctx context.Context, deviceAddress string) (hmapi.CentralLinksReport, error)
	RemoveCentralLinks(ctx context.Context, deviceAddress string) (hmapi.CentralLinksReport, error)
	CentralLinksStatus(deviceAddress string) (hmapi.CentralLinksStatus, error)
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
	UnpairDevice(ctx context.Context, address string) error
	// RenameDevice persists the device name to the CCU. When
	// includeChannels is true every channel is renamed along with the
	// pattern "<name>:<channelNo>", matching the CCU WebUI behaviour.
	RenameDevice(ctx context.Context, address, name string, includeChannels bool) error
	// RenameChannel persists a single channel name to the CCU. The
	// channel address is resolved as deviceAddr + ":" + channelNo.
	RenameChannel(ctx context.Context, deviceAddr string, channelNo int, name string) error
	AcceptInboxDevice(ctx context.Context, address string) error
	UpdateFirmware(ctx context.Context, address string) error
	SetRooms(ctx context.Context, address string, rooms []string) error
	SetFunctions(ctx context.Context, address string, functions []string) error
}

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
	AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error
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
