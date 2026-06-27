// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// QoSProfile is the per-category default. Individual topics can
// override via the bridge config (not yet implemented — MVP uses
// the defaults).
type QoSProfile struct {
	State     QoS
	Commands  QoS
	Discovery QoS
}

// DefaultQoS mirrors the policy documented in ADR 0011.
var DefaultQoS = QoSProfile{
	State:     QoS0,
	Commands:  QoS1,
	Discovery: QoS1,
}

// All MQTT state topics carry a JSON envelope
// (`{"value": <v>, "available": <bool>, "modified_at": "<rfc3339>"}`).
// The earlier bare-scalar mode was retired as part of the ADR-0011
// payload unification — every consumer reads `value_json.value`.

// VisibilitySet is the narrow gate the bridge consults before each
// publish. A parameter that is not visible is silently dropped — no
// error, no MQTT message. Nil means "all visible" (no filter).
// Implement using [github.com/SukramJ/openccu-loom/internal/north/filter.Adapter]
// or any struct that satisfies both methods.
type VisibilitySet interface {
	// Visible reports whether (model, channelType, paramset, parameter)
	// should be published.
	Visible(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool

	// VisibleForChannel is like Visible but accepts the concrete channel
	// number for MASTER-paramset rules that vary per-channel.
	VisibleForChannel(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool
}

// BridgeConfig controls which planes are active and the topic base.
type BridgeConfig struct {
	Base                 string
	CentralName          string
	RawEnabled           bool
	HADiscoveryEnabled   bool
	QoS                  QoSProfile
	DiscoveryBuilder     DiscoveryBuilder // optional, may be nil
	DiscoveryObjectIDFmt string           // defaults to "{addr}_{channel}_{parameter}"

	// SubDevicesEnabled toggles the per-channel-group sub-device split
	// in the HA discovery `device` block. When true, multi-channel-group
	// devices (HmIP-DRBLI4, HmIP-DRSI4, …) appear as one HA parent
	// device with N sub-devices instead of one flat device with N×K
	// entities.
	SubDevicesEnabled bool

	// LegacyAlias mirrors PublishState + PublishAvailability under
	// The
	// same data during migration. Disabled by default.
	LegacyAlias LegacyAliasConfig

	// Visibility, when non-nil, gates every PublishState call: if the
	// parameter is not visible the publish is silently skipped. Nil
	// disables the gate (all parameters pass through).
	Visibility VisibilitySet

	// Collector, when non-nil, receives per-publish counter increments
	// for messages_sent, discovery_sent and publish_errors. Mirrors the
	// Python py:10).
	// Nil disables instrumentation (no-op).
	Collector *metrics.MqttCollector

	// HealthSupplier is invoked by [Bridge.AnnounceOnline] to compose
	// the JSON body for the retained `<base>/bridge/health` topic.
	// When nil the bridge emits a minimal `{"status":"online"}` so
	// dashboards still see liveness. Production wires this to a
	// daemon-side function that returns build metadata, the boot
	// timestamp, configured central names, and any other operator-
	// visible state.
	HealthSupplier func() map[string]any
}

// DiscoveryBuilder describes the HA payload for one device channel's
// data point. Bridges that don't speak HA Discovery may leave it nil.
//
// The returned `nodeID` and `objectID` are concatenated into the HA
// Discovery topic as `homeassistant/<component>/<nodeID>/<objectID>/config`.
// Builders should use `nodeID` to group entities of one physical
// device (matching HA's device-card grouping convention) and
// `objectID` to disambiguate entities within that device.
type DiscoveryBuilder interface {
	Build(event Event) (component, nodeID, objectID string, payload []byte, ok bool)
}

// Event is the input the bridge translates into wire publishes.
type Event struct {
	// Source, when non-nil, lets the discovery aggregator read the
	// channel's semantic state straight from the model instead of
	// driving every HA-discovery field through a per-parameter wire
	// topic + Jinja template. ADR 0007 step 10.
	Source pload.Source

	Central        string
	Interface      string
	DeviceAddress  string
	DeviceName     string
	Model          string
	ChannelNo      int
	ChannelAddress string
	// ChannelType is the CCU channel-type marker
	// (CLIMATECONTROL_RT_TRANSCEIVER, BLIND_VIRTUAL_RECEIVER, …).
	// Drives the channel-aware Custom-DP-Aggregation in the default
	// Discovery builder — when a known custom-domain channel-type is
	// recognised, one HA entity is emitted per channel instead of one
	// per parameter. Empty string falls back to the per-parameter
	// heuristic.
	ChannelType string
	Parameter   string
	// Category is the model-derived [hmenum.DataPointCategory] of the
	// originating data point. The discovery builder uses it to derive
	// the HA component (binary_sensor / sensor / number / switch /
	// climate / cover / lock / light / valve / siren / event / button
	// / select / text / update). Per ADR 0011, every Source must
	// populate this field — the bridge no longer falls back to
	// parameter-name heuristics. An empty (zero-value) Category means
	// "no model classification available"; the discovery builder will
	// return ok=false and skip HA Discovery emission for the event.
	Category hmenum.DataPointCategory
	Value    any

	// Writable carries the descriptor's effective writable flag (bit 1 of
	// OPERATIONS) so the per-parameter Discovery classifier can distinguish
	// read-only STATE (`binary_sensor`) from writable STATE (`switch`) without
	// re-reading the channel descriptor. Defaults to false; the EventBridge sets
	// it from the DP it just observed.
	Writable bool

	// Usage carries the model's [hmenum.DataPointUsage] verdict for the
	// originating data point — the same value the REST plane surfaces as
	// `DataPointSummary.usage`. The per-parameter discovery path consults
	// it to suppress entities the reference stack never creates:
	// `no_create` / `ignored` DPs and the `ce_primary` / `ce_secondary`
	// constituents that are absorbed by the channel's custom-DP
	// aggregate. The zero value (empty string) means "no verdict
	// available" and passes the gate (synthetic events, calculated DPs).
	Usage hmenum.DataPointUsage

	// Device, when non-nil, is consulted by the
	// [DefaultDiscoveryBuilder] for its `info_payload` map. Any
	// struct that carries `payload:"info"` tags works — the
	// internal/payload package does the partitioning.
	Device any
	// Channel exposes the originating Channel for HA-Discovery
	// aggregation. The aggregator queries it for the parameter set
	// to decide whether to emit auxiliary topics (LEVEL_2 for tilt,
	// HUMIDITY for thermostat) — without it the aggregator falls
	// back to its assumed-present heuristics.
	Channel ChannelInspector

	// Calculated marks the event as carrying a calculated/derived data
	// point (DEW_POINT, ENTHALPY, OPERATING_VOLTAGE_LEVEL, …) rather
	// than a wire VALUES parameter. The discovery builder uses this to
	// route `state_topic` to `calculated/<name>` instead of
	// `values/<name>`; without it HA reads an empty topic and renders
	// the entity as `unavailable` even though the calculator is
	// actively publishing on the calculated bucket. Mirrors the
	// `BucketCalculated` routing in [EventBridge.publishSlotState].
	Calculated bool

	// Descriptor is the canonical typed source of truth for every
	// descriptor-derived field the discovery builder and the per-DP
	// /config topic consume. Nil means "no descriptor available" —
	// the discovery builder then skips the descriptor-dependent
	// fields (min/max/options/unit_of_measurement/paramset/label)
	// gracefully.
	//
	// Holds one of the typed structs from `internal/payload`
	// (GenericConfig for per-parameter wire DPs, ClimateConfig /
	// LockConfig / … for the custom-DP aggregates). Consumers
	// type-switch on the concrete value.
	Descriptor pload.ConfigPayload
}

// genericDesc returns the descriptor as a [pload.GenericConfig]
// when the event carries a wire-DP descriptor; nil otherwise. Used
// by helpers that only need the generic fields.
func (e Event) genericDesc() *pload.GenericConfig {
	if e.Descriptor == nil {
		return nil
	}
	if d, ok := e.Descriptor.(*pload.GenericConfig); ok {
		return d
	}
	return nil
}

// descMin returns the descriptor minimum bound, or nil when the
// event carries no GenericConfig descriptor.
func (e Event) descMin() *float64 {
	if d := e.genericDesc(); d != nil {
		return d.Min
	}
	return nil
}

// descMax mirrors descMin for the upper bound.
func (e Event) descMax() *float64 {
	if d := e.genericDesc(); d != nil {
		return d.Max
	}
	return nil
}

// descDefault returns the descriptor default value. GenericConfig.Default
// is typed `any` so any numeric value the source supplied is preserved.
func (e Event) descDefault() any {
	if d := e.genericDesc(); d != nil {
		return d.Default
	}
	return nil
}

// descValueList returns the descriptor enum options list.
func (e Event) descValueList() []string {
	if d := e.genericDesc(); d != nil {
		return d.ValueList
	}
	return nil
}

// descUnit returns the descriptor unit string.
func (e Event) descUnit() string {
	if d := e.genericDesc(); d != nil {
		return d.Unit
	}
	return ""
}

// descType returns the descriptor wire type.
func (e Event) descType() hmenum.ParameterType {
	if d := e.genericDesc(); d != nil {
		return d.Type
	}
	return ""
}

// descParamset returns the descriptor paramset key.
func (e Event) descParamset() hmenum.ParamsetKey {
	if d := e.genericDesc(); d != nil {
		return d.Paramset
	}
	return ""
}

// descLabel returns the localised parameter label.
func (e Event) descLabel() string {
	if d := e.genericDesc(); d != nil {
		return d.Label
	}
	return ""
}

// descLabelOmitted reports whether the parameter is marked as
// "primary" in the embedded translation_custom catalogue
// (translation key present, value is the empty string). HA discovery
// emits `name: null` in the payload so the entity id collapses to
// the device name alone — the same effect HA-native integrations
// achieve through `_attr_translation_key` plus an HA-translation
// entry `"name": ""`.
func (e Event) descLabelOmitted() bool {
	if d := e.genericDesc(); d != nil {
		return d.LabelOmitted
	}
	return false
}

// ChannelInspector is the narrow read-side contract on a channel
// the MQTT aggregator needs. Implemented by `internal/model/device`.
// Defining it here keeps the bridge package free of a domain import.
type ChannelInspector interface {
	HasParameter(name string) bool
}

// ChannelNamer is the optional extension on ChannelInspector that exposes the
// CCU-operator-assigned channel name. Used by the HA-Discovery name builder
// for press-event entities so the device card surfaces "Taster Wohnzimmer
// oben links" instead of a bare `ch1`. Falls back silently to the channel
// number when not satisfied (test fakes, channels without a CCU name).
type ChannelNamer interface {
	ChannelName() string
}

// SubDeviceInspector is the optional ChannelInspector extension that lets
// the HA-Discovery builder construct a sub-device `device` block. Channels
// that satisfy it (production: `*device.Channel`) expose enough state to
// compute the sub-device identifier + name. The full sub-device pipeline
// additionally requires the parent device (the `Event.Device` field) to
// satisfy [deviceWithSubDevices] so its `HasSubDevices()` toggle can be
// honoured.
type SubDeviceInspector interface {
	GroupNumber() int
	IsInMultiGroup() bool
	SubDeviceName() string
}

// CustomDPNamingInspector is an optional extension on ChannelInspector
// that exposes the custom-DP primary/secondary classification used by
// the HA-Discovery name builder. Channels that satisfy it drive the
// Naming convention
//
//   - HasSinglePrimaryCustomDP=true  → entity name is empty
//     (HA falls back to `device.name`); used when the device has
//     exactly one primary custom-DP (HmIP-BWTH climate, HmIP-eTRV
//     climate, HmIP-FSI16 switch, …).
//   - IsCustomDPPrimaryChannel=true (and not the only-primary case) →
//     entity name `ch<N>`; used when several independent custom-DPs
//     share the device (HmIP-PSM ch3/ch4/ch5, HmIP-DRSI4 …).
//   - IsCustomDPSecondaryChannel=true → entity name `vch<N>` and
//     `enabled_by_default: false`; used for the secondary mirror
//     channels declared via `secondary_channels` in the profile
//     config (HmIP-BSL bicolor light, IP_DIMMER virtual channels).
//
// `*device.Channel` implements this interface natively. Other
// inspectors (test fakes, narrow shims) may omit it; the name
// builder falls back to the legacy "channel-number-as-string" path
// in that case.
type CustomDPNamingInspector interface {
	IsCustomDPPrimaryChannel() bool
	IsCustomDPSecondaryChannel() bool
	HasSinglePrimaryCustomDP() bool
}

// IgnoreMultipleChannelsForNameInspector is an optional extension on
// the channel inspector consulted by [displayChannelName]. When the
// channel implements it and returns true, the discovery name builder
// treats the channel as if it were the only primary even if the
// device hosts multiple primaries of the same custom-DP category.
// Lock sets it to true so multi-channel locks render as
// "<Lock>" / "<Lock>" rather than "<Lock> ch1" / "<Lock> ch2".
type IgnoreMultipleChannelsForNameInspector interface {
	IgnoreMultipleChannelsForName() bool
}

// ParameterLabeler resolves a (channelType, parameter) pair to its
// localised human-readable label (e.g. `RSSI_DEVICE` →
// `"Signalstärke Gerät"`). Empty return from [ParameterLabel] means
// "no translation available — fall back to a title-cased parameter
// name".
//
// [ParameterLabelOk] is the (label, found) variant. The second
// return value lets callers distinguish a missing entry from an
// explicit-empty translation. An explicit-empty entry signals that
// the parameter is "primary"; HA discovery emits `name: null` in
// the payload so HA renders friendly_name + entity_id from the
// device name alone.
//
// Implementations wrap the OCCU translation archive
// (`*ccudata.Translations`). A nil [ParameterLabeler] is treated as
// "no labels" by callers.
type ParameterLabeler interface {
	ParameterLabel(channelType, parameter string) string
	ParameterLabelOk(channelType, parameter string) (string, bool)
}

// Bridge orchestrates the raw + HA Discovery planes atop a shared
// [Publisher].
type Bridge struct {
	cfg      BridgeConfig
	topics   *TopicBuilder
	legacy   *LegacyTopicBuilder // nil when LegacyAlias.Enabled = false
	client   Publisher
	mu       sync.Mutex
	declared map[string][]byte // discovery topic → last published payload
	// configCache diff-gates ADR 0011 `/config` companion publishes.
	// Config payloads are static descriptor projections (min/max/
	// value_list/unit for generic DPs; modes/preset_modes/min_temp/…
	// for custom-DPs) so they don't change between most events.
	// Without diff-gating every value event would re-publish identical
	// bytes — the broker would normally dedup retained-message
	// rebroadcasts to clients, but a bridge-side gate keeps the
	// outbound traffic genuinely small.
	configCache map[string][]byte
	// collector is the optional MqttCollector for per-bridge counters.
	// Nil when no collector was wired in BridgeConfig.Collector.
	collector *metrics.MqttCollector
}

// NewBridge constructs the bridge.
func NewBridge(cfg BridgeConfig, client Publisher) *Bridge {
	if cfg.QoS == (QoSProfile{}) {
		cfg.QoS = DefaultQoS
	}
	var legacy *LegacyTopicBuilder
	if cfg.LegacyAlias.Enabled {
		legacy = NewLegacyTopicBuilder(cfg.LegacyAlias.Base)
	}
	topics := NewTopicBuilder(cfg.Base)
	// Auto-wire the default HA Discovery builder when discovery is
	// enabled but no builder was injected. Without this the
	// `cfg.HADiscoveryEnabled && cfg.DiscoveryBuilder != nil` gate in
	// PublishState always evaluates false — discovery configs are
	// never published even though the operator turned discovery on.
	if cfg.HADiscoveryEnabled && cfg.DiscoveryBuilder == nil {
		cfg.DiscoveryBuilder = NewDefaultDiscoveryBuilder(topics, cfg.CentralName).
			WithSubDevices(cfg.SubDevicesEnabled)
	}
	return &Bridge{
		cfg:         cfg,
		topics:      topics,
		legacy:      legacy,
		client:      client,
		declared:    make(map[string][]byte),
		configCache: make(map[string][]byte),
		collector:   cfg.Collector,
	}
}

// SetHubInfo updates the central-level metadata stored on the
// builder. Subsequent discovery payloads (per-device + hub
// entities) carry the populated `sw_version`, `serial_number` and
// `configuration_url` fields. Pass an empty [HubInfo] to clear.
//
// Single-CCU helper: sets the default HubInfo applied when no
// per-central entry exists. Multi-CCU deployments should call
// [SetHubInfoFor] for each registered central instead.
//
// No-op when the bridge runs without a [DefaultDiscoveryBuilder]
// (e.g. tests that inject a custom builder).
func (b *Bridge) SetHubInfo(info HubInfo) {
	if b == nil || b.cfg.DiscoveryBuilder == nil {
		return
	}
	if dd, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder); ok {
		dd.WithHubInfo(info)
	}
}

// SetHubInfoFor registers per-central CCU metadata so a multi-CCU
// daemon emits the right device-block (Name / Model / sw_version /
// serial / configuration_url) for each CCU's hub + per-device
// discovery payloads. central must match the value the daemon
// passes into discovery-builder calls (i.e. Unit.Name()).
func (b *Bridge) SetHubInfoFor(centralName string, info HubInfo) {
	if b == nil || b.cfg.DiscoveryBuilder == nil {
		return
	}
	if dd, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder); ok {
		dd.SetHubInfoFor(centralName, info)
	}
}

// DefaultBuilder returns the bridge's [DefaultDiscoveryBuilder] when
// the configured DiscoveryBuilder is the default implementation, nil
// otherwise. Hub-entity publishers MUST use this shared instance (not
// a fresh builder) so the per-central HubInfo registered via
// [Bridge.SetHubInfoFor] — most importantly the CCU serial that feeds
// every hub unique_id — is visible to the hub discovery builders.
func (b *Bridge) DefaultBuilder() *DefaultDiscoveryBuilder {
	if b == nil || b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	if dd, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder); ok {
		return dd
	}
	return nil
}

// RepublishDiscovery re-emits every cached Discovery config. Used by
// the HA-Birth-Sync hook: when HA emits `homeassistant/status: online`
// after a restart the broker has the retained configs but HA may not
// pick them up reliably across some firmwares — a deterministic
// re-publish closes that race.
func (b *Bridge) RepublishDiscovery(ctx context.Context) error {
	b.mu.Lock()
	snapshot := make(map[string][]byte, len(b.declared))
	maps.Copy(snapshot, b.declared)
	b.mu.Unlock()
	for topic, payload := range snapshot {
		if err := b.client.Publish(ctx, topic, payload, b.cfg.QoS.Discovery, true); err != nil {
			return err
		}
	}
	return nil
}

// AnnounceOnline publishes the "online" LWT counterpart and a
// retained health snapshot to `<base>/bridge/health`. The broker
// client must have been configured with the matching offline LWT.
//
// The health body is composed by [BridgeConfig.HealthSupplier];
// without a supplier the bridge emits the minimal
// `{"status":"online"}` payload so dashboards still see liveness.
// The supplier is invoked synchronously — it must not block.
func (b *Bridge) AnnounceOnline(ctx context.Context) error {
	if err := b.client.Publish(ctx, b.topics.BridgeStatus(), []byte("online"), QoS1, true); err != nil {
		return err
	}
	health := map[string]any{"status": "online"}
	if b.cfg.HealthSupplier != nil {
		if extra := b.cfg.HealthSupplier(); extra != nil {
			maps.Copy(health, extra)
			// "status" must stay authoritative — suppliers cannot
			// shadow the LWT signal.
			health["status"] = "online"
		}
	}
	body, err := json.Marshal(health)
	if err != nil {
		return nil //nolint:nilerr // health is best-effort
	}
	_ = b.client.Publish(ctx, b.topics.BridgeHealth(), body, b.cfg.QoS.State, true)
	return nil
}

// PublishState publishes a device data point's current value to the
// raw plane and — when enabled — emits the corresponding HA
// Discovery config (idempotent per topic).
//
// When LegacyAlias is enabled the same payload is mirrored under
// The The mirror is best-effort: a
// publish error on the legacy topic is logged but does not roll
// back the primary publish, because the legacy tree is by definition
// secondary.
func (b *Bridge) PublishState(ctx context.Context, ev Event) error {
	// Visibility gate: skip the entire publish (raw + discovery) when the
	// parameter is not allowed. Returns nil — a not-visible parameter is not
	// an error.
	if b.cfg.Visibility != nil {
		if !b.cfg.Visibility.VisibleForChannel(ev.Model, ev.ChannelType, ev.ChannelNo, hmenum.ParamsetKeyValues, hmenum.Parameter(ev.Parameter)) {
			return nil
		}
	}
	// Per-parameter raw state publish lives on
	// [EventBridge.publishSlotState] / [Bridge.PublishSlotState],
	// which owns the canonical `<addr>/<ch>/<bucket>/<param>` shape
	// and emits the full PerDPState envelope. PublishState here only
	// Handles the legacy_alias mirror
	// and the HA-Discovery payload publish. The custom-DP slot
	// publish (`<addr>/<ch>/custom/<kind>`) is owned by
	// [EventBridge.publishCustomDPState] — not this method.
	if b.cfg.RawEnabled && b.legacy != nil {
		payloadBytes, err := b.renderStatePayload(ev)
		if err != nil {
			return err
		}
		legacyTopic := b.legacy.DataPointState(ev.DeviceAddress, ev.ChannelNo, ev.Parameter)
		// Best-effort mirror — the canonical PerDPState publish via
		// PublishSlotState is the source of truth.
		_ = b.client.Publish(ctx, legacyTopic, payloadBytes, b.cfg.QoS.State, true)
	}
	if b.cfg.HADiscoveryEnabled && b.cfg.DiscoveryBuilder != nil {
		component, nodeID, objectID, cfgPayload, ok := b.cfg.DiscoveryBuilder.Build(ev)
		if ok {
			if err := b.publishDiscovery(ctx, component, nodeID, objectID, cfgPayload); err != nil {
				b.incPublishErrors()
				return err
			}
		}
		b.publishPressButton(ctx, ev)
	}
	return nil
}

// PublishDiscoveryOnly publishes ONLY the HA-Discovery payload for the given
// event — no state, no legacy mirror, no slot-state side effect. HA shows the
// entity as `unavailable` until a wire event populates the slot-state topic,
// but the entity *exists* in HA's registry — the operator sees it on the
// device card from boot, not "after the first observed value".
//
// Visibility-gated identical to [Bridge.PublishState]. Returns nil silently
// when discovery is disabled or the builder declines the event.
func (b *Bridge) PublishDiscoveryOnly(ctx context.Context, ev Event) error {
	if b.cfg.Visibility != nil {
		if !b.cfg.Visibility.VisibleForChannel(ev.Model, ev.ChannelType, ev.ChannelNo, hmenum.ParamsetKeyValues, hmenum.Parameter(ev.Parameter)) {
			return nil
		}
	}
	if !b.cfg.HADiscoveryEnabled || b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	component, nodeID, objectID, cfgPayload, ok := b.cfg.DiscoveryBuilder.Build(ev)
	if ok {
		if err := b.publishDiscovery(ctx, component, nodeID, objectID, cfgPayload); err != nil {
			b.incPublishErrors()
			return err
		}
	}
	b.publishPressButton(ctx, ev)
	return nil
}

// PublishSourceState publishes one channel's aggregated semantic
// state under [TopicBuilder.AggregatedState], JSON-encoded from
// `src.State()`.
//
// **ADR 0011 retired the staggered-DP problem this method
// used to gate.** The aggregate state topic now carries only derived
// fields the model can compute deterministically (Climate's
// `hvac_mode`, `preset_mode`, `action`; Lock's `lock_state`; Cover's
// `direction`); direct wire values went to the per-DP slot topics
// each with its own availability lane. The `payload.Observable`
// gate that lived here in commit a7e1f0a (gating the publish until
// every constituent DP was observed) is therefore obsolete — the
// JSON shape is now intrinsically consistent.
//
// `central`, `iface`, `address`, `channel` scope the topic; passing
// an empty central falls back to the bridge's configured default.
// Returns nil silently when the raw plane is disabled.
func (b *Bridge) PublishSourceState(ctx context.Context, centralName, iface, address string, channel int, src interface{ State() pload.StatePayload }) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if src == nil {
		return nil
	}
	state := src.State()
	if state == nil {
		state = map[string]any{}
	}
	payloadBytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.AggregatedState(centralName, iface, address, channel)
	return b.client.Publish(ctx, topic, payloadBytes, b.cfg.QoS.State, true)
}

// --- ADR 0011 publish helpers (per-DP topology) -----------------------

// PublishCustomDPState publishes the curated derived-state JSON for
// a custom-DP at its `channels/<ch>/custom/<kind>/state` slot. Unlike
// per-DP wire publishes, the body is a plain map[string]any (the
// custom-DP's own [Source.StatePayload]) — derived fields like
// `hvac_mode`, `preset_mode`, `action`, `lock_state`, … that have no
// single wire-DP analogue.
//
// Mirrors ADR 0011 §"Custom-DP `custom/<kind>/state`" — the
// aggregate carries derived fields only; direct wire values stay
// under values/<param>/state.
func (b *Bridge) PublishCustomDPState(ctx context.Context, centralName, iface string, slot pload.TopicSlot, state pload.StatePayload) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if state == nil {
		state = map[string]any{}
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.topics.SlotState(centralName, iface, slot), body, b.cfg.QoS.State, true)
}

// PublishSlotConfig publishes a [pload.Source.ConfigPayload] map at the
// slot's per-DP config topic — the static-capability companion to
// [PublishSlotState] / [PublishCustomDPState]. Carries fields like modes /
// preset_modes / min_temp / max_temp / temp_step (Climate), supports_tilt /
// inverted_control (Cover), available_tones (Siren), or min / max /
// value_list / unit / multiplier (Generic-DP).
//
// Retained — readers see the latest descriptor on subscribe. Diff-gated
// against the bridge's configCache: the EventBridge calls this on every value
// event so DiscoveryDynamic capabilities (Climate's mode-aware preset_modes)
// are reflected, but identical bytes do not generate any broker traffic.
func (b *Bridge) PublishSlotConfig(ctx context.Context, centralName, iface string, slot pload.TopicSlot, config pload.ConfigPayload) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if config == nil {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(config)
	if err != nil {
		return err
	}
	// Empty JSON object — nothing useful to publish.
	if len(body) == 2 && body[0] == '{' && body[1] == '}' {
		return nil
	}
	topic := b.topics.SlotConfig(centralName, iface, slot)
	b.mu.Lock()
	previous, declared := b.configCache[topic]
	if declared && bytesEqual(previous, body) {
		b.mu.Unlock()
		return nil
	}
	b.configCache[topic] = body
	b.mu.Unlock()
	return b.client.Publish(ctx, topic, body, b.cfg.QoS.State, true)
}

// PublishSlotState publishes a [pload.PerDPState] JSON wrapper at the slot's
// per-DP state topic.
//
// Returns nil silently when the raw plane is disabled.
func (b *Bridge) PublishSlotState(ctx context.Context, centralName, iface string, slot pload.TopicSlot, state pload.PerDPState) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	topic := b.topics.SlotState(centralName, iface, slot)
	if err := b.client.Publish(ctx, topic, body, b.cfg.QoS.State, true); err != nil {
		b.incPublishErrors()
		return err
	}
	b.incMessagesSent()
	return nil
}

// PublishDeviceInfo publishes the umfangreich device-info JSON to
// the per-device retained `<addr>/info` topic. The info shape is
// declared in ADR 0011 §"Device info"; producers (typically a
// device-pipeline adapter) hand a pre-built map[string]any.
func (b *Bridge) PublishDeviceInfo(ctx context.Context, centralName, iface, address string, info pload.InfoPayload) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.topics.DeviceInfo(centralName, iface, address), body, b.cfg.QoS.State, true)
}

// PublishDeviceDiagnostics publishes the per-device diagnostics
// aggregate to `<addr>/diagnostics`. Same shape contract as
// [PublishDeviceInfo] — caller composes the JSON body.
func (b *Bridge) PublishDeviceDiagnostics(ctx context.Context, centralName, iface, address string, diag pload.StatePayload) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(diag)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.topics.DeviceDiagnostics(centralName, iface, address), body, b.cfg.QoS.State, true)
}

// PublishAvailability toggles the retained availability topic.
// LegacyAlias mirrors the availability flag under
// `{legacy_base}/device/availability/{address}` when enabled.
func (b *Bridge) PublishAvailability(ctx context.Context, centralName, iface, address string, online bool) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	body := []byte("offline")
	if online {
		body = []byte("online")
	}
	if err := b.client.Publish(ctx, b.topics.DeviceAvailability(centralName, iface, address), body, QoS1, true); err != nil {
		return err
	}
	if b.legacy != nil {
		_ = b.client.Publish(ctx, b.legacy.DeviceAvailability(address), body, QoS1, true)
	}
	return nil
}

// PublishEvent emits a pulse (non-retained) event on the raw plane.
func (b *Bridge) PublishEvent(ctx context.Context, centralName, iface, address string, channel int, etype string, payload any) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	buf, err := renderValue(payload)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.topics.DataPointEvent(centralName, iface, address, channel, etype), buf, QoS0, false)
}

// PublishChannelEventState emits a non-retained aggregate press-event
// JSON payload to the per-channel event topic. Called by the
// EventBridge whenever a PRESS_* parameter fires on a multi-press
// channel (one that has 2+ PRESS_* parameters). HA reads the
// `event_type` field from the JSON and advances the entity state.
//
// Payload shape: `{"event_type": "<press_short|press_long|…>",
// "available": true, "modified_at": "<rfc3339>"}`.
//
// The topic is non-retained — HA event entities must receive a fresh
// pulse per keypress, not a stale retained value.
func (b *Bridge) PublishChannelEventState(ctx context.Context, centralName, iface, address string, channel int, pressType string) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	body := map[string]any{
		"event_type":  strings.ToLower(pressType),
		"available":   true,
		"modified_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	topic := b.topics.ChannelEvent(b.resolvedCentral(centralName), iface, address, channel)
	// QoS0, non-retained — mirrors HA's event entity contract: each
	// keypress fires a fresh pulse; brokers must not replay a stale press.
	return b.client.Publish(ctx, topic, buf, QoS0, false)
}

// PublishSysvar emits a sysvar value on the canonical ADR-0011 topic
// owned by the sysvar model object (`<base>/<central>/hub/sysvars/
// <name>/state`). The bridge only fills in `base` and JSON-encodes
// the value; it never decides the topic shape.
func (b *Bridge) PublishSysvar(ctx context.Context, centralName string, sv pload.MQTTAddressable, value any) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := sv.MQTTTopics(b.cfg.Base, b.resolvedCentral(centralName))
	if topics.State == "" {
		return nil
	}
	body, err := renderValue(value)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true)
}

// PublishProgram emits the program state on the canonical ADR-0011
// topics owned by the program model object. Active is mirrored to
// both `…/state` and `…/trigger` so switch entities see it on either
// reference.
func (b *Bridge) PublishProgram(ctx context.Context, centralName string, prog pload.MQTTAddressable, active bool) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := prog.MQTTTopics(b.cfg.Base, b.resolvedCentral(centralName))
	if topics.State == "" {
		return nil
	}
	body := []byte("false")
	if active {
		body = []byte("true")
	}
	if err := b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true); err != nil {
		return err
	}
	if topics.Trigger != "" {
		_ = b.client.Publish(ctx, topics.Trigger, body, b.cfg.QoS.State, true)
	}
	return nil
}

// PublishInstallMode emits the per-interface install-mode countdown
// (remaining seconds) to the retained topic
// `<base>/<central>/hub/install_mode/<iface>`. The reference stack
// exposes one remaining-seconds sensor per interface; each carries its
// own retained state topic.
func (b *Bridge) PublishInstallMode(ctx context.Context, centralName, iface string, seconds int) error {
	if !b.cfg.RawEnabled || iface == "" {
		return nil
	}
	topic := naming.MQTTHubInstallModeForInterface(b.cfg.Base, b.resolvedCentral(centralName), iface)
	body := fmt.Appendf(nil, "%d", seconds)
	return b.client.Publish(ctx, topic, body, b.cfg.QoS.State, true)
}

// PublishAlarmMessages emits the active alarm-message list to the
// topic owned by the [hub.AlarmMessages] aggregate.
func (b *Bridge) PublishAlarmMessages(ctx context.Context, centralName string, agg pload.MQTTAddressable, items any) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := agg.MQTTTopics(b.cfg.Base, b.resolvedCentral(centralName))
	if topics.State == "" {
		return nil
	}
	body, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true)
}

// PublishServiceMessages mirrors PublishAlarmMessages for the
// service-messages aggregate.
func (b *Bridge) PublishServiceMessages(ctx context.Context, centralName string, agg pload.MQTTAddressable, items any) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := agg.MQTTTopics(b.cfg.Base, b.resolvedCentral(centralName))
	if topics.State == "" {
		return nil
	}
	body, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true)
}

// PublishInbox emits the pending inbox-device list to the topic
// owned by the inbox aggregate.
func (b *Bridge) PublishInbox(ctx context.Context, centralName string, agg pload.MQTTAddressable, items any) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := agg.MQTTTopics(b.cfg.Base, b.resolvedCentral(centralName))
	if topics.State == "" {
		return nil
	}
	body, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true)
}

// ConnectivityPublisher is the contract the [hub.Connectivity]
// aggregate satisfies for per-interface topic resolution. It is a
// per-interface specialisation of [pload.MQTTAddressable] because
// the aggregate spans many interfaces under one model object.
type ConnectivityPublisher interface {
	MQTTTopicsForInterface(base, centralName, iface string) pload.MQTTTopicSet
}

// PublishConnectivity flips the per-interface availability flag at
// the topic owned by the [hub.Connectivity] aggregate.
func (b *Bridge) PublishConnectivity(ctx context.Context, centralName string, conn ConnectivityPublisher, iface string, connected bool) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topics := conn.MQTTTopicsForInterface(b.cfg.Base, b.resolvedCentral(centralName), iface)
	if topics.State == "" {
		return nil
	}
	body := []byte("false")
	if connected {
		body = []byte("true")
	}
	return b.client.Publish(ctx, topics.State, body, b.cfg.QoS.State, true)
}

// dataPointStateTopic returns the state topic for a data-point defined
// by the given coordinates. Extracted from [PublishState] so that
// [EvictState] can build the same topic without duplicating the
// construction logic. The raw-plane topic is the only topic that
// EvictState needs to clear; Discovery and slot topics are not erased
// because they carry semantic metadata (config, modes, …) that does
// not go stale in the same way a value payload does.
func (b *Bridge) dataPointStateTopic(centralName, iface, address string, channel int, parameter string) string {
	return b.topics.DataPointState(b.resolvedCentral(centralName), iface, address, channel, parameter)
}

// resolvedCentral returns central if non-empty, otherwise the bridge's
// configured default central name. Used by topic helpers that are
// called with an empty central in some synthetic-event flows.
func (b *Bridge) resolvedCentral(centralName string) string {
	if centralName != "" {
		return centralName
	}
	return b.cfg.CentralName
}

// EvictState publishes an empty retained payload (nil / zero bytes)
// to the raw-plane state topic for the given data point. This deletes
// the retained message from the broker so that HA — and any other
// subscriber — stops showing a stale value from a previous daemon
// run. Should only be called when the daemon cannot obtain a fresh
// observed value (LoadValue returned observed=false or an error).
//
// EvictState is best-effort: it publishes only to the raw-plane state
// topic (the same one [PublishState] writes to). Discovery and slot
// companion topics are NOT touched — their content is metadata
// (min/max/value_list/modes) that does not go stale like a scalar
// value does.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) EvictState(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	parameter string,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	topic := b.dataPointStateTopic(centralName, iface, address, channel, parameter)
	// Empty payload with retain=true is the MQTT specification's
	// mechanism for deleting a retained message. HA's docs explicitly
	// recommend this pattern for clearing stale entity state.
	if err := b.client.Publish(ctx, topic, []byte{}, b.cfg.QoS.State, true); err != nil {
		return err
	}
	if b.legacy != nil {
		legacyTopic := b.legacy.DataPointState(address, channel, parameter)
		_ = b.client.Publish(ctx, legacyTopic, []byte{}, b.cfg.QoS.State, true)
	}
	return nil
}

// Topics exposes the builder for adapters that need to subscribe to
// specific command topics.
func (b *Bridge) Topics() *TopicBuilder { return b.topics }

// PublishSystemStatus publishes payload to the per-central system-status
// topic (`<base>/<central>/system/status`). Non-retained, QoS 0 — the
// topic carries live events, not persistent state. Returns nil when
// RawEnabled is false (disabled broker plane).
func (b *Bridge) PublishSystemStatus(ctx context.Context, centralName string, payload []byte) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	return b.client.Publish(ctx, b.topics.SystemStatus(centralName), payload, QoS0, false)
}

// PublishHubSystemHealthScore publishes the system-health score (0–100) to
// the retained topic `<base>/<central>/system/health_score`. Returns nil when
// RawEnabled is false.
func (b *Bridge) PublishHubSystemHealthScore(ctx context.Context, centralName string, score float64) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	body := []byte(strconv.FormatFloat(score, 'f', -1, 64))
	return b.client.Publish(ctx, b.topics.HubSystemHealthScore(centralName), body, b.cfg.QoS.State, true)
}

// PublishHubConnectionLatency publishes the aggregated CCU round-trip
// latency (ms) to the retained topic `<base>/<central>/system/latency`.
// The reference stack exposes ONE central-wide connection-latency sensor
// fed from the aggregated ping/pong metric, not per-interface samples.
// Returns nil when RawEnabled is false.
func (b *Bridge) PublishHubConnectionLatency(ctx context.Context, centralName string, latencyMs float64) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	body := []byte(strconv.FormatFloat(latencyMs, 'f', -1, 64))
	return b.client.Publish(ctx, b.topics.HubConnectionLatency(centralName), body, b.cfg.QoS.State, true)
}

// PublishHubLastEventAge publishes the age (seconds) of the newest
// backend event to the retained topic
// `<base>/<central>/system/last_event_age`. Returns nil when RawEnabled
// is false.
func (b *Bridge) PublishHubLastEventAge(ctx context.Context, centralName string, ageSeconds float64) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	body := []byte(strconv.FormatFloat(ageSeconds, 'f', -1, 64))
	return b.client.Publish(ctx, b.topics.HubLastEventAge(centralName), body, b.cfg.QoS.State, true)
}

// PublishHubUpdate publishes the CCU's firmware-update state to the
// retained topic `<base>/<central>/hub/update` as a JSON object with
// the fields expected by HA's MQTT Update entity:
// `installed_version`, `latest_version`, and `in_progress`.
// Returns nil when RawEnabled is false.
func (b *Bridge) PublishHubUpdate(ctx context.Context, centralName, installedVersion, latestVersion string, inProgress bool) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	payload := struct {
		InstalledVersion string `json:"installed_version"`
		LatestVersion    string `json:"latest_version"`
		InProgress       bool   `json:"in_progress"`
	}{
		InstalledVersion: installedVersion,
		LatestVersion:    latestVersion,
		InProgress:       inProgress,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.topics.HubUpdate(centralName), body, b.cfg.QoS.State, true)
}

// PublishChannelEventDiscovery publishes the HA Discovery payload for
// a press-button channel without going through a synthetic
// value-change event. Boot-time entry point: PublishInitialSnapshot
// calls this for every channel that exposes PRESS_* parameters so
// HA's event entities exist immediately, even before the first button
// press. Without this an HA user wouldn't see button entities until
// somebody actually pressed the button — and many physical buttons
// have no observed value persisted on the CCU between presses.
//
// Routes through the same per-parameter / aggregated decision as the
// runtime [Build] flow:
//   - Multi-press channels (≥2 PRESS_* parameters) emit one HA `event`
//     entity per channel via [BuildChannelEvent].
//   - Single-press channels emit one HA `event` entity per
//     PRESS_* parameter via the per-parameter [Build] heuristic
//     (HAComponentEvent classifier).
//
// Idempotent — discovery topics are diff-gated by `b.declared`.
func (b *Bridge) PublishChannelEventDiscovery(ctx context.Context, ev Event) error {
	if !b.cfg.HADiscoveryEnabled || b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	component, nodeID, objectID, payload, ok := b.cfg.DiscoveryBuilder.Build(ev)
	if !ok {
		return nil
	}
	if err := b.publishDiscovery(ctx, component, nodeID, objectID, payload); err != nil {
		return err
	}
	b.publishPressButton(ctx, ev)
	return nil
}

// PublishCustomDPDiscovery publishes the aggregate (channel-level)
// HA-Discovery payload for a channel's custom-DP plus any companion
// entities it spawns. ev must carry the custom-DP as ev.Source.
//
// The regular per-DP register-and-load path only emits the aggregate as
// a side effect of an observed VALUES parameter. Write-only custom-DPs
// (HmIP-WRCD text-display) have no readable parameter, so that path
// never fires and the entity never reaches HA. This snapshot helper
// emits the aggregate directly so write-only custom-DPs surface from
// boot. Idempotent — discovery topics are diff-gated by `b.declared`.
//
// Companion entities: a text-display custom-DP spawns ONLY a `notify`
// entity (reference parity — TEXT_DISPLAY maps to notify alone; the
// aggregate `text` entity is suppressed in aggregateChannel).
func (b *Bridge) PublishCustomDPDiscovery(ctx context.Context, ev Event) error {
	if !b.cfg.HADiscoveryEnabled || b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	if ev.Source == nil {
		return nil
	}
	component, nodeID, objectID, payload, ok := b.cfg.DiscoveryBuilder.Build(ev)
	if ok {
		if err := b.publishDiscovery(ctx, component, nodeID, objectID, payload); err != nil {
			return err
		}
	}
	b.publishTextDisplayNotify(ctx, ev)
	return nil
}

// publishPressButton publishes the press-button companion entity for a
// click-event parameter the model marked as a button (category=button,
// usage=data_point). Writable presses — virtual-remote actions, a plain KEY
// channel's PRESS_SHORT/PRESS_LONG, and additional_data_points-promoted
// dimmer-input presses — are each exposed as a clickable HA `button`
// (disabled by default) IN ADDITION to the per-channel keypress `event`
// entity that every press channel gets. Best-effort: errors are swallowed —
// the primary discovery publish has already succeeded, and the button
// re-publishes on the next press / snapshot pass.
//
// No-op when the configured builder is not the [DefaultDiscoveryBuilder]
// or the event is not a press-button parameter.
func (b *Bridge) publishPressButton(ctx context.Context, ev Event) {
	dd, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder)
	if !ok {
		return
	}
	item := dd.BuildPressButton(ev)
	if !item.OK {
		return
	}
	if err := b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload); err != nil {
		b.incPublishErrors()
	}
}

// publishTextDisplayNotify publishes the notify entity for a
// text-display custom-DP (HmIP-WRCD). The reference stack maps a
// TEXT_DISPLAY custom-DP onto a `notify` entity ONLY; the aggregate
// `text` entity is suppressed in aggregateChannel, so this is the sole
// HA surface for the display. Best-effort: errors are swallowed.
//
// No-op when the configured builder is not the [DefaultDiscoveryBuilder]
// or the event does not carry a text-display custom-DP.
func (b *Bridge) publishTextDisplayNotify(ctx context.Context, ev Event) {
	dd, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder)
	if !ok {
		return
	}
	item := dd.BuildTextDisplayNotify(ev)
	if !item.OK {
		return
	}
	if err := b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload); err != nil {
		b.incPublishErrors()
	}
}

// RetractDiscoveryForDevice retracts every HA-Discovery config this bridge
// declared for the given device address — publishing an empty retained payload
// to each topic — and removes those entries from the declared map. Called when
// the daemon processes a device-removed callback so the removed device's
// entities disappear from Home Assistant immediately, rather than lingering as
// permanently "unavailable" until the next boot's orphan-cleanup pass evicts
// them. Pruning the declared entries additionally stops the dedup gate from
// suppressing a re-publish should the same address reappear later.
//
// The discovery topic shape is
// `homeassistant/<component>/<node_id>/<object_id>/config`
// where node_id is `<central>_<lower(address)>`. We match on `_<lower(addr)>/`
// which is unambiguous for the node_id segment — addresses are formatted as
// hex strings (e.g. `000c9709aef157`) and can only collide if two devices
// share the same address string, which the CCU prevents.
//
// Returns the number of config topics retracted. A no-op when HA Discovery is
// disabled or the address declared no configs.
func (b *Bridge) RetractDiscoveryForDevice(ctx context.Context, deviceAddress string) int {
	if deviceAddress == "" || !b.cfg.HADiscoveryEnabled {
		return 0
	}
	needle := "_" + strings.ToLower(deviceAddress) + "/"
	b.mu.Lock()
	topics := make([]string, 0)
	for topic := range b.declared {
		if strings.Contains(topic, needle) {
			topics = append(topics, topic)
			delete(b.declared, topic)
		}
	}
	b.mu.Unlock()
	for _, topic := range topics {
		// Empty retained payload clears the retained config so HA drops the
		// entity. Best-effort: a publish error leaves the boot orphan-cleanup
		// pass as the backstop.
		if err := b.client.Publish(ctx, topic, nil, b.cfg.QoS.Discovery, true); err != nil {
			b.incPublishErrors()
		}
	}
	return len(topics)
}

func (b *Bridge) publishDiscovery(ctx context.Context, component, nodeID, objectID string, payload []byte) error {
	topic := b.topics.DiscoveryConfig(component, nodeID, objectID)
	b.mu.Lock()
	previous, declared := b.declared[topic]
	b.declared[topic] = payload
	b.mu.Unlock()
	// Deduplicate identical payloads — bytes.Equal would alloc
	// nothing on a typical stable schema.
	if declared && bytesEqual(previous, payload) {
		return nil
	}
	if err := b.client.Publish(ctx, topic, payload, b.cfg.QoS.Discovery, true); err != nil {
		b.incPublishErrors()
		return err
	}
	b.incDiscoverySent()
	return nil
}

// incMessagesSent increments the messages_sent counter when a collector is wired.
func (b *Bridge) incMessagesSent() {
	if b.collector != nil {
		b.collector.MessagesSent.Inc()
	}
}

// incDiscoverySent increments the discovery_sent counter when a collector is wired.
func (b *Bridge) incDiscoverySent() {
	if b.collector != nil {
		b.collector.DiscoverySent.Inc()
	}
}

// incPublishErrors increments the publish_errors counter when a collector is wired.
func (b *Bridge) incPublishErrors() {
	if b.collector != nil {
		b.collector.PublishErrors.Inc()
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderStatePayload returns the canonical JSON envelope
// `{"value": <v>, "available": true, "modified_at": "<rfc3339>"}` for
// the legacy-alias mirror topic. The canonical slot-state pipeline
// uses [PublishSlotState] with the typed [pload.PerDPState] envelope.
//
// "available" is always true: this path runs for fresh data points
// the daemon has just observed. The per-device-availability topic
// carries the device-level reachability flag and is referenced as a
// secondary availability source in the HA discovery payload.
func (b *Bridge) renderStatePayload(ev Event) ([]byte, error) {
	value := resolveEnumLabel(ev.Value, ev.descType(), ev.descValueList())
	body := map[string]any{
		"value":       value,
		"available":   true,
		"modified_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	return json.Marshal(body)
}

// ResolveEnumLabel converts an ENUM-typed wire value (an int index) into the
// matching VALUE_LIST label. HA's MQTT discovery declares `options: [...]`
// from the same VALUE_LIST; without the lookup the raw integer "2" reaches
// the broker and HA logs `Ignoring invalid option received ... got '2',
// allowed: ...`.
//
// Returns the original value for non-enum types or out-of-bounds indices so
// the call is safe at the rendering boundary. Exported because the
// EventBridge applies the same resolution in its PerDPState publish path.
func ResolveEnumLabel(value any, wireType hmenum.ParameterType, valueList []string) any {
	if wireType != hmenum.ParameterTypeEnum || len(valueList) == 0 {
		return value
	}
	idx, ok := indexFromValue(value)
	if !ok || idx < 0 || idx >= int64(len(valueList)) {
		return value
	}
	return valueList[idx]
}

// resolveEnumLabel is the unexported alias used internally by the
// bridge. New callers should use [ResolveEnumLabel].
func resolveEnumLabel(value any, wireType hmenum.ParameterType, valueList []string) any {
	return ResolveEnumLabel(value, wireType, valueList)
}

// indexFromValue extracts a non-negative int64 index from a Go value
// that the wire layer might deliver as int / int32 / int64 / float64
// / numeric string. Returns (idx, false) for nil, negative, or
// non-numeric inputs.
func indexFromValue(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true //nolint:gosec // bounded by ValueList length downstream; see #20
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true //nolint:gosec // bounded by ValueList length downstream; see #20
	case float64:
		return int64(x), true
	case float32:
		return int64(x), true
	case string:
		if x == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// renderValue converts a primitive Go value into the raw-plane
// payload. Booleans, numbers, and strings map to their canonical
// string form; complex values JSON-encode.
func renderValue(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return []byte(""), nil
	case bool:
		if x {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case string:
		return []byte(x), nil
	case int:
		return fmt.Appendf(nil, "%d", x), nil
	case int32:
		return fmt.Appendf(nil, "%d", x), nil
	case int64:
		return fmt.Appendf(nil, "%d", x), nil
	case float32:
		return []byte(strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")), nil
	case float64:
		return []byte(strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")), nil
	}
	return json.Marshal(v)
}
