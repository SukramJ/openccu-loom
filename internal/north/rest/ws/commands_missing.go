// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// commands_missing.go — the 9 previously-unimplemented WebSocket commands.
//
// Commands that cannot yet be wired through the domain register with
// a stub that returns errors.New("ws: feature not yet wired through
// domain").
//
// Wired commands (full implementation):
// - ccu.get_signal_quality — reads RSSI+reachability from DevicesProvider
// - schedules.list_devices — lists devices with HasWeekProfile via DevicesProvider
// - ccu.get_hub_data — reads service/alarm message counts from HubDataProvider
// - system.user_permissions — reads auth.Identity from context via UserPermissionsProvider
//
// Stub commands (domain method missing):
// - links.get_form_schema — needs GetLinkParamsetDescription on ParamsetsDomain
// - links.get_profiles — needs link-profile store
// - links.test_profile — needs link-profile store + put_link_paramset
// - schedules.set_enabled — needs SetScheduleEnabled on SchedulesDomain
// - paramset.determine — needs determine_parameter on InterfaceClient backend

package ws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// ─── interfaces ──────────────────────────────────────────────────────────────

// SignalQualityProvider is the minimal read surface for
// `ccu.get_signal_quality`. Iterates every device and returns RSSI
// + reachability data. Mirrors Python `ws_get_signal_quality`
// (websocket_api.py:2158).
type SignalQualityProvider interface {
	// AllDevices returns every known device across all centrals.
	AllDevices() []*device.Device
}

// ScheduleDevicesProvider is the minimal read surface for
// `schedules.list_devices`. Mirrors Python `ws_list_schedule_devices`
// (websocket_api.py:1491) which calls `list_schedule_devices`.
type ScheduleDevicesProvider interface {
	// AllDevices returns every known device across all centrals.
	AllDevices() []*device.Device
}

// RSSIProvider is the read surface for `ccu.get_rssi_info`. It returns
// per-device RF reception strength (rssi_device = RSSI_DEVICE, rssi_peer
// = RSSI_PEER, both dBm) plus reachability, read from the device model's
// maintenance channel so it works for HmIP and BidCos alike.
type RSSIProvider interface {
	// RSSIInfo returns { "devices": [...] } across every central. Each
	// entry carries address, name, interface_id, central, rssi_device,
	// rssi_peer (null when absent), and reachable.
	RSSIInfo(ctx context.Context) (map[string]any, error)
}

// HubDataProvider is the minimal read surface for `ccu.get_hub_data`.
// Returns service-message count and alarm-message count from the hub.
// Mirrors Python `ws_get_hub_data` (websocket_api.py:2052).
type HubDataProvider interface {
	// HubMessageCounts returns (serviceMessages, alarmMessages).
	// Either field is nil when the data point has never been observed.
	HubMessageCounts() (serviceMessages, alarmMessages *int)
}

// ScheduleEnabler is the mutating surface for `schedules.set_enabled`.
// Mirrors Python `ws_set_schedule_enabled` (websocket_api.py:1698).
// The implementation is a stub until SchedulesDomain exposes
// SetScheduleEnabled.
type ScheduleEnabler interface {
	SetScheduleEnabled(ctx context.Context, deviceAddress string, enabled bool, channelKey string) error
}

// LinkFormSchemaProvider is the read surface for `links.get_form_schema`.
// Mirrors Python `ws_get_link_form_schema` (websocket_api.py:1057).
// Stub until GetLinkParamsetDescription is wired on ParamsetsDomain
type LinkFormSchemaProvider interface {
	GetLinkFormSchema(ctx context.Context, interfaceID, receiverChannelAddr, senderChannelAddr string) (map[string]any, error)
}

// LinkProfilesProvider is the read surface for `links.get_profiles` and
// `links.test_profile`. Mirrors Python `ws_get_link_profiles`
// (websocket_api.py:1123) and `ws_test_link_profile` (websocket_api.py:2643).
// Stub until the link-profile store is implemented.
type LinkProfilesProvider interface {
	GetLinkProfiles(ctx context.Context, receiverChannelType, senderChannelType, locale string) ([]map[string]any, error)
	TestLinkProfile(ctx context.Context, interfaceID, senderAddr, receiverAddr string, profileID int) (map[string]any, error)
}

// ParameterDeterminer is the write/read surface for `paramset.determine`.
// Mirrors Python `ws_determine_parameter` (websocket_api.py:2556) which
// calls `device.client.determine_parameter`.
// Stub until determine_parameter is wired on InterfaceClient.
type ParameterDeterminer interface {
	DetermineParameter(ctx context.Context, interfaceID, channelAddress, parameterID string) (any, error)
}

// UserPermissionsProvider is the read surface for `system.user_permissions`.
// Mirrors Python `ws_get_user_permissions` (websocket_api.py:2727).
// In openccu-loom the auth.Identity is injected into the context by the
// HTTP middleware; no external adapter is needed — the handler reads it
// directly from ctx via auth.IdentityFrom.
type UserPermissionsProvider interface {
	// BackendModel returns the CCU model string, e.g. "CCU3".
	// May return "" when not yet known.
	BackendModel() string
}

// ─── config struct ────────────────────────────────────────────────────────────

// MissingCommandsConfig bundles the optional providers consumed by
// RegisterMissingCommands. Nil fields are silently skipped (command not
// registered).
type MissingCommandsConfig struct {
	// SignalQuality backs `ccu.get_signal_quality` (wired).
	SignalQuality SignalQualityProvider
	// RSSIInfo backs `ccu.get_rssi_info` (wired). Nil leaves the command
	// unregistered.
	RSSIInfo RSSIProvider
	// ScheduleDevices backs `schedules.list_devices` (wired).
	ScheduleDevices ScheduleDevicesProvider
	// HubData backs `ccu.get_hub_data` (wired).
	HubData HubDataProvider
	// UserPermissions backs `system.user_permissions` (wired — reads from ctx).
	UserPermissions UserPermissionsProvider
	// ScheduleEnabler backs `schedules.set_enabled` (stub).
	ScheduleEnabler ScheduleEnabler
	// LinkFormSchema backs `links.get_form_schema` (stub).
	LinkFormSchema LinkFormSchemaProvider
	// LinkProfiles backs `links.get_profiles` and `links.test_profile` (stub).
	LinkProfiles LinkProfilesProvider
	// ParameterDeterminer backs `paramset.determine` (stub).
	ParameterDeterminer ParameterDeterminer
}

// RegisterMissingCommands wires the 9 previously-missing WebSocket
// commands onto router. Call alongside RegisterDefaultCommands,
// RegisterExtendedCommands, and RegisterCustomDPCommands at boot time.
func RegisterMissingCommands(router *Router, cfg MissingCommandsConfig) {
	if router == nil {
		return
	}
	if cfg.SignalQuality != nil {
		// ccu.get_signal_quality — RSSI + reachability per device.
		router.Register("ccu.get_signal_quality", ccuGetSignalQualityHandler(cfg.SignalQuality))
	}
	if cfg.RSSIInfo != nil {
		// ccu.get_rssi_info — per-device RF reception strength.
		router.Register("ccu.get_rssi_info", ccuGetRSSIInfoHandler(cfg.RSSIInfo))
	}
	if cfg.ScheduleDevices != nil {
		// schedules.list_devices — devices that expose a week-profile.
		router.Register("schedules.list_devices", schedulesListDevicesHandler(cfg.ScheduleDevices))
	}
	if cfg.HubData != nil {
		// ccu.get_hub_data — service/alarm message counts.
		router.Register("ccu.get_hub_data", ccuGetHubDataHandler(cfg.HubData))
	}
	// system.user_permissions reads from ctx; provider gives the CCU model.
	// We always register it — without a provider the model field is "".
	router.Register("system.user_permissions", systemUserPermissionsHandler(cfg.UserPermissions))

	if cfg.ScheduleEnabler != nil {
		// schedules.set_enabled — enable/disable the weekly program.
		router.Register("schedules.set_enabled", schedulesSetEnabledHandler(cfg.ScheduleEnabler))
	} else {
		// Stub registration so the command appears in system.commands even
		// before the domain is wired.
		router.Register("schedules.set_enabled", stubHandler("ws: schedules.set_enabled: schedule domain provider not configured in this deployment"))
	}

	if cfg.LinkFormSchema != nil {
		router.Register("links.get_form_schema", linksGetFormSchemaHandler(cfg.LinkFormSchema))
	} else {
		router.Register("links.get_form_schema", stubHandler("ws: links.get_form_schema: link form-schema provider not configured in this deployment"))
	}

	if cfg.LinkProfiles != nil {
		router.Register("links.get_profiles", linksGetProfilesHandler(cfg.LinkProfiles))
		router.Register("links.test_profile", linksTestProfileHandler(cfg.LinkProfiles))
	} else {
		router.Register("links.get_profiles", stubHandler("ws: links.get_profiles: link-profile provider not configured in this deployment"))
		router.Register("links.test_profile", stubHandler("ws: links.test_profile: link-profile provider not configured in this deployment"))
	}

	if cfg.ParameterDeterminer != nil {
		router.Register("paramset.determine", paramsetDetermineHandler(cfg.ParameterDeterminer))
	} else {
		router.Register("paramset.determine", stubHandler("ws: paramset.determine: parameter-determiner provider not configured in this deployment"))
	}
}

// ─── handler implementations ──────────────────────────────────────────────────

// ccuGetSignalQualityHandler implements `ccu.get_signal_quality`.
// Returns RSSI_DEVICE, RSSI_PEER (via AvailabilityInfo), is_reachable,
// low_battery, and signal_strength per device.
// Mirrors Python `ws_get_signal_quality` (websocket_api.py:2158).
//
// Request: {} (no params required).
// Response: { "devices": [ { "address", "name", "model", "interface_id",
//
//	"is_reachable", "rssi_device", "low_battery", "signal_strength" }, … ] }
func ccuGetSignalQualityHandler(p SignalQualityProvider) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		devs := p.AllDevices()
		out := make([]map[string]any, 0, len(devs))
		for _, d := range devs {
			info := d.AvailabilityInfo()
			entry := map[string]any{
				"address":      d.Address,
				"name":         d.Name,
				"model":        d.Model,
				"interface_id": d.InterfaceID,
				"is_reachable": info.IsReachable,
			}
			// Optional nullable fields — nil when the parameter is absent.
			if info.SignalStrength != nil {
				entry["signal_strength"] = *info.SignalStrength
			} else {
				entry["signal_strength"] = nil
			}
			if info.LowBattery != nil {
				entry["low_battery"] = *info.LowBattery
			} else {
				entry["low_battery"] = nil
			}
			out = append(out, entry)
		}
		return map[string]any{"devices": out}, nil
	}
}

// ccuGetRSSIInfoHandler implements `ccu.get_rssi_info`. Returns per-device
// RF reception strength (RSSI_DEVICE / RSSI_PEER, dBm) plus reachability,
// across every central — read from the device model so it works for HmIP
// and BidCos alike.
//
// Request: {} (no params required).
// Response: { "devices": [ { "address", "name", "interface_id",
//
//	"central", "partners": [ { "address", "name", "rssi_device",
//	"rssi_peer" } ] }, … ] }
func ccuGetRSSIInfoHandler(p RSSIProvider) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		return p.RSSIInfo(ctx)
	}
}

// schedulesListDevicesHandler implements `schedules.list_devices`.
// Returns only devices that carry a week-profile data point.
// Mirrors Python `ws_list_schedule_devices` (websocket_api.py:1491).
//
// Request: {} (no params required).
// Response: { "devices": [ { "address", "name", "model", "interface_id" }, … ] }
func schedulesListDevicesHandler(p ScheduleDevicesProvider) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		devs := p.AllDevices()
		out := make([]map[string]any, 0)
		for _, d := range devs {
			if !d.HasWeekProfile() {
				continue
			}
			out = append(out, map[string]any{
				"address":      d.Address,
				"name":         d.Name,
				"model":        d.Model,
				"interface_id": d.InterfaceID,
				"interface":    string(d.Interface),
			})
		}
		return map[string]any{"devices": out}, nil
	}
}

// ccuGetHubDataHandler implements `ccu.get_hub_data`.
// Returns service_messages count and alarm_messages count.
// Mirrors Python `ws_get_hub_data` (websocket_api.py:2052).
//
// Request: {} (no params required).
// Response: { "service_messages": <int|null>, "alarm_messages": <int|null> }
func ccuGetHubDataHandler(p HubDataProvider) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		svc, alarm := p.HubMessageCounts()
		return map[string]any{
			"service_messages": svc,
			"alarm_messages":   alarm,
		}, nil
	}
}

// systemUserPermissionsHandler implements `system.user_permissions`.
// Reads auth.Identity from the request context (populated by the HTTP
// middleware that authenticates the WS upgrade). If no identity is
// present the handler returns viewer-level permissions.
// Mirrors Python `ws_get_user_permissions` (websocket_api.py:2727).
//
// Request: {} (no params required).
// Response: { "is_admin": bool, "role": string, "backend": string|"" }
func systemUserPermissionsHandler(p UserPermissionsProvider) CommandHandler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		id, ok := auth.IdentityFrom(ctx)
		isAdmin := ok && id.Role == auth.RoleAdmin
		role := string(auth.RoleViewer)
		if ok {
			role = string(id.Role)
		}
		backend := ""
		if p != nil {
			backend = p.BackendModel()
		}
		return map[string]any{
			"is_admin": isAdmin,
			"role":     role,
			"backend":  backend,
		}, nil
	}
}

// schedulesSetEnabledHandler implements `schedules.set_enabled`.
// Enables or disables the weekly program on a device.
// Mirrors Python `ws_set_schedule_enabled` (websocket_api.py:1698).
//
// Request: { "device_address": str, "enabled": bool, "channel_key"?: str }
// Response: { "success": true }
func schedulesSetEnabledHandler(e ScheduleEnabler) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			DeviceAddress string `json:"device_address"`
			Enabled       bool   `json:"enabled"`
			ChannelKey    string `json:"channel_key"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.DeviceAddress == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device_address is required")
		}
		if err := e.SetScheduleEnabled(ctx, p.DeviceAddress, p.Enabled, p.ChannelKey); err != nil {
			return nil, fmt.Errorf("schedules.set_enabled: %w", err)
		}
		return map[string]any{"success": true}, nil
	}
}

// linksGetFormSchemaHandler implements `links.get_form_schema`.
// Returns the form schema for a link paramset between sender and receiver.
// Mirrors Python `ws_get_link_form_schema` (websocket_api.py:1057).
//
// Request: { "interface_id": str, "sender_channel_address": str, "receiver_channel_address": str }
// Response: { form schema map }
func linksGetFormSchemaHandler(p LinkFormSchemaProvider) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var req struct {
			InterfaceID     string `json:"interface_id"`
			SenderChannel   string `json:"sender_channel_address"`
			ReceiverChannel string `json:"receiver_channel_address"`
		}
		if err := decodeOrEmpty(raw, &req); err != nil {
			return nil, err
		}
		if req.InterfaceID == "" || req.SenderChannel == "" || req.ReceiverChannel == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "interface_id, sender_channel_address, and receiver_channel_address are required")
		}
		schema, err := p.GetLinkFormSchema(ctx, req.InterfaceID, req.ReceiverChannel, req.SenderChannel)
		if err != nil {
			return nil, fmt.Errorf("links.get_form_schema: %w", err)
		}
		return schema, nil
	}
}

// linksGetProfilesHandler implements `links.get_profiles`.
// Returns available easymode profiles for a link and the active profile id.
// Mirrors Python `ws_get_link_profiles` (websocket_api.py:1123).
//
// Request: { "interface_id": str, "sender_channel_address": str,
//
//	"receiver_channel_address": str, "locale"?: str }
//
// Response: { "profiles": [...] | null, "active_profile_id": int }
func linksGetProfilesHandler(p LinkProfilesProvider) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var req struct {
			InterfaceID     string `json:"interface_id"`
			SenderChannel   string `json:"sender_channel_address"`
			ReceiverChannel string `json:"receiver_channel_address"`
			Locale          string `json:"locale"`
		}
		if err := decodeOrEmpty(raw, &req); err != nil {
			return nil, err
		}
		if req.SenderChannel == "" || req.ReceiverChannel == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "sender_channel_address and receiver_channel_address are required")
		}
		if req.Locale == "" {
			req.Locale = "en"
		}
		profiles, err := p.GetLinkProfiles(ctx, req.ReceiverChannel, req.SenderChannel, req.Locale)
		if err != nil {
			return nil, fmt.Errorf("links.get_profiles: %w", err)
		}
		return map[string]any{
			"profiles":          profiles,
			"active_profile_id": 0,
		}, nil
	}
}

// linksTestProfileHandler implements `links.test_profile`.
// Temporarily applies a link profile's default values to the link paramset.
// Mirrors Python `ws_test_link_profile` (websocket_api.py:2643).
//
// Request: { "interface_id": str, "sender_channel_address": str,
//
//	"receiver_channel_address": str, "profile_id": int }
//
// Response: { "success": true, "applied_values": map }
func linksTestProfileHandler(p LinkProfilesProvider) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var req struct {
			InterfaceID     string `json:"interface_id"`
			SenderChannel   string `json:"sender_channel_address"`
			ReceiverChannel string `json:"receiver_channel_address"`
			ProfileID       int    `json:"profile_id"`
		}
		if err := decodeOrEmpty(raw, &req); err != nil {
			return nil, err
		}
		if req.InterfaceID == "" || req.SenderChannel == "" || req.ReceiverChannel == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "interface_id, sender_channel_address, and receiver_channel_address are required")
		}
		result, err := p.TestLinkProfile(ctx, req.InterfaceID, req.SenderChannel, req.ReceiverChannel, req.ProfileID)
		if err != nil {
			return nil, fmt.Errorf("links.test_profile: %w", err)
		}
		return result, nil
	}
}

// paramsetDetermineHandler implements `paramset.determine`.
// Auto-detects a parameter value from the device via determine_parameter.
// Mirrors Python `ws_determine_parameter` (websocket_api.py:2556).
//
// Request: { "interface_id": str, "channel_address": str, "parameter_id": str }
// Response: { "success": true, "value": any }
func paramsetDetermineHandler(d ParameterDeterminer) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var req struct {
			InterfaceID    string `json:"interface_id"`
			ChannelAddress string `json:"channel_address"`
			ParameterID    string `json:"parameter_id"`
		}
		if err := decodeOrEmpty(raw, &req); err != nil {
			return nil, err
		}
		if req.InterfaceID == "" || req.ChannelAddress == "" || req.ParameterID == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "interface_id, channel_address, and parameter_id are required")
		}
		value, err := d.DetermineParameter(ctx, req.InterfaceID, req.ChannelAddress, req.ParameterID)
		if err != nil {
			return nil, fmt.Errorf("paramset.determine: %w", err)
		}
		return map[string]any{"success": true, "value": value}, nil
	}
}

// stubHandler returns a CommandHandler that always fails with the given
// message. Used to register stub commands so they appear in
// system.commands and return a useful error instead of "unknown_command".
// stubHandler returns a handler that always reports the feature as
// not yet implemented. Distinct from CommandErrorUnknownCommand —
// the command IS registered, the wiring just isn't complete. SPA /
// Walker code can branch on the typed code without string-matching
// the message.
func stubHandler(msg string) CommandHandler {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, NewCommandError(CommandErrorNotImplemented, msg)
	}
}
