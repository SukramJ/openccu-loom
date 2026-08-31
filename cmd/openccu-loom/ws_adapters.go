// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// ws_adapters.go — bridges the REST domain adapters onto the narrower
// WS-specific interfaces declared in internal/north/rest/ws.
//
// Design: each wrapper is minimal — no business logic, just
// method-signature translation and type conversion. Where a WS
// interface method has no direct domain equivalent the wrapper returns
// errors.New("ws: feature not yet wired through domain") and documents
// why. Extensions land in +.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// wsCommandWiring bundles every domain adapter + auxiliary deps the
// wireWSCommands function needs. Constructed once in daemonServe so
// the WS hub and the REST router share the same adapter instances.
type wsCommandWiring struct {
	health           *adapter.HealthAdapter
	devices          *adapter.DevicesAdapter
	hub              *adapter.HubAdapter
	linksDomain      *adapter.LinksDomain
	schedulesDomain  *adapter.SchedulesDomain
	centralLinks     *adapter.CentralLinksDomain
	groupsDomain     *adapter.GroupsDomain
	definitionExport *adapter.DefinitionExportDomain
	deviceAdmin      *adapter.DeviceAdminDomain
	paramsets        *adapter.ParamsetsDomain
	customDP         *adapter.CustomDPDispatcher
	linkProfiles     *linkprofile.Store
	valueWriter      *clientpkg.ValueWriter
	registry         *central.Registry
	// deviceReloader backs config.reload_device_config and
	// ccu.reload_device_config. The same adapter also backs
	// config.reload_channel_config / ccu.reload_channel_config.
	deviceReloader *adapter.DeviceReloaderAdapter
	// backups backs backups.trigger — *adapter.BackupAdapter satisfies
	// ws.BackupsService directly via TriggerBackupForCentral.
	backups *adapter.BackupAdapter
	// rpcRecorder is the shared RPC session recorder the REST
	// `/diagnostics/rpc-recording` route also uses. Backing recording.* with
	// it (rather than a separate registry walk) makes a WS-started recording
	// arm the same auto-stop timer and marker. Nil on minimal boots — the WS
	// wiring then builds a registry-backed fallback.
	rpcRecorder interfaces.RPCRecorderService
	// auditRec records the recording.start / recording.stop lifecycle so WS
	// and REST leave one audit trail. Nil skips the row.
	auditRec audit.Recorder
	// cacheResetSvc backs ccu.cache_clear — scope-aware cache clear + re-pull.
	cacheResetSvc *cachereset.Service
	// alarm backs the alarm_panel.* command family. Nil when the alarm
	// service is disabled — the family is then left unregistered.
	alarm *alarm.Service
	// editSessions is the shared edit-lock registry (also wired into the
	// REST router). Backs the strict MASTER/LINK enforcement on
	// `paramset.put` so REST and WS share one lock namespace.
	editSessions *handlers.EditSessions
	// addonUpdater backs addon_update.check/.install — nil when the
	// platform has no self-update capability (commands then stay
	// unregistered).
	addonUpdater ws.AddonUpdater
	logger       *slog.Logger
	// centralName scopes every WS-command log record. Empty in multi-
	// central setups; populated from [singleCentralName] in daemon.go.
	centralName string
	// sessionStore backs config.session.* commands. When non-nil the
	// session-open / save / discard / changes commands are registered.
	sessionStore *configui.SessionStore
	// changeLog receives one entry per successful config.session.save.
	// When nil the save path proceeds without recording.
	changeLog *audit.ChangeLog
	// labels resolves locale-aware parameter labels for the
	// translated_name field of calc_dp.* responses.
	labels handlers.ParameterLabeler
}

// wireWSCommands registers every WS command set onto hub.
//
// Adapters that cannot yet be bridged (ChangeHistory, ThrottleStats,
// DeviceStatistics, IncidentClearer) are left nil — the corresponding command
// families are simply not registered, which is safe: the client receives
// "unknown_command" rather than a panic.
func wireWSCommands(wsHub *ws.Hub, w wsCommandWiring) {
	router := wsHub.Router()
	// Install the cross-cutting boundary so every WS command emits the
	// same logging shape as REST requests (audit O13). The central
	// name comes from the wiring struct so multi-central deployments
	// stay unambiguous in log aggregation.
	router.SetBoundary(w.logger, w.centralName)

	schedQueryAdapter := adapter.NewScheduleQueryAdapter(w.schedulesDomain)

	deviceQuery := &wsDeviceQuery{devs: w.devices, paramsets: w.paramsets, registry: w.registry, writer: w.valueWriter}
	ws.RegisterDefaultCommands(router, ws.DefaultCommandsConfig{
		Health:           w.health, // *adapter.HealthAdapter directly satisfies ws.HealthSnapshotProvider
		Devices:          deviceQuery,
		DefinitionExport: w.definitionExport,
		Hub:              &wsHubQuery{hub: w.hub, registry: w.registry, deviceAdmin: w.deviceAdmin},
		Links:            &wsLinkQuery{domain: w.linksDomain, registry: w.registry, paramsets: w.paramsets},
		// ScheduleQueryAdapter already satisfies ws.ScheduleQuery — no wrapper needed.
		Schedules: schedQueryAdapter,
		// Sessions: wired via configui.SessionStore stored in wsCommandWiring.
		// SessionBackend: wsSessionBackend delegates Open to the device-query path
		// and PutParamset to the paramsets domain.
		Sessions:       w.sessionStore,
		SessionBackend: &wsSessionBackend{deviceQuery: deviceQuery, paramsets: &wsParamsetWriter{domain: w.paramsets}},
		// ChangeLog receives one entry per successful config.session.save.
		ChangeLog: w.changeLog,
		// DeviceReloader backs config.reload_device_config and
		// ccu.reload_device_config — re-pulls device descriptions from the
		// CCU and recreates missing channels/DPs.
		DeviceReloader: w.deviceReloader,
		// ChannelReloader backs config.reload_channel_config and
		// ccu.reload_channel_config — re-pulls a single channel's paramset
		// descriptions + MASTER values and refreshes its data points.
		ChannelReloader: w.deviceReloader,
		// Backups backs backups.trigger — the create-and-download
		// counterpart to the Rega-script-based backup.trigger.
		Backups: w.backups,
	})

	ws.RegisterExtendedCommands(router, ws.ExtendedCommandsConfig{
		Devices:   &wsDeviceWriter{admin: w.deviceAdmin},
		Paramsets: &wsParamsetWriter{domain: w.paramsets},
		// EditLocks: shared registry — MASTER/LINK paramset.put writes
		// must hold the edit lock, mirroring the REST strict gate.
		EditLocks: w.editSessions,
		// CacheClearer: wired — delegates to the cachereset.Service (ADR 0042).
		CacheClearer: wsCacheClearerFrom(w.cacheResetSvc),
		AddonUpdater: w.addonUpdater,
		// CentralLinks: wired — *adapter.CentralLinksDomain satisfies
		// ws.CentralLinksManager directly. Backs central.create_links /
		// central.remove_links / central.links_status.
		CentralLinks: w.centralLinks,
		// Groups: wired — read-only heating-group listing. The same
		// groupsAdapter backs the REST GET /api/v1/groups reader.
		Groups: newGroupsAdapter(w.groupsDomain),
		// GroupsAdmin: wired — heating-group create/edit/delete + type /
		// suitable-member helpers (GR02). Same adapter as the REST writer.
		GroupsAdmin: newGroupsAdapter(w.groupsDomain),
		// SessionRecorder: wired — routes recording.start/stop/status through
		// the same shared RPC recorder domain method the REST route uses, so a
		// WS start arms the auto-stop timer and persists the marker.
		SessionRecorder: wsSessionRecorderFrom(w.registry, w.rpcRecorder),
		// RecordingAudit: wired — records the recording lifecycle so WS and
		// REST leave one audit trail.
		RecordingAudit: w.auditRec,
		// FirmwareRefresher: wired — re-pulls device descriptions (incl.
		// firmware versions) across every central + interface. Backs
		// firmware.refresh (mirrors the Python ws_refresh_firmware_data).
		FirmwareRefresher: adapter.NewFirmwareDomain(w.registry, w.valueWriter),
		// ChangeHistory, ThrottleStats, DeviceStatistics, IncidentClearer,
		// ChangeHistoryClearer, ExtendedHub, Central, ParamsetReader: all nil —
		// see notes/parity/by_design.md "ws-rest-split". The in-tree Svelte SPA
		// uses REST + WS event-stream; these families remain dormant until an
		// external WS bridge wires them.
	})

	ws.RegisterCustomDPCommands(router, ws.CustomDPCommandsConfig{
		Index:   w.devices, // *adapter.DevicesAdapter satisfies ws.CustomDPIndex
		Invoker: w.customDP,
		Labels:  w.labels,
	})

	// RegisterMissingCommands wires all 9 previously-missing WS commands.
	// The 5 that were stubs (L01-L05) are now fully wired via domain adapters.
	allDevices := &wsAllDevices{devs: w.devices}
	ws.RegisterMissingCommands(router, ws.MissingCommandsConfig{
		// ccu.get_signal_quality — RSSI + reachability per device.
		SignalQuality: allDevices,
		// ccu.get_rssi_info — per-device RF reception strength across every
		// central. Same domain backs the GET /diagnostics/rssi REST endpoint.
		RSSIInfo: adapter.NewRSSIInfoDomain(w.registry),
		// schedules.list_devices — devices that expose a week-profile.
		ScheduleDevices: allDevices,
		// ccu.get_hub_data — service/alarm message counts.
		HubData: &wsHubMessageCounts{hub: w.hub},
		// system.user_permissions — reads from ctx; no extra provider needed.
		UserPermissions: nil, // always registered via nil-safe handler

		// L05: schedules.set_enabled — SchedulesDomain now has SetScheduleEnabled.
		ScheduleEnabler: w.schedulesDomain,

		// L01: links.get_form_schema — ParamsetsDomain now has GetLinkFormSchema.
		LinkFormSchema: w.paramsets,

		// L02 + L03: links.get_profiles + links.test_profile —
		// LinkProfilesAdapter wraps linkprofile.Store.
		LinkProfiles: adapter.NewLinkProfilesAdapter(w.registry, w.linkProfiles, w.paramsets),

		// L04: paramset.determine — ParameterDeterminerAdapter resolves via registry.
		ParameterDeterminer: adapter.NewParameterDeterminerAdapter(w.registry, w.valueWriter),
	})

	// alarm_panel.* — the daemon-level alarm engine + journal. Registered
	// only when the alarm service is present (nil-safe): *alarm.Service
	// satisfies ws.AlarmPanelQuery via its Engine()/Stores() accessors.
	// Codes is left nil until the argon2id code facade is wired (§11); the
	// codes_* commands then serve "unavailable" rather than panicking.
	if w.alarm != nil {
		ws.RegisterAlarmPanelCommands(router, ws.AlarmPanelCommandsConfig{
			Panel: w.alarm,
			Codes: wsAlarmCodeAdminFrom(w.alarm),
		})
	}
}

// wsAlarmCodeAdminFrom yields the codes_* WS command facade — the same
// store-backed adapter the REST surface drives (notes/concepts/alarm-concept.md
// §11). A nil service or store yields a genuinely nil interface so the
// codes_* commands answer "unavailable" instead of panicking.
func wsAlarmCodeAdminFrom(s *alarm.Service) ws.AlarmCodeAdmin {
	if s == nil || s.Stores() == nil || s.Stores().Codes == nil {
		return nil
	}
	return handlers.NewAlarmCodeStoreAdmin(s.Stores().Codes).OnChange(s.NotifyCodesChanged)
}

// ── wsAllDevices ─────────────────────────────────────────────────────────────

// wsAllDevices adapts *adapter.DevicesAdapter (which exposes Devices())
// to ws.SignalQualityProvider and ws.ScheduleDevicesProvider (which
// require AllDevices()). The rename is purely a naming delta.
type wsAllDevices struct {
	devs *adapter.DevicesAdapter
}

func (w *wsAllDevices) AllDevices() []*device.Device {
	if w.devs == nil {
		return nil
	}
	return w.devs.Devices()
}

// ── wsHubMessageCounts ───────────────────────────────────────────────────────

// wsHubMessageCounts adapts *adapter.HubAdapter onto ws.HubDataProvider.
// HubAdapter exposes the per-central hubs; this wrapper sums their message
// counts without importing hub directly.
//
// The command carries no central parameter, so its answer is the fleet's:
// summing every registered central is the only reading that stays true with
// more than one CCU. Reporting the first central's counts instead hid every
// other CCU's service messages from the command, permanently and silently.
type wsHubMessageCounts struct {
	hub *adapter.HubAdapter
}

func (w *wsHubMessageCounts) HubMessageCounts() (serviceMessages, alarmMessages *int) {
	if w.hub == nil {
		return nil, nil
	}
	hubs := w.hub.Hubs()
	if len(hubs) == 0 {
		return nil, nil
	}
	var svc, alarmCount int
	for _, nh := range hubs {
		if nh.Hub == nil {
			continue
		}
		svc += nh.Hub.ServiceMessages.Count()
		alarmCount += nh.Hub.Messages.Count()
	}
	return &svc, &alarmCount
}

// ── wsLinkQuery ─────────────────────────────────────────────────────────────

// wsLinkQuery bridges *adapter.LinksDomain → ws.LinkQuery.
//
// Signature deltas:
// - ListLinks: domain takes (ctx, deviceAddress, locale); WS takes
// (ctx, deviceAddress) — use "" locale (falls back to raw CCU string).
// - LinkableChannels: domain takes (ctx, interfaceID, sourceChannelAddr,
// role, locale); WS takes (ctx, deviceAddress). We resolve the device's
// interface ID from the device address and forward all channels from
// that interface with an empty role (match-all in the MVP).
type wsLinkQuery struct {
	domain   *adapter.LinksDomain
	registry *central.Registry
	// paramsets carries the LINK paramset read and write. LinksDomain
	// owns the link topology — listing, adding and removing links — but
	// the paramset behind a link is a paramset like any other, and
	// ParamsetsDomain is the one path that applies the visibility gate,
	// coerces against the descriptor and records the changed values in
	// the audit entry. WS used to reach the backend through LinksDomain
	// and skipped all three.
	paramsets *adapter.ParamsetsDomain
}

func (w *wsLinkQuery) ListLinks(ctx context.Context, deviceAddress string) ([]map[string]any, error) {
	if w.domain == nil {
		return nil, errors.New("ws: links domain not wired")
	}
	links, err := w.domain.ListLinks(ctx, deviceAddress, "")
	if err != nil {
		return nil, err
	}
	return structSliceToMapSlice(links)
}

func (w *wsLinkQuery) ListAllLinks(ctx context.Context, centralName string) ([]map[string]any, error) {
	if w.domain == nil {
		return nil, errors.New("ws: links domain not wired")
	}
	links, err := w.domain.ListAllLinks(ctx, centralName, "")
	if err != nil {
		return nil, err
	}
	return structSliceToMapSlice(links)
}

func (w *wsLinkQuery) AddLink(ctx context.Context, sender, receiver, name, description string) error {
	if w.domain == nil {
		return errors.New("ws: links domain not wired")
	}
	return w.domain.AddLink(ctx, sender, receiver, name, description)
}

func (w *wsLinkQuery) ActivateLinkParamset(ctx context.Context, receiverChannelAddress, senderChannelAddress string, longPress bool) error {
	if w.domain == nil {
		return errors.New("ws: links domain not wired")
	}
	return w.domain.ActivateLink(ctx, receiverChannelAddress, senderChannelAddress, longPress)
}

func (w *wsLinkQuery) SetLinkInfo(ctx context.Context, sender, receiver, name, description string) error {
	if w.domain == nil {
		return errors.New("ws: links domain not wired")
	}
	return w.domain.SetLinkInfo(ctx, sender, receiver, name, description)
}

func (w *wsLinkQuery) RemoveLink(ctx context.Context, sender, receiver string) error {
	if w.domain == nil {
		return errors.New("ws: links domain not wired")
	}
	return w.domain.RemoveLink(ctx, sender, receiver)
}

// LinkableChannels resolves the device's interfaceID via the registry
// and delegates to the domain with empty role ("" = match-all in MVP)
// and empty locale. The WS surface only provides deviceAddress, so the
// device address itself is the source-channel placeholder — every
// channel of every device on the same interface (except the source
// device's own channels) becomes a candidate.
func (w *wsLinkQuery) LinkableChannels(ctx context.Context, deviceAddress string) ([]map[string]any, error) {
	if w.domain == nil {
		return nil, errors.New("ws: links domain not wired")
	}
	if w.registry == nil {
		return nil, errors.New("ws: registry not wired")
	}
	for _, u := range w.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		channels, err := w.domain.LinkableChannels(ctx, dev.InterfaceID, deviceAddress, "", "")
		if err != nil {
			return nil, err
		}
		return structSliceToMapSlice(channels)
	}
	return nil, fmt.Errorf("ws: device not found: %s", deviceAddress)
}

// GetLinkParamset bridges to ParamsetsDomain, the single LINK paramset path.
func (w *wsLinkQuery) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	if w.paramsets == nil {
		return nil, errors.New("ws: paramsets domain not wired")
	}
	return w.paramsets.GetLinkParamset(ctx, channelAddress, peerAddress)
}

// PutLinkParamset bridges to ParamsetsDomain, the single LINK paramset path.
func (w *wsLinkQuery) PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error {
	if w.paramsets == nil {
		return errors.New("ws: paramsets domain not wired")
	}
	return w.paramsets.PutLinkParamset(ctx, channelAddress, peerAddress, values)
}

// ── wsHubQuery ──────────────────────────────────────────────────────────────

// wsHubQuery bridges *adapter.HubAdapter onto ws.HubQuery and, once
// bound to a central via CentralHub, onto ws.CentralHub. All per-central
// methods delegate to that central's hub.Hub model directly.
//
// InstallMode methods read the per-interface InstallMode trackers via
// hub.Hub.InstallModeDPs() / InstallModeDP(interfaceID). Each tracker
// is registered by the Unit boot sequence on the ServiceRegistry.
type wsHubQuery struct {
	hub      *adapter.HubAdapter
	registry *central.Registry
	// deviceAdmin drives the inbox.accept follow-up orchestration
	// (accept + optional rename/rooms/functions) through the same
	// multi-CCU-safe path the REST accept endpoint uses. Left nil in
	// minimal wirings, in which case AcceptInboxDevice falls back to a
	// plain accept via the resolved central's hub.
	deviceAdmin *adapter.DeviceAdminDomain
	// model is the central this query is bound to. Nil on the unbound
	// entry point (whose only methods are the cross-central ones) and on
	// a bound query whose central has no hub model yet.
	model *hub.Hub
}

// CentralHub binds the per-central hub surface to centralName, so that a
// command carries its target CCU instead of the daemon guessing one.
func (w *wsHubQuery) CentralHub(centralName string) (ws.CentralHub, error) {
	h, err := w.resolveHub(centralName)
	if err != nil {
		return nil, err
	}
	bound := *w
	bound.model = h
	return &bound, nil
}

// resolveHub maps a (possibly empty) central name onto one hub model.
//
// An empty name is the single-central convenience case. With several
// centrals it is [ws.ErrCentralRequired]: picking one would run a sysvar
// write, a program execute or a pairing window against a CCU the caller
// never named, and answer success.
func (w *wsHubQuery) resolveHub(centralName string) (*hub.Hub, error) {
	if w.hub == nil {
		return nil, nil
	}
	if centralName != "" {
		h := w.hub.HubFor(centralName)
		if h == nil {
			return nil, fmt.Errorf("%w: %s", ws.ErrCentralUnknown, centralName)
		}
		return h, nil
	}
	hubs := w.hub.Hubs()
	switch {
	case len(hubs) > 1:
		return nil, ws.ErrCentralRequired
	case len(hubs) == 1:
		return hubs[0].Hub, nil
	default:
		// No hub model exists yet (boot, or a central whose model has not
		// loaded). The per-method nil guards answer with the same empty
		// results as before rather than turning a starting daemon into a
		// client-side error.
		return nil, nil
	}
}

func (w *wsHubQuery) ListPrograms(_ context.Context, includeInternal *bool) ([]map[string]any, error) {
	h := w.model
	if h == nil {
		return []map[string]any{}, nil
	}
	// An explicit include_internal wins; absent, the central's configured
	// include_internal_programs default applies. The hub always holds
	// internal (Tmp_*) programs, so this only steers what is delivered.
	include := includeInternal != nil && *includeInternal
	if includeInternal == nil {
		include = h.IncludeInternalProgramsDefault()
	}
	progs := h.Programs()
	out := make([]map[string]any, 0, len(progs))
	for _, p := range progs {
		// Name and the internal flag are refreshed in place by every hub
		// scan (Program.UpdateMetadata), so both are read through the
		// accessors that take the program's own lock — reading the fields
		// races the refresh, and a string header read mid-replacement is
		// not a stale name but a corrupt one.
		internal := p.Internal()
		if internal && !include {
			continue
		}
		active, observed := p.Active()
		e := map[string]any{
			"id":          p.ID,
			"name":        p.LegacyName(),
			"description": p.Description,
			"is_internal": internal,
		}
		if observed {
			e["active"] = active
		}
		if ts, ok := p.LastExecution(); ok {
			e["last_executed"] = ts.UTC().Format(time.RFC3339)
		}
		// Device association (name match). Present only when the program
		// belongs to a device — clients without the fields fall back to the
		// hub card. Mirrors the REST ProgramSummary shape.
		if ch := p.Channel(); ch != "" {
			e["channel"] = ch
			e["device_address"] = p.DeviceAddress()
		}
		out = append(out, e)
	}
	return out, nil
}

func (w *wsHubQuery) ExecuteProgram(ctx context.Context, id string, checkConditions bool) (bool, error) {
	h := w.model
	if h == nil {
		return false, errors.New("ws: hub not available")
	}
	p, ok := h.Program(id)
	if !ok {
		return false, fmt.Errorf("ws: program not found: %s", id)
	}
	// Stamp the surface so the program-execute audit/log subscriber can
	// attribute the run to the WebSocket API.
	ctx = reqctx.WithOperation(ctx, "ws:program-execute")
	if checkConditions {
		return p.ExecuteWithConditionCheck(ctx)
	}
	if err := p.Execute(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (w *wsHubQuery) DeleteProgram(ctx context.Context, id string) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.DeleteProgramRemote(ctx, id)
}

func (w *wsHubQuery) ListSysvars(_ context.Context) ([]map[string]any, error) {
	h := w.model
	if h == nil {
		return []map[string]any{}, nil
	}
	sysvars := h.Sysvars()
	out := make([]map[string]any, 0, len(sysvars))
	for _, s := range sysvars {
		// One guarded snapshot of the mutable descriptor: the 30 s hub refresh
		// rewrites these ten fields in place through Sysvar.ApplyMeta under the
		// sysvar's own lock while this handler serves the list on another
		// goroutine. Reading them straight off the struct (as this path used to)
		// is a data race with that rewrite — REST and the MQTT publisher already
		// read the same Meta() snapshot.
		m := s.Meta()
		e := map[string]any{
			// Renaming a system variable on the CCU rewrites the name in
			// place on the live entry (Hub.RenameSysvar), so it is read
			// through the data point's own lock.
			"name":        s.LegacyName(),
			"description": m.Description,
			"unit":        m.Unit,
			"value_type":  string(m.ValueType),
			"value_list":  m.ValueList,
			"is_visible":  m.IsVisible,
			"is_logged":   m.IsLogged,
		}
		// Binary value labels are present only for LOGIC/ALARM variables;
		// mirror the REST SysvarSummary by omitting them when empty.
		if m.ValueName0 != "" {
			e["value_name_0"] = m.ValueName0
		}
		if m.ValueName1 != "" {
			e["value_name_1"] = m.ValueName1
		}
		if v, ok := s.Value(); ok {
			e["value"] = v.Unwrap()
			e["observed"] = true
		} else {
			e["observed"] = false
		}
		// Read the bound type-aware: an INTEGER sysvar carries it in
		// ParamValue.Int, a FLOAT in .Float — reading .Float raw reports 0/0
		// for every INTEGER variable (the REST/MQTT planes convert correctly).
		if mn := hub.SysvarBoundAsFloat(m.Min); mn != nil {
			e["min"] = *mn
		}
		if mx := hub.SysvarBoundAsFloat(m.Max); mx != nil {
			e["max"] = *mx
		}
		// Device association (explicit CCU channel assignment or name
		// match). Present only when the sysvar belongs to a device —
		// clients without the fields fall back to the hub card. Mirrors
		// the REST SysvarSummary shape.
		if ch := s.Channel(); ch != "" {
			e["channel"] = ch
			e["device_address"] = s.DeviceAddress()
		}
		out = append(out, e)
	}
	return out, nil
}

func (w *wsHubQuery) SetSysvar(ctx context.Context, name string, value any) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	s, ok := h.Sysvar(name)
	if !ok {
		return fmt.Errorf("ws: sysvar not found: %s", name)
	}
	pv, err := hmtypes.NewParamValue(value)
	if err != nil {
		return fmt.Errorf("ws: set_sysvar value: %w", err)
	}
	return s.Set(ctx, pv)
}

// FetchSystemVariables force re-pulls the sysvar catalogue from the CCU
// via the SysvarFetchAdapter (which delegates to each central's
// HubCoordinator.RefreshSysvars). An empty centralName refreshes all.
func (w *wsHubQuery) FetchSystemVariables(ctx context.Context, centralName string) error {
	if w.registry == nil {
		return errors.New("ws: registry not wired")
	}
	return adapter.NewSysvarFetchAdapter(w.registry).FetchSystemVariables(ctx, centralName)
}

// SysvarUsagePrograms lists the CCU programs referencing a sysvar,
// resolving the target hub by name (or the single-central convenience
// case) and enriching each program from the hub's program registry.
func (w *wsHubQuery) SysvarUsagePrograms(ctx context.Context, centralName, name string) ([]map[string]any, error) {
	if name == "" {
		return nil, errors.New("ws: name required")
	}
	h := w.hub.HubFor(centralName)
	if h == nil && centralName == "" {
		if hubs := w.hub.Hubs(); len(hubs) == 1 {
			h = hubs[0].Hub
		} else if len(hubs) > 1 {
			return nil, errors.New("ws: central_name required (multiple CCUs)")
		}
	}
	if h == nil {
		return nil, errors.New("ws: hub not found")
	}
	usage, err := h.SysvarUsageRemote(ctx, name)
	if err != nil {
		return nil, err
	}
	serial := w.hub.SerialSuffix(h.CentralName)
	out := make([]map[string]any, 0, len(usage))
	for _, u := range usage {
		e := map[string]any{"id": u.ID, "name": u.Name, "active": u.Active}
		if p, ok := h.Program(u.ID); ok {
			// Both fields are rewritten in place by the hub scan; read them
			// through the locked accessors, as the REST plane does.
			if n := p.LegacyName(); n != "" {
				e["name"] = n
			}
			e["unique_id"] = p.CanonicalUniqueID(serial)
			e["is_internal"] = p.Internal()
			if a, observed := p.Active(); observed {
				e["active"] = a
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// ListAlarmMessages returns the active alarm set. An alarm entry has no
// device, channel or room — the CCU backs it by an alarm system variable,
// not a device datapoint — so the map carries only identity and timing
// fields. See [hub.AlarmMessage]. A zero Timestamp / LastTimestamp is
// omitted from the entry rather than serialised as the Go zero time.
func (w *wsHubQuery) ListAlarmMessages(_ context.Context) ([]map[string]any, error) {
	h := w.model
	if h == nil {
		return []map[string]any{}, nil
	}
	msgs := h.Messages.List()
	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		m := &msgs[i]
		entry := map[string]any{
			"id":          m.ID,
			"name":        m.Name,
			"description": m.Description,
			"counter":     m.Counter,
		}
		if !m.Timestamp.IsZero() {
			entry["timestamp"] = m.Timestamp
		}
		if !m.LastTimestamp.IsZero() {
			entry["last_timestamp"] = m.LastTimestamp
		}
		out = append(out, entry)
	}
	return out, nil
}

func (w *wsHubQuery) AcknowledgeAlarmMessage(ctx context.Context, id string) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.Messages.Acknowledge(ctx, id)
}

func (w *wsHubQuery) AcknowledgeAllAlarmMessages(ctx context.Context) (int, error) {
	h := w.model
	if h == nil {
		return 0, errors.New("ws: hub not available")
	}
	return h.Messages.AcknowledgeAll(ctx)
}

// ListServiceMessages returns the active service-message set. A zero
// Timestamp / LastTimestamp is omitted from the entry rather than
// serialised as the Go zero time, mirroring [wsHubQuery.ListAlarmMessages].
func (w *wsHubQuery) ListServiceMessages(_ context.Context) ([]map[string]any, error) {
	h := w.model
	if h == nil {
		return []map[string]any{}, nil
	}
	msgs := h.ServiceMessages.List()
	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		m := &msgs[i]
		entry := map[string]any{
			"id":          m.ID,
			"name":        m.Name,
			"address":     m.Address,
			"device_name": m.DeviceName,
			"type":        m.Type.String(),
			"counter":     m.Counter,
			"quittable":   m.Quittable,
		}
		if len(m.Rooms) > 0 {
			entry["rooms"] = m.Rooms
		}
		if len(m.Functions) > 0 {
			entry["functions"] = m.Functions
		}
		if !m.Timestamp.IsZero() {
			entry["timestamp"] = m.Timestamp
		}
		if !m.LastTimestamp.IsZero() {
			entry["last_timestamp"] = m.LastTimestamp
		}
		out = append(out, entry)
	}
	return out, nil
}

func (w *wsHubQuery) AcknowledgeServiceMessage(ctx context.Context, id string) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.ServiceMessages.Acknowledge(ctx, id)
}

func (w *wsHubQuery) AcknowledgeAllServiceMessages(ctx context.Context) (int, error) {
	h := w.model
	if h == nil {
		return 0, errors.New("ws: hub not available")
	}
	return h.ServiceMessages.AcknowledgeAll(ctx)
}

// InstallModeStatus returns the per-interface install-mode state by
// iterating the InstallMode trackers registered on the hub. The result
// is keyed by interfaceID; each entry carries enabled/remaining_seconds/
// observed.
func (w *wsHubQuery) InstallModeStatus(_ context.Context) (map[string]any, error) {
	h := w.model
	if h == nil {
		return nil, errors.New("ws: hub not available")
	}
	dps := h.InstallModeDPs()
	out := make(map[string]any, len(dps))
	for _, m := range dps {
		enabled, remaining, observed := m.InstallState()
		out[m.InterfaceID] = map[string]any{
			"enabled":           enabled,
			"remaining_seconds": int(remaining.Seconds()),
			"observed":          observed,
		}
	}
	return out, nil
}

// EnableInstallMode opens the pairing window for interfaceID for the
// given duration. InstallMode.Enable validates duration > 0.
func (w *wsHubQuery) EnableInstallMode(ctx context.Context, interfaceID string, durationSecs int) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	m, ok := h.InstallModeDP(interfaceID)
	if !ok {
		return fmt.Errorf("ws: install mode for interface %q not registered", interfaceID)
	}
	return m.Enable(ctx, time.Duration(durationSecs)*time.Second)
}

// EnableInstallModeLocal opens the keyserver-less HmIP LOCAL pairing
// window (SGTIN + device-key whitelist) via InstallMode.EnableLocal,
// which normalises both inputs and refuses to fall back to broadcast.
func (w *wsHubQuery) EnableInstallModeLocal(ctx context.Context, interfaceID string, durationSecs int, sgtin, key string) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	m, ok := h.InstallModeDP(interfaceID)
	if !ok {
		return fmt.Errorf("ws: install mode for interface %q not registered", interfaceID)
	}
	return m.EnableLocal(ctx, time.Duration(durationSecs)*time.Second, sgtin, key)
}

// DisableInstallMode closes the pairing window for interfaceID.
func (w *wsHubQuery) DisableInstallMode(ctx context.Context, interfaceID string) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	m, ok := h.InstallModeDP(interfaceID)
	if !ok {
		return fmt.Errorf("ws: install mode for interface %q not registered", interfaceID)
	}
	return m.Disable(ctx)
}

// SearchWiredDevices triggers a wired-bus scan via the device-admin
// registry scan (multi-CCU safe), not the single-hub path.
func (w *wsHubQuery) SearchWiredDevices(ctx context.Context, interfaceID, centralName string) (int, error) {
	if w.deviceAdmin == nil {
		return 0, errors.New("ws: device admin not wired")
	}
	return w.deviceAdmin.SearchWiredDevices(ctx, centralName, interfaceID)
}

func (w *wsHubQuery) TriggerBackup(ctx context.Context) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.TriggerBackupRemote(ctx)
}

func (w *wsHubQuery) BackupStatus(ctx context.Context) (map[string]any, error) {
	h := w.model
	if h == nil {
		return nil, errors.New("ws: hub not available")
	}
	status, err := h.BackupStatusRemote(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": status}, nil
}

// FirmwareInfo returns the latest CCU firmware-update snapshot from the
// hub's Update entity. Reports `observed: false` when no info has been
// recorded yet (first OnInfo callback hasn't fired).
func (w *wsHubQuery) FirmwareInfo(_ context.Context) (map[string]any, error) {
	h := w.model
	if h == nil || h.Update == nil {
		return map[string]any{"observed": false}, nil
	}
	info, observed := h.Update.UpdateInfo()
	if !observed {
		return map[string]any{"observed": false, "in_progress": h.Update.InProgress()}, nil
	}
	return map[string]any{
		"observed":               true,
		"in_progress":            h.Update.InProgress(),
		"current_firmware":       info.CurrentFirmware,
		"available_firmware":     info.AvailableFirmware,
		"update_available":       info.UpdateAvailable,
		"check_script_available": info.CheckScriptAvailable,
	}, nil
}

func (w *wsHubQuery) TriggerFirmwareUpdate(ctx context.Context) error {
	h := w.model
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.TriggerFirmwareUpdateRemote(ctx)
}

func (w *wsHubQuery) InboxDevices(_ context.Context) ([]map[string]any, error) {
	h := w.model
	if h == nil || h.Inbox == nil {
		return []map[string]any{}, nil
	}
	entries := h.Inbox.List()
	out := make([]map[string]any, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		out = append(out, map[string]any{
			"address":      e.Address,
			"model":        e.Model,
			"serial":       e.Serial,
			"manufacturer": e.Manufacturer,
			"first_seen":   e.FirstSeen,
			// pending_creation marks a device this daemon parked because
			// delay_new_device_creation is on; accepting it materialises it.
			"pending_creation": e.PendingCreation,
		})
	}
	return out, nil
}

func (w *wsHubQuery) AcceptInboxDevice(
	ctx context.Context, deviceAddress string, opts ws.InboxAcceptOptions,
) error {
	// Preferred path: delegate to the device-admin domain so the WS
	// accept walks every central (multi-CCU-safe) and runs the same
	// first-time-configuration orchestration as the REST endpoint.
	if w.deviceAdmin != nil {
		return w.deviceAdmin.AcceptInboxDevice(ctx, deviceAddress, interfaces.AcceptInboxOptions{
			Name:            opts.Name,
			IncludeChannels: opts.IncludeChannels,
			Rooms:           opts.Rooms,
			Functions:       opts.Functions,
		})
	}
	// Fallback for minimal wirings without a device-admin domain: a plain
	// accept via the sole central's hub, no follow-up configuration. It
	// resolves rather than guesses, so a multi-CCU daemon without the
	// domain reports the ambiguity instead of accepting on a CCU the
	// caller never named.
	h, err := w.resolveHub("")
	if err != nil {
		return err
	}
	if h == nil {
		return errors.New("ws: hub not available")
	}
	return h.AcceptInboxDeviceRemote(ctx, deviceAddress)
}

// ── wsDeviceQuery ────────────────────────────────────────────────────────────

// wsDeviceQuery bridges *adapter.DevicesAdapter + *adapter.ParamsetsDomain
// onto ws.DeviceQuery. Device rendering uses JSON round-trip to produce
// the opaque map[string]any the WS layer hands clients unchanged.
type wsDeviceQuery struct {
	devs      *adapter.DevicesAdapter
	paramsets *adapter.ParamsetsDomain
	registry  *central.Registry
	writer    *clientpkg.ValueWriter
}

func (w *wsDeviceQuery) ListDevices(_ context.Context) ([]map[string]any, error) {
	if w.devs == nil {
		return []map[string]any{}, nil
	}
	devs := w.devs.Devices()
	out := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		out = append(out, map[string]any{
			"address":        d.Address,
			"central":        w.devs.CentralOf(d.Address),
			"interface":      string(d.Interface),
			"interface_id":   d.InterfaceID,
			"model":          d.Model,
			"name":           d.Name(),
			"available":      d.Available(),
			"channels_count": len(d.Channels()),
			"rooms":          d.Rooms(),
			"functions":      d.Functions(),
		})
	}
	return out, nil
}

func (w *wsDeviceQuery) GetDevice(_ context.Context, address string) (map[string]any, error) {
	if w.devs == nil {
		return nil, errors.New("ws: devices adapter not wired")
	}
	d, ok := w.devs.Device(address)
	if !ok {
		return nil, fmt.Errorf("ws: device not found: %s", address)
	}
	channels := make([]map[string]any, 0, len(d.Channels()))
	for _, ch := range d.Channels() {
		channels = append(channels, map[string]any{
			"address": ch.Address,
			"number":  ch.Number,
			"type":    ch.Type,
			"name":    ch.Name(),
		})
	}
	return map[string]any{
		"address":        d.Address,
		"central":        w.devs.CentralOf(d.Address),
		"interface":      string(d.Interface),
		"interface_id":   d.InterfaceID,
		"model":          d.Model,
		"name":           d.Name(),
		"available":      d.Available(),
		"channels_count": len(d.Channels()),
		"channels":       channels,
		"rooms":          d.Rooms(),
		"functions":      d.Functions(),
	}, nil
}

func (w *wsDeviceQuery) GetParamsetDescription(ctx context.Context, key configui.SessionKey) (map[string]any, error) {
	if w.paramsets == nil || w.writer == nil {
		return nil, errors.New("ws: paramset backend not wired")
	}
	// Look up the device's interface so we can reach the backend directly.
	if w.registry == nil {
		return nil, errors.New("ws: registry not wired")
	}
	for _, u := range w.registry.List() {
		if key.CentralName != "" && u.Name() != key.CentralName {
			continue
		}
		deviceAddr := deviceAddrFromChannel(key.ChannelAddress)
		dev, ok := u.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		backend, ok := w.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return nil, fmt.Errorf("ws: no backend for %s/%s", u.Name(), dev.InterfaceID)
		}
		psKey := key.ParamsetKey
		if psKey == "" {
			psKey = hmenum.ParamsetKeyMaster
		}
		raw, err := backend.GetParamsetDescription(ctx, key.ChannelAddress, psKey)
		if err != nil {
			return nil, err
		}
		// Convert map[string]hmproto.ParameterData → map[string]any via JSON round-trip.
		return structToMap(raw)
	}
	return nil, fmt.Errorf("ws: device not found for channel %s", key.ChannelAddress)
}

func (w *wsDeviceQuery) GetParamset(ctx context.Context, key configui.SessionKey) (map[string]any, error) {
	if w.paramsets == nil {
		return nil, errors.New("ws: paramsets domain not wired")
	}
	psKey := key.ParamsetKey
	if psKey == "" {
		psKey = hmenum.ParamsetKeyMaster
	}
	// A session scoped to a central must read that central: the unscoped
	// domain call resolves an address to the first matching central (registry
	// order is name-sorted), which is the wrong CCU whenever the address
	// repeats across them. The scoped domain method keeps the model-first
	// read, the refresh fallback and the backend fallback intact while
	// pinning every one of them to the named central.
	return w.paramsets.GetParamsetOn(ctx, key.CentralName, key.ChannelAddress, psKey)
}

// ── wsParamsetWriter ─────────────────────────────────────────────────────────

// wsParamsetWriter bridges *adapter.ParamsetsDomain onto ws.ParamsetWriter.
// The WS layer passes a configui.SessionKey; the domain takes
// (central, address, key) and owns every resolution the write needs.
type wsParamsetWriter struct {
	domain *adapter.ParamsetsDomain
}

func (w *wsParamsetWriter) PutParamset(ctx context.Context, key configui.SessionKey, values map[string]any) error {
	if w.domain == nil {
		return errors.New("ws: paramsets domain not wired")
	}
	psKey := key.ParamsetKey
	if psKey == "" {
		psKey = hmenum.ParamsetKeyMaster
	}
	// A session scoped to a central must write that central (see the matching
	// note on wsDeviceQuery.GetParamset). The scoped domain method pins the
	// resolution without losing anything the unscoped one does: descriptor
	// coercion (a JSON number is a float64 and would reach the CCU as
	// <double> for an INTEGER parameter), min/max validation, the visibility
	// gate, the post-write model refresh and the audit row. Writing to the
	// scoped backend directly skipped all five.
	return w.domain.PutParamsetOn(ctx, key.CentralName, key.ChannelAddress, psKey, values)
}

// ── wsSessionBackend ─────────────────────────────────────────────────────────

// wsSessionBackend implements ws.SessionBackend by delegating Open to the
// device-query path (descriptions + initial values) and PutParamset to the
// paramsets domain. This wires the config.session.open + save commands
// without duplicating the lookup logic already in wsDeviceQuery and
// wsParamsetWriter.
type wsSessionBackend struct {
	deviceQuery *wsDeviceQuery
	paramsets   *wsParamsetWriter
}

// Open fetches the paramset descriptions and current values for the session
// key. The returned descriptions map is opaque (map[string]any) so the WS
// layer stays decoupled from the wire protocol.
func (b *wsSessionBackend) Open(ctx context.Context, key configui.SessionKey) (descs, values map[string]any, err error) {
	if b.deviceQuery == nil {
		return nil, nil, errors.New("ws: session backend: device query not wired")
	}
	descs, err = b.deviceQuery.GetParamsetDescription(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("ws: session backend: descriptions: %w", err)
	}
	values, err = b.deviceQuery.GetParamset(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("ws: session backend: values: %w", err)
	}
	return descs, values, nil
}

// PutParamset writes the changed values via the paramsets domain.
func (b *wsSessionBackend) PutParamset(ctx context.Context, key configui.SessionKey, values map[string]any) error {
	if b.paramsets == nil {
		return errors.New("ws: session backend: paramsets not wired")
	}
	return b.paramsets.PutParamset(ctx, key, values)
}

// ── wsDeviceWriter ───────────────────────────────────────────────────────────

// wsDeviceWriter bridges *adapter.DeviceAdminDomain onto ws.DeviceWriter.
type wsDeviceWriter struct {
	admin *adapter.DeviceAdminDomain
}

func (w *wsDeviceWriter) Rename(ctx context.Context, address, name string, includeChannels bool) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.RenameDevice(ctx, address, name, includeChannels)
}

// RenameChannel renames a single channel via
// DeviceAdminDomain.RenameChannel (CCU JSON-RPC `Channel.setName`).
func (w *wsDeviceWriter) RenameChannel(ctx context.Context, deviceAddr string, channelNo int, name string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.RenameChannel(ctx, deviceAddr, channelNo, name)
}

// SetInstallMode opens a per-device pairing window via
// DeviceAdminDomain.SetInstallMode (backend's XML-RPC `setInstallMode`).
func (w *wsDeviceWriter) SetInstallMode(ctx context.Context, address string, durationSeconds int) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.SetInstallMode(ctx, address, durationSeconds)
}

// SetChannelRooms replaces a single channel's room assignments via
// DeviceAdminDomain.SetChannelRooms (Rega `set_device_rooms` with the
// channel address).
func (w *wsDeviceWriter) SetChannelRooms(ctx context.Context, deviceAddr string, channelNo int, rooms []string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.SetChannelRooms(ctx, deviceAddr, channelNo, rooms)
}

// SetChannelFunctions replaces a single channel's function (Gewerk)
// assignments via DeviceAdminDomain.SetChannelFunctions.
func (w *wsDeviceWriter) SetChannelFunctions(ctx context.Context, deviceAddr string, channelNo int, functions []string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.SetChannelFunctions(ctx, deviceAddr, channelNo, functions)
}

// RestoreConfig re-transmits the stored configuration to the device via
// DeviceAdminDomain.RestoreDeviceConfig (XML-RPC
// `restoreConfigToDevice`).
func (w *wsDeviceWriter) RestoreConfig(ctx context.Context, address string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.RestoreDeviceConfig(ctx, address)
}

// ReplaceCandidates lists the devices the new device may replace via
// DeviceAdminDomain.ReplaceCandidates.
func (w *wsDeviceWriter) ReplaceCandidates(ctx context.Context, centralName, newAddress string) ([]hmapi.ReplaceCandidate, error) {
	if w.admin == nil {
		return nil, errors.New("ws: device admin not wired")
	}
	return w.admin.ReplaceCandidates(ctx, centralName, newAddress)
}

// ReplaceDevice swaps a paired device for a new one via
// DeviceAdminDomain.ReplaceDevice.
func (w *wsDeviceWriter) ReplaceDevice(ctx context.Context, centralName, oldAddress, newAddress string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.ReplaceDevice(ctx, centralName, oldAddress, newAddress)
}

// TestDeviceCommunication runs the CCU's per-device communication test
// via DeviceAdminDomain.TestDeviceCommunication.
func (w *wsDeviceWriter) TestDeviceCommunication(ctx context.Context, address string) (hmapi.CommunicationTestResult, error) {
	if w.admin == nil {
		return hmapi.CommunicationTestResult{}, errors.New("ws: device admin not wired")
	}
	return w.admin.TestDeviceCommunication(ctx, address)
}

// TeamCandidates lists the team channels a channel may join via
// DeviceAdminDomain.TeamCandidates.
func (w *wsDeviceWriter) TeamCandidates(ctx context.Context, deviceAddr string, channelNo int) ([]hmapi.TeamCandidate, error) {
	if w.admin == nil {
		return nil, errors.New("ws: device admin not wired")
	}
	return w.admin.TeamCandidates(ctx, deviceAddr, channelNo)
}

// SetChannelTeam assigns a channel to a team via
// DeviceAdminDomain.SetChannelTeam.
func (w *wsDeviceWriter) SetChannelTeam(ctx context.Context, deviceAddr string, channelNo int, teamChannelAddress string) error {
	if w.admin == nil {
		return errors.New("ws: device admin not wired")
	}
	return w.admin.SetChannelTeam(ctx, deviceAddr, channelNo, teamChannelAddress)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// structSliceToMapSlice JSON-encodes a slice of typed structs and decodes them
// as []map[string]any. Used for handlers.Link etc.
func structSliceToMapSlice[T any](in []T) ([]map[string]any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("ws: encode: %w", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ws: decode: %w", err)
	}
	return out, nil
}

// structToMap JSON-encodes an arbitrary struct and decodes it as map[string]any.
func structToMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("ws: encode: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ws: decode: %w", err)
	}
	return out, nil
}

// ── wsCacheClearer ───────────────────────────────────────────────────────────

// wsCacheClearer adapts *cachereset.Service onto ws.CacheClearer.
type wsCacheClearer struct{ svc *cachereset.Service }

func (w *wsCacheClearer) ClearCache(ctx context.Context, scope cachereset.Scope) (cachereset.Report, error) {
	return w.svc.Clear(ctx, scope)
}

// wsCacheClearerFrom returns a ws.CacheClearer backed by svc, or nil when svc
// is nil (leaves ccu.cache_clear unregistered in reduced builds).
func wsCacheClearerFrom(svc *cachereset.Service) ws.CacheClearer {
	if svc == nil {
		return nil
	}
	return &wsCacheClearer{svc: svc}
}

// ── wsSessionRecorder ─────────────────────────────────────────────────────────

// wsSessionRecorder adapts the shared RPC recorder domain service onto
// ws.SessionRecorder. Start / Stop delegate to the same
// [interfaces.RPCRecorderService] the REST `/diagnostics/rpc-recording` route
// drives, so a WS-started recording arms the same auto-stop safety timer and
// persists the same restart-survival marker instead of poking every central's
// recorder directly and leaving neither. Start / Stop target every central
// (empty scope); IsActive reports true when any central is currently
// capturing — the recorder is multi-central by design (ADR 0002), so a
// per-central scope is intentionally not exposed on this minimal surface.
type wsSessionRecorder struct{ svc interfaces.RPCRecorderService }

// Start activates recording on every central via the shared domain method,
// which arms the auto-stop timer and writes the active marker.
func (w *wsSessionRecorder) Start() bool {
	if w == nil || w.svc == nil {
		return false
	}
	return anyRecordingActive(w.svc.Start(nil, 0, false))
}

// Stop deactivates recording on every central via the shared domain method,
// which cancels the auto-stop timer.
func (w *wsSessionRecorder) Stop() bool {
	if w == nil || w.svc == nil {
		return false
	}
	return anyRecordingActive(w.svc.Stop(nil))
}

// IsActive reports whether any central's recorder is currently capturing.
func (w *wsSessionRecorder) IsActive() bool {
	if w == nil || w.svc == nil {
		return false
	}
	return anyRecordingActive(w.svc.Status())
}

// anyRecordingActive reports whether any central in the status slice is
// currently capturing.
func anyRecordingActive(status []hmapi.RPCRecordingStatus) bool {
	for _, s := range status {
		if s.Active {
			return true
		}
	}
	return false
}

// wsSessionRecorderFrom returns a ws.SessionRecorder backed by the shared RPC
// recorder domain service. When no shared service is wired (svc == nil) it
// builds a registry-backed one so the family stays available; it returns nil
// only when there is nothing at all to back it (which leaves the recording.*
// commands unregistered).
//
// The decision is deliberately NOT "does a central expose a recorder right
// now": command registration happens once at boot, while every central.New
// builds a recorder, so probing the registry only answers "is the fleet empty
// yet". On a fresh install — no centrals in the config, the operator adding
// the first CCU through the onboarding wizard — that probe left
// recording.start/stop/status answering unknown_command for the whole run,
// exactly when a new CCU is most likely being debugged. The returned recorder
// re-walks the registry on every call, so a central adopted later is covered
// and an empty fleet simply reports "not active".
func wsSessionRecorderFrom(reg *central.Registry, svc interfaces.RPCRecorderService) ws.SessionRecorder {
	if svc == nil {
		if reg == nil {
			return nil
		}
		// No shared service wired (e.g. a REST-disabled or minimal boot):
		// build a registry-backed one so recording.* stays functional. The
		// empty data dir disables only the restart-survival marker.
		svc = adapter.NewRPCRecorderAdapter(reg, "")
	}
	return &wsSessionRecorder{svc: svc}
}

// deviceAddrFromChannel strips the ":N" channel suffix from a channel
// address to produce the device address.
func deviceAddrFromChannel(channelAddress string) string {
	for i := len(channelAddress) - 1; i >= 0; i-- {
		if channelAddress[i] == ':' {
			return channelAddress[:i]
		}
	}
	return channelAddress
}
