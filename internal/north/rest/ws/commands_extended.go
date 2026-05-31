// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DeviceWriter is the mutating surface the daemon exposes for
// device-level operations beyond paramset writes. Wires against the
// central's device coordinator.
//
// Note: `devices.copy` and `devices.export` were removed in favour of
// the REST equivalents `/devices/{addr}/channels/{no}/config/export`
// and the channel-level paramset-copy flow — see
// docs/parity/by_design.md (entry "ws-rest-split").
type DeviceWriter interface {
	// Rename renames the device (CCU JSON-RPC `Device.setName`).
	Rename(ctx context.Context, address, name string) error
	// SetInstallMode toggles a single device into install mode for the
	// given duration. Used by `device.install_mode`.
	SetInstallMode(ctx context.Context, address string, durationSeconds int) error
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

// ExtendedHub adds the niche service-message operation that was
// missing from the base [HubQuery] contract.
type ExtendedHub interface {
	DisableServiceMessage(ctx context.Context, id string) error
}

// ThrottleStats is the read contract for `ccu.throttle_stats`.
// Returns per-interface command-throttle diagnostics (in_flight, waiting,
// burst_downgrades, waited_for_burst_slot). Mirrors Python
// `ws_get_command_throttle_stats` (websocket_api.py:1786).
type ThrottleStats interface {
	CommandThrottleStats(ctx context.Context) ([]map[string]any, error)
}

// CacheClearer is the write contract for `ccu.cache_clear`.
// Clears all in-memory caches on the central so the next read fetches
// fresh data from the CCU. Mirrors Python `ws_clear_cache`
// (websocket_api.py:1885).
type CacheClearer interface {
	ClearAllCaches(ctx context.Context) error
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
	Devices       DeviceWriter
	Paramsets     ParamsetWriter
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
	// IncidentClearer backs `incidents.clear`.
	IncidentClearer IncidentClearer
	// IncidentLister backs `incidents.list` and its alias `incidents.get`.
	IncidentLister IncidentLister
	// UISchema backs `paramset.form_schema`.
	UISchema UISchemaQuery
	// ParamsetReader backs the read half of `paramset.copy`.
	// When nil, paramset.copy is not registered.
	ParamsetReader ParamsetReader
}

// RegisterExtendedCommands wires the post-MVP command set onto router.
// The set complements [RegisterDefaultCommands] — call both at boot
// to expose the full command surface. Any nil sub-config field skips its
// commands.
func RegisterExtendedCommands(router *Router, cfg ExtendedCommandsConfig) {
	if router == nil {
		return
	}
	// --- Schreibpfad zuerst (priorisiert) ---
	if cfg.Devices != nil {
		router.Register("device.rename", deviceRenameHandler(cfg.Devices))
		router.Register("device.install_mode", deviceInstallModeHandler(cfg.Devices))
	}
	if cfg.Paramsets != nil {
		router.Register("paramset.put", paramsetPutHandler(cfg.Paramsets))
	}
	if cfg.MasterProfiles != nil {
		router.Register("master_profiles.list", masterProfilesListHandler(cfg.MasterProfiles))
		router.Register("master_profiles.get", masterProfilesGetHandler(cfg.MasterProfiles))
		router.Register("master_profiles.apply", masterProfilesApplyHandler(cfg.MasterProfiles, cfg.Paramsets))
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
	if cfg.ExtendedHub != nil {
		router.Register("service_messages.disable", serviceMessagesDisableHandler(cfg.ExtendedHub))
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
}

// --- handler implementations -----------------------------------------

func deviceRenameHandler(d DeviceWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Address string `json:"address"`
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
		if err := d.Rename(ctx, p.Address, p.Name); err != nil {
			return nil, fmt.Errorf("device.rename: %w", err)
		}
		return map[string]any{"address": p.Address, "name": p.Name}, nil
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

func paramsetPutHandler(w ParamsetWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Channel  string         `json:"channel_address"`
			Paramset string         `json:"paramset_key"`
			Values   map[string]any `json:"values"`
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
		key := configui.SessionKey{ChannelAddress: p.Channel, ParamsetKey: hmenum.ParamsetKey(p.Paramset)}
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

func masterProfilesApplyHandler(s *masterprofile.Store, w ParamsetWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceType     string `json:"device_type"`
			ChannelType    string `json:"channel_type"`
			ChannelAddress string `json:"channel_address"`
			ID             int    `json:"id"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceType == "" || p.ChannelAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_type and channel_address are required")
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
// Clears all in-memory caches on the central; next read fetches fresh data.
// Mirrors Python `ws_clear_cache` (websocket_api.py:1885).
func ccuCacheClearHandler(c CacheClearer) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := c.ClearAllCaches(ctx); err != nil {
			return nil, fmt.Errorf("ccu.cache_clear: %w", err)
		}
		return map[string]any{"success": true}, nil
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
// Mirrors Python `ws_get_incidents` — closes parity-audit gap L12.
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

func decodeOrEmpty(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("ws: invalid params: %w", err)
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
