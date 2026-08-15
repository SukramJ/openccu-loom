// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DeviceWriter is the mutating surface the daemon exposes for
// device-level operations beyond paramset writes. Wires against the
// central's device coordinator.
//
// Note: `devices.copy` and `devices.export` were removed in favour of
// the REST equivalents `/devices/{addr}/channels/{no}/config/export`
// and the channel-level paramset-copy flow — see
// notes/parity/by_design.md (entry "ws-rest-split").
type DeviceWriter interface {
	// Rename renames the device (CCU JSON-RPC `Device.setName`). When
	// includeChannels is true every channel is renamed along with the
	// "<name>:<channelNo>" pattern.
	Rename(ctx context.Context, address, name string, includeChannels bool) error
	// RenameChannel renames a single channel (CCU JSON-RPC
	// `Channel.setName`). The channel address is resolved as
	// deviceAddr + ":" + channelNo.
	RenameChannel(ctx context.Context, deviceAddr string, channelNo int, name string) error
	// SetInstallMode toggles a single device into install mode for the
	// given duration. Used by `device.install_mode`.
	SetInstallMode(ctx context.Context, address string, durationSeconds int) error
	// SetChannelRooms replaces a single channel's room assignments. An
	// explicit empty slice clears the set. Used by
	// `device.set_channel_rooms`.
	SetChannelRooms(ctx context.Context, deviceAddr string, channelNo int, rooms []string) error
	// SetChannelFunctions replaces a single channel's function (Gewerk)
	// assignments. Used by `device.set_channel_functions`.
	SetChannelFunctions(ctx context.Context, deviceAddr string, channelNo int, functions []string) error
	// RestoreConfig re-transmits the centrally stored configuration to
	// the device after a factory reset. Used by
	// `device.restore_config`.
	RestoreConfig(ctx context.Context, address string) error
	// ReplaceCandidates lists the paired devices the new device may
	// replace. Read-only; used by `device.replace_candidates`.
	ReplaceCandidates(ctx context.Context, centralName, newAddress string) ([]hmapi.ReplaceCandidate, error)
	// ReplaceDevice swaps a paired device for a new one. Used by
	// `device.replace`.
	ReplaceDevice(ctx context.Context, centralName, oldAddress, newAddress string) error
	// TestDeviceCommunication runs the CCU's per-device communication
	// test. Used by `device.test`.
	TestDeviceCommunication(ctx context.Context, address string) (hmapi.CommunicationTestResult, error)
	// TeamCandidates lists the team channels a channel may join.
	// Read-only; used by `device.team_candidates`.
	TeamCandidates(ctx context.Context, deviceAddr string, channelNo int) ([]hmapi.TeamCandidate, error)
	// SetChannelTeam assigns a channel to a team. Used by
	// `device.set_team`.
	SetChannelTeam(ctx context.Context, deviceAddr string, channelNo int, teamChannelAddress string) error
}

// ParamsetWriter mutates a paramset in one shot. Used by `paramset.put`
// — the session-based path is `config.session.*`. ParamsetWriter is
// for direct, non-transactional writes (e.g. one-off sysadmin
// operations).
type ParamsetWriter interface {
	PutParamset(ctx context.Context, key configui.SessionKey, values map[string]any) error
}

// ParamsetReader reads a paramset's current values. Used together with
// [ParamsetWriter] for `paramset.copy` which reads from source
// and writes to target in one logical operation.
type ParamsetReader interface {
	GetParamset(ctx context.Context, key configui.SessionKey) (map[string]any, error)
}

// EditLockVerifier reports whether `token` currently holds the edit
// lock for a resource `key`. It mirrors the REST strict edit-lock
// gate so every WS command that writes a configuration paramset —
// `paramset.put` and `master_profiles.apply` — enforces the same
// contract as `PUT /devices/{addr}/paramsets/{key}`. *handlers.EditSessions
// satisfies it. A nil verifier disables enforcement — a test-only
// escape hatch; the production mount always wires the shared registry
// (see cmd/openccu-loom/ws_adapters.go).
type EditLockVerifier interface {
	Verify(key, token string) bool
}

// ChangeHistoryQuery is the read-only contract for `change_history.list`.
// The daemon journals every paramset write via the change-history store;
// the WebSocket command exposes the most recent entries.
type ChangeHistoryQuery interface {
	List(ctx context.Context, limit int, channelAddress string) ([]map[string]any, error)
}

// CentralInfo is the introspection surface exposed via the
// `central.*` command family. Handlers are read-only except for
// `central.reconcile` which kicks an ad-hoc reconciliation pass.
type CentralInfo interface {
	Info(ctx context.Context) (map[string]any, error)
	Connectivity(ctx context.Context) ([]map[string]any, error)
	SystemHealth(ctx context.Context) (map[string]any, error)
	Reconcile(ctx context.Context) error
}

// ExtendedHub adds the niche service-message operations that were
// missing from the base [HubQuery] contract: durable suppression of a
// message ([ExtendedHub.DisableServiceMessage]), listing the current
// suppressions, and clearing one.
type ExtendedHub interface {
	DisableServiceMessage(ctx context.Context, id string) error
	// ListSuppressedServiceMessages returns the durably-suppressed
	// channel parameters across every central.
	ListSuppressedServiceMessages(ctx context.Context) ([]map[string]any, error)
	// UnsuppressServiceMessage clears a durable suppression. interfaceID
	// may be empty (resolved from the stored suppression); an empty
	// parameter clears every service parameter of the channel.
	UnsuppressServiceMessage(ctx context.Context, interfaceID, channel, parameter string) error
}

// CentralLinksManager is the mutating + read surface for the
// `central.create_links` / `central.remove_links` / `central.links_status`
// command family. It toggles the CCU's per-channel click-event forwarding
// (REPORT_VALUE_USAGE) for a device. Mirrors Python `create_central_links` /
// `remove_central_links`. The concrete implementation is
// [adapter.CentralLinksDomain]; the same facade backs the REST
// `/devices/{addr}/central-links` endpoints.
type CentralLinksManager interface {
	// CreateCentralLinks / RemoveCentralLinks toggle click-event routing.
	// An empty channelAddress scopes the call to the whole device; a
	// non-empty channelAddress scopes it to that single channel.
	CreateCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error)
	RemoveCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error)
	CentralLinksStatus(ctx context.Context, deviceAddress string) (hmapi.CentralLinksStatus, error)
}

// SessionRecorder is the diagnostic start/stop/status surface for the
// `recording.start` / `recording.stop` / `recording.status` command family.
// It mirrors Python `record_session` (start/stop) and toggles the per-central
// RPC session recorder used to capture golden-file replay traces. Start clears
// and arms the recorder; Stop disarms it; IsActive reports whether new RPC
// calls are being captured.
type SessionRecorder interface {
	// Start activates recording and reports the resulting active state.
	Start() bool
	// Stop deactivates recording and reports the resulting active state.
	Stop() bool
	// IsActive reports whether the recorder is currently capturing.
	IsActive() bool
}

// ThrottleStats is the read contract for `ccu.throttle_stats`.
// Returns per-interface command-throttle diagnostics (in_flight, waiting,
// burst_downgrades, waited_for_burst_slot). Mirrors Python
// `ws_get_command_throttle_stats` (websocket_api.py:1786).
type ThrottleStats interface {
	CommandThrottleStats(ctx context.Context) ([]map[string]any, error)
}

// CacheClearer is the write contract for `ccu.cache_clear`.
// Clears CCU-derivable caches scoped by kind (global/central/interface/device)
// and triggers a re-pull so the next read fetches fresh data from the CCU.
// Mirrors Python `ws_clear_cache` (websocket_api.py:1885).
type CacheClearer interface {
	ClearCache(ctx context.Context, scope cachereset.Scope) (cachereset.Report, error)
}

// DeviceStatisticsQuery is the read contract for `ccu.device_statistics`.
// Returns per-interface device counts (total, unreachable, firmware_updatable)
// plus grand totals. Mirrors Python `ws_get_device_statistics`
// (websocket_api.py:1906).
type DeviceStatisticsQuery interface {
	DeviceStatistics(ctx context.Context) (map[string]any, error)
}

// FirmwareRefresher is the write contract for `firmware.refresh`.
// Triggers a force-refresh of the firmware cache from the CCU.
// Mirrors Python `ws_refresh_firmware_data` (websocket_api.py:2252).
type FirmwareRefresher interface {
	RefreshFirmwareData(ctx context.Context) error
}

// ChangeHistoryClearer is the write contract for `change_history.clear`.
// Truncates the persisted change-history log. Mirrors Python
// `ws_clear_change_history` (websocket_api.py:999).
type ChangeHistoryClearer interface {
	ClearChangeHistory(ctx context.Context) error
}

// AddonUpdater is the write contract for `addon_update.check` and
// `addon_update.install` (ADR 0057). Both verbs are fire-and-forget:
// progress and outcome arrive on the addon_update.state_changed
// broadcast. Nil (platform unsupported) leaves the commands
// unregistered — clients see the standard unknown-command error.
type AddonUpdater interface {
	Check(ctx context.Context) error
	InstallAsync(ctx context.Context) error
}

// IncidentClearer is the write contract for `incidents.clear`.
// Clears all entries from the incident store. Mirrors Python
// `ws_clear_incidents` (websocket_api.py:1863).
type IncidentClearer interface {
	ClearIncidents(ctx context.Context) error
}

// IncidentLister is the read contract for `incidents.list` and its alias
// `incidents.get`. Returns a list of current diagnostic incidents across
// all centrals. Mirrors Python `ws_get_incidents`
// (`websocket_api.py` — closes parity-audit gap L12).
type IncidentLister interface {
	ListIncidents(ctx context.Context) ([]map[string]any, error)
}

// UISchemaQuery is the read contract for `paramset.form_schema`.
// Returns a full UI schema (groups, parameters, visibility rules,
// profiles) for one channel/paramset pair. Mirrors Python
// `ws_get_form_schema` (websocket_api.py:252): input channel_address +
// paramset_key (MASTER|VALUES), output the FormSchema object.
// The concrete implementation is [handlers.UISchemaService].
type UISchemaQuery interface {
	// FormSchema returns the UI schema for address/channel/paramset.
	// address is the channel address, channelNo the channel number
	// within the device, paramset one of "VALUES"|"MASTER", locale
	// the display locale (default "en"), peer the peer channel address
	// for LINK paramsets (otherwise "").
	FormSchema(ctx context.Context, address string, channelNo int, paramset, locale, peer string) (map[string]any, error)
}

// ExtendedCommandsConfig bundles the optional providers the new
// `RegisterExtendedCommands` consumes. Same nil-disables semantics
// as [DefaultCommandsConfig].
type ExtendedCommandsConfig struct {
	Devices   DeviceWriter
	Paramsets ParamsetWriter
	// EditLocks enforces the strict per-resource edit lock on the
	// commands that write a configuration paramset — `paramset.put`
	// (MASTER/LINK keys) and `master_profiles.apply` (always MASTER).
	// Nil disables enforcement (test-only); production wires the shared
	// REST edit-session registry so REST and WS share one lock namespace.
	EditLocks     EditLockVerifier
	ChangeHistory ChangeHistoryQuery
	// ChangeHistoryClearer backs `change_history.clear`.
	ChangeHistoryClearer ChangeHistoryClearer
	Central              CentralInfo
	ExtendedHub          ExtendedHub
	MasterProfiles       *masterprofile.Store
	// ThrottleStats backs `ccu.throttle_stats`.
	ThrottleStats ThrottleStats
	// CacheClearer backs `ccu.cache_clear`.
	CacheClearer CacheClearer
	// DeviceStatistics backs `ccu.device_statistics`.
	DeviceStatistics DeviceStatisticsQuery
	// FirmwareRefresher backs `firmware.refresh`.
	FirmwareRefresher FirmwareRefresher
	// AddonUpdater backs `addon_update.check` / `addon_update.install`.
	AddonUpdater AddonUpdater
	// IncidentClearer backs `incidents.clear`.
	IncidentClearer IncidentClearer
	// IncidentLister backs `incidents.list` and its alias `incidents.get`.
	IncidentLister IncidentLister
	// UISchema backs `paramset.form_schema`.
	UISchema UISchemaQuery
	// ParamsetReader backs the read half of `paramset.copy`.
	// When nil, paramset.copy is not registered.
	ParamsetReader ParamsetReader
	// CentralLinks backs `central.create_links`, `central.remove_links`,
	// and `central.links_status`.
	CentralLinks CentralLinksManager
	// SessionRecorder backs `recording.start`, `recording.stop`, and
	// `recording.status`.
	SessionRecorder SessionRecorder
	// Groups backs `groups.list` — the read-only heating-group listing.
	Groups GroupsQuery
	// GroupsAdmin backs the heating-group administration commands (GR02):
	// groups.create / groups.update / groups.delete plus the groups.types /
	// groups.suitable_members read helpers. Same cmd-level adapter as the
	// REST group-admin surface.
	GroupsAdmin handlers.GroupsWriter
}

// RegisterExtendedCommands wires the post-MVP command set onto router.
// The set complements [RegisterDefaultCommands] — call both at boot
// to expose the full command surface. Any nil sub-config field skips its
// commands.
// registerDeviceCommands registers the device.* WS command family.
// Extracted from RegisterExtendedCommands to keep it under the funlen
// budget as the family grows.
func registerDeviceCommands(router *Router, d DeviceWriter) {
	if d == nil {
		return
	}
	router.Register("device.rename", deviceRenameHandler(d))
	router.Register("device.rename_channel", deviceRenameChannelHandler(d))
	router.Register("device.install_mode", deviceInstallModeHandler(d))
	router.Register("device.set_channel_rooms", deviceSetChannelRoomsHandler(d))
	router.Register("device.set_channel_functions", deviceSetChannelFunctionsHandler(d))
	router.Register("device.restore_config", deviceRestoreConfigHandler(d))
	router.Register("device.test", deviceTestHandler(d))
	router.Register("device.team_candidates", deviceTeamCandidatesHandler(d))
	router.Register("device.set_team", deviceSetTeamHandler(d))
	router.Register("device.replace_candidates", deviceReplaceCandidatesHandler(d))
	router.Register("device.replace", deviceReplaceHandler(d))
}

// RegisterExtendedCommands registers the extended (non-default) WS
// command families onto router — device lifecycle, paramsets, master
// profiles, schedules, links and the rest — each guarded by its
// corresponding non-nil config facade.
func RegisterExtendedCommands(router *Router, cfg ExtendedCommandsConfig) {
	if router == nil {
		return
	}
	// --- Schreibpfad zuerst (priorisiert) ---
	registerDeviceCommands(router, cfg.Devices)
	if cfg.Paramsets != nil {
		router.Register("paramset.put", paramsetPutHandler(cfg.Paramsets, cfg.EditLocks))
	}
	if cfg.MasterProfiles != nil {
		router.Register("master_profiles.list", masterProfilesListHandler(cfg.MasterProfiles))
		router.Register("master_profiles.get", masterProfilesGetHandler(cfg.MasterProfiles))
		router.Register("master_profiles.apply", masterProfilesApplyHandler(cfg.MasterProfiles, cfg.Paramsets, cfg.EditLocks))
		router.Register("master_profiles.match", masterProfilesMatchHandler(cfg.MasterProfiles))
	}
	// --- Reports zweit ---
	if cfg.ChangeHistory != nil {
		router.Register("change_history.list", changeHistoryListHandler(cfg.ChangeHistory))
	}
	if cfg.ChangeHistoryClearer != nil {
		// change_history.clear — truncate the persisted log.
		router.Register("change_history.clear", changeHistoryClearHandler(cfg.ChangeHistoryClearer))
	}
	if cfg.Central != nil {
		router.Register("central.info", centralInfoHandler(cfg.Central))
		router.Register("central.connectivity", centralConnectivityHandler(cfg.Central))
		router.Register("central.system_health", centralSystemHealthHandler(cfg.Central))
		router.Register("central.reconcile", centralReconcileHandler(cfg.Central))
	}
	if cfg.ThrottleStats != nil {
		// ccu.throttle_stats — per-interface command-throttle diagnostics.
		router.Register("ccu.throttle_stats", ccuThrottleStatsHandler(cfg.ThrottleStats))
	}
	if cfg.CacheClearer != nil {
		// ccu.cache_clear — clear all in-memory caches.
		router.Register("ccu.cache_clear", ccuCacheClearHandler(cfg.CacheClearer))
	}
	if cfg.DeviceStatistics != nil {
		// ccu.device_statistics — per-interface device telemetry.
		router.Register("ccu.device_statistics", ccuDeviceStatisticsHandler(cfg.DeviceStatistics))
	}
	if cfg.FirmwareRefresher != nil {
		// firmware.refresh — force-refresh firmware cache from CCU.
		router.Register("firmware.refresh", firmwareRefreshHandler(cfg.FirmwareRefresher))
	}
	if cfg.AddonUpdater != nil {
		// addon_update.check / .install — CCU add-on self-update verbs
		// (ADR 0057); results stream via addon_update.state_changed.
		router.Register("addon_update.check", addonUpdateCheckHandler(cfg.AddonUpdater))
		router.Register("addon_update.install", addonUpdateInstallHandler(cfg.AddonUpdater))
	}
	if cfg.IncidentClearer != nil {
		// incidents.clear — clear the incident store.
		router.Register("incidents.clear", incidentsClearHandler(cfg.IncidentClearer))
	}
	if cfg.IncidentLister != nil {
		// incidents.list and its alias incidents.get both map to the same
		// handler closure. Mirrors Python ws_get_incidents (L12).
		h := incidentsListHandler(cfg.IncidentLister)
		router.Register("incidents.list", h)
		router.Register("incidents.get", h)
	}
	if cfg.Groups != nil {
		// groups.list — read-only heating-group listing (one entry per
		// central; optional `central` narrows to one).
		router.Register("groups.list", groupsListHandler(cfg.Groups))
	}
	if cfg.GroupsAdmin != nil {
		// Heating-group administration (GR02): create / update / delete via
		// the CCU jpages proxy, plus the type / suitable-member read helpers.
		router.Register("groups.types", groupsTypesHandler(cfg.GroupsAdmin))
		router.Register("groups.suitable_members", groupsSuitableMembersHandler(cfg.GroupsAdmin))
		router.Register("groups.create", groupsCreateHandler(cfg.GroupsAdmin))
		router.Register("groups.update", groupsUpdateHandler(cfg.GroupsAdmin))
		router.Register("groups.delete", groupsDeleteHandler(cfg.GroupsAdmin))
	}
	if cfg.ExtendedHub != nil {
		router.Register("service_messages.disable", serviceMessagesDisableHandler(cfg.ExtendedHub))
		router.Register("service_messages.suppressed", serviceMessagesSuppressedHandler(cfg.ExtendedHub))
		router.Register("service_messages.unsuppress", serviceMessagesUnsuppressHandler(cfg.ExtendedHub))
	}
	if cfg.UISchema != nil {
		// paramset.form_schema — full UI schema for one channel/paramset.
		// Mirrors Python `ws_get_form_schema` (websocket_api.py:252).
		router.Register("paramset.form_schema", paramsetFormSchemaHandler(cfg.UISchema))
	}
	if cfg.ParamsetReader != nil && cfg.Paramsets != nil {
		// paramset.copy — generic paramset-to-paramset copy between
		// channels of matching type. Reads the VALUES or MASTER paramset
		// from the source channel and writes the writable subset to the
		// target. Mirrors Python `ws_copy_paramset` (websocket_api.py:916).
		router.Register("paramset.copy", paramsetCopyHandler(cfg.ParamsetReader, cfg.Paramsets))
	}
	if cfg.CentralLinks != nil {
		// central.create_links / central.remove_links — toggle CCU
		// click-event forwarding for a device's press-event channels.
		// Mirrors Python create_central_links / remove_central_links.
		router.Register("central.create_links", centralCreateLinksHandler(cfg.CentralLinks))
		router.Register("central.remove_links", centralRemoveLinksHandler(cfg.CentralLinks))
		router.Register("central.links_status", centralLinksStatusHandler(cfg.CentralLinks))
	}
	if cfg.SessionRecorder != nil {
		// recording.start / recording.stop — toggle the diagnostic RPC
		// session recorder. Mirrors Python record_session.
		router.Register("recording.start", recordingStartHandler(cfg.SessionRecorder))
		router.Register("recording.stop", recordingStopHandler(cfg.SessionRecorder))
		router.Register("recording.status", recordingStatusHandler(cfg.SessionRecorder))
	}
}

// --- handler implementations -----------------------------------------

func deviceRenameHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address         string `json:"address"`
			Name            string `json:"name"`
			IncludeChannels bool   `json:"include_channels"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name is required")
		}
		if err := d.Rename(ctx, p.Address, p.Name, p.IncludeChannels); err != nil {
			return nil, fmt.Errorf("device.rename: %w", err)
		}
		return map[string]any{
			"address":          p.Address,
			"name":             p.Name,
			"include_channels": p.IncludeChannels,
		}, nil
	}
}

func deviceRenameChannelHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
			Channel int    `json:"channel"`
			Name    string `json:"name"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name is required")
		}
		if err := d.RenameChannel(ctx, p.Address, p.Channel, p.Name); err != nil {
			return nil, fmt.Errorf("device.rename_channel: %w", err)
		}
		return map[string]any{
			"address": p.Address,
			"channel": p.Channel,
			"name":    p.Name,
		}, nil
	}
}

func deviceSetChannelRoomsHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string   `json:"address"`
			Channel int      `json:"channel"`
			Rooms   []string `json:"rooms"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Rooms == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "rooms is required (an empty array clears the assignment)")
		}
		if err := d.SetChannelRooms(ctx, p.Address, p.Channel, p.Rooms); err != nil {
			return nil, fmt.Errorf("device.set_channel_rooms: %w", err)
		}
		return map[string]any{
			"address": p.Address,
			"channel": p.Channel,
			"rooms":   p.Rooms,
		}, nil
	}
}

func deviceSetChannelFunctionsHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address   string   `json:"address"`
			Channel   int      `json:"channel"`
			Functions []string `json:"functions"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Functions == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "functions is required (an empty array clears the assignment)")
		}
		if err := d.SetChannelFunctions(ctx, p.Address, p.Channel, p.Functions); err != nil {
			return nil, fmt.Errorf("device.set_channel_functions: %w", err)
		}
		return map[string]any{
			"address":   p.Address,
			"channel":   p.Channel,
			"functions": p.Functions,
		}, nil
	}
}

func deviceRestoreConfigHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if err := d.RestoreConfig(ctx, p.Address); err != nil {
			return nil, fmt.Errorf("device.restore_config: %w", err)
		}
		return map[string]any{"address": p.Address}, nil
	}
}

func deviceTestHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		result, err := d.TestDeviceCommunication(ctx, p.Address)
		if err != nil {
			return nil, fmt.Errorf("device.test: %w", err)
		}
		return result, nil
	}
}

func deviceTeamCandidatesHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
			Channel int    `json:"channel"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		candidates, err := d.TeamCandidates(ctx, p.Address, p.Channel)
		if err != nil {
			return nil, fmt.Errorf("device.team_candidates: %w", err)
		}
		if candidates == nil {
			candidates = []hmapi.TeamCandidate{}
		}
		return map[string]any{"candidates": candidates}, nil
	}
}

func deviceSetTeamHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string  `json:"address"`
			Channel int     `json:"channel"`
			Team    *string `json:"team"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		team := ""
		if p.Team != nil {
			team = *p.Team
		}
		if err := d.SetChannelTeam(ctx, p.Address, p.Channel, team); err != nil {
			return nil, fmt.Errorf("device.set_team: %w", err)
		}
		return map[string]any{"address": p.Address, "channel": p.Channel, "team": team}, nil
	}
}

func deviceReplaceCandidatesHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
			Central string `json:"central"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		candidates, err := d.ReplaceCandidates(ctx, p.Central, p.Address)
		if err != nil {
			return nil, fmt.Errorf("device.replace_candidates: %w", err)
		}
		if candidates == nil {
			candidates = []hmapi.ReplaceCandidate{}
		}
		return map[string]any{"candidates": candidates}, nil
	}
}

func deviceReplaceHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address    string `json:"address"`
			OldAddress string `json:"old_address"`
			Central    string `json:"central"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.OldAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "old_address is required")
		}
		if err := d.ReplaceDevice(ctx, p.Central, p.OldAddress, p.Address); err != nil {
			return nil, fmt.Errorf("device.replace: %w", err)
		}
		return map[string]any{
			"status":      "replacing",
			"old_address": p.OldAddress,
			"new_address": p.Address,
			"central":     p.Central,
		}, nil
	}
}

func deviceInstallModeHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address  string `json:"address"`
			Duration int    `json:"duration_seconds"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Duration <= 0 {
			p.Duration = 60
		}
		if err := d.SetInstallMode(ctx, p.Address, p.Duration); err != nil {
			return nil, fmt.Errorf("device.install_mode: %w", err)
		}
		return map[string]any{"address": p.Address, "duration_seconds": p.Duration}, nil
	}
}

func paramsetPutHandler(w ParamsetWriter, locks EditLockVerifier) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Channel  string         `json:"channel_address"`
			Paramset string         `json:"paramset_key"`
			Values   map[string]any `json:"values"`
			// EditToken carries the edit-lock token for MASTER/LINK
			// writes under strict enforcement. Ignored for VALUES.
			EditToken string `json:"edit_token"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Channel == "" || p.Paramset == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel_address and paramset_key are required")
		}
		if len(p.Values) == 0 {
			return nil, NewCommandError(CommandErrorBadRequest, "values must not be empty")
		}
		psKey := hmenum.ParamsetKey(p.Paramset)
		// Strict edit-lock enforcement mirrors the REST gate: MASTER and
		// LINK are configuration writes that require holding the lock.
		// The lock key mirrors the SPA's channel:{addr}:{key} format;
		// this WS path carries no peer suffix (per-peer LINK writes use
		// the REST link-ps route), so it locks the whole set.
		if locks != nil && (psKey == hmenum.ParamsetKeyMaster || psKey == hmenum.ParamsetKeyLink) {
			if !locks.Verify("channel:"+p.Channel+":"+string(psKey), p.EditToken) {
				return nil, NewCommandError(CommandErrorLocked,
					"edit lock required for "+string(psKey)+" write; open an edit session and pass edit_token")
			}
		}
		key := configui.SessionKey{ChannelAddress: p.Channel, ParamsetKey: psKey}
		if err := w.PutParamset(ctx, key, p.Values); err != nil {
			return nil, fmt.Errorf("paramset.put: %w", err)
		}
		return map[string]any{"written": len(p.Values)}, nil
	}
}

func masterProfilesListHandler(s *masterprofile.Store) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceType  string `json:"device_type"`
			ChannelType string `json:"channel_type"`
			Locale      string `json:"locale"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceType == "" {
			types, err := s.DeviceTypes()
			if err != nil {
				return nil, err
			}
			return map[string]any{"device_types": types}, nil
		}
		profiles, err := s.Profiles(p.DeviceType, p.ChannelType)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(profiles))
		for _, prof := range profiles {
			out = append(out, map[string]any{
				"id":          prof.ID,
				"name":        prof.LocalisedName(p.Locale),
				"description": prof.LocalisedDescription(p.Locale),
				"param_count": len(prof.Params),
			})
		}
		return map[string]any{"profiles": out}, nil
	}
}

func masterProfilesGetHandler(s *masterprofile.Store) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceType  string `json:"device_type"`
			ChannelType string `json:"channel_type"`
			ID          int    `json:"id"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceType == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_type is required")
		}
		prof, err := s.Profile(p.DeviceType, p.ChannelType, p.ID)
		if err != nil {
			return nil, err
		}
		return prof, nil
	}
}

// masterProfilesMatchHandler implements the `master_profiles.match` WebSocket
// command.
//
// Request payload:
//
// { "device_type": "HmIP-eTRV", (required) "channel_type": "CLIMATECONTROL",
// (optional, default "KEY") "current_values": { "MODE": 1,
// "SETPOINT_TEMPERATURE": 19.0 } }
//
// Response:
//
// { "active_id": 2 } // 0 means "no profile matches" (Expert)
func masterProfilesMatchHandler(s *masterprofile.Store) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceType    string         `json:"device_type"`
			ChannelType   string         `json:"channel_type"`
			CurrentValues map[string]any `json:"current_values"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceType == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_type is required")
		}
		id := s.MatchActiveProfile(p.DeviceType, p.ChannelType, p.CurrentValues)
		return map[string]any{"active_id": id}, nil
	}
}

func masterProfilesApplyHandler(s *masterprofile.Store, w ParamsetWriter, locks EditLockVerifier) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceType     string `json:"device_type"`
			ChannelType    string `json:"channel_type"`
			ChannelAddress string `json:"channel_address"`
			ID             int    `json:"id"`
			// EditToken carries the edit-lock token, as on `paramset.put`:
			// applying a profile is a MASTER write on the target channel.
			EditToken string `json:"edit_token"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceType == "" || p.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_type and channel_address are required")
		}
		// Applying a profile writes the channel's MASTER paramset, so it runs
		// through the same strict edit-lock gate as `paramset.put`. Without it
		// the lock guarantees nothing: the write another editor's open session
		// is protected against arrives through this sibling command instead.
		if locks != nil {
			key := "channel:" + p.ChannelAddress + ":" + string(hmenum.ParamsetKeyMaster)
			if !locks.Verify(key, p.EditToken) {
				return nil, NewCommandError(CommandErrorLocked,
					"edit lock required for MASTER write; open an edit session and pass edit_token")
			}
		}
		prof, err := s.Profile(p.DeviceType, p.ChannelType, p.ID)
		if err != nil {
			return nil, err
		}
		if w == nil {
			return nil, NewCommandError(CommandErrorBadRequest, "paramset writer not configured")
		}
		// Apply only "fixed"-constraint profile params via PutParamset
		// (partial write). Non-profile parameters on the target channel
		// are NOT touched because PutParamset is a sparse merge on the
		// CCU side: the CCU only updates the keys present in the map,
		// leaving all other MASTER parameters at their current device
		// values. The sparse dict avoids a pre-read and carries no
		// data-loss risk for parameters outside the profile.
		values := make(map[string]any, len(prof.Params))
		for k, v := range prof.Params {
			if v.ConstraintType == "fixed" {
				values[k] = v.Value
			}
		}
		key := configui.SessionKey{ChannelAddress: p.ChannelAddress, ParamsetKey: hmenum.ParamsetKeyMaster}
		if err := w.PutParamset(ctx, key, values); err != nil {
			return nil, fmt.Errorf("master_profiles.apply: %w", err)
		}
		return map[string]any{"applied": p.ID, "param_count": len(values)}, nil
	}
}

func changeHistoryListHandler(q ChangeHistoryQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Limit          int    `json:"limit"`
			ChannelAddress string `json:"channel_address"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Limit <= 0 {
			p.Limit = 100
		}
		entries, err := q.List(ctx, p.Limit, p.ChannelAddress)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil
	}
}

func centralInfoHandler(c CentralInfo) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		return c.Info(ctx)
	}
}

func centralConnectivityHandler(c CentralInfo) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		out, err := c.Connectivity(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"interfaces": out}, nil
	}
}

func centralSystemHealthHandler(c CentralInfo) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		return c.SystemHealth(ctx)
	}
}

func centralReconcileHandler(c CentralInfo) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := c.Reconcile(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"reconciled": true}, nil
	}
}

func serviceMessagesDisableHandler(h ExtendedHub) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.ID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "id is required")
		}
		if err := h.DisableServiceMessage(ctx, p.ID); err != nil {
			return nil, fmt.Errorf("service_messages.disable: %w", err)
		}
		return map[string]any{"disabled": p.ID}, nil
	}
}

func serviceMessagesSuppressedHandler(h ExtendedHub) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		items, err := h.ListSuppressedServiceMessages(ctx)
		if err != nil {
			return nil, fmt.Errorf("service_messages.suppressed: %w", err)
		}
		if items == nil {
			items = []map[string]any{}
		}
		return map[string]any{"items": items}, nil
	}
}

func serviceMessagesUnsuppressHandler(h ExtendedHub) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Interface string `json:"interface"`
			Channel   string `json:"channel"`
			Parameter string `json:"parameter"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Channel == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "channel is required")
		}
		if err := h.UnsuppressServiceMessage(ctx, p.Interface, p.Channel, p.Parameter); err != nil {
			return nil, fmt.Errorf("service_messages.unsuppress: %w", err)
		}
		return map[string]any{"unsuppressed": p.Channel}, nil
	}
}

// ccuThrottleStatsHandler implements `ccu.throttle_stats`.
// Returns per-interface throttle diagnostics (in_flight, waiting,
// burst_downgrades, waited_for_burst_slot).
// Mirrors Python `ws_get_command_throttle_stats` (websocket_api.py:1786).
func ccuThrottleStatsHandler(s ThrottleStats) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		stats, err := s.CommandThrottleStats(ctx)
		if err != nil {
			return nil, fmt.Errorf("ccu.throttle_stats: %w", err)
		}
		return map[string]any{"throttles": stats}, nil
	}
}

// ccuCacheClearHandler implements `ccu.cache_clear`.
// Accepts optional params {kind, central, interface, device} to scope the
// clear; omitting kind (or passing "global") clears every central.
// Mirrors Python `ws_clear_cache` (websocket_api.py:1885).
func ccuCacheClearHandler(c CacheClearer) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Kind      string `json:"kind"`
			Central   string `json:"central"`
			Interface string `json:"interface"`
			Device    string `json:"device"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Kind == "" {
			p.Kind = string(cachereset.ScopeGlobal)
		}
		scope := cachereset.Scope{
			Kind:      cachereset.ScopeKind(p.Kind),
			Central:   p.Central,
			Interface: p.Interface,
			Device:    p.Device,
		}
		if err := scope.Validate(); err != nil {
			return nil, NewCommandError(CommandErrorBadRequest, err.Error())
		}
		report, err := c.ClearCache(ctx, scope)
		if err != nil {
			// Partial errors: surface the report alongside the error so
			// the caller can inspect what was cleared before the failure.
			return map[string]any{
				"scope":           string(report.Scope.Kind),
				"devices":         report.Devices,
				"paramsets":       report.Paramsets,
				"values":          report.Values,
				"master":          report.Master,
				"centrals_reinit": report.CentralsReinit,
				"errors":          report.Errors,
			}, fmt.Errorf("ccu.cache_clear: %w", err)
		}
		return map[string]any{
			"scope":           string(report.Scope.Kind),
			"devices":         report.Devices,
			"paramsets":       report.Paramsets,
			"values":          report.Values,
			"master":          report.Master,
			"centrals_reinit": report.CentralsReinit,
		}, nil
	}
}

// ccuDeviceStatisticsHandler implements `ccu.device_statistics`.
// Returns per-interface counts (total, unreachable, firmware_updatable)
// plus grand totals. Mirrors Python `ws_get_device_statistics`
// (websocket_api.py:1906).
func ccuDeviceStatisticsHandler(q DeviceStatisticsQuery) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		stats, err := q.DeviceStatistics(ctx)
		if err != nil {
			return nil, fmt.Errorf("ccu.device_statistics: %w", err)
		}
		return stats, nil
	}
}

// firmwareRefreshHandler implements `firmware.refresh`.
// Triggers a force-refresh of the firmware cache from the CCU.
// Mirrors Python `ws_refresh_firmware_data` (websocket_api.py:2252).
func firmwareRefreshHandler(r FirmwareRefresher) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := r.RefreshFirmwareData(ctx); err != nil {
			return nil, fmt.Errorf("firmware.refresh: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// changeHistoryClearHandler implements `change_history.clear`.
// Truncates the persisted change-history log.
// Mirrors Python `ws_clear_change_history` (websocket_api.py:999).
func changeHistoryClearHandler(c ChangeHistoryClearer) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := c.ClearChangeHistory(ctx); err != nil {
			return nil, fmt.Errorf("change_history.clear: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// addonUpdateCheckHandler implements `addon_update.check`: kick a
// release check; state transitions arrive on the broadcast.
func addonUpdateCheckHandler(u AddonUpdater) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := u.Check(ctx); err != nil {
			return nil, fmt.Errorf("addon_update.check: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// addonUpdateInstallHandler implements `addon_update.install`: start
// the verified download + firmware install. Fire-and-forget — the
// daemon restarts on success; failures surface on the broadcast.
func addonUpdateInstallHandler(u AddonUpdater) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := u.InstallAsync(ctx); err != nil {
			return nil, fmt.Errorf("addon_update.install: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// incidentsClearHandler implements `incidents.clear`.
// Clears all entries from the incident store.
// Mirrors Python `ws_clear_incidents` (websocket_api.py:1863).
func incidentsClearHandler(c IncidentClearer) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := c.ClearIncidents(ctx); err != nil {
			return nil, fmt.Errorf("incidents.clear: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// incidentsListHandler implements `incidents.list` and its alias
// `incidents.get`. Returns the full incident list from the store.
// Mirrors the Python reference `ws_get_incidents` WebSocket command.
//
// Request: {} (no params required).
// Response: { "incidents": [ ... ] }
func incidentsListHandler(l IncidentLister) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		items, err := l.ListIncidents(ctx)
		if err != nil {
			return nil, fmt.Errorf("incidents.list: %w", err)
		}
		if items == nil {
			items = []map[string]any{}
		}
		return map[string]any{"incidents": items}, nil
	}
}

// GroupsQuery is the read facade the `groups.list` command pulls from.
// The cmd-level groups adapter satisfies it (the same adapter that backs
// the REST `GET /api/v1/groups` reader), so the two transports share one
// implementation. An empty `central` aggregates over all centrals.
type GroupsQuery interface {
	List(ctx context.Context, central string) ([]handlers.GroupCentralEntry, error)
}

// groupsListHandler implements `groups.list`. Request:
// { "central": str (optional) }. Response: { "entries": [...] }.
func groupsListHandler(q GroupsQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		entries, err := q.List(ctx, p.Central)
		if err != nil {
			return nil, fmt.Errorf("groups.list: %w", err)
		}
		if entries == nil {
			entries = []handlers.GroupCentralEntry{}
		}
		return map[string]any{"entries": entries}, nil
	}
}

// paramsetCopyHandler implements `paramset.copy`.
// Reads the current paramset values from the source channel and writes
// the (non-empty) value map to the target channel. The paramset_key
// defaults to "MASTER". Typically used to clone configuration from one
// actor channel to another after pairing a replacement device.
//
// Mirrors Python `ws_copy_paramset` (websocket_api.py:916).
//
// Request: {
//
//	"source_channel_address": str,
//	"target_channel_address": str,
//	"paramset_key": "MASTER"|"VALUES" (default "MASTER")
//
// }
// Response: { "copied": int, "source": str, "target": str }
func paramsetCopyHandler(r ParamsetReader, w ParamsetWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SourceChannel string `json:"source_channel_address"`
			TargetChannel string `json:"target_channel_address"`
			ParamsetKey   string `json:"paramset_key"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.SourceChannel == "" || p.TargetChannel == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "source_channel_address and target_channel_address are required")
		}
		if p.ParamsetKey == "" {
			p.ParamsetKey = string(hmenum.ParamsetKeyMaster)
		}
		srcKey := configui.SessionKey{
			ChannelAddress: p.SourceChannel,
			ParamsetKey:    hmenum.ParamsetKey(p.ParamsetKey),
		}
		values, err := r.GetParamset(ctx, srcKey)
		if err != nil {
			return nil, fmt.Errorf("paramset.copy: read source: %w", err)
		}
		if len(values) == 0 {
			return map[string]any{"copied": 0, "source": p.SourceChannel, "target": p.TargetChannel}, nil
		}
		dstKey := configui.SessionKey{
			ChannelAddress: p.TargetChannel,
			ParamsetKey:    hmenum.ParamsetKey(p.ParamsetKey),
		}
		if err := w.PutParamset(ctx, dstKey, values); err != nil {
			return nil, fmt.Errorf("paramset.copy: write target: %w", err)
		}
		return map[string]any{
			"copied": len(values),
			"source": p.SourceChannel,
			"target": p.TargetChannel,
		}, nil
	}
}

// centralLinksAddress decodes the device address from either `device_address`
// or its `address` alias. The two names coexist so SPA and external WS callers
// can use whichever matches their REST-path convention.
func centralLinksAddress(raw json.RawMessage) (string, error) {
	addr, _, err := centralLinksTarget(raw)
	return addr, err
}

// centralLinksTarget decodes the device address (see centralLinksAddress)
// plus the optional `channel` channel address. An empty channel scopes the
// call to the whole device; a non-empty channel scopes it to that single
// channel, mirroring the CCU channel-config dialog.
func centralLinksTarget(raw json.RawMessage) (address, channel string, err error) {
	var p struct {
		DeviceAddress string `json:"device_address"`
		Address       string `json:"address"`
		Channel       string `json:"channel"`
	}
	if decErr := decodeOrEmpty(raw, &p); decErr != nil {
		return "", "", decErr
	}
	addr := p.DeviceAddress
	if addr == "" {
		addr = p.Address
	}
	if addr == "" {
		return "", "", NewCommandError(CommandErrorBadRequest, "device_address is required")
	}
	return addr, p.Channel, nil
}

// centralCreateLinksHandler implements `central.create_links`.
// Enables CCU click-event forwarding. Without `channel` every press-event
// channel of the device is switched on; with `channel` only that single
// channel is touched. Mirrors Python create_central_links.
//
// Request: { "device_address": str (alias "address"), "channel"?: str }.
// Response: { "touched": int, "skipped": int, "failed": int }
func centralCreateLinksHandler(m CentralLinksManager) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		addr, channel, err := centralLinksTarget(raw)
		if err != nil {
			return nil, err
		}
		report, err := m.CreateCentralLinks(ctx, addr, channel)
		if err != nil {
			return nil, fmt.Errorf("central.create_links: %w", err)
		}
		return report, nil
	}
}

// centralRemoveLinksHandler implements `central.remove_links`.
// Tears down CCU click-event forwarding. Without `channel` the whole device
// is switched off; with `channel` only that single channel is touched.
// Mirrors Python remove_central_links.
//
// Request: { "device_address": str (alias "address"), "channel"?: str }.
// Response: { "touched": int, "skipped": int, "failed": int }
func centralRemoveLinksHandler(m CentralLinksManager) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		addr, channel, err := centralLinksTarget(raw)
		if err != nil {
			return nil, err
		}
		report, err := m.RemoveCentralLinks(ctx, addr, channel)
		if err != nil {
			return nil, fmt.Errorf("central.remove_links: %w", err)
		}
		return report, nil
	}
}

// centralLinksStatusHandler implements `central.links_status`.
// Reports whether the device supports central links and how many channels
// are eligible.
//
// Request: { "device_address": str } (alias "address").
// Response: { "supported": bool, "reason"?: str, "eligible_channels"?: int }
func centralLinksStatusHandler(m CentralLinksManager) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		addr, err := centralLinksAddress(raw)
		if err != nil {
			return nil, err
		}
		status, err := m.CentralLinksStatus(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("central.links_status: %w", err)
		}
		return status, nil
	}
}

// recordingStartHandler implements `recording.start`.
// Activates the diagnostic RPC session recorder. Mirrors Python
// record_session (start).
//
// Request: {} (no params required).
// Response: { "recording": bool }
func recordingStartHandler(r SessionRecorder) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		r.Start()
		return map[string]any{"recording": r.IsActive()}, nil
	}
}

// recordingStopHandler implements `recording.stop`.
// Deactivates the diagnostic RPC session recorder. Mirrors Python
// record_session (stop).
//
// Request: {} (no params required).
// Response: { "recording": bool }
func recordingStopHandler(r SessionRecorder) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		r.Stop()
		return map[string]any{"recording": r.IsActive()}, nil
	}
}

// recordingStatusHandler implements `recording.status`.
// Reports whether the recorder is currently capturing.
//
// Request: {} (no params required).
// Response: { "recording": bool }
func recordingStatusHandler(r SessionRecorder) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"recording": r.IsActive()}, nil
	}
}

// decodeOrEmpty unmarshals raw into into, treating an empty or literal-null
// raw as a no-op (into keeps its zero value) rather than a decode error —
// many commands accept an omitted args object as "use defaults". A genuine
// unmarshal failure returns a *CommandError tagged CommandErrorBadRequest so
// Router.Dispatch surfaces it as a client-input error rather than falling
// through to the generic CommandErrorInternal it applies to any other
// error type.
func decodeOrEmpty(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return NewCommandError(CommandErrorBadRequest, "invalid args: "+err.Error())
	}
	return nil
}

// paramsetFormSchemaHandler implements `paramset.form_schema`.
// Mirrors Python `ws_get_form_schema` (websocket_api.py:252).
// Input: {address, channel_no, paramset, locale?, peer?}.
// Output: the FormSchema object (same as REST GET /ui-schema).
func paramsetFormSchemaHandler(q UISchemaQuery) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address   string `json:"address"`
			ChannelNo int    `json:"channel_no"`
			Paramset  string `json:"paramset"`
			Locale    string `json:"locale"`
			Peer      string `json:"peer"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "address is required")
		}
		if p.Paramset == "" {
			p.Paramset = "VALUES"
		}
		if p.Locale == "" {
			p.Locale = "en"
		}
		schema, err := q.FormSchema(ctx, p.Address, p.ChannelNo, p.Paramset, p.Locale, p.Peer)
		if err != nil {
			return nil, fmt.Errorf("paramset.form_schema: %w", err)
		}
		return schema, nil
	}
}
