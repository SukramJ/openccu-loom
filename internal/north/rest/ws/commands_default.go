// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HealthSnapshotProvider is the minimal contract the `system.health`
// command needs. The daemon's [health.Tracker] satisfies it; tests
// supply a stub.
type HealthSnapshotProvider interface {
	Snapshot() []health.Component
	Overall() health.Status
	Score() float64
}

// SessionBackend is the minimal contract the `config.session.*`
// command set needs to open / read / write paramsets. The central
// adapter wires a concrete implementation; tests supply a stub.
//
// `Open` returns the descriptions + initial values for the channel/
// paramset, so the command handler can hydrate a fresh session
// without leaking transport details into the WebSocket layer.
//
// `PutParamset` is invoked by the `config.session.save` handler with
// the diff [Session.Changes] returned. Implementations should map
// CCU errors into a sensible CommandError so the frontend can render
// a useful message.
type SessionBackend interface {
	Open(ctx context.Context, key configui.SessionKey) (
		descriptions map[string]any, // pass-through; opaque to ws layer
		initialValues map[string]any,
		err error,
	)
	PutParamset(ctx context.Context, key configui.SessionKey, changes map[string]any) error
}

// ConstraintProvider supplies the easymode cross-validation constraints for a
// channel/paramset pair. Used by the `config.session.save` handler to run
// [configui.Session.ValidateChanges] before writing to the CCU.
//
// The concrete implementation reads the schema from the UI-schema builder;
// tests supply a stub or nil (nil means "no constraints" — the save path
// remains unblocked).
//
// ensures Easymode cross-validation constraints are passed to
// ValidateChanges() at save time, not just at form-render time.
type ConstraintProvider interface {
	// Constraints returns the cross-validation constraints for key.
	// An empty/nil slice means "no constraints" — valid by definition.
	Constraints(ctx context.Context, key configui.SessionKey) ([]configui.CrossValidationConstraint, error)
}

// DeviceQuery is the read-only surface the `devices.*` and
// `paramset.*` commands consume. The central adapter wires a concrete
// implementation against `internal/central/coordinators/configuration`
// + `model_registry`; tests supply a stub.
type DeviceQuery interface {
	// ListDevices returns one summary entry per registered device,
	// sorted alphabetically by address. Each entry MUST be JSON-
	// serialisable so the result envelope can carry it unchanged.
	ListDevices(ctx context.Context) ([]map[string]any, error)
	// GetDevice returns the full detail of one device or an error
	// when the address is unknown.
	GetDevice(ctx context.Context, address string) (map[string]any, error)
	// GetParamsetDescription returns the parameter descriptors for a
	// channel + paramset key.
	GetParamsetDescription(ctx context.Context, key configui.SessionKey) (map[string]any, error)
	// GetParamset returns the current values for a channel + paramset
	// key.
	GetParamset(ctx context.Context, key configui.SessionKey) (map[string]any, error)
}

// LinkQuery is the contract the `links.*` commands consume. Wires
// against the central's link coordinator at runtime.
type LinkQuery interface {
	// ListLinks returns every direct link a device participates in,
	// sender-first then receiver, both as channel addresses.
	ListLinks(ctx context.Context, deviceAddress string) ([]map[string]any, error)
	// AddLink creates a new direct link.
	AddLink(ctx context.Context, sender, receiver, name, description string) error
	// RemoveLink deletes an existing link.
	RemoveLink(ctx context.Context, sender, receiver string) error
	// LinkableChannels reports the channels eligible to be linked
	// against deviceAddress (typically receivers for the device's
	// senders).
	LinkableChannels(ctx context.Context, deviceAddress string) ([]map[string]any, error)
	// GetLinkParamset reads the LINK paramset on channelAddress keyed by
	// peerAddress. Mirrors Python `ws_get_link_paramset`
	// (websocket_api.py:1313, `config/get_link_paramset`).
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
	// PutLinkParamset writes values to the LINK paramset on channelAddress
	// keyed by peerAddress. Mirrors Python `ws_put_link_paramset`
	// (websocket_api.py:1387, `config/put_link_paramset`).
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error
}

// ScheduleQuery is the contract the `schedules.*` commands consume.
type ScheduleQuery interface {
	// GetClimateSchedule reads the active climate week-program for a
	// channel.
	GetClimateSchedule(ctx context.Context, channelAddress string) (map[string]any, error)
	// SetClimateSchedule writes a climate week-program back.
	SetClimateSchedule(ctx context.Context, channelAddress string, profile map[string]any) error
	// SetActiveProfile selects which P1..P3 profile is currently
	// active for a thermostat channel.
	SetActiveProfile(ctx context.Context, channelAddress string, profileIndex int) error

	// GetDeviceSchedule resolves the schedule channel of deviceAddress (climate
	// or simple) and returns the unified schedule DTO.
	GetDeviceSchedule(ctx context.Context, deviceAddress string) (map[string]any, error)
	// SetDeviceSchedule writes the schedule (climate or simple,
	// distinguished by profile["kind"]) back to whichever channel of
	// the device carries it.
	SetDeviceSchedule(ctx context.Context, deviceAddress string, profile map[string]any) error
	// SetDeviceActiveProfile selects the active climate profile
	// ("P1".."P6") on the resolved schedule channel.
	SetDeviceActiveProfile(ctx context.Context, deviceAddress, profile string) error
}

// HubQuery is the read-only / lightly-mutating surface the
// `programs.*`, `sysvars.*`, `alarm_messages.*`, `service_messages.*`,
// and `install_mode.*` commands consume. The hub-wiring adapter
// supplies the concrete implementation against `internal/model/hub`;
// tests use a stub.
type HubQuery interface {
	// ListPrograms returns one entry per CCU program.
	ListPrograms(ctx context.Context) ([]map[string]any, error)
	// ExecuteProgram runs a CCU program by id.
	ExecuteProgram(ctx context.Context, id string) error
	// ListSysvars returns one entry per system variable.
	ListSysvars(ctx context.Context) ([]map[string]any, error)
	// SetSysvar updates a system variable's value.
	SetSysvar(ctx context.Context, name string, value any) error

	// ListAlarmMessages returns active CCU alarm messages.
	ListAlarmMessages(ctx context.Context) ([]map[string]any, error)
	// AcknowledgeAlarmMessage clears one alarm message by id.
	AcknowledgeAlarmMessage(ctx context.Context, id string) error
	// ListServiceMessages returns active CCU service messages
	// (UNREACH, low-battery, config-pending, …).
	ListServiceMessages(ctx context.Context) ([]map[string]any, error)
	// AcknowledgeServiceMessage clears one service message by id.
	AcknowledgeServiceMessage(ctx context.Context, id string) error

	// InstallModeStatus reports the current pairing-mode state per
	// interface.
	InstallModeStatus(ctx context.Context) (map[string]any, error)
	// EnableInstallMode opens the pairing window for the named
	// interface for the given duration in seconds.
	EnableInstallMode(ctx context.Context, interfaceID string, durationSeconds int) error
	// DisableInstallMode closes the pairing window.
	DisableInstallMode(ctx context.Context, interfaceID string) error

	// TriggerBackup kicks off a CCU configuration backup (OpenCCU
	// only). Returns immediately — callers poll `backup.status`.
	TriggerBackup(ctx context.Context) error
	// BackupStatus returns the current backup operation state.
	BackupStatus(ctx context.Context) (map[string]any, error)

	// FirmwareInfo reports the current/available firmware versions
	// and whether an update is staged.
	FirmwareInfo(ctx context.Context) (map[string]any, error)
	// TriggerFirmwareUpdate runs the OpenCCU firmware update + reboot
	// sequence.
	TriggerFirmwareUpdate(ctx context.Context) error

	// InboxDevices returns devices the CCU has seen in pairing mode
	// but that have not yet been accepted into the configuration.
	InboxDevices(ctx context.Context) ([]map[string]any, error)
	// AcceptInboxDevice promotes a paired-but-unconfigured device
	// into the active set.
	AcceptInboxDevice(ctx context.Context, deviceAddress string) error
}

// DeviceReloader is the write surface for `config.reload_device_config`
// and `ccu.reload_device_config`. Both Python commands call
// `device.reload_device_config()` which re-pulls the device's parameter
// descriptions from the CCU and recreates any missing channels/DPs.
// Mirrors Python `ws_reload_device_config` (websocket_api.py:1735) and
// `ws_panel_reload_device_config` (websocket_api.py:2285).
type DeviceReloader interface {
	// ReloadDeviceConfig re-fetches the device description and recreates
	// missing devices/channels from the CCU. Corresponds to
	// DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices
	// scoped to a single device address.
	ReloadDeviceConfig(ctx context.Context, deviceAddress string) error
}

// DefaultCommandsConfig bundles the optional providers consumed by
// [RegisterDefaultCommands]. Any nil field disables the dependent
// command(s) — useful for tests and for daemons that only wire up a
// subset of the defaults.
type DefaultCommandsConfig struct {
	Health         HealthSnapshotProvider
	Sessions       *configui.SessionStore
	SessionBackend SessionBackend
	Devices        DeviceQuery
	Hub            HubQuery
	Links          LinkQuery
	Schedules      ScheduleQuery
	// DefinitionExport backs `devices.export_definition` (an the Python reference-
	// compatible device-definition zip). Nil skips the command.
	DefinitionExport DefinitionExporter
	// DeviceReloader backs `config.reload_device_config` and
	// `ccu.reload_device_config`.
	DeviceReloader DeviceReloader
	// Constraints backs the cross-validation pass in
	// `config.session.save`. When nil the save handler
	// skips cross-validation (backwards-compatible).
	Constraints ConstraintProvider
	// ChangeLog receives one entry per successful paramset save via
	// `config.session.save`. The entry carries the before/after diff so
	// the SPA can render a change history. When nil the save path
	// proceeds without recording (backwards-compatible).
	ChangeLog *audit.ChangeLog
}

// RegisterDefaultCommands wires the openccu-loom-default command set
// onto router. The set is small and safe to register at boot:
//
//	system.health — health snapshot + overall status + score
//	system.commands — list of registered commands (introspection)
//	devices.list / get — read-only device enumeration
//	paramset.description — paramset descriptors
//	paramset.get — current paramset values
//	config.session.open / set / undo / redo / save / discard / changes
//
// Components depend on the corresponding cfg field; passing a nil
// component skips its commands entirely.
func RegisterDefaultCommands(router *Router, cfg DefaultCommandsConfig) {
	if router == nil {
		return
	}

	if cfg.Health != nil {
		router.Register("system.health", systemHealthHandler(cfg.Health))
	}
	router.Register("system.commands", systemCommandsHandler(router))

	if cfg.Devices != nil {
		router.Register("devices.list", devicesListHandler(cfg.Devices))
		router.Register("devices.get", devicesGetHandler(cfg.Devices))
		router.Register("paramset.description", paramsetDescriptionHandler(cfg.Devices))
		router.Register("paramset.get", paramsetGetHandler(cfg.Devices))
	}

	if cfg.DefinitionExport != nil {
		router.Register("devices.export_definition", definitionExportHandler(cfg.DefinitionExport))
	}

	if cfg.Hub != nil {
		router.Register("programs.list", programsListHandler(cfg.Hub))
		router.Register("programs.execute", programsExecuteHandler(cfg.Hub))
		router.Register("sysvars.list", sysvarsListHandler(cfg.Hub))
		router.Register("sysvars.set", sysvarsSetHandler(cfg.Hub))
		router.Register("alarm_messages.list", alarmMessagesListHandler(cfg.Hub))
		router.Register("alarm_messages.ack", alarmMessagesAckHandler(cfg.Hub))
		router.Register("service_messages.list", serviceMessagesListHandler(cfg.Hub))
		router.Register("service_messages.ack", serviceMessagesAckHandler(cfg.Hub))
		router.Register("install_mode.status", installModeStatusHandler(cfg.Hub))
		router.Register("install_mode.enable", installModeEnableHandler(cfg.Hub))
		router.Register("install_mode.disable", installModeDisableHandler(cfg.Hub))
		router.Register("backup.trigger", backupTriggerHandler(cfg.Hub))
		router.Register("backup.status", backupStatusHandler(cfg.Hub))
		router.Register("firmware.info", firmwareInfoHandler(cfg.Hub))
		router.Register("firmware.update", firmwareUpdateHandler(cfg.Hub))
		router.Register("inbox.list", inboxListHandler(cfg.Hub))
		router.Register("inbox.accept", inboxAcceptHandler(cfg.Hub))
	}

	if cfg.Links != nil {
		router.Register("links.list", linksListHandler(cfg.Links))
		router.Register("links.add", linksAddHandler(cfg.Links))
		router.Register("links.remove", linksRemoveHandler(cfg.Links))
		router.Register("links.linkable_channels", linksLinkableChannelsHandler(cfg.Links))
		router.Register("links.get_paramset", linksGetParamsetHandler(cfg.Links))
		router.Register("links.put_paramset", linksPutParamsetHandler(cfg.Links))
	}

	if cfg.Schedules != nil {
		router.Register("schedules.climate.get", schedulesClimateGetHandler(cfg.Schedules))
		router.Register("schedules.climate.set", schedulesClimateSetHandler(cfg.Schedules))
		router.Register("schedules.active_profile.set", schedulesActiveProfileSetHandler(cfg.Schedules))
		router.Register("schedules.device.get", schedulesDeviceGetHandler(cfg.Schedules))
		router.Register("schedules.device.set", schedulesDeviceSetHandler(cfg.Schedules))
		router.Register("schedules.device.active_profile.set", schedulesDeviceActiveProfileSetHandler(cfg.Schedules))
	}

	if cfg.Sessions != nil {
		router.Register("config.session.set", sessionSetHandler(cfg.Sessions))
		router.Register("config.session.undo", sessionStackHandler(cfg.Sessions, true))
		router.Register("config.session.redo", sessionStackHandler(cfg.Sessions, false))
		router.Register("config.session.discard", sessionDiscardHandler(cfg.Sessions))
		router.Register("config.session.changes", sessionChangesHandler(cfg.Sessions))
		if cfg.SessionBackend != nil {
			router.Register("config.session.open", sessionOpenHandler(cfg.Sessions, cfg.SessionBackend))
			router.Register("config.session.save", sessionSaveHandler(cfg.Sessions, cfg.SessionBackend, cfg.Constraints, cfg.ChangeLog))
		}
	}

	if cfg.DeviceReloader != nil {
		// config.reload_device_config — re-pull device description
		// from the CCU and recreate missing channels/DPs. Mirrors Python
		// `ws_reload_device_config` (websocket_api.py:1735).
		router.Register("config.reload_device_config", reloadDeviceConfigHandler(cfg.DeviceReloader))
		// ccu.reload_device_config — panel variant with the same
		// domain action. Mirrors Python `ws_panel_reload_device_config`
		// (websocket_api.py:2285).
		router.Register("ccu.reload_device_config", reloadDeviceConfigHandler(cfg.DeviceReloader))
	}
}

func systemHealthHandler(hp HealthSnapshotProvider) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{
			"overall":    string(hp.Overall()),
			"score":      hp.Score(),
			"components": hp.Snapshot(),
		}, nil
	}
}

func systemCommandsHandler(r *Router) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		commands := r.Commands()
		sort.Strings(commands)
		return map[string]any{"commands": commands}, nil
	}
}

// --- devices.* / paramset.* ---

func devicesListHandler(q DeviceQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		devs, err := q.ListDevices(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_devices: "+err.Error())
		}
		return map[string]any{"devices": devs}, nil
	}
}

type deviceAddrArgs struct {
	Address string `json:"address"`
}

func devicesGetHandler(q DeviceQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args deviceAddrArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address required")
		}
		dev, err := q.GetDevice(ctx, args.Address)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_device: "+err.Error())
		}
		if dev == nil {
			return nil, NewCommandError("not_found", "no device at "+args.Address)
		}
		return dev, nil
	}
}

type paramsetArgs struct {
	CentralName    string `json:"central_name"`
	ChannelAddress string `json:"channel_address"`
	ParamsetKey    string `json:"paramset_key"`
}

func (a paramsetArgs) sessionKey() configui.SessionKey {
	psKey := hmenum.ParamsetKey(a.ParamsetKey)
	if psKey == "" {
		psKey = hmenum.ParamsetKeyMaster
	}
	return configui.SessionKey{
		CentralName:    a.CentralName,
		ChannelAddress: a.ChannelAddress,
		ParamsetKey:    psKey,
	}
}

func paramsetDescriptionHandler(q DeviceQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args paramsetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		desc, err := q.GetParamsetDescription(ctx, args.sessionKey())
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_paramset_description: "+err.Error())
		}
		return map[string]any{"descriptions": desc}, nil
	}
}

func paramsetGetHandler(q DeviceQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args paramsetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		values, err := q.GetParamset(ctx, args.sessionKey())
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_paramset: "+err.Error())
		}
		return map[string]any{"values": values}, nil
	}
}

// --- programs.* / sysvars.* ---

type programIDArgs struct {
	ID string `json:"id"`
}

type sysvarSetArgs struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func programsListHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		progs, err := q.ListPrograms(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_programs: "+err.Error())
		}
		return map[string]any{"programs": progs}, nil
	}
}

func programsExecuteHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args programIDArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id required")
		}
		if err := q.ExecuteProgram(ctx, args.ID); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "execute_program: "+err.Error())
		}
		return map[string]any{"executed": true, "id": args.ID}, nil
	}
}

func sysvarsListHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		vars, err := q.ListSysvars(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_sysvars: "+err.Error())
		}
		return map[string]any{"sysvars": vars}, nil
	}
}

func sysvarsSetHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args sysvarSetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name required")
		}
		if err := q.SetSysvar(ctx, args.Name, args.Value); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "set_sysvar: "+err.Error())
		}
		return map[string]any{"saved": true, "name": args.Name}, nil
	}
}

// --- alarm_messages.* / service_messages.* ---

type messageIDArgs struct {
	ID string `json:"id"`
}

func alarmMessagesListHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		msgs, err := q.ListAlarmMessages(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_alarm_messages: "+err.Error())
		}
		return map[string]any{"messages": msgs}, nil
	}
}

func alarmMessagesAckHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args messageIDArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id required")
		}
		if err := q.AcknowledgeAlarmMessage(ctx, args.ID); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "ack_alarm: "+err.Error())
		}
		return map[string]any{"acknowledged": true, "id": args.ID}, nil
	}
}

func serviceMessagesListHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		msgs, err := q.ListServiceMessages(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_service_messages: "+err.Error())
		}
		return map[string]any{"messages": msgs}, nil
	}
}

func serviceMessagesAckHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args messageIDArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id required")
		}
		if err := q.AcknowledgeServiceMessage(ctx, args.ID); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "ack_service: "+err.Error())
		}
		return map[string]any{"acknowledged": true, "id": args.ID}, nil
	}
}

// --- install_mode.* ---

type installModeEnableArgs struct {
	InterfaceID     string `json:"interface_id"`
	DurationSeconds int    `json:"duration_seconds"`
}

type installModeIfaceArgs struct {
	InterfaceID string `json:"interface_id"`
}

func installModeStatusHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		st, err := q.InstallModeStatus(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "install_mode_status: "+err.Error())
		}
		return st, nil
	}
}

func installModeEnableHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args installModeEnableArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.InterfaceID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "interface_id required")
		}
		if args.DurationSeconds <= 0 {
			return nil, NewCommandError(CommandErrorBadRequest, "duration_seconds must be > 0")
		}
		if err := q.EnableInstallMode(ctx, args.InterfaceID, args.DurationSeconds); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "enable_install_mode: "+err.Error())
		}
		return map[string]any{"enabled": true, "interface_id": args.InterfaceID, "duration_seconds": args.DurationSeconds}, nil
	}
}

func installModeDisableHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args installModeIfaceArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.InterfaceID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "interface_id required")
		}
		if err := q.DisableInstallMode(ctx, args.InterfaceID); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "disable_install_mode: "+err.Error())
		}
		return map[string]any{"enabled": false, "interface_id": args.InterfaceID}, nil
	}
}

// --- backup.* / firmware.* / inbox.* ---

type inboxAcceptArgs struct {
	DeviceAddress string `json:"device_address"`
}

func backupTriggerHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := q.TriggerBackup(ctx); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "trigger_backup: "+err.Error())
		}
		return map[string]any{"triggered": true}, nil
	}
}

func backupStatusHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		st, err := q.BackupStatus(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "backup_status: "+err.Error())
		}
		return st, nil
	}
}

func firmwareInfoHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		info, err := q.FirmwareInfo(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "firmware_info: "+err.Error())
		}
		return info, nil
	}
}

func firmwareUpdateHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := q.TriggerFirmwareUpdate(ctx); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "trigger_firmware_update: "+err.Error())
		}
		return map[string]any{"triggered": true}, nil
	}
}

func inboxListHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		devs, err := q.InboxDevices(ctx)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "inbox_list: "+err.Error())
		}
		return map[string]any{"devices": devs}, nil
	}
}

func inboxAcceptHandler(q HubQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args inboxAcceptArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		if err := q.AcceptInboxDevice(ctx, args.DeviceAddress); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "inbox_accept: "+err.Error())
		}
		return map[string]any{"accepted": true, "device_address": args.DeviceAddress}, nil
	}
}

// --- links.* ---

type linksDeviceArgs struct {
	DeviceAddress string `json:"device_address"`
}

type linkAddArgs struct {
	Sender      string `json:"sender"`
	Receiver    string `json:"receiver"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type linkRemoveArgs struct {
	Sender   string `json:"sender"`
	Receiver string `json:"receiver"`
}

func linksListHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linksDeviceArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		links, err := q.ListLinks(ctx, args.DeviceAddress)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "list_links: "+err.Error())
		}
		return map[string]any{"links": links}, nil
	}
}

func linksAddHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linkAddArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Sender == "" || args.Receiver == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "sender and receiver required")
		}
		if err := q.AddLink(ctx, args.Sender, args.Receiver, args.Name, args.Description); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "add_link: "+err.Error())
		}
		return map[string]any{"added": true, "sender": args.Sender, "receiver": args.Receiver}, nil
	}
}

func linksRemoveHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linkRemoveArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Sender == "" || args.Receiver == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "sender and receiver required")
		}
		if err := q.RemoveLink(ctx, args.Sender, args.Receiver); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "remove_link: "+err.Error())
		}
		return map[string]any{"removed": true, "sender": args.Sender, "receiver": args.Receiver}, nil
	}
}

func linksLinkableChannelsHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linksDeviceArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		channels, err := q.LinkableChannels(ctx, args.DeviceAddress)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "linkable_channels: "+err.Error())
		}
		return map[string]any{"channels": channels}, nil
	}
}

type linkParamsetArgs struct {
	Address     string `json:"address"`
	PeerAddress string `json:"peer_address"`
}

type linkPutParamsetArgs struct {
	Address     string         `json:"address"`
	PeerAddress string         `json:"peer_address"`
	Parameters  map[string]any `json:"parameters"`
}

// linksGetParamsetHandler implements `links.get_paramset`.
// Mirrors Python `ws_get_link_paramset` (websocket_api.py:1313).
// Input: {address, peer_address}.
// Output: {values: {...}}.
func linksGetParamsetHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linkParamsetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Address == "" || args.PeerAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address and peer_address required")
		}
		values, err := q.GetLinkParamset(ctx, args.Address, args.PeerAddress)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_link_paramset: "+err.Error())
		}
		return map[string]any{"values": values}, nil
	}
}

// linksPutParamsetHandler implements `links.put_paramset`.
// Mirrors Python `ws_put_link_paramset` (websocket_api.py:1387).
// Input: {address, peer_address, parameters}.
// Output: {success: true}.
func linksPutParamsetHandler(q LinkQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args linkPutParamsetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Address == "" || args.PeerAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address and peer_address required")
		}
		if args.Parameters == nil {
			args.Parameters = map[string]any{}
		}
		if err := q.PutLinkParamset(ctx, args.Address, args.PeerAddress, args.Parameters); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "put_link_paramset: "+err.Error())
		}
		return map[string]any{"success": true}, nil
	}
}

// --- schedules.* ---

type scheduleChannelArgs struct {
	ChannelAddress string `json:"channel_address"`
}

type scheduleClimateSetArgs struct {
	ChannelAddress string         `json:"channel_address"`
	Profile        map[string]any `json:"profile"`
}

type scheduleActiveProfileArgs struct {
	ChannelAddress string `json:"channel_address"`
	ProfileIndex   int    `json:"profile_index"`
}

func schedulesClimateGetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleChannelArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		s, err := q.GetClimateSchedule(ctx, args.ChannelAddress)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_climate_schedule: "+err.Error())
		}
		return map[string]any{"schedule": s}, nil
	}
}

func schedulesClimateSetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleClimateSetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		if args.Profile == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "profile required")
		}
		if err := q.SetClimateSchedule(ctx, args.ChannelAddress, args.Profile); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "set_climate_schedule: "+err.Error())
		}
		return map[string]any{"saved": true, "channel_address": args.ChannelAddress}, nil
	}
}

func schedulesActiveProfileSetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleActiveProfileArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		if args.ProfileIndex < 1 || args.ProfileIndex > 6 {
			return nil, NewCommandError(CommandErrorBadRequest, "profile_index must be 1..6")
		}
		if err := q.SetActiveProfile(ctx, args.ChannelAddress, args.ProfileIndex); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "set_active_profile: "+err.Error())
		}
		return map[string]any{"saved": true, "channel_address": args.ChannelAddress, "profile_index": args.ProfileIndex}, nil
	}
}

// scheduleDeviceArgs is the shared shape for `schedules.device.*`. The
// schedule channel is resolved server-side — the caller only knows the
// device address.
type scheduleDeviceArgs struct {
	DeviceAddress string `json:"device_address"`
}

type scheduleDeviceSetArgs struct {
	DeviceAddress string         `json:"device_address"`
	Profile       map[string]any `json:"profile"`
}

type scheduleDeviceActiveProfileArgs struct {
	DeviceAddress string `json:"device_address"`
	Profile       string `json:"profile"`
}

func schedulesDeviceGetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleDeviceArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		s, err := q.GetDeviceSchedule(ctx, args.DeviceAddress)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "get_device_schedule: "+err.Error())
		}
		return map[string]any{"schedule": s}, nil
	}
}

func schedulesDeviceSetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleDeviceSetArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		if args.Profile == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "profile required")
		}
		if err := q.SetDeviceSchedule(ctx, args.DeviceAddress, args.Profile); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "set_device_schedule: "+err.Error())
		}
		return map[string]any{"saved": true, "device_address": args.DeviceAddress}, nil
	}
}

func schedulesDeviceActiveProfileSetHandler(q ScheduleQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args scheduleDeviceActiveProfileArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		if args.Profile == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "profile required (e.g. P1..P6)")
		}
		if err := q.SetDeviceActiveProfile(ctx, args.DeviceAddress, args.Profile); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "set_device_active_profile: "+err.Error())
		}
		return map[string]any{"saved": true, "device_address": args.DeviceAddress, "profile": args.Profile}, nil
	}
}

// sessionOpenArgs are the inbound arguments for `config.session.open`.
type sessionOpenArgs struct {
	CentralName    string `json:"central_name"`
	ChannelAddress string `json:"channel_address"`
	ParamsetKey    string `json:"paramset_key"`
}

func (a sessionOpenArgs) key() configui.SessionKey {
	psKey := hmenum.ParamsetKey(a.ParamsetKey)
	if psKey == "" {
		psKey = hmenum.ParamsetKeyMaster
	}
	return configui.SessionKey{
		CentralName:    a.CentralName,
		ChannelAddress: a.ChannelAddress,
		ParamsetKey:    psKey,
	}
}

func sessionOpenHandler(store *configui.SessionStore, backend SessionBackend) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args sessionOpenArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address required")
		}
		key := args.key()
		descs, initial, err := backend.Open(ctx, key)
		if err != nil {
			return nil, NewCommandError(CommandErrorInternal, "open: "+err.Error())
		}
		// Sessions are stored without typed descriptions at the WS layer (the ws
		// layer stays decoupled from the wire protocol). The opaque descriptions
		// map is forwarded to the client so the frontend can render parameter
		// constraints (Min/Max/ValueList/Unit) without a second round-trip.
		store.Put(key, configui.NewSession(nil, initial))
		return map[string]any{
			"central_name":    args.CentralName,
			"channel_address": args.ChannelAddress,
			"paramset_key":    string(key.ParamsetKey),
			"current_values":  initial,
			"descriptions":    descs,
		}, nil
	}
}

// sessionMutateArgs is the common shape for set / undo / redo
// discard / save / changes — the key tuple plus optional fields.
type sessionMutateArgs struct {
	CentralName    string `json:"central_name"`
	ChannelAddress string `json:"channel_address"`
	ParamsetKey    string `json:"paramset_key"`
	Parameter      string `json:"parameter,omitempty"`
	Value          any    `json:"value,omitempty"`
}

func (a sessionMutateArgs) key() configui.SessionKey {
	psKey := hmenum.ParamsetKey(a.ParamsetKey)
	if psKey == "" {
		psKey = hmenum.ParamsetKeyMaster
	}
	return configui.SessionKey{
		CentralName:    a.CentralName,
		ChannelAddress: a.ChannelAddress,
		ParamsetKey:    psKey,
	}
}

func sessionSetHandler(store *configui.SessionStore) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args sessionMutateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.Parameter == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "parameter required")
		}
		s := store.Get(args.key())
		if s == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "no open session for key")
		}
		s.Set(args.Parameter, args.Value)
		return sessionStateMap(s), nil
	}
}

func sessionStackHandler(store *configui.SessionStore, undo bool) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args sessionMutateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		s := store.Get(args.key())
		if s == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "no open session for key")
		}
		var ok bool
		if undo {
			ok = s.Undo()
		} else {
			ok = s.Redo()
		}
		out := sessionStateMap(s)
		out["performed"] = ok
		return out, nil
	}
}

func sessionDiscardHandler(store *configui.SessionStore) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args sessionMutateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		key := args.key()
		s := store.Get(key)
		if s == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "no open session for key")
		}
		s.Discard()
		store.Delete(key)
		return map[string]any{"discarded": true}, nil
	}
}

func sessionChangesHandler(store *configui.SessionStore) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var args sessionMutateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		s := store.Get(args.key())
		if s == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "no open session for key")
		}
		return map[string]any{
			"changes":  s.Changes(),
			"detailed": s.ChangedParameters(),
			"dirty":    s.IsDirty(),
		}, nil
	}
}

// sessionSaveHandler implements `config.session.save`.
// Validates the current changes against cross-validation constraints
// before writing to the CCU. When cp is nil the validation
// step is skipped (backwards-compatible; no regression for callers
// that do not wire a ConstraintProvider).
//
// When cl is non-nil, a [audit.ChangeEntry] is appended to the log after a
// successful write. The entry carries the before/after diff so the SPA
// can render a change history. Session key is used as the sessionID so
// entries are scoped per-channel-address per-paramset.
//
// Request: { "central_name": str, "channel_address": str, "paramset_key": str }
// Response: { "saved": bool, "applied": map } or validation_error.
func sessionSaveHandler(store *configui.SessionStore, backend SessionBackend, cp ConstraintProvider, cl *audit.ChangeLog) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args sessionMutateArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		key := args.key()
		s := store.Get(key)
		if s == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "no open session for key")
		}
		// Snapshot initial values before the write for the changelog diff.
		beforeValues := s.InitialValues()
		changes := s.Changes()
		if len(changes) == 0 {
			return map[string]any{"saved": false, "reason": "nothing to save"}, nil
		}
		// Run cross-validation constraints before writing to the CCU.
		// Only cross-parameter rules are evaluated here; per-parameter
		// validation (bounds, types, enum membership) is the UI's
		// responsibility and is not repeated at save time.
		//
		// A nil ConstraintProvider means "no constraints" — the save path
		// proceeds unblocked (backwards-compatible).
		if cp != nil {
			constraints, err := cp.Constraints(ctx, key)
			if err != nil {
				return nil, NewCommandError(CommandErrorInternal, "constraints: "+err.Error())
			}
			if issues := s.ValidateCrossConstraints(constraints); len(issues) > 0 {
				msgs := make([]string, 0, len(issues))
				for _, iss := range issues {
					msgs = append(msgs, iss.Parameter+": "+iss.Reason)
				}
				return nil, NewCommandError("validation_error", "validation failed: "+joinStrings(msgs, "; "))
			}
		}
		if err := backend.PutParamset(ctx, key, changes); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "put_paramset: "+err.Error())
		}
		// Record to the change log after a successful write.
		if cl != nil {
			diff := audit.BuildChangeDiff(beforeValues, changes)
			if len(diff) > 0 {
				sessionID := key.CentralName + "/" + key.ChannelAddress + "/" + string(key.ParamsetKey)
				cl.Add(sessionID, audit.ChangeEntry{
					ChannelAddress: key.ChannelAddress,
					ParamsetKey:    string(key.ParamsetKey),
					Changes:        diff,
					Source:         "session_save",
				})
			}
		}
		store.Delete(key)
		return map[string]any{"saved": true, "applied": changes}, nil
	}
}

// joinStrings joins strings with a separator — avoids importing strings
// just for this one call site.
func joinStrings(ss []string, sep string) string {
	var out strings.Builder
	for i, s := range ss {
		if i > 0 {
			out.WriteString(sep)
		}
		out.WriteString(s)
	}
	return out.String()
}

func sessionStateMap(s *configui.Session) map[string]any {
	return map[string]any{
		"dirty":    s.IsDirty(),
		"can_undo": s.CanUndo(),
		"can_redo": s.CanRedo(),
	}
}

// reloadDeviceConfigHandler implements `config.reload_device_config` and
// `ccu.reload_device_config` (both share the same domain action).
// Re-pulls the device description from the CCU and recreates missing
// channels and data points.
// Mirrors Python `ws_reload_device_config` (websocket_api.py:1735) and
// `ws_panel_reload_device_config` (websocket_api.py:2285) — L-7002.
//
// Request: { "device_address": str }
// Response: { "success": true, "device_address": str }
func reloadDeviceConfigHandler(r DeviceReloader) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			DeviceAddress string `json:"device_address"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
		}
		if args.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address required")
		}
		if err := r.ReloadDeviceConfig(ctx, args.DeviceAddress); err != nil {
			return nil, NewCommandError(CommandErrorInternal, "reload_device_config: "+err.Error())
		}
		return map[string]any{"success": true, "device_address": args.DeviceAddress}, nil
	}
}
