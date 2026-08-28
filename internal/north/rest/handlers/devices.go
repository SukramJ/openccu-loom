// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/internal/restapi"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmui"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DeviceIndex is an alias for the canonical interface in internal/restapi.
type DeviceIndex = restapi.DeviceIndex

// DataPointWriter is an alias for the canonical interface in pkg/interfaces.
type DataPointWriter = interfaces.DataPointWriter

// --- DTOs ---

// DeviceSummary is one entry in `GET /api/v1/devices`.
type DeviceSummary struct {
	Address     string `json:"address"`
	Central     string `json:"central,omitempty"`
	Interface   string `json:"interface"`
	InterfaceID string `json:"interface_id"`
	// IseID is the CCU-internal numeric device id. External clients that
	// address devices by ISE_ID (e.g. the HA rename-by-ise_id path) need
	// it to map back to the device address. 0 when the CCU did not report
	// one (e.g. non-CCU backends).
	IseID        int    `json:"ise_id,omitempty"`
	Model        string `json:"model"`
	ModelLabel   string `json:"model_label,omitempty"`
	ModelIcon    string `json:"model_icon,omitempty"`
	SubModel     string `json:"sub_model,omitempty"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer,omitempty"`
	ProductGroup string `json:"product_group,omitempty"`
	IsAvailable  bool   `json:"available"`
	// Released reports whether the device has finished onboarding. False
	// means it is accepted and configurable but deliberately not yet
	// published to the ecosystems.
	//
	// This endpoint still lists it: the Config UI has to see it to
	// configure it, which is the state's whole purpose. But a consumer of
	// this API can be an ecosystem as much as a configuration client —
	// the transport does not determine the role — and an ecosystem that
	// adopts a device before it is named keeps the identity it saw: Home
	// Assistant its entity ids, a Matter controller its endpoint number.
	// So the state travels with the device and the consumer decides.
	//
	// Always true on an installation that never used the onboarding
	// wizard, so an existing client needs no filter to keep working.
	Released      bool `json:"released"`
	ChannelsCount int  `json:"channels_count"`
	// Updatable reports whether the device *supports* firmware updates
	// (CCU UPDATABLE capability) — NOT whether one is pending.
	Updatable bool `json:"updatable"`
	// UpdateAvailable reports whether an installable firmware update is
	// actually pending — the gated latest version differs from the installed
	// one (image already delivered for HmIP-RF / available for BidCos). The
	// UI flags "update available" on this, not on Updatable.
	UpdateAvailable bool `json:"update_available"`
	// UpdateStatus is the daemon-derived firmware-update verdict
	// (`up_to_date` | `update_available` | `installing`), collapsing the raw
	// CCU firmware phase + UpdateAvailable signal so a client renders the
	// update entity without carrying the phase-classification sets itself.
	UpdateStatus string   `json:"update_status,omitempty"`
	Rooms        []string `json:"rooms,omitempty"`
	Functions    []string `json:"functions,omitempty"`
	// MasterPushesConfigPending is true when the device's interface
	// delivers reliable CONFIG_PENDING events on MASTER writes — the
	// SPA then waits for the true→false transition before refreshing
	// MASTER (HmIP-RF, which serves both RF and Wired devices). On
	// the others (BidCos-*, VirtualDevices, CUxD) CONFIG_PENDING is
	// either silent or unreliable; the SPA falls back to a save-path
	// reload. Sourced from `hmenum.Interface.PushesConfigPending`.
	MasterPushesConfigPending bool `json:"master_pushes_config_pending"`

	// ConfigRestoreSupported is true when the device's interface
	// exposes `restoreConfigToDevice` (HmIP-RF, BidCos-RF). The SPA
	// gates the "restore config" action on it so the button never
	// shows for a device that cannot serve the write.
	ConfigRestoreSupported bool `json:"config_restore_supported"`

	// CommunicationTestSupported is true when the device's interface can
	// run the CCU's per-device communication test (radio interfaces).
	// The SPA gates the "test" action on it.
	CommunicationTestSupported bool `json:"communication_test_supported"`

	// TeamSupported is true when the device's interface exposes channel
	// team assignment (setTeam / listTeams — BidCos-RF, HmIP-RF). The
	// SPA gates the team picker on it.
	TeamSupported bool `json:"team_supported"`

	// HasSubDevices mirrors [device.Device.HasSubDevices] so SPA / WS
	// consumers can apply the same per-channel-group split the MQTT
	// bridge does under the `sub_devices_enabled` toggle.
	HasSubDevices bool `json:"has_sub_devices"`

	// RxMode decodes the device's CCU RX_MODE bitmask into named flags.
	// Its `wakeup` / `lazy_config` bits mark a battery-powered device that
	// only applies pending configuration on its next wakeup — the SPA uses
	// them to show a "pending wakeup" hint after a link/config write.
	// Omitted when the CCU reports no rx mode (RX_MODE == 0).
	RxMode *RxModeInfo `json:"rx_mode,omitempty"`
}

// RxModeInfo decodes a device's CCU RX_MODE bitmask into named boolean
// flags. Set bits are emitted; cleared bits are omitted.
type RxModeInfo struct {
	// Always marks a mains-powered device that is permanently reachable
	// (RX_ALWAYS) and applies configuration immediately.
	Always bool `json:"always,omitempty"`
	// Burst marks a device reachable via burst wakeup (RX_BURST).
	Burst bool `json:"burst,omitempty"`
	// Config marks a device reachable in its configuration window (RX_CONFIG).
	Config bool `json:"config,omitempty"`
	// Wakeup marks a battery device that only accepts pending configuration
	// when it next wakes up (RX_WAKEUP).
	Wakeup bool `json:"wakeup,omitempty"`
	// LazyConfig marks a battery device whose configuration transfer is
	// deferred until its next wakeup (RX_LAZY_CONFIG).
	LazyConfig bool `json:"lazy_config,omitempty"`
}

// rxModeInfo decodes a device's RX_MODE bitmask into a [RxModeInfo]. It
// returns nil when no bit is set (RX_MODE == 0), so the DTO omits the
// field for devices the CCU reports no rx mode for.
func rxModeInfo(m hmenum.RxMode) *RxModeInfo {
	if m == hmenum.RxModeUndefined {
		return nil
	}
	return &RxModeInfo{
		Always:     m.Has(hmenum.RxModeAlways),
		Burst:      m.Has(hmenum.RxModeBurst),
		Config:     m.Has(hmenum.RxModeConfig),
		Wakeup:     m.Has(hmenum.RxModeWakeup),
		LazyConfig: m.Has(hmenum.RxModeLazyConfig),
	}
}

// DeviceDetail extends [DeviceSummary] with the firmware snapshot
// and channel summaries.
type DeviceDetail struct {
	DeviceSummary
	Firmware     device.FirmwareInfo     `json:"firmware"`
	Availability device.AvailabilityInfo `json:"availability"`
	Channels     []ChannelSummary        `json:"channels"`
}

// ChannelSummary is one entry in `GET .../channels`.
type ChannelSummary struct {
	Address string `json:"address"`
	Number  int    `json:"number"`
	Type    string `json:"type,omitempty"`
	// TypeLabel is the localised, human-readable channel-type label
	// resolved through the OCCU `channel_types_<locale>` table — e.g.
	// "Energiemesser" for `ENERGIE_METER_TRANSMITTER`. Empty when no
	// translation exists; the SPA falls back to the raw `type`.
	TypeLabel   string `json:"type_label,omitempty"`
	Name        string `json:"name,omitempty"`
	ParamsetKey string `json:"paramset_key"`
	// ParamsetKeys lists the paramsets this channel actually exposes —
	// "VALUES" when it has value data points and "MASTER" when it has
	// master (config) data points. The config-panel / HA drop-in uses
	// this to offer the right paramset tabs without probing each key.
	ParamsetKeys []string `json:"paramset_keys,omitempty"`
	DataPoints   int      `json:"data_points_count"`
	// Category is the OCCU channel-type string (same as Type), exposed
	// under its own `category` key so consumers can route on channel
	// purpose without parsing the Type string.
	Category string `json:"category,omitempty"`
	// CustomDpName is the stable name of the Custom-DP that owns this
	// channel — empty when the channel is not attached to any CDP.
	// Lets the SPA's CDP-aware Übersicht view filter out channels that
	// are already represented by a CDP tile (ADR 0016).
	CustomDpName string `json:"custom_dp_name,omitempty"`

	// GroupNo is the channel-group number the channel belongs to (0
	// when the channel is not part of any group).
	GroupNo int `json:"group_no,omitempty"`
	// IsGroupMaster is true when the channel itself is the master of
	// its channel group (`GroupNo == Number`).
	IsGroupMaster bool `json:"is_group_master,omitempty"`
	// IsInMultiGroup signals that the channel sits in a channel group
	// with more than one member — i.e. it participates in the parent
	// device's sub-device split.
	IsInMultiGroup bool `json:"is_in_multi_group,omitempty"`
	// SubDeviceName is the resolved sub-device label per
	// [device.Channel.SubDeviceName]. Empty when no sub-device split
	// applies to this channel.
	SubDeviceName string `json:"sub_device_name,omitempty"`
	// Room is the channel's single resolved room with the group-master
	// fallback applied ([device.Channel.Room]). Empty when no unique
	// room can be resolved. External clients use it as the
	// suggested-area of the channel group's sub-device.
	Room string `json:"room,omitempty"`
	// Rooms is the channel's full room-assignment set
	// ([device.Channel.Rooms]) — unlike [ChannelSummary.Room] it is not
	// collapsed to the unique case, so editors can round-trip the
	// assignment. Empty when the channel carries no room assignment.
	Rooms []string `json:"rooms,omitempty"`
	// Functions are the channel's resolved "Gewerke" (function) labels
	// ([device.Channel.Functions]) — the channel-level twin of
	// [DeviceSummary.Functions]. Surfaced so clients can map functions at
	// channel granularity instead of folding them up to the device. Empty
	// when the channel carries no function assignment.
	Functions []string `json:"functions,omitempty"`
	// IsCustomDpPrimary is true when this channel both owns a Custom-DP and is
	// the primary (group-master) channel of its group
	// ([device.Channel.IsCustomDPPrimaryChannel]). It is the daemon-derived
	// "which channel is the device's primary" marker a client otherwise
	// reconstructs from the device profile — the entity that should carry the
	// device-level name. False for secondary / non-CDP channels.
	IsCustomDpPrimary bool `json:"is_custom_dp_primary,omitempty"`
	// Hidden / Locked are the operator per-channel overrides (G12): hidden
	// removes the channel from the operation surfaces (data-point list / MQTT
	// / Matter), locked blocks control writes. Both surface so the SPA can
	// badge the channel and render the toggles.
	Hidden bool `json:"hidden,omitempty"`
	Locked bool `json:"locked,omitempty"`
}

// DataPointSummary is one entry in `GET .../data-points`.
type DataPointSummary struct {
	Parameter string `json:"parameter"`
	// UniqueID is the canonical loom-namespaced routing key for this data
	// point (the [routingkey.CanonicalUniqueID] result) — identical to the
	// value on the WS `data_point.value_changed` payload. Lets a client build
	// its entity registry straight from the snapshot/summary instead of
	// recomputing the key. Always present and non-empty: a central does not
	// serve any entity until its CCU serial — the central-id slot of the
	// canonical key — is resolved (the bring-up readiness gate, see
	// `internal/central/adapter/hub_wiring.go`).
	UniqueID       string `json:"unique_id"`
	ParameterLabel string `json:"parameter_label,omitempty"`
	Value          any    `json:"value"`
	// DisplayValue is Value expressed in the unit `unit` names, i.e.
	// Value × Multiplier. Present only when that projection is
	// non-trivial; absent means Value already is the displayable
	// number.
	//
	// It exists because Value is the raw CCU wire value and cannot stop
	// being that — the write path sends it back, and the domain computes
	// on it. A LEVEL reads 0.42 with unit `%`, so a client that renders
	// the pair shows 0.42 % unless it multiplies. Rendering DisplayValue
	// when present, and Value otherwise, is always correct and needs no
	// arithmetic on the client.
	DisplayValue any    `json:"display_value,omitempty"`
	Observed     bool   `json:"observed"`
	ModifiedAt   string `json:"modified_at,omitempty"`
	// Source is the wire-side lifecycle token: "unobserved" | "cache"
	// | "live" | "stale". Surfaced so UI consumers can render a
	// freshness badge without inferring state from timestamps alone.
	// See ADR 0018.
	Source string `json:"source,omitempty"`
	// LastSeenAt is when the data point was last observed via any
	// push or fetch_all event (RFC3339). Differs from ModifiedAt when
	// the CCU sent a cyclic info telegram that repeated the previous
	// value: ModifiedAt stays put, LastSeenAt advances.
	LastSeenAt string `json:"last_seen_at,omitempty"`
	// LastChangedAt is when the value actually changed (RFC3339).
	// Alias for ModifiedAt with the cache-friendly naming; the older
	// field stays in place for backwards compatibility.
	LastChangedAt string `json:"last_changed_at,omitempty"`
	// ValueAgeSeconds is the integer number of seconds between
	// LastSeenAt and the time the response is built. Pre-computed so
	// the browser does not need to parse the timestamp on every
	// render.
	ValueAgeSeconds int64               `json:"value_age_seconds,omitempty"`
	Operations      DataPointSummaryOps `json:"operations"`
	// Category is the fine-grained DataPointCategory ("light", "cover",
	// "climate", "sensor", "binary_sensor", "switch", …) the daemon
	// derives internally. External clients (the HA drop-in) spawn entities
	// off this instead of re-deriving categories from raw paramsets.
	// Empty only when the DP does not implement the categorised surface.
	Category string `json:"category,omitempty"`
	// DataPointType is the consumer-facing functional type mapped from
	// Category via hmenum.CategoryToType ("light", "number", "select",
	// "sensor", …). Distinct from Type above, which is the CCU descriptor
	// primitive (BOOL/INTEGER/FLOAT/ENUM); the two must not be conflated.
	DataPointType string `json:"data_point_type,omitempty"`
	// Usage is the visibility verdict the daemon's pipeline computed for
	// this DP ("data_point", "no_create", "ignored", "ce_primary",
	// "ce_secondary", "ce_visible", "event"). Clients skip entity
	// creation for "no_create"/"ignored" — the same gate the MQTT
	// discovery plane applies.
	Usage string `json:"usage,omitempty"`
	// TranslatedName is the locale-aware per-entity name HA assigns to
	// this data point — resolved through the same primitives as the
	// MQTT discovery `name` field. It is the parameter portion only;
	// HA prepends the device name. When LabelOmitted is true it
	// carries the channel-level collapsed name instead (channel name
	// plus multi-channel marker, device prefix stripped) — possibly
	// empty when the collapse reduces to the device name alone.
	TranslatedName string `json:"translated_name,omitempty"`
	// LabelOmitted is true when the parameter is flagged "primary" in
	// the embedded translation_custom catalogue. The entity is then
	// named after the channel: TranslatedName holds the collapsed
	// channel-level name (MQTT instead emits `name: null` and lets HA
	// fall back to the device name).
	LabelOmitted bool `json:"label_omitted,omitempty"`
	// Control is the CCU paramset descriptor's CONTROL attribute,
	// of the form WIDGET_FAMILY.SLOT (e.g. "HEATING_CONTROL_HMIP.SETPOINT",
	// "DIMMER.LEVEL"). Drives the SPA's CONTROL-aware widget resolver
	// under assets/ui/src/lib/control/ — see notes/concepts/ui/control-widget-concept.md.
	// Empty when the descriptor carries no CONTROL.
	Control string `json:"control,omitempty"`
	// Type is the CCU descriptor TYPE (BOOL, INTEGER, FLOAT, ENUM, ...).
	// Surfacing it lets the SPA pick the right widget primitive without a
	// second round-trip into the paramset descriptor (especially for ENUM
	// pickers such as HmIP-BSL COLOR / COLOR_BEHAVIOUR).
	Type string `json:"type,omitempty"`
	// ValueList is the descriptor's VALUE_LIST when present — the
	// ordered enum labels (e.g. ["BLACK","BLUE","GREEN",...]). Empty
	// for non-ENUM parameters.
	ValueList []string `json:"value_list,omitempty"`
	// ValueTranslations maps each raw VALUE_LIST entry to its localised
	// display string, resolved through the OCCU `parameter_values_<locale>`
	// table in the request locale. Only entries that have an actual
	// translation are included (an untranslated value is omitted so the
	// client falls back to the raw `value_list` token), and the map is
	// absent entirely for non-ENUM parameters or when no value translates.
	// Mirrors the reference stack's per-DP `value_translations` property.
	ValueTranslations map[string]string `json:"value_translations,omitempty"`
	// Unit is the parameter descriptor's UNIT string ("°C", "%",
	// "mA", "Hz", "Wh", ...) as the CCU declares it. Empty when the
	// descriptor carries no unit.
	Unit string `json:"unit,omitempty"`
	// Multiplier converts Value (the raw wire value) into the unit
	// Unit names — e.g. a LEVEL data point reports Value in 0.0-1.0
	// with Unit "%", so a client must multiply by Multiplier (100) to
	// render "42 %" instead of "0.42 %". Only emitted for a non-trivial
	// multiplier so most responses stay unchanged; a client that omits
	// this field must treat it as 1. Mirrors the MQTT raw-plane
	// GenericDataPointConfig.Multiplier (internal/payload/info.go).
	Multiplier float64 `json:"multiplier,omitempty"`
	// Min / Max / Default carry the descriptor's numeric bounds and
	// preset value verbatim from the wire. The SPA's AutoTile
	// composer reads them to decide between slider / stepper /
	// free-input primitives for writable numeric DPs.
	Min     json.RawMessage `json:"min,omitempty"`
	Max     json.RawMessage `json:"max,omitempty"`
	Default json.RawMessage `json:"default,omitempty"`
	// UIHint is the daemon's per-DP classification envelope for the
	// AutoTile composer (icon, semantic, optional state-color rule).
	// Computed once at serialise-time via [hmui.HintFor]; the SPA
	// renders the values verbatim without re-classifying client-side.
	// See notes/concepts/ui/auto-tile-concept.md.
	UIHint *hmui.Hint `json:"ui_hint,omitempty"`
	// AdditionalInformation carries enriched model metadata (e.g. battery
	// type / quantity / low-voltage limits for a battery-backed device)
	// when the data point provides it. Absent for plain scalar DPs
	// (elided via omitempty), so existing responses are unchanged. Mirrors
	// the optional `additional_information` object on the per-DP MQTT state
	// topic.
	AdditionalInformation map[string]any `json:"additional_information,omitempty"`
}

// DataPointSummaryOps mirrors the OPERATIONS bitmask the CCU returns
// for each parameter. Surfacing read/write/event lets the SPA decide
// when to render an interactive widget vs. a status display — the
// QuickControl tab uses `Write` to drop sensor-only channels (e.g.
// SWITCH_TRANSMITTER) from the actor list.
type DataPointSummaryOps struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Event bool `json:"event"`
}

// RoomEntry is one row in `GET /api/v1/rooms`.
type RoomEntry struct {
	Name        string `json:"name"`
	DeviceCount int    `json:"device_count"`
}

// RefreshDevicesService is the optional facade behind
// `POST /devices/refresh`. Triggers a fresh ListDevices sweep on
// every backend so the registry catches up after a CCU-side change
// (paired/unpaired devices, room edits) without waiting for the
// next periodic tick.
type RefreshDevicesService interface {
	RefreshDevices(ctx context.Context) error
}

// RefreshDevices forces a re-pull of the device list from every CCU.
func RefreshDevices(svc RefreshDevicesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Refresh unavailable", ""))
			return
		}
		if err := svc.RefreshDevices(r.Context()); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Refresh failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// FunctionEntry is one row in `GET /api/v1/functions`.
type FunctionEntry struct {
	Name        string `json:"name"`
	DeviceCount int    `json:"device_count"`
}

// ListFunctions aggregates function (Gewerk) assignments across
// every device. Mirrors [ListRooms]; both indices feed the SPA's
// settings overview.
func ListFunctions(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		counts := map[string]int{}
		if idx != nil {
			for _, d := range idx.Devices() {
				for _, f := range d.Functions() {
					counts[f]++
				}
			}
		}
		out := make([]FunctionEntry, 0, len(counts))
		for name, c := range counts {
			out = append(out, FunctionEntry{Name: name, DeviceCount: c})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		JSON(w, http.StatusOK, out)
	}
}

// ListRooms aggregates rooms across every device. The CCU exposes
// rooms via `Room.getAll`-style JSON-RPC; until that bridge ships
// we derive the index from the device summaries directly.
func ListRooms(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		counts := map[string]int{}
		if idx != nil {
			for _, d := range idx.Devices() {
				for _, r := range d.Rooms() {
					counts[r]++
				}
			}
		}
		out := make([]RoomEntry, 0, len(counts))
		for name, c := range counts {
			out = append(out, RoomEntry{Name: name, DeviceCount: c})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		JSON(w, http.StatusOK, out)
	}
}

// ParameterLabeler is an alias for the canonical interface in pkg/interfaces.
type ParameterLabeler = interfaces.ParameterLabeler

// ChannelTypedLabeler extends [ParameterLabeler] with channel-typed
// lookups: the channel-type-specific parameter translation (so
// `POWER` on `ENERGIE_METER_TRANSMITTER` can resolve differently from
// the bare-parameter table) and the channel-type label itself.
//
// Handlers probe via type-assertion — implementations that only carry
// the bare-parameter translation keep working with the un-typed
// fallbacks.
type ChannelTypedLabeler interface {
	ChannelTypedParameterLabel(channelType, parameter string) string
	ChannelTypeLabel(channelType string) string
}

// ChannelTypedValueLabeler resolves the localised display string for a
// single ENUM value. Implemented by the request-scoped label adapter
// ([adapter.ParameterLabelAdapter]); handlers probe for it via
// type-assertion so an adapter without value translations degrades to no
// `value_translations` rather than failing.
type ChannelTypedValueLabeler interface {
	ChannelTypedValueLabel(channelType, parameter, value string) string
}

// resolvedValueTranslations builds the `value_translations` map for an ENUM
// data point: each raw VALUE_LIST entry mapped to its localised label. Only
// entries that actually translate (the resolved label differs from the raw
// token) are included, so a client falls back to `value_list` for the rest;
// the result is nil when the labeler carries no value translations or none of
// the values resolve, keeping the field absent for non-ENUM DPs. Mirrors the
// reference stack's per-DP `value_translations` property.
func resolvedValueTranslations(labels ParameterLabeler, channelType, paramName string, valueList []string) map[string]string {
	if len(valueList) == 0 || labels == nil {
		return nil
	}
	vl, ok := labels.(ChannelTypedValueLabeler)
	if !ok {
		return nil
	}
	var out map[string]string
	for _, v := range valueList {
		label := vl.ChannelTypedValueLabel(channelType, paramName, v)
		if label == "" || label == v {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(valueList))
		}
		out[v] = label
	}
	return out
}

// channelTypeLabel is the type-asserted helper that returns the
// localised channel-type label, falling back to the empty string.
func channelTypeLabel(labels ParameterLabeler, channelType string) string {
	if labels == nil {
		return ""
	}
	c, ok := labels.(ChannelTypedLabeler)
	if !ok {
		return ""
	}
	return c.ChannelTypeLabel(channelType)
}

// channelTypedParameterLabel preserves the existing un-typed
// `ParameterLabel` chain but lets handlers attach channel context
// when they have one. The channel-typed translation wins over the
// bare-parameter one.
func channelTypedParameterLabel(labels ParameterLabeler, channelType, paramName string) string {
	if labels == nil {
		return ""
	}
	if c, ok := labels.(ChannelTypedLabeler); ok && channelType != "" {
		if s := c.ChannelTypedParameterLabel(channelType, paramName); s != "" {
			return s
		}
	}
	return labels.ParameterLabel(paramName)
}

// resolvedParameterLabel renders the ready-to-display caption for a
// parameter row: the channel-typed translation when one exists,
// otherwise the title-cased parameter via the shared naming
// primitive — so clients (the SPA in particular) never re-derive a
// fallback label themselves. Unlike `translated_name` the result
// always carries text; the "primary parameter" collapse semantics
// live in `label_omitted`, not here.
func resolvedParameterLabel(labels ParameterLabeler, channelType, paramName string) string {
	if s := channelTypedParameterLabel(labels, channelType, paramName); s != "" {
		return s
	}
	return naming.TitleCaseParameter(paramName)
}

// SetValueRequest is the body for `PUT .../value`.
type SetValueRequest struct {
	Value    any    `json:"value"`
	Priority string `json:"priority,omitempty"` // "default" | "high" | "critical"
}

// --- handlers ---

// ListDevices renders the device summary list with pagination.
func ListDevices(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devs := idx.Devices()
		// Optional client-side filters — case-insensitive substring
		// match on the listed fields. Empty query strings turn into
		// no-op filters so /devices keeps its default everything-
		// returns-everything contract.
		q := r.URL.Query()
		ifaceFilter := strings.ToLower(strings.TrimSpace(q.Get("interface")))
		modelFilter := strings.ToLower(strings.TrimSpace(q.Get("model")))
		nameFilter := strings.ToLower(strings.TrimSpace(q.Get("name")))
		addrFilter := strings.ToLower(strings.TrimSpace(q.Get("address")))
		// `central` is the per-CCU scoping discriminator, matched exactly
		// (not substring): it is the canonical central name
		// (`SystemCCUEntry.name == CentralRow.name == payload.central`), so
		// a multi-CCU client can fetch exactly one CCU's device list.
		centralFilter := strings.TrimSpace(q.Get("central"))
		// released_only is opt-in and never the default: this endpoint's
		// other consumer is the Config UI, which has to see a device that
		// is still being onboarded in order to configure it. Withholding
		// by default would make a device silently vanish from every
		// existing client's list — the failure mode opposite to the one
		// this filter exists for.
		releasedOnly := q.Get("released_only") == "true"
		filtered := devs[:0:0]
		for _, d := range devs {
			if centralFilter != "" && idx.CentralOf(d.Address) != centralFilter {
				continue
			}
			if ifaceFilter != "" && !strings.Contains(strings.ToLower(string(d.Interface)), ifaceFilter) {
				continue
			}
			if modelFilter != "" && !strings.Contains(strings.ToLower(d.Model), modelFilter) {
				continue
			}
			if nameFilter != "" && !strings.Contains(strings.ToLower(d.Name()), nameFilter) {
				continue
			}
			if addrFilter != "" && !strings.Contains(strings.ToLower(d.Address), addrFilter) {
				continue
			}
			if releasedOnly && !idx.Released(d.Address) {
				continue
			}
			filtered = append(filtered, d)
		}
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Address < filtered[j].Address })

		page, perPage := parsePagination(r)
		total := len(filtered)
		start := (page - 1) * perPage
		end := start + perPage
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}
		out := make([]DeviceSummary, 0, end-start)
		for _, d := range filtered[start:end] {
			out = append(out, toDeviceSummary(d, idx.CentralOf(d.Address), idx.Released(d.Address)))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		JSON(w, http.StatusOK, map[string]any{
			"items":    out,
			"page":     page,
			"per_page": perPage,
			"total":    total,
		})
	}
}

// GetDevice returns a single device's detail.
func GetDevice(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		chans := d.Channels()
		summaries := make([]ChannelSummary, 0, len(chans))
		for _, ch := range chans {
			summaries = append(summaries, toChannelSummary(ch, labels))
		}
		JSON(w, http.StatusOK, DeviceDetail{
			DeviceSummary: toDeviceSummary(d, idx.CentralOf(d.Address), idx.Released(d.Address)),
			Firmware:      d.Firmware().Info(),
			Availability:  d.AvailabilityInfo(),
			Channels:      summaries,
		})
	}
}

// ListChannels returns the device's channels.
func ListChannels(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		chans := d.Channels()
		out := make([]ChannelSummary, 0, len(chans))
		for _, ch := range chans {
			out = append(out, toChannelSummary(ch, labels))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetChannel returns a single channel.
func GetChannel(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, toChannelSummary(ch, labels))
	}
}

func channelParamsetKeys(ch *device.Channel) []string {
	keys := make([]string, 0, 2)
	if ch.Len() > 0 {
		keys = append(keys, string(hmenum.ParamsetKeyValues))
	}
	if ch.MasterLen() > 0 {
		keys = append(keys, string(hmenum.ParamsetKeyMaster))
	}
	return keys
}

func toChannelSummary(ch *device.Channel, labels ParameterLabeler) ChannelSummary {
	s := ChannelSummary{
		Address:      ch.Address,
		Number:       ch.Number,
		Type:         ch.Type,
		TypeLabel:    channelTypeLabel(labels, ch.Type),
		Category:     ch.Type,
		Name:         ch.Name(),
		ParamsetKey:  string(ch.ParamsetIn),
		ParamsetKeys: channelParamsetKeys(ch),
		DataPoints:   ch.Len(),
	}
	if cdp := ch.CustomDataPoint(); cdp != nil {
		s.CustomDpName = cdp.DataPointKey().Parameter
	}
	if groupNo := ch.GroupNumber(); groupNo != 0 {
		s.GroupNo = groupNo
		s.IsGroupMaster = ch.IsGroupMaster()
		if ch.IsInMultiGroup() {
			s.IsInMultiGroup = true
			s.SubDeviceName = ch.SubDeviceName()
		}
	}
	s.Room = ch.Room()
	if rooms := ch.Rooms(); len(rooms) > 0 {
		s.Rooms = rooms
	}
	if functions := ch.Functions(); len(functions) > 0 {
		s.Functions = functions
	}
	s.IsCustomDpPrimary = ch.IsCustomDPPrimaryChannel()
	// Operator per-channel overrides (G12): surfaced so the SPA can badge a
	// hidden/locked channel and render the toggles. The channel stays in the
	// detail list (so it is manageable); the hidden filter applies to the
	// operation surfaces (data-point list, MQTT, Matter).
	s.Hidden = ch.IsHidden()
	s.Locked = ch.IsLocked()
	return s
}

// ListDataPoints renders a channel's data points. The optional labeler fills
// [DataPointSummary.ParameterLabel].
//
// By default only parameters in the visibility-set are returned (those for
// which vis.Visible returns true). Callers that want the complete model can
// append ?include=all to bypass the filter. When vis is nil every parameter
// is returned regardless (no filter configured — backward-compatible).
func ListDataPoints(idx DeviceIndex, labels ParameterLabeler, vis filter.VisibilitySet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		includeAll := r.URL.Query().Get("include") == "all"
		// An operator-hidden channel (G12) is excluded from the operation
		// data-point list; ?include=all still returns it for management.
		if !includeAll && ch.IsHidden() {
			JSON(w, http.StatusOK, []DataPointSummary{})
			return
		}
		dps := ch.DataPoints()
		model := ""
		if d := ch.Device(); d != nil {
			model = d.Model
		}
		serial := serialSuffixForChannel(idx, ch)
		out := make([]DataPointSummary, 0, len(dps))
		for _, dp := range dps {
			// ch.Number is available — use VisibleForChannel for precise
			// MASTER channel-whitelist filtering.
			if !includeAll && vis != nil && !vis.VisibleForChannel(model, ch.Type, ch.Number, ch.ParamsetIn, dp.Parameter()) {
				continue
			}
			out = append(out, toDataPointSummary(dp, labels, ch, serial))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetDataPoint returns a single data point.
func GetDataPoint(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		param := hmenum.Parameter(chi.URLParam(r, "param"))
		dp := ch.Parameter(param)
		if dp == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Parameter not found", string(param)))
			return
		}
		JSON(w, http.StatusOK, toDataPointSummary(dp, labels, ch, serialSuffixForChannel(idx, ch)))
	}
}

// PutDataPointValue writes a new value to the channel's parameter.
//
// The write is routed through [device.Channel.Set] so the model is the
// single source of truth. The channel must have had its ChannelWriter
// installed during hydration; if not, Channel.Set returns
// [device.ErrNoChannelWriter] which maps to 503.
//
// The `writer` parameter is accepted for API compatibility but is NOT
// used on this path — the channel's installed writer handles dispatch.
// Callers that need the legacy direct-write path (integration tests,
// pre-hydration tooling) should call [DataPointWriter] directly.
func PutDataPointValue(idx DeviceIndex, _ DataPointWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		param := hmenum.Parameter(chi.URLParam(r, "param"))
		dp := ch.Parameter(param)
		if dp == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Parameter not found", string(param)))
			return
		}
		var req SetValueRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		// Coerce the raw JSON value against the descriptor's declared type
		// so a Float-typed LEVEL receiving the integer-valued JSON number
		// `1` lands as FloatValue, not IntValue. NewParamValue alone
		// drops the type context and silently collapses `1.0` to int,
		// which the downstream validator then rejects with
		// "want float, got int" → 502 on a write that should succeed.
		pv, pvErr := parameter.Coerce(dp.ParameterData(), req.Value)
		if pvErr != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Unsupported value type", pvErr.Error()))
			return
		}
		prio := parsePriority(req.Priority)
		opts := device.SetOptions{
			Validate:   true,
			Optimistic: true,
			Priority:   prio,
			Source:     "rest:PUT /value",
		}
		if err := ch.Set(r.Context(), hmenum.ParamsetKeyValues, param, pv, opts); err != nil {
			switch {
			case errors.Is(err, device.ErrNoChannelWriter):
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Writer not wired", err.Error()))
			case errors.Is(err, device.ErrValidation), errors.Is(err, device.ErrParameterNotWritable):
				// Client-side rejection (type / range / enum / length /
				// writability): the value never reached the wire, so this is a
				// 400, not a 502 upstream failure.
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeValidation, r, "Value rejected", err.Error()))
			case errors.Is(err, device.ErrUnknownParameter):
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Parameter not found", err.Error()))
			case errors.Is(err, device.ErrChannelOperationLocked):
				writeChannelLocked(w, r)
			case problem.IsUpstreamUnavailable(err):
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Upstream temporarily unavailable", err)
			default:
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Set failed", err)
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- helpers ---

// writeChannelLocked answers a control write that the operator's own
// channel lock rejected. The value never reached the wire and a retry
// cannot help until the lock is lifted, so this is a 423 Locked — the
// same status the edit-lock uses — and never a 502 upstream failure.
func writeChannelLocked(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusLocked,
		problem.New(problem.TypeConflict, r, "Channel locked",
			"the channel is locked against control writes; lift the lock via PUT /devices/{addr}/channels/{no}/flags"))
}

func lookupChannel(idx DeviceIndex, r *http.Request) (*device.Channel, error) {
	addr := chi.URLParam(r, "addr")
	d, ok := idx.Device(addr)
	if !ok {
		return nil, errors.Join(problem.ErrNotFound, errors.New("device "+addr))
	}
	numStr := chi.URLParam(r, "no")
	if numStr == "" {
		return nil, errors.Join(problem.ErrNotFound, errors.New("channel"))
	}
	chAddr := addr + ":" + numStr
	ch := d.Channel(chAddr)
	if ch == nil {
		return nil, errors.Join(problem.ErrNotFound, errors.New("channel "+chAddr))
	}
	return ch, nil
}

// serialSuffixForChannel resolves the routing-key serial suffix of the central
// owning ch's device, so the data-point converters can stamp the canonical
// unique_id. Returns "" when idx, the channel, its device or the central are
// unknown — callers then emit the omitempty field absent.
func serialSuffixForChannel(idx DeviceIndex, ch *device.Channel) string {
	if idx == nil || ch == nil {
		return ""
	}
	dev := ch.Device()
	if dev == nil {
		return ""
	}
	return idx.SerialSuffix(idx.CentralOf(dev.Address))
}

func toDeviceSummary(d *device.Device, centralName string, released bool) DeviceSummary {
	return DeviceSummary{
		Released:                   released,
		Address:                    d.Address,
		Central:                    centralName,
		Interface:                  string(d.Interface),
		InterfaceID:                d.InterfaceID,
		IseID:                      d.IseID,
		Model:                      d.Model,
		ModelLabel:                 d.ModelLabel,
		ModelIcon:                  d.ModelIcon,
		SubModel:                   d.SubModel,
		Name:                       d.Name(),
		Manufacturer:               string(d.Manufacturer),
		ProductGroup:               string(d.ProductGroup),
		IsAvailable:                d.Available(),
		ChannelsCount:              len(d.Channels()),
		Updatable:                  d.Updatable,
		UpdateAvailable:            d.UpdateAvailable(),
		UpdateStatus:               string(hmenum.DeriveDeviceUpdateStatus(d.Firmware().Info().UpdateState, d.UpdateAvailable())),
		Rooms:                      d.Rooms(),
		Functions:                  d.Functions(),
		MasterPushesConfigPending:  hmenum.PushesConfigPendingFor(d.Interface, d.ProductGroup),
		ConfigRestoreSupported:     d.Interface.SupportsConfigRestore(),
		CommunicationTestSupported: d.Interface.SupportsCommunicationTest(),
		TeamSupported:              d.Interface.SupportsTeams(),
		HasSubDevices:              d.HasSubDevices(),
		RxMode:                     rxModeInfo(d.RxMode),
	}
}

func toDataPointSummary(dp device.ParameterDataPoint, labels ParameterLabeler, ch *device.Channel, serialSuffix string) DataPointSummary {
	channelType := ""
	if ch != nil {
		channelType = ch.Type
	}
	raw, ok := dp.RawValue()
	pd := dp.ParameterData()
	s := DataPointSummary{
		Parameter: string(dp.Parameter()),
		Observed:  ok,
		Operations: DataPointSummaryOps{
			Read:  pd.IsReadable(),
			Write: pd.IsWritable(),
			Event: pd.IsEvent(),
		},
	}
	// Stamp the canonical loom routing key — identical to the WS
	// value-changed payload — so a client builds its entity registry from the
	// summary/snapshot without recomputing the algorithm. Empty serialSuffix
	// (central serial not yet known) leaves the omitempty field absent.
	if serialSuffix != "" {
		k := dp.DataPointKey()
		s.UniqueID = routingkey.CanonicalUniqueID(serialSuffix, k.ChannelAddress, string(dp.Parameter()), "")
	}
	// Channel-typed lookup wins when the labeler supports it — so e.g.
	// `POWER` on `ENERGIE_METER_TRANSMITTER` resolves to "Wirkleistung"
	// instead of the bare-parameter "Leistung". Falls back to the
	// un-typed translation, then to the title-cased parameter, so the
	// field is always ready to render.
	s.ParameterLabel = resolvedParameterLabel(labels, channelType, s.Parameter)
	// TranslatedName + LabelOmitted resolve through the same primitives
	// as the MQTT discovery builder (device.TranslatedDataPointLabel →
	// naming.EntityDisplayName), so REST and MQTT consumers spawn
	// entities with identical names.
	if t, ok := labels.(device.ParameterTranslator); ok && ch != nil {
		nd, labelOmitted := device.TranslatedDataPointNameData(ch, s.Parameter, channelType, t)
		s.TranslatedName, s.LabelOmitted = naming.ComposedEntityName(nd, labelOmitted, s.Parameter)
	}
	// Category + functional type let a client classify the DP declaratively.
	// Same assertion pattern as CustomDPSummary / calculated_data_points.go:
	// every concrete generic.DataPoint implements CategorisedDataPoint.
	if cdp, ok := dp.(device.CategorisedDataPoint); ok {
		cat := cdp.Category()
		s.Category = string(cat)
		s.DataPointType = string(hmenum.CategoryToType[cat])
	}
	// Usage carries the pipeline's visibility verdict (forced sensors,
	// un-ignore overrides, HIDDEN_PARAMETERS, custom-DP absorption).
	if u, ok := dp.(interface{ Usage() hmenum.DataPointUsage }); ok {
		s.Usage = string(u.Usage())
	}
	s.Control = pd.Control
	s.Type = string(pd.Type)
	// Surface the canonical (cleaned) unit so REST consumers see the same
	// string as the direct-CCU twin — the reference stack applies the same
	// _fix_unit normalisation (100% → %, LEVEL → %, …). The DataPoint's Unit()
	// method runs that cleanup; fall back to the raw descriptor unit for DPs
	// that do not implement it.
	if u, ok := dp.(interface{ Unit() string }); ok {
		s.Unit = u.Unit()
	} else {
		s.Unit = pd.Unit
	}
	// Same non-trivial-only gate as the MQTT raw-plane config payload
	// (internal/model/generic/payload.go): a DP without a Multiplier()
	// method, or one that resolves to the identity multiplier, leaves
	// the field at its zero value so omitempty drops it.
	if m, ok := dp.(interface{ Multiplier() float64 }); ok {
		if mult := m.Multiplier(); mult != 0 && mult != 1.0 {
			s.Multiplier = mult
		}
	}
	if len(pd.ValueList) > 0 {
		s.ValueList = pd.ValueList
		s.ValueTranslations = resolvedValueTranslations(labels, channelType, s.Parameter, pd.ValueList)
	}
	if len(pd.Min) > 0 {
		s.Min = pd.Min
	}
	if len(pd.Max) > 0 {
		s.Max = pd.Max
	}
	if len(pd.Default) > 0 {
		s.Default = pd.Default
	}
	// Compute the UI hint once per DP. HintFor is pure and never
	// returns an empty value, so the SPA's AutoTile always has
	// something to render even for completely unknown DPs.
	hint := hmui.HintFor(s.Parameter, s.Unit, s.Type, s.ValueList)
	s.UIHint = &hint
	if ok {
		s.Value = raw
		// s.Multiplier is only ever non-zero for a non-trivial multiplier
		// (the gate above), so DisplayValue and Multiplier agree on when
		// the projection applies without a second lookup.
		if dv, dvOK := generic.DisplayValue(raw, s.Multiplier); dvOK {
			s.DisplayValue = dv
		}
	}
	if t := dp.ModifiedAt(); !t.IsZero() {
		s.ModifiedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
		s.LastChangedAt = s.ModifiedAt
	}
	// Lifecycle source + freshness timestamps land on every DP that
	// implements the wire-side state machine (generic.DataPoint).
	// Unobserved DPs report source=unobserved but no timestamps so
	// the JSON stays compact.
	if src, ok := dp.(interface {
		Source() hmenum.ValueSource
		LastSeenAt() time.Time
	}); ok {
		s.Source = string(src.Source())
		if t := src.LastSeenAt(); !t.IsZero() {
			s.LastSeenAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
			if age := time.Since(t); age >= 0 {
				s.ValueAgeSeconds = int64(age / time.Second)
			}
		}
	}
	// Enriched model metadata (battery type/quantity/limits, …) via the
	// optional-capability assertion — only DPs that override it (e.g. the
	// calculated operating-voltage sensor) contribute; plain scalars leave
	// the field absent. Mirrors the per-DP MQTT state's additional_information.
	if ai, ok := dp.(interface{ AdditionalInformation() map[string]any }); ok {
		if m := ai.AdditionalInformation(); len(m) > 0 {
			s.AdditionalInformation = m
		}
	}
	return s
}

const (
	// maxPerPage is the largest per_page a list endpoint honours; larger
	// values fall back to the default rather than being clamped.
	maxPerPage = 500
	// maxPage bounds page so the `(page-1)*per_page` slice arithmetic every
	// list handler performs cannot overflow. The OpenAPI parameter declares
	// a minimum and no maximum, so a probe could send a page whose product
	// wrapped negative — both `start > total` clamps are no-ops for a
	// negative bound, and the slice expression panicked the request. int is
	// 32 bits on the armv7 build, so the ceiling is derived from MaxInt32
	// and leaves room for the `start + per_page` end bound.
	maxPage = math.MaxInt32/maxPerPage - 1
)

// parsePagination reads the optional `page` / `per_page` query parameters,
// falling back to the defaults for anything out of range. Both results are
// bounded so the caller's slice arithmetic stays representable; see [maxPage].
func parsePagination(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = min(n, maxPage)
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= maxPerPage {
			perPage = n
		}
	}
	return page, perPage
}

func parsePriority(s string) hmenum.CommandPriority {
	switch s {
	case "critical":
		return hmenum.CommandPriorityCritical
	case "high":
		return hmenum.CommandPriorityHigh
	case "low":
		return hmenum.CommandPriorityLow
	}
	return hmenum.CommandPriorityHigh
}
