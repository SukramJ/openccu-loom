// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmui"
)

// DeviceIndex is the narrow facade the device endpoints depend on.
// Implementations live in the central layer and translate address
// lookups into model.Device access.
type DeviceIndex interface {
	Devices() []*device.Device
	Device(address string) (*device.Device, bool)
	// CentralOf returns the name of the central unit owning the
	// device. Empty string when the device is unknown — handlers
	// surface that as an empty `central` field rather than an error.
	CentralOf(address string) string
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
	IseID         int    `json:"ise_id,omitempty"`
	Model         string `json:"model"`
	ModelLabel    string `json:"model_label,omitempty"`
	ModelIcon     string `json:"model_icon,omitempty"`
	SubModel      string `json:"sub_model,omitempty"`
	Name          string `json:"name"`
	Manufacturer  string `json:"manufacturer,omitempty"`
	ProductGroup  string `json:"product_group,omitempty"`
	IsAvailable   bool   `json:"available"`
	ChannelsCount int    `json:"channels_count"`
	// Updatable reports whether the device *supports* firmware updates
	// (CCU UPDATABLE capability) — NOT whether one is pending.
	Updatable bool `json:"updatable"`
	// UpdateAvailable reports whether an installable firmware update is
	// actually pending — the gated latest version differs from the installed
	// one (image already delivered for HmIP-RF / available for BidCos). The
	// UI flags "update available" on this, not on Updatable.
	UpdateAvailable bool     `json:"update_available"`
	Rooms           []string `json:"rooms,omitempty"`
	Functions       []string `json:"functions,omitempty"`
	// MasterPushesConfigPending is true when the device's interface
	// delivers reliable CONFIG_PENDING events on MASTER writes — the
	// SPA then waits for the true→false transition before refreshing
	// MASTER (HmIP-RF, which serves both RF and Wired devices). On
	// the others (BidCos-*,
	// VirtualDevices, CUxD) CONFIG_PENDING is either silent or
	// unreliable; the SPA falls back to a save-path reload (mirrors
	// Sourced from
	// `hmenum.Interface.PushesConfigPending`.
	MasterPushesConfigPending bool `json:"master_pushes_config_pending"`

	// HasSubDevices mirrors [device.Device.HasSubDevices] so SPA / WS
	// consumers can apply the same per-channel-group split the MQTT
	// bridge does under the `sub_devices_enabled` toggle.
	HasSubDevices bool `json:"has_sub_devices"`
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
}

// DataPointSummary is one entry in `GET .../data-points`.
type DataPointSummary struct {
	Parameter      string `json:"parameter"`
	ParameterLabel string `json:"parameter_label,omitempty"`
	Value          any    `json:"value"`
	Observed       bool   `json:"observed"`
	ModifiedAt     string `json:"modified_at,omitempty"`
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
	// TranslatedName is the locale-aware per-entity name HA assigns to
	// this data point — identical to the MQTT discovery `name` field
	// (both resolve through naming.EntityDisplayName). It is the
	// parameter portion only; HA prepends the device name. Empty when
	// LabelOmitted is true.
	TranslatedName string `json:"translated_name,omitempty"`
	// LabelOmitted is true when the parameter is flagged "primary" in
	// the embedded translation_custom catalogue. Consumers then collapse
	// the entity name to the device name alone (MQTT emits `name: null`).
	LabelOmitted bool `json:"label_omitted,omitempty"`
	// Control is the CCU paramset descriptor's CONTROL attribute,
	// of the form WIDGET_FAMILY.SLOT (e.g. "HEATING_CONTROL_HMIP.SETPOINT",
	// "DIMMER.LEVEL"). Drives the SPA's CONTROL-aware widget resolver
	// under assets/ui/src/lib/control/ — see docs/ui/control-widget-concept.md.
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
	// Unit is the parameter descriptor's UNIT string ("°C", "%",
	// "mA", "Hz", "Wh", ...) as the CCU declares it. Empty when the
	// descriptor carries no unit.
	Unit string `json:"unit,omitempty"`
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
	// See docs/ui/auto-tile-concept.md.
	UIHint *hmui.Hint `json:"ui_hint,omitempty"`
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
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Refresh failed", err.Error()))
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
				for _, f := range d.Functions {
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
				for _, r := range d.Rooms {
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

// ParameterLabeler is the optional translator the data-point
// endpoints consult for a human-readable parameter name. It is
// locale-scoped: the concrete implementation captures the active
// locale so handlers stay language-agnostic.
type ParameterLabeler interface {
	ParameterLabel(parameter string) string
}

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
			if nameFilter != "" && !strings.Contains(strings.ToLower(d.Name), nameFilter) {
				continue
			}
			if addrFilter != "" && !strings.Contains(strings.ToLower(d.Address), addrFilter) {
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
			out = append(out, toDeviceSummary(d, idx.CentralOf(d.Address)))
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
			DeviceSummary: toDeviceSummary(d, idx.CentralOf(d.Address)),
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
		Name:         ch.Name,
		ParamsetKey:  string(ch.ParamsetIn),
		ParamsetKeys: channelParamsetKeys(ch),
		DataPoints:   ch.Len(),
	}
	if cdp := ch.CustomDataPoint(); cdp != nil {
		s.CustomDpName = cdp.DataPointKey().Parameter
	}
	if ch.GroupNo != 0 {
		s.GroupNo = ch.GroupNo
		s.IsGroupMaster = ch.IsGroupMaster()
		if ch.IsInMultiGroup() {
			s.IsInMultiGroup = true
			s.SubDeviceName = ch.SubDeviceName()
		}
	}
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
		dps := ch.DataPoints()
		model := ""
		if d := ch.Device(); d != nil {
			model = d.Model
		}
		out := make([]DataPointSummary, 0, len(dps))
		for _, dp := range dps {
			// ch.Number is available — use VisibleForChannel for precise
			// MASTER channel-whitelist filtering.
			if !includeAll && vis != nil && !vis.VisibleForChannel(model, ch.Type, ch.Number, ch.ParamsetIn, dp.Parameter()) {
				continue
			}
			out = append(out, toDataPointSummary(dp, labels, ch))
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
		JSON(w, http.StatusOK, toDataPointSummary(dp, labels, ch))
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
			problem.Write(w, http.StatusBadRequest,
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
			if errors.Is(err, device.ErrNoChannelWriter) {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Writer not wired", err.Error()))
				return
			}
			if problem.IsUpstreamUnavailable(err) {
				problem.Write(w, http.StatusBadGateway,
					problem.New(problem.TypeUpstreamUnavailable, r, "Upstream temporarily unavailable", err.Error()))
				return
			}
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Set failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// --- helpers ---

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

func toDeviceSummary(d *device.Device, centralName string) DeviceSummary {
	return DeviceSummary{
		Address:                   d.Address,
		Central:                   centralName,
		Interface:                 string(d.Interface),
		InterfaceID:               d.InterfaceID,
		IseID:                     d.IseID,
		Model:                     d.Model,
		ModelLabel:                d.ModelLabel,
		ModelIcon:                 d.ModelIcon,
		SubModel:                  d.SubModel,
		Name:                      d.Name,
		Manufacturer:              string(d.Manufacturer),
		ProductGroup:              string(d.ProductGroup),
		IsAvailable:               d.Available(),
		ChannelsCount:             len(d.Channels()),
		Updatable:                 d.Updatable,
		UpdateAvailable:           d.UpdateAvailable(),
		Rooms:                     d.Rooms,
		Functions:                 d.Functions,
		MasterPushesConfigPending: hmenum.PushesConfigPendingFor(d.Interface, d.ProductGroup),
		HasSubDevices:             d.HasSubDevices(),
	}
}

func toDataPointSummary(dp device.ParameterDataPoint, labels ParameterLabeler, ch *device.Channel) DataPointSummary {
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
	// Channel-typed lookup wins when the labeler supports it — so e.g.
	// `POWER` on `ENERGIE_METER_TRANSMITTER` resolves to "Wirkleistung"
	// instead of the bare-parameter "Leistung". Falls back to the
	// un-typed translation when the channel-type entry is missing.
	s.ParameterLabel = channelTypedParameterLabel(labels, channelType, s.Parameter)
	// TranslatedName + LabelOmitted resolve through the same primitives
	// as the MQTT discovery builder (device.TranslatedDataPointLabel →
	// naming.EntityDisplayName), so REST and MQTT consumers spawn
	// entities with identical names.
	if t, ok := labels.(device.ParameterTranslator); ok && ch != nil {
		label, labelOmitted := device.TranslatedDataPointLabel(ch, s.Parameter, channelType, t)
		s.TranslatedName, s.LabelOmitted = naming.EntityDisplayName(label, labelOmitted, s.Parameter)
	}
	// Category + functional type let a client classify the DP declaratively.
	// Same assertion pattern as CustomDPSummary / calculated_data_points.go:
	// every concrete generic.DataPoint implements CategorisedDataPoint.
	if cdp, ok := dp.(device.CategorisedDataPoint); ok {
		cat := cdp.Category()
		s.Category = string(cat)
		s.DataPointType = string(hmenum.CategoryToType[cat])
	}
	s.Control = pd.Control
	s.Type = string(pd.Type)
	s.Unit = pd.Unit
	if len(pd.ValueList) > 0 {
		s.ValueList = pd.ValueList
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
	return s
}

func parsePagination(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 500 {
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
