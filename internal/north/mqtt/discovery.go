// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// valueJSONValueTemplate is the canonical Jinja extractor for the
// PerDPState `value` field. The defensive guard renders empty (so HA
// falls back to the "unknown" entity state) in two cases:
//
//   - `value_json is defined` — the payload is not the empty retained
//     eviction body (the `'value_json' is undefined` template error HA
//     otherwise raises).
//   - `value_json.value is not none` — the DP is registered but has not
//     reported a value yet (the unobserved-DP boot path publishes
//     `{"value":null,"available":true}`). Without this clause
//     `{{ value_json.value }}` renders the literal string "None".
//
// The entity stays available (the per-device + per-DP availability
// topics resolve to online); only its value reads "unknown" until the
// CCU pushes a real value.
const valueJSONValueTemplate = `{% if value_json is defined and value_json.value is not none %}{{ value_json.value }}{% endif %}`

// valueJSONValueLowerTemplate is the boolean-aware variant used for
// switch / lock / binary_sensor entities. PerDPState carries Python-
// boolean rendering (`{"value":true}` → Jinja `True`/`False` with
// capitalised initial), but HA compares to lowercase tokens
// (`payload_on:"true"`, `payload_off:"false"`). Pipe through `| lower`
// so the comparison is case-stable. Same guard semantics as the
// non-lower variant (`none | lower` would otherwise render "none").
const valueJSONValueLowerTemplate = `{% if value_json is defined and value_json.value is not none %}{{ value_json.value | lower }}{% endif %}`

// enumOptionTemplates builds the pair of templates that let an entity
// display a localised enum option while still writing the CCU's own
// token.
//
// Home Assistant shows an MQTT entity's `options` verbatim — a discovered
// entity has no translation file behind it, so raw tokens
// ("auto_mode", "manu_mode") are what an operator reads. Publishing the
// labels as options fixes the display but breaks the write, because HA
// sends the chosen option string back. The mapping closes that loop:
// state side maps token → label, command side maps label → token, and
// both fall through to the input when a value is not in the list (an
// unexpected token then shows as itself rather than blanking the entity).
//
// values and labels must be index-aligned; the caller checks that the
// labels are distinct, since HA keys the option by its display string.
func enumOptionTemplates(values, labels []string) (valueTemplate, commandTemplate string) {
	var state, command strings.Builder
	state.WriteString(`{% set m = {`)
	command.WriteString(`{% set m = {`)
	for i, v := range values {
		if i > 0 {
			state.WriteString(", ")
			command.WriteString(", ")
		}
		state.WriteString(jinjaQuote(v) + ": " + jinjaQuote(labels[i]))
		command.WriteString(jinjaQuote(labels[i]) + ": " + jinjaQuote(v))
	}
	state.WriteString(`} %}{% if value_json is defined and value_json.value is not none %}` +
		`{{ m.get(value_json.value, value_json.value) }}{% endif %}`)
	command.WriteString(`} %}{{ m.get(value, value) }}`)
	return state.String(), command.String()
}

// jinjaQuote renders s as a single-quoted Jinja string literal,
// escaping the backslash and quote that would otherwise end it. Without
// the escape a label carrying an apostrophe ("Ein'aus") would produce a
// template Home Assistant cannot parse, and the entity would silently
// stop updating.
func jinjaQuote(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return `'` + escaped + `'`
}

// HAComponent identifies a Home Assistant entity category. Only the
// MVP-relevant subset is listed; the catalogue grows as profiles
// are ported.
type HAComponent string

// HAComponent values. Aligned with HA's MQTT-Discovery component
// list — the names match HA's `homeassistant/<component>/...`
// discovery prefix segments verbatim.
const (
	HAComponentSwitch       HAComponent = "switch"
	HAComponentLight        HAComponent = "light"
	HAComponentSensor       HAComponent = "sensor"
	HAComponentBinarySensor HAComponent = "binary_sensor"
	HAComponentNumber       HAComponent = "number"
	HAComponentCover        HAComponent = "cover"
	HAComponentLock         HAComponent = "lock"
	HAComponentClimate      HAComponent = "climate"
	HAComponentValve        HAComponent = "valve"
	HAComponentSiren        HAComponent = "siren"
	HAComponentSelect       HAComponent = "select"
	HAComponentButton       HAComponent = "button"
	HAComponentEvent        HAComponent = "event"
	HAComponentUpdate       HAComponent = "update"
	HAComponentText         HAComponent = "text"
	HAComponentNotify       HAComponent = "notify"
)

// Origin metadata embedded in every Discovery payload (HA 2024+).
// Helps HA users see which integration produced an entity.
const (
	originName       = "openccu-loom"
	originSupportURL = "https://github.com/SukramJ/openccu-loom"
)

// originVersionStore is set from the build version at daemon start-up so the
// release banner can keep this compile-time constant in sync with
// the build metadata. The Discovery payload uses whatever value
// was last assigned — falls back to "dev". atomic.Value so concurrent
// reads (every Discovery emit) and the occasional SetOriginVersion
// don't race under `-race`.
var originVersionStore atomic.Value

func init() {
	originVersionStore.Store("dev")
}

// originVersion returns the currently-set version string.
func originVersion() string {
	v, _ := originVersionStore.Load().(string)
	if v == "" {
		return "dev"
	}
	return v
}

// SetOriginVersion updates the version string baked into Discovery
// payloads. Safe to call concurrently — the store is atomic.
func SetOriginVersion(v string) {
	if v != "" {
		originVersionStore.Store(v)
	}
}

// DefaultDiscoveryBuilder is the MVP HA Discovery payload generator.
// It derives the right component + payload from the parameter name
// using a very small rule table — enough to light up the common
// switch / sensor / binary_sensor cases. Complex device types (cover
// position + slats, siren, lock with capability sets) are handled
// by targeted overrides in future scheibes.
//
// State topics carry the canonical JSON envelope
// `{"value":..,"available":..,"modified_at":..}` and the discovery
// payload uses `value_template` filters to pick the scalar out of
// the JSON. The earlier bare-scalar mode was retired with the
// ADR-0011 payload unification.
type DefaultDiscoveryBuilder struct {
	TopicBuilder *TopicBuilder
	BridgeBase   string
	Central      string
	// Hub carries the default CCU metadata that enriches the synthetic HA
	// device block for hub entities. Zero value falls back to static
	// defaults. Populate via [WithHubInfo]. Used when no per-central entry
	// in [hubs] matches the discovery target.
	Hub HubInfo
	// hubsMu guards [hubs] (including its lazy allocation). The map is
	// written from the composition root, from the central's snapshot
	// goroutine, and from the hub publisher's worker, while [hubFor]
	// reads it on the discovery hot path of every value event — an
	// unsynchronised map there is a `fatal error: concurrent map read
	// and map write` process abort, not a benign race.
	hubsMu sync.RWMutex
	// hubs holds per-central HubInfo entries so a multi-CCU daemon emits
	// the correct device-block metadata (Name / Model / Version / Serial /
	// URL) for each CCU. Populated via [SetHubInfoFor]; lookup performed
	// by [hubFor]. Falls back to [Hub] when the central name is unknown.
	hubs map[string]HubInfo
	// SubDevicesEnabled splits multi-channel-group devices into one HA
	// device per channel group. When true and the event's parent device
	// reports HasSubDevices() and the channel sits in a multi-group, the
	// discovery payload's `device` block stamps a sub-device identifier
	// with the parent device as `via_device`.
	SubDevicesEnabled bool
	// Locale selects the language of the discovery entity names the daemon
	// synthesises itself (hub entities + CCU-auto-generated system variables).
	// Resolved against [Translations]; empty falls back to the catalogue's
	// default locale.
	Locale string
	// Translations resolves the localized discovery entity names from the
	// embedded i18n catalogues. Adding a language is purely a new
	// `internal/i18n/catalogs/<locale>.json` file — no Go change. Auto-loaded by
	// [NewDefaultDiscoveryBuilder]; nil makes [DefaultDiscoveryBuilder.tr] return
	// the raw key.
	Translations *i18n.Catalogs
}

// NewDefaultDiscoveryBuilder constructs the default builder. It auto-loads the
// embedded i18n catalogues so every synthesised entity name is localizable
// without threading the catalogues through the wiring; the catalogues are
// immutable embedded data, so a per-builder instance is cheap.
func NewDefaultDiscoveryBuilder(topics *TopicBuilder, centralName string) *DefaultDiscoveryBuilder {
	b := &DefaultDiscoveryBuilder{TopicBuilder: topics, BridgeBase: topics.Base, Central: centralName}
	if cat, err := i18n.NewCatalogs(); err == nil {
		b.Translations = cat
	}
	return b
}

// tr resolves an i18n catalogue key in the builder's locale. Falls back to the
// catalogue default locale and finally the raw key (see [i18n.Catalogs.T]);
// returns the key unchanged when no catalogues are wired.
func (d *DefaultDiscoveryBuilder) tr(key string) string {
	if d.Translations == nil {
		return key
	}
	return d.Translations.T(d.Locale, key)
}

// WithHubInfo stores CCU metadata in the builder. Subsequent hub
// Discovery payloads (sysvars, programs, alarm/service messages,
// install-mode) will carry the populated device block. Returns the
// receiver for fluent wiring.
//
// Multi-CCU note: this sets the *default* HubInfo applied when no
// per-central entry exists. Use [SetHubInfoFor] to register
// central-specific metadata.
func (d *DefaultDiscoveryBuilder) WithHubInfo(info HubInfo) *DefaultDiscoveryBuilder {
	d.Hub = info
	return d
}

// SetHubInfoFor registers per-central HubInfo so a multi-CCU daemon
// emits the correct device-block metadata for each CCU. central must
// match the value passed into the discovery-builder method's
// `central` argument.
//
// Safe to call from any goroutine: the daemon stamps the hub serial
// from the boot path, from the southbound-ready subscriber, and from
// the hub publisher's worker while discovery payloads are already
// being built for incoming CCU events.
func (d *DefaultDiscoveryBuilder) SetHubInfoFor(centralName string, info HubInfo) {
	if d == nil || centralName == "" {
		return
	}
	d.hubsMu.Lock()
	defer d.hubsMu.Unlock()
	if d.hubs == nil {
		d.hubs = make(map[string]HubInfo)
	}
	d.hubs[centralName] = info
}

// hubFor returns the HubInfo to use for the named central. Falls
// back to the default [Hub] when no per-central entry is registered.
// The returned value is a copy, so the lock is released before the
// caller touches it.
//
// [Hub] itself needs no lock: it is the wiring-time default, written
// once before the builder is handed to the publish path.
func (d *DefaultDiscoveryBuilder) hubFor(centralName string) HubInfo {
	if d == nil {
		return HubInfo{}
	}
	d.hubsMu.RLock()
	hi, ok := d.hubs[centralName]
	d.hubsMu.RUnlock()
	if ok {
		return hi
	}
	return d.Hub
}

// serialSuffix returns the last-10-characters serial discriminator for the
// given central. It feeds the serial-prefix slot of [routingkey.CanonicalUniqueID]
// for address classes whose addresses repeat across CCUs (hub roots, INT000*,
// virtual remotes).
func (d *DefaultDiscoveryBuilder) serialSuffix(centralName string) string {
	return routingkey.SerialSuffix(d.hubFor(centralName).Serial)
}

// centralFor resolves the CCU name to scope a device-bound topic to.
// Multi-CCU correctness: the per-device topics MUST use the event's
// central (the CCU the device actually lives on), not the builder's
// default `Central` (which is just one configured CCU — typically the
// first). Using d.Central for every device routes non-first-CCU devices'
// discovery topics to the wrong central segment while the publish path
// uses the device's real central, so HA subscribes to topics that never
// receive data and marks the entity `unavailable`. Falls back to the
// builder default only when the event carries no central (hub-level
// payloads built without a device context).
func (d *DefaultDiscoveryBuilder) centralFor(ev Event) string {
	if ev.Central != "" {
		return ev.Central
	}
	return d.Central
}

// hubURLFor returns the WebUI URL of the CCU the event belongs to, for
// the per-device `configuration_url`.
//
// It has to go through [hubFor]: the daemon registers its CCU metadata
// per central via [SetHubInfoFor] and never writes the wiring-time
// default, so reading d.Hub directly yielded an empty URL on every
// device card — HA's "Visit device" button was missing everywhere but on
// the synthetic hub device, which already resolved per central. In a
// multi-CCU daemon the default would additionally be the wrong CCU.
func (d *DefaultDiscoveryBuilder) hubURLFor(ev Event) string {
	return d.hubFor(d.centralFor(ev)).URL
}

// hubAggregateUniqueID builds the unique_id for loom-specific hub
// aggregate entities that have no equivalent in the canonical
// routing-key contract (alarm_messages, service_messages, inbox,
// connectivity_<iface>, system_health_score, latency_<iface>,
// hub update). The shape is "loom_<serial10>_<kind>".
func hubAggregateUniqueID(serial10, kind string) string {
	return "loom_" + serial10 + "_" + kind
}

// WithSubDevices toggles per-channel-group sub-device splitting in the
// HA `device` block.
func (d *DefaultDiscoveryBuilder) WithSubDevices(on bool) *DefaultDiscoveryBuilder {
	d.SubDevicesEnabled = on
	return d
}

// WithLocale sets the language used for daemon-synthesised discovery names.
// Returns the receiver for fluent wiring.
func (d *DefaultDiscoveryBuilder) WithLocale(locale string) *DefaultDiscoveryBuilder {
	d.Locale = locale
	return d
}

// Build translates ev into (component, objectID, payload).
//
// Three paths layered on top of each other:
// 1. Channel-aware aggregator (`aggregateChannel`): when the
// event carries a known custom-domain ChannelType, the whole
// channel collapses into one HA entity (climate, cover, lock,
// light, valve, siren). Per-parameter events on the same
// channel re-emit the same payload — dedup happens in the
// bridge's discovery cache.
// 2. Press-event aggregator (`BuildChannelEvent`): every press channel
// collapses its PRESS_* parameters into ONE channel-level HA `event`
// entity with `event_types: [...]` (a single PRESS_SHORT channel gets
// the same channel-level entity as a four-type remote). The writable
// presses additionally get a button companion via
// [Bridge.publishPressButton].
// 3. Per-parameter fallback via [resolveComponent]: uses ev.Category
// (model-driven). Drives sensor / binary_sensor / number entities
// that are not part of an aggregate, plus VALUES paramsets on
// channels we don't classify as a custom domain.
func (d *DefaultDiscoveryBuilder) Build(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) { //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	if ev.ChannelType != "" {
		if comp, nid, oid, p, agg := d.aggregateChannel(ev); agg {
			return comp, nid, oid, p, true
		}
	}
	// Press-event aggregation: collapse every press channel's PRESS_*
	// parameters into ONE channel-level event entity. Per-parameter PRESS_*
	// discovery is suppressed — the aggregated entity is what HA receives.
	if isPressParameter(ev.Parameter) {
		// Event-suppression gate (IGNORE_DEVICES_FOR_DATA_POINT_EVENTS):
		// HmIP-PS* schaltaktoren expose a KEY_TRANSCEIVER channel with
		// PRESS_* parameters, but the reference stack never spawns a
		// keypress event for them. Skipping the event path keeps the
		// openccu-loom event plane in parity (no `event` entity for these
		// models).
		if visibility.IsParameterIgnoredForDataPointEvent(ev.Model, hmenum.Parameter(ev.Parameter)) {
			return "", "", "", nil, false
		}
		if comp, nid, oid, p, agg := d.BuildChannelEvent(ev); agg {
			return comp, nid, oid, p, true
		}
		// No channel inspector (agg=false) — fall through to the per-parameter
		// path below.
	}
	// Impulse and device-error aggregation, the same shape one kind over.
	// Both were absent from this plane until 0.69.0 — the events reached the
	// REST and WebSocket planes and simply never appeared here — so this adds
	// entities rather than moving any.
	if kind, known := event.Classify(hmenum.Parameter(ev.Parameter)); known && kind != event.KindKeypress {
		if comp, nid, oid, p, agg := d.BuildChannelKindEvent(ev, kind); agg {
			return comp, nid, oid, p, true
		}
		// No channel inspector — fall through to the per-parameter path.
	}
	// Usage gate — the model's DataPointUsage verdict (same source as
	// the REST `DataPointSummary.usage` field) decides whether a wire DP
	// surfaces as its own HA entity:
	//
	//   - no_create / ignored — suppressed everywhere in the reference
	//     stack (hidden parameters, custom-DP absorption, operator
	//     ignores). No per-parameter entity.
	//   - ce_primary / ce_secondary — constituents of the channel's
	//     custom-DP aggregate (the STATE behind a Switch CDP, the LEVEL
	//     behind a Cover CDP, …). The aggregate path above is their only
	//     HA surface; emitting a generic switch/number NEXT TO the
	//     aggregate duplicates the control. ce_visible deliberately
	//     passes — those are the aggregate's declared extra sensors
	//     (HmIP-BWTH HUMIDITY / ACTUAL_TEMPERATURE).
	//
	// The zero value passes: synthetic events and calculated DPs carry
	// no verdict.
	switch ev.Usage { //nolint:exhaustive // every other usage (data_point, event, ce_visible, …) passes the gate
	case hmenum.DataPointUsageNoCreate,
		hmenum.DataPointUsageIgnored,
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPSecondary:
		return "", "", "", nil, false
	}
	// A click-event parameter the model typed as a button (category=button)
	// only surfaces as a standalone button entity when its usage is
	// data_point. An event-only press the regular press-aggregation path does
	// not route (PRESS_LOCK / PRESS_UNLOCK / PRESS_CONT — they carry
	// usage=event) must not fall through to a per-DP button here; its surface
	// is the keypress event group, and a writable press additionally gets the
	// press-button companion via [Bridge.publishPressButton].
	if ev.Category == hmenum.DataPointCategoryButton && ev.Usage == hmenum.DataPointUsageEvent {
		return "", "", "", nil, false
	}
	comp, classified := resolveComponent(ev)
	if !classified {
		return "", "", "", nil, false
	}
	// Writability override: a non-writable wire DP that classify mapped
	// to `switch` (or `light` / `lock` / `select`) is actually a
	// read-only sensor surface. HmIP-PSM-2 ch2 STATE is the canonical
	// example: status output of the relay channel, the operator drives
	// it through ch3-5 (the actual switching outputs). Without this
	// override HA renders a non-functional switch entity that would
	// throw RPC errors on every toggle.
	if !ev.Writable {
		switch comp { //nolint:exhaustive // only writable-eligible components matter
		case HAComponentSwitch, HAComponentLock, HAComponentLight, HAComponentSelect, HAComponentNumber:
			comp = HAComponentBinarySensor
			if ev.Parameter != "STATE" {
				// Only STATE has a clean bool→binary_sensor mapping;
				// other params (LEVEL, SET_POINT_TEMPERATURE, …) become
				// numeric sensors instead.
				comp = HAComponentSensor
			}
		}
	}
	// _SWITCH_DP_TO_SENSOR override: certain (model, parameter) pairs must
	// surface as read-only sensors regardless of the descriptor's operations.
	if generic.IsForceSensorParameter(ev.Model, hmenum.Parameter(ev.Parameter)) {
		comp = HAComponentSensor
	}
	bucket := payload.BucketValues
	switch {
	case ev.descParamset() == hmenum.ParamsetKeyMaster:
		bucket = payload.BucketMaster
	case ev.Calculated:
		// Calculated DPs publish their state under `calculated/<name>`;
		// discovery's `state_topic` must point at the same bucket or
		// HA reads the (empty) values/ topic and shows the sensor as
		// unavailable.
		bucket = payload.BucketCalculated
	}
	central := d.centralFor(ev)
	pd := naming.NewDataPointPathData(
		central,
		hmtypes.ParseWireInterfaceID(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
		bucket,
		ev.Parameter,
	)
	nodeID = pd.DiscoveryNodeID(central)
	objectID = pd.DiscoveryObjectID(ev.Parameter)
	// The unique_id follows the same family split as the state bucket
	// above: a calculated DP carries the `calculated` marker, so the MQTT
	// key matches the REST and WS ones a consumer keys its registry on.
	chAddr := ev.DeviceAddress + ":" + strconv.Itoa(ev.ChannelNo)
	// A parameter forced to a read-only sensor (HmIP-eTRV / HmIP-HEATING
	// LEVEL) carries the same "_sensor" disambiguation suffix the daemon's
	// internal and REST identities use, so all three planes spell one
	// identity. The classifier above already renders these as Sensor.
	keyParameter := ev.Parameter
	if generic.IsForceSensorParameter(ev.Model, hmenum.Parameter(ev.Parameter)) {
		keyParameter += datapoint.ForcedSensorSuffix
	}
	uniqueID, scoped := d.scopedUniqueID(ev.Central, chAddr, keyParameter, "")
	if ev.Calculated {
		uniqueID, scoped = d.scopedUniqueID(ev.Central, chAddr, ev.Parameter, routingkey.CalculatedFamilyPrefix)
	}
	if !scoped {
		return "", "", "", nil, false
	}

	stateTopic := pd.MQTTState(d.TopicBuilder.Base, central)
	commandTopic := pd.MQTTCommand(d.TopicBuilder.Base, central)

	// The frame — device, origin, unique_id, name, the two topics, the
	// availability list and the envelope's value template — is rendered by
	// the shared model (ADR 0070) rather than assembled here. What follows
	// the render is the decoration chain: the Quantity tables, the HA
	// registry rules and the per-component vocabulary that reads their
	// verdicts back, which is why they cannot run before it.
	entityLabel, entityLabelNull := entityName(ev)
	vocab := perDatapointVocabulary(ev, comp)
	modelEntity := &perDatapointEntity{
		Basic: hamodel.Basic{
			EntityKey:      objectID,
			EntityPlatform: hacatalog.Platform(comp),
			Description: hamodel.Description{
				Name: hamodel.L(entityLabel),
				// bridge + device + the DP's own `available` flag, in that
				// order. [hamodel.LevelSelf] resolves against the state
				// binding's envelope, which is the third entry this plane
				// has always appended by hand.
				Availability: hamodel.Availability{Levels: []hamodel.AvailabilityLevel{
					hamodel.LevelBridge, hamodel.LevelDevice, hamodel.LevelSelf,
				}},
				// json_attributes_topic + template — exposes the per-DP config
				// payload (min/max/value_list/unit/default/usage) as HA entity
				// attributes for diagnostics.
				JSONAttributesTopic: d.TopicBuilder.ParameterConfig(
					central, ev.Interface, ev.DeviceAddress, ev.ChannelNo, bucket, ev.Parameter,
				),
				JSONAttributesTemplate: "{{ value_json | tojson }}",
				ValueTemplate:          vocab.valueTemplate,
				Optimistic:             vocab.optimistic,
				Min:                    vocab.min,
				Max:                    vocab.max,
				Step:                   vocab.step,
			},
			Binds: perDatapointBinds(perDatapointSlot(ev, central, bucket), vocab.writable),
		},
		fields:          vocab.fields,
		commandTemplate: vocab.commandTemplate,
		nameNull:        entityLabelNull,
		comp:            comp,
	}
	if len(vocab.options) > 0 {
		modelEntity.Description.Options = &hamodel.Enum{Codes: vocab.options}
	}
	ctx := perDatapointContext{
		StdContext: hadiscovery.StdContext{
			Layout: perDatapointLayout{
				state:   stateTopic,
				command: commandTopic,
				device:  d.TopicBuilder.DeviceAvailability(central, ev.Interface, ev.DeviceAddress),
				bridge:  d.TopicBuilder.BridgeStatus(),
			},
			Lang:       d.Locale,
			Translator: d.tr,
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
	}
	entity, renderErr := hadiscovery.RenderComponent(
		ctx, modelDeviceFromInfo(deviceDescriptor(ev, d.hubURLFor(ev), d.SubDevicesEnabled)),
		modelEntity, *BuildOriginInfo(),
	)
	if renderErr != nil {
		return "", "", "", nil, false
	}
	// device_class — Quantity-based resolution walks the
	// (deviceModel, parameter, unit) → Quantity → HA device_class chain.
	// Falls back to the legacy parameter-name table when no Quantity
	// classification applies (rare but covers a few device classes not
	// covered by the Quantity table, like "duration").
	if dc := componentDeviceClass(comp, ev.Model, ev.Parameter, ev.descUnit()); dc != "" {
		entity.DeviceClass = dc
	} else if dc, ok := deviceClassFor(ev.Parameter); ok {
		entity.DeviceClass = dc
	}
	if cat, ok := entityCategoryFor(ev.Parameter); ok {
		entity.EntityCategory = hacatalog.EntityCategory(cat)
	}
	// MASTER-paramset default: all configuration parameters belong to HA's
	// "config" entity-category so they are relegated to the secondary
	// "Configuration" section in the HA UI instead of cluttering the primary
	// dashboard. Per-parameter overrides (via EntityDescriptionFor) applied
	// below can still promote a MASTER param to no-category or "diagnostic" when
	// semantically appropriate.
	if ev.descParamset() == hmenum.ParamsetKeyMaster {
		entity.EntityCategory = EntityCategoryConfig
	}
	// state_class — only applies to sensor entities. The value behaviour is
	// the domain's answer (parameter.MetadataFor); this adapter only renames
	// it into Home Assistant's vocabulary. A parameter the domain cannot
	// classify gets no state_class rather than a guess from its name.
	if comp == HAComponentSensor {
		if cls := resolveSensorStateClass(ev.Model, ev.Parameter, ev.descUnit()); cls != "" {
			entity.StateClass = hacatalog.StateClass(cls)
		}
	}
	// `suggested_display_precision` is sourced exclusively from the
	// HA entity registry (HARegistryDescription.SuggestedPrecision) applied
	// below via applyEntityDescription. The discovery payload does not
	// derive precision from a parameter-name table to avoid over-emitting
	// vs. the HA-native integration.

	// HARegistryDescription overrides — applied after the Quantity-based resolution
	// so the per-parameter/device table takes precedence over the Quantity-derived defaults.
	if desc := EntityDescriptionFor(comp, ev.Model, ev.Parameter); desc.HasHAOverrides() {
		if desc.EntityCategory != "" {
			entity.EntityCategory = hacatalog.EntityCategory(desc.EntityCategory)
		}
		if desc.EnabledByDefault != nil {
			entity.EnabledByDefault = hadiscovery.Ptr(*desc.EnabledByDefault)
		}
		if desc.Icon != "" {
			entity.Icon = desc.Icon
		}
		if desc.SuggestedDisplayPrecision != nil {
			entity.Precision = hadiscovery.Ptr(*desc.SuggestedDisplayPrecision)
		}
		if desc.UnitOfMeasurement != "" {
			entity.UnitOfMeasure = desc.UnitOfMeasurement
		}
		if desc.DeviceClass != "" {
			entity.DeviceClass = desc.DeviceClass
		}
		if desc.StateClass != "" {
			entity.StateClass = hacatalog.StateClass(desc.StateClass)
		}
	}
	// Authoritative HA-
	// attribute source. Applied after the legacy EntityDescriptionFor
	// table so it wins. The lookup uses the **raw** model category
	// (`ev.Category`) when available
	// Keys rules by
	// (`button`/`action`/`switch`/`schedule_switch`/…), not the
	// HA-collapsed component. For ACTION-DPs the lookup must miss the
	// BUTTON default (the HA integration would not apply it either) so
	// openccu-loom does not over-emit `translation_key=button_press`.
	hmipCat := string(ev.Category)
	if hmipCat == "" {
		hmipCat = string(comp)
	}
	haDesc := applyEntityDescription(&entity, hmipCat, ev.Parameter, ev.Model, ev.descUnit(), "")
	// The lookup above is keyed on the model's category and `comp` may have
	// been downgraded since (a read-only switch renders as a binary_sensor),
	// so the description can carry a device class the rendered platform does
	// not declare. Home Assistant drops such a class in silence.
	entity.DeviceClass = dropForeignDeviceClass(comp, entity.DeviceClass)
	// MASTER-paramset fallback: when neither EntityDescriptionFor nor
	// the HA integration sets an entity_category, force "config". This is a
	// openccu-loom-MQTT UX convention so MASTER parameters land in HA's
	// "Configuration" section instead of the primary dashboard. The
	// HA-native integration handles MASTER via a separate UI panel, so
	// Set-only-if-missing
	// preserves per-parameter overrides like RSSI_DEVICE → "diagnostic".
	if ev.descParamset() == hmenum.ParamsetKeyMaster {
		if entity.EntityCategory == "" {
			entity.EntityCategory = EntityCategoryConfig
		}
	}

	// The residue of the per-component switch: everything below reads a
	// verdict the decoration chain above produced, so it cannot move into
	// [perDatapointVocabulary] and run before the render. Everything that
	// can has.
	switch comp { //nolint:exhaustive // only the components whose vocabulary depends on the chain above appear here
	case HAComponentBinarySensor:
		// NOTE on expire_after: deliberately NOT set for binary_sensor.
		// Door / window contacts, sabotage flags, alarm bits and similar
		// are event-driven — they only emit when the state changes. A door
		// that stays closed for a week sends no events, but the sensor
		// itself is fine; an `expire_after=3600` would falsely mark it
		// "unavailable" after an hour of inactivity. Availability is
		// already covered by the per-device UNREACH topic the daemon
		// publishes via [EventBridge.markAvailability].
		//
		// force_update + off_delay are different concerns: HA's
		// last_changed should still advance for motion/presence bursts,
		// and the auto-reset is HA-side state-machine behaviour that
		// doesn't claim the sensor is offline.
		//
		// The pair is a patch rather than part of the entity's declared
		// vocabulary because it keys on the RESOLVED device class — the
		// Quantity chain, the registry rules and dropForeignDeviceClass
		// all have a say in that, and all of them run after the render.
		if binaryFields, ok := entity.Fields.(hadiscovery.BinarySensorFields); ok &&
			isMotionDeviceClass(entity.DeviceClass) {
			binaryFields.ForceUpdate = hadiscovery.Ptr(true)
			// off_delay=300 → HA auto-resets the binary_sensor after five
			// minutes without a follow-up update, motion/presence/
			// occupancy. Without this motion sensors stay "on" forever
			// after the first trigger.
			binaryFields.OffDelay = hadiscovery.Ptr(300)
			entity.Fields = binaryFields
		}
	case HAComponentSensor:
		// Enum-typed sensors (device_class=enum) require an `options` list —
		// without it HA refuses the discovery. Source: paramset descriptor's
		// VALUE_LIST. The reference stack lowercases enum sensor options and
		// states ("CLOSED" → "closed", "IDLE_OFF" → "idle_off") so they are
		// translatable in HA; mirror that by lowercasing the options and
		// piping the state through the `| lower` template.
		//
		// Keyed on the RESOLVED device class, which is why it is here and
		// not in the declared vocabulary: only the chain above knows whether
		// this parameter ended up an enum.
		if entity.DeviceClass == "enum" && len(ev.descValueList()) > 0 {
			if labels, ok := localisedEnumOptions(ev); ok {
				entity.Options = labels
				entity.ValueTemplate, _ = enumOptionTemplates(ev.descValueList(), ev.descValueLabels())
			} else {
				entity.Options = lowercasedOptions(ev.descValueList())
				entity.ValueTemplate = valueJSONValueLowerTemplate
			}
		}
		// The wire unit, cleaned of the CCU's firmware quirks ("100%" → "%",
		// "Lux" → "lx", "degree" → "°C", …) — but only where the
		// authoritative passes above left the field empty. Without that
		// guard the raw CleanupUnit result would overwrite a rule's own
		// override (GAS_FLOW's "m³/h" by the descriptor's "m³"), which HA
		// rejects as `device_class volume_flow_rate not valid with m³`.
		// See [unitOrWireFallback].
		entity.UnitOfMeasure = unitOrWireFallback(ev, entity.UnitOfMeasure)
		// Apply data_point.multiplier so HA receives the same scaled
		// value the Python reference implementation's HA integration
		// would emit (`sensor.py:161-169`, `:201`:
		// `new_value = self._data_point.value * self._multiplier`).
		// Without this template Energy/Power readings would be off by
		// the unit factor when the CCU firmware reports the raw count.
		applyMultiplierSensor(ev, &entity, registryMultiplier(haDesc))
	case HAComponentNumber:
		// Scale `min`/`max`/`step` by `data_point.multiplier` and invert
		// the scaling on writes (`value / multiplier`). The bounds it
		// scales were seeded by the declared vocabulary; the multiplier
		// itself comes from the registry rule the chain above matched, so
		// the scaling cannot precede the render.
		applyMultiplierNumber(ev, &entity, registryMultiplier(haDesc))
		// unit_of_measurement defaults to the Python reference
		// implementation's `data_point.unit` when the HARegistryDescription
		// doesn't override (`number.py:236-237`). Mirror that here so
		// wire units like "s" / "%" / "°C" propagate to HA.
		entity.UnitOfMeasure = unitOrWireFallback(ev, entity.UnitOfMeasure)
	case HAComponentSelect:
		// Action-selects (write-only enum parameters) are operator inputs;
		// the reference stack relegates them to HA's Configuration section.
		// Last-wins over the whole entity-category chain above, which is
		// the reason it is applied here.
		if ev.Category == hmenum.DataPointCategoryActionSelect {
			entity.EntityCategory = EntityCategoryConfig
		}
	case HAComponentEvent:
		// `device_class: button` is the canonical HA choice for key-press
		// events, and it overrides whatever the registry chain resolved —
		// the event platform's class vocabulary is its own.
		entity.DeviceClass = EventDeviceClassForModel(ev.Model)
	}
	return discoveryItemFor(entity, nodeID, objectID).unpack()
}

// unpack restores the tuple [DefaultDiscoveryBuilder.Build] returns. The
// item form is what the shared helpers produce; the tuple is what this
// plane's callers have always taken, and widening that signature is a
// change to every call site for no gain here.
func (i DiscoveryItem) unpack() (component, nodeID, objectID string, buf []byte, ok bool) {
	if !i.OK {
		return "", "", "", nil, false
	}
	return i.Component, i.NodeID, i.ObjectID, i.Payload, true
}

// unitOrWireFallback returns the unit already established by the
// authoritative table passes, or the cleaned-up wire-descriptor unit when
// they left it empty — the `BaseHmEntity` order, where the wire unit is the
// last resort rather than the first.
func unitOrWireFallback(ev Event, current string) string {
	if current != "" {
		return current
	}
	return generic.CleanupUnit(hmenum.Parameter(ev.Parameter), ev.descUnit())
}

// jsonValueTemplate returns the Jinja template HA needs to extract
// the scalar from the bridge's JSON state payload, with the right
// post-filter for the entity type. Booleans on switch/binary_sensor/
// lock get a `| lower` filter so the on/off matchers see "true"/
// "false" rather than "True"/"False". The `is defined` guard catches
// the register-and-load-data eviction case where HA reads an empty
// retained payload — without it Jinja raises `'value_json' is
// undefined` and HA logs the error for every such sensor at startup.
func jsonValueTemplate(comp HAComponent) string {
	switch comp {
	case HAComponentSwitch, HAComponentBinarySensor, HAComponentLock:
		return valueJSONValueLowerTemplate
	default:
		return valueJSONValueTemplate
	}
}

// lowercasedOptions converts a descriptor VALUE_LIST into the
// lower-cased `options` array HA receives for enum sensors and
// selects. The reference stack lowercases enum tokens so HA can
// translate them; the `| lower` value_template keeps the state side
// consistent and the select command_template (`| upper`) restores the
// CCU token on write.
// localisedEnumOptions returns the localised options for an enum entity
// and whether they are usable. They are usable only when the labeler
// supplied one label per value and the labels are distinct and
// non-empty — Home Assistant addresses an option by its display string,
// so a duplicate or empty label would make the write ambiguous. The
// caller then falls back to the raw tokens, which look worse but never
// misroute a command.
func localisedEnumOptions(ev Event) ([]string, bool) {
	values, labels := ev.descValueList(), ev.descValueLabels()
	if len(labels) == 0 || len(labels) != len(values) {
		return nil, false
	}
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, len(labels))
	for i, l := range labels {
		if strings.TrimSpace(l) == "" {
			return nil, false
		}
		if _, dup := seen[l]; dup {
			return nil, false
		}
		seen[l] = struct{}{}
		out[i] = l
	}
	return out, true
}

// binarySensorPayloads returns the (off, on) tokens a binary_sensor has to
// declare so they match what the state plane renders for that data point
// through [valueJSONValueLowerTemplate].
//
// A BOOL data point publishes the JSON boolean, which the template renders
// as "false" / "true" — the case-stable spelling of Jinja's `True`/`False`
// that HA's uppercase `payload_on:"ON"` defaults would never match.
//
// An ENUM data point publishes its VALUE_LIST label instead: the
// EventBridge's PerDPState path replaces the wire index with the label
// (`paramlib.EnumLabelFromWire`) before the state topic is written, so
// the slot carries `{"value":"OPEN"}` and the template renders
// "open". Declaring the boolean pair for those compares "open" against
// "true", and HA's binary_sensor matches payload_on / payload_off by exact
// string — on a miss it logs at INFO and returns without touching the
// entity's state. The contact therefore stays *available* and `unknown`
// forever, with no error anywhere. Every door / window contact in the
// fleet is one of these.
//
// Two-entry lists map first→off, second→on. That holds for the descriptors
// which reach this function, and NOT for two-entry enums in general — the
// distinction is the whole content of the rule.
//
// Measured over the device-descriptor corpus: of 81 distinct two-entry
// VALUE_LISTs only 29 put an inactive-looking label first, and one is
// literally {ON, OFF}. Restricted to what can become a binary sensor —
// two entries, in VALUES, not writable — all 14 do: NORMAL, STABLE,
// NO_ERROR (nine variants), CLOSED, DRY. The counterexamples are writable
// config and action selects, which never take this path.
//
// So the rule is sound where it is applied and would be wrong one step
// wider. It also rests on the standing assumption under every enum-index
// decision here: that a VALUE_LIST delivered over XML-RPC is ordered by
// ordinal, which no firmware source consulted so far states. A longer list has no correct
// declaration — HA offers exactly one on and one off token — so it keeps
// the boolean pair rather than silently claiming two of its values;
// no descriptor in the fleet reaches that branch.
func binarySensorPayloads(ev Event) (off, on string) {
	if vl := ev.descValueList(); ev.descType() == hmenum.ParameterTypeEnum && len(vl) == 2 {
		return strings.ToLower(vl[0]), strings.ToLower(vl[1])
	}
	return "false", "true"
}

func lowercasedOptions(valueList []string) []string {
	opts := make([]string, len(valueList))
	for i, v := range valueList {
		opts[i] = strings.ToLower(v)
	}
	return opts
}

// discoveryNodeID is retained as a thin alias to the canonical
// [naming.PathData.DiscoveryNodeID] form. Used by the few discovery
// helpers (week-profile, update-entity) that don't yet build their
// PathData up front.
func discoveryNodeID(centralName, deviceAddress string) string {
	pd := naming.NewDevicePathData("", deviceAddress)
	return pd.DiscoveryNodeID(centralName)
}

// registryMultiplier extracts the HA-registry Multiplier override from a
// matched entity-description rule (nil when no rule matched or the rule
// left Multiplier unset), for [applyMultiplierSensor] / [applyMultiplierNumber].
func registryMultiplier(desc *HARegistryDescription) *float64 {
	if desc == nil {
		return nil
	}
	return desc.Multiplier
}

// resolveMultiplier picks the scaling factor for ev.Parameter. The
// per-entity registry override wins when present, mirroring the Python
// reference implementation's precedence (`entity_description.multiplier
// if ... is not None else data_point.multiplier`, `sensor.py:196-201`,
// `number.py:226-235`): the descriptor-driven multiplier
// ([channelMultiplierReader.ParameterMultiplier], unit-based) only sees a
// parameter's *reported* CCU unit, which is empty for several device/
// parameter combinations (e.g. LEVEL on HmIP-eTRV/-HEATING/-FALMOT-C12)
// whose registry rule still declares the /100 valve-position scaling —
// without the override those readings publish unscaled (0.42 instead of
// 42).
func resolveMultiplier(ev Event, override *float64) (float64, bool) {
	if override != nil && *override != 0 && *override != 1.0 {
		return *override, true
	}
	r, ok := ev.Channel.(channelMultiplierReader)
	if !ok {
		return 0, false
	}
	return r.ParameterMultiplier(ev.Parameter)
}

// applyMultiplierSensor patches body["value_template"] when ev.Channel or
// override reports a non-trivial multiplier for ev.Parameter. The emitted
// Jinja template multiplies the wire scalar — `value_json.value` on a
// state topic, the bare payload on a raw one — by the multiplier.
func applyMultiplierSensor(ev Event, entity *hadiscovery.Component, override *float64) {
	m, nontrivial := resolveMultiplier(ev, override)
	if !nontrivial {
		return
	}
	// State topics carry the JSON envelope (ADR 0011); the multiplied
	// template pulls value_json.value, wrapped in the defined/not-none
	// guard so HA renders empty (entity "unknown") when the slot carries
	// no payload yet (empty eviction body) or a null value (unobserved
	// DP boot publish) rather than logging Jinja errors or rendering a
	// misleading multiplied 0.0.
	entity.ValueTemplate = fmt.Sprintf("{%% if value_json is defined and value_json.value is not none %%}{{ (value_json.value | float * %s) }}{%% endif %%}", formatMultiplier(m))
}

// applyMultiplierNumber patches the component so HA scales `min`/`max`/`step`
// to the multiplied range and inverts the multiplier on writes.
func applyMultiplierNumber(ev Event, entity *hadiscovery.Component, override *float64) {
	m, nontrivial := resolveMultiplier(ev, override)
	if !nontrivial {
		return
	}
	mStr := formatMultiplier(m)
	// State topics carry JSON; the multiplied template pulls
	// value_json.value (ADR 0011 — JSON is the only supported shape).
	// The defined/not-none guard renders empty (entity "unknown") for
	// the empty eviction body or an unobserved null value instead of a
	// misleading multiplied 0.0.
	entity.ValueTemplate = fmt.Sprintf("{%% if value_json is defined and value_json.value is not none %%}{{ (value_json.value | float * %s) }}{%% endif %%}", mStr)
	// Write template — invert (HA-supplied value / multiplier).
	entity.CommandTemplate = fmt.Sprintf("{{ (value | float / %s) }}", mStr)
	// Bounds — multiply min/max/step if the component already carries them
	// (number-bound population is descriptor-driven).
	if entity.Min != nil {
		entity.Min = hadiscovery.Ptr(*entity.Min * m)
	}
	if entity.Max != nil {
		entity.Max = hadiscovery.Ptr(*entity.Max * m)
	}
	if entity.Step != nil {
		entity.Step = hadiscovery.Ptr(*entity.Step * m)
	}
}

// formatMultiplier returns m rendered without trailing zeros so the
// emitted Jinja template stays readable (`0.1` instead of `0.100000`).
func formatMultiplier(m float64) string {
	s := strconv.FormatFloat(m, 'f', -1, 64)
	return s
}

// pressEventTypesFor returns the HA `event_types` list for a button-press
// parameter. HA's `event` platform requires the full list of possible event
// types upfront.
//
// Mapping mirrors
// `ChannelEventGroup` device_trigger_event_type groupings:
//
// - PRESS_SHORT → ["press_short"]
// - PRESS_LONG → ["press_long"]
// - PRESS_LONG_RELEASE → ["press_long_release"]
// - PRESS_LONG_START → ["press_long_start"]
//
// When no specific mapping is found the parameter name is lower-cased and
// returned as the sole element so novel parameters don't silently vanish.
func pressEventTypesFor(parameter string) []string {
	switch strings.ToUpper(parameter) {
	case "PRESS_SHORT":
		return []string{"press_short"}
	case "PRESS_LONG":
		return []string{"press_long"}
	case "PRESS_LONG_RELEASE":
		return []string{"press_long_release"}
	case "PRESS_LONG_START":
		return []string{"press_long_start"}
	}
	return []string{strings.ToLower(parameter)}
}

// deviceClassFor maps a parameter onto the matching HA device_class
// when one applies. Returns false to signal "no device_class hint",
// which lets HA fall back to its default rendering.
func deviceClassFor(parameter string) (string, bool) {
	switch strings.ToUpper(parameter) {
	case "ACTUAL_TEMPERATURE", "TEMPERATURE", "SET_POINT_TEMPERATURE", "SET_TEMPERATURE",
		// Calculated climate sensors with a temperature semantic.
		// DEW_POINT / FROST_POINT report the °C below ambient where
		// condensation / freezing would occur; APPARENT_TEMPERATURE is
		// the felt temperature.
		"DEW_POINT", "FROST_POINT", "APPARENT_TEMPERATURE":
		return "temperature", true
	case "HUMIDITY":
		return "humidity", true
	case "DEW_POINT_SPREAD":
		// A K-spread between actual and dew-point temperature.
		// HA has no dedicated `temperature_delta` device_class on
		// every release — fall through to no device_class to keep
		// the discovery payload portable.
		return "", false
	case "VAPOR_CONCENTRATION":
		// g/m³ water vapour in air. HA has no dedicated device_class
		// for this; leaving it unset is the safe default.
		return "", false
	case "ENTHALPY":
		// kJ/kg specific enthalpy of humid air. No HA device_class.
		return "", false
	case "WINDOW_OPEN":
		return "window", true
	case "SMOKE_ALARM":
		return "smoke", true
	case "INTRUSION_ALARM":
		return "tamper", true
	case "POWER", "GAS_POWER":
		return "power", true
	case "ENERGY_COUNTER", "GAS_ENERGY_COUNTER":
		return "energy", true
	case "VOLTAGE", "OPERATING_VOLTAGE":
		return "voltage", true
	case "CURRENT":
		return "current", true
	case "FREQUENCY":
		return "frequency", true
	case "AIR_PRESSURE":
		return "atmospheric_pressure", true
	case "BRIGHTNESS", "ILLUMINATION":
		return "illuminance", true
	case "WIND_SPEED":
		return "wind_speed", true
	case "BATTERY_STATE", "OPERATING_VOLTAGE_LEVEL":
		return "battery", true
	case "RSSI_DEVICE", "RSSI_PEER":
		return "signal_strength", true
	case "LOW_BAT":
		return "battery", true
	case "UNREACH", "STICKY_UNREACH":
		return "connectivity", true
	case "WINDOW_STATE", "DOOR_STATE":
		return "door", true
	case "MOTION", "PRESENCE_DETECTION_STATE":
		return "motion", true
	case "RAINING":
		return "moisture", true
	case "CONFIG_PENDING", "UPDATE_PENDING":
		return "problem", true
	}
	return "", false
}

// entityCategoryFor places diagnostic / configuration parameters in
// HA's secondary buckets so they don't pollute the main dashboard.
func entityCategoryFor(parameter string) (string, bool) {
	switch strings.ToUpper(parameter) {
	case "RSSI_DEVICE", "RSSI_PEER",
		"OPERATING_VOLTAGE", "OPERATING_VOLTAGE_LEVEL",
		"LOW_BAT", "UNREACH", "STICKY_UNREACH",
		"CONFIG_PENDING", "UPDATE_PENDING",
		"BATTERY_STATE":
		return "diagnostic", true
	}
	return "", false
}

// entityName returns the value to assign to the HA Discovery `name`
// field. Returns the literal `nil` (HA's signal for "use the device
// name alone for friendly_name and entity_id") when the parameter is
// flagged primary by the embedded translation_custom catalogue (see
// [GenericConfig.LabelOmitted]). Otherwise returns the locale-aware
// label via [naming.EntityDisplayName] — the single source of truth
// shared with the REST data-point handler so both emit identical names.
func entityName(ev Event) (name string, null bool) {
	name, omitted := naming.EntityDisplayName(ev.descLabel(), ev.descLabelOmitted(), ev.Parameter)
	if omitted {
		// An omitted label publishes `name: null`, which is how Home
		// Assistant is told to show the device's name alone. It is not the
		// same as an absent key, which makes Home Assistant derive one — see
		// [hadiscovery.Component.NameNull].
		return "", true
	}
	return name, false
}

// assignDeviceInfo copies the whitelisted keys of a device's `payload:"info"`
// partition onto the typed device block.
//
// This used to be a loop over a haDeviceFields whitelist, because the info
// partition carries HM-specific fields (`interface`, `interfaceid`,
// `model_icon`, `model_label`, `product_group`) that Home Assistant rejects
// with `extra keys not allowed @ data['device'][...]`. The whitelist is gone:
// [hadiscovery.DeviceInfo] *is* the set of keys Home Assistant accepts, so a
// field that does not exist there cannot be assigned here.
//
// Assignment is per key present, in the caller's order, so an info partition
// that carries one of the pre-set fields still overrides it exactly as the
// loop did.
func assignDeviceInfo(dev *hadiscovery.DeviceInfo, info map[string]any) {
	str := func(key string) (string, bool) {
		v, ok := info[key].(string)
		return v, ok && v != ""
	}
	if v, ok := info["identifiers"].([]string); ok && len(v) > 0 {
		dev.Identifiers = v
	}
	if v, ok := info["connections"].([][2]string); ok && len(v) > 0 {
		dev.Connections = v
	}
	if v, ok := str("manufacturer"); ok {
		dev.Manufacturer = v
	}
	if v, ok := str("model"); ok {
		dev.Model = v
	}
	if v, ok := str("model_id"); ok {
		dev.ModelID = v
	}
	if v, ok := str("name"); ok {
		dev.Name = v
	}
	if v, ok := str("serial_number"); ok {
		dev.SerialNumber = v
	}
	if v, ok := str("sw_version"); ok {
		dev.SWVersion = v
	}
	if v, ok := str("hw_version"); ok {
		dev.HWVersion = v
	}
	if v, ok := str("via_device"); ok {
		dev.ViaDevice = v
	}
	if v, ok := str("suggested_area"); ok {
		dev.SuggestedArea = v
	}
	if v, ok := str("configuration_url"); ok {
		dev.ConfigurationURL = v
	}
}

// deviceDescriptor builds the HA `device` block. When ev.Device is
// non-nil we harvest its `payload:"info"` tags — that is the payload
// Partition
// every field HA does not accept (see [haDeviceFields]). Missing
// HA-required fields fall back to event-level defaults.
// isMotionDeviceClass reports whether dc is one of the HA
// binary_sensor device-classes that benefit from `force_update=true`
// + `off_delay=300`.
// (binary_sensor.py — `device_class in {motion, presence, occupancy}`).
func isMotionDeviceClass(dc string) bool {
	switch dc {
	case "motion", "presence", "occupancy":
		return true
	}
	return false
}

// deviceWithSwVersion is the narrow read-side contract the device-
// block builder uses to extract the current firmware string for HA's
// `sw_version` field. `*device.Device` exposes [Device.SwVersion]
// for exactly this purpose; defining it locally as an unexported
// interface keeps the mqtt package free of the model import.
type deviceWithSwVersion interface {
	SwVersion() string
}

// deviceWithRoom is the narrow read-side contract the device-block
// builder uses to extract the device's single canonical room for HA's
// `suggested_area`. The device model derives it from the operator's room
// assignment behind its own lock, so the block builder asks for it instead
// of reflecting over a field that a concurrent request may be rewriting.
type deviceWithRoom interface {
	Room() string
}

// deviceWithSubDevices is the narrow read-side contract used to decide
// whether the parent device should be split into multiple HA sub-devices.
// `*device.Device` satisfies it via [Device.HasSubDevices].
type deviceWithSubDevices interface {
	HasSubDevices() bool
}

// deviceDescriptor builds the HA `device` block. hubURL, when
// non-empty, is propagated into the `configuration_url` field
// callers source it from [DefaultDiscoveryBuilder.Hub.URL] so the
// per-device configuration link points at the same CCU WebUI as
// the synthetic hub device. Pass "" to omit the field.
//
// When subDevices is true and the event's parent device + channel report
// `HasSubDevices() && IsInMultiGroup()`, the descriptor identifies the
// logical sub-device (one HA device per channel group) and stamps the
// parent device as `via_device`. Otherwise the descriptor identifies the
// physical device with the central as `via_device`.
// physicalDeviceIdentifier returns the HA device-block `identifiers` value for
// a physical CCU device. It is the single source of truth for that string so
// per-device-DP discovery ([deviceDescriptor]) and device-linked hub-entity
// discovery ([hubEntityDeviceBlock]) always agree — HA only merges an entity
// into a device when the identifier matches byte-for-byte.
//
// Addresses that repeat verbatim across CCUs — the INT000* internal devices,
// the virtual-remote buses (BidCoS-RF, HmIP-RCV-1, BidCoS-Wir) and the hub
// pseudo-addresses — carry the central slug so two CCUs never collapse into a
// single HA device card. The slug spelling is [safeLower], the same one
// [centralDeviceIdentifier] stamps into the hub card and every via_device, so
// the whole device hierarchy reads consistently. Globally unique hardware
// addresses keep their bare identifier, so a single-CCU device is unchanged.
// This mirrors the gate the entity unique_id path applies via [scopedUniqueID]
// ([routingkey.NeedsCentralScope]).
func physicalDeviceIdentifier(centralName, deviceAddress string) string {
	addr := strings.ToLower(deviceAddress)
	if centralName != "" && routingkey.NeedsCentralScope(deviceAddress) {
		return "openccu-loom_" + safeLower(centralName) + "_" + addr
	}
	return "openccu-loom_" + addr
}

// centralDeviceIdentifier returns the HA device-block `identifiers` value for
// the synthetic central hub card. Single source of truth so the hub card
// ([hubDeviceBlock]) and every `via_device` that references it agree
// byte-for-byte — HA resolves a parent only on an exact match, and a device
// whose via_device does not resolve floats at the top level instead of
// nesting under its CCU.
//
// The name goes through the same discovery slug as every other discovery
// identifier, so a central called "Haus CCU" or "Büro" reads the same on both
// halves; a plain lower-casing left space and umlaut untouched and broke the
// hierarchy for every such CCU.
func centralDeviceIdentifier(centralName string) string {
	return "openccu-loom_central_" + safeLower(centralName)
}

func deviceDescriptor(ev Event, hubURL string, subDevices bool) *hadiscovery.DeviceInfo {
	parentID := physicalDeviceIdentifier(ev.Central, ev.DeviceAddress)
	dev := &hadiscovery.DeviceInfo{
		Identifiers:  []string{parentID},
		Manufacturer: "eQ-3",
	}
	// Stamp via_device so HA renders this device as a child of the
	// OpenCCU-Loom central — same hierarchy as the Python reference
	// implementation's `generic_entity.py:142` and
	// `platforms/generic_entity.py:118`. A device without
	// via_device floats at the top level, mixed with the central
	// itself — confusing in the HA Devices view.
	if ev.Central != "" {
		dev.ViaDevice = centralDeviceIdentifier(ev.Central)
	}
	// Sub-device override: when enabled and the parent device + channel
	// confirm the multi-group structure, swap the descriptor to identify
	// the sub-device.
	var subDeviceName string
	if subDevices && ev.Device != nil && ev.Channel != nil {
		hasSubs := false
		if hsd, ok := ev.Device.(deviceWithSubDevices); ok {
			hasSubs = hsd.HasSubDevices()
		}
		if hasSubs {
			if sdi, ok := ev.Channel.(SubDeviceInspector); ok && sdi.IsInMultiGroup() {
				groupNo := sdi.GroupNumber()
				if groupNo > 0 {
					subDeviceID := parentID + "-" + strconv.Itoa(groupNo)
					dev.Identifiers = []string{subDeviceID}
					dev.ViaDevice = parentID
					subDeviceName = sdi.SubDeviceName()
				}
			}
		}
	}
	var (
		room       string
		modelLabel string
	)
	if ev.Device != nil {
		info := payload.ForWith(ev.Device, payload.KindInfo, payload.Options{UseAltNames: true})
		assignDeviceInfo(dev, info)
		// Capture the singular room (set by the model when exactly
		// one room is assigned) for the suggested_area fallback
		// below. Multi-room and unassigned devices intentionally
		// produce no suggested_area: HA accepts only a single string
		// there, and silently picking any entry from a multi-room
		// list mis-attributes devices that span rooms.
		if dwr, ok := ev.Device.(deviceWithRoom); ok {
			room = dwr.Room()
		}
		// Capture the translated, human-readable model label for the
		// model_id fallback below. Not assignable directly because
		// `model_label` is not a key Home Assistant accepts — only
		// `model_id` is, and we deliberately route the label there.
		if ml, ok := info["model_label"].(string); ok {
			modelLabel = ml
		}
	}
	// Sub-device naming wins over both the harvested info name and
	// the event-level default — the sub-device represents only the
	// channel-group slice of the physical device.
	switch {
	case subDeviceName != "":
		dev.Name = subDeviceName
	case dev.Name != "":
	case ev.DeviceName != "":
		dev.Name = ev.DeviceName
	default:
		// HA requires a name; fall back to the address so the
		// entity surfaces with a recognisable label rather than
		// being rejected with `required key not provided`.
		dev.Name = ev.DeviceAddress
	}
	if dev.Model == "" && ev.Model != "" {
		dev.Model = ev.Model
	}
	// "HmIP-eTRV-2") and HA `model_id` carries the translated, human-readable
	// label (e.g. "Heizkörperthermo- stat"). Without this, HA only sees the
	// cryptic wire type. `Device.ModelLabel` is filled by
	// [DevicePipeline.WithTranslations] during ingest from the openccu
	// translation catalogue; an empty label (no translation match) leaves
	// model_id unset rather than duplicating the wire type, so HA falls back to
	// its own model rendering.
	if dev.ModelID == "" && modelLabel != "" {
		dev.ModelID = modelLabel
	}
	// Stamp sw_version from the device's firmware tracker. Empty firmware
	// strings (CCU has not reported one yet) leave the field unset rather than
	// emitting "" — HA renders "Unknown" cleanly when sw_version is absent.
	if dev.SWVersion == "" && ev.Device != nil {
		if dwsv, ok := ev.Device.(deviceWithSwVersion); ok {
			if v := dwsv.SwVersion(); v != "" {
				dev.SWVersion = v
			}
		}
	}
	// configuration_url points HA at the CCU's WebUI. Same value as the
	// synthetic hub device (hubDeviceBlock embeds info.URL there) so HA's "Visit
	// device" button on the per-device card opens the same operator console.
	if dev.ConfigurationURL == "" && hubURL != "" {
		dev.ConfigurationURL = hubURL
	}
	// Stamp suggested_area from the device's singular room when the
	// model has resolved exactly one assignment. Multi-room devices
	// (the model resolves no singular room) produce no suggested_area
	// on purpose — HA only accepts a single string and an arbitrary
	// pick would mis-attribute the device. `room` is not a key Home
	// Assistant accepts, so this is the only path the per-device room
	// reaches HA Discovery.
	if dev.SuggestedArea == "" && room != "" {
		dev.SuggestedArea = room
	}
	return dev
}

// scopedUniqueID builds a device-bound unique_id and reports whether it
// is safe to publish.
//
// It is not safe when the address only becomes unique through the CCU's
// serial — the virtual-remote buses, INT000*, the hub pseudo-addresses —
// and no serial is registered for the central yet. Every CCU would then
// declare the identical id, and a consumer that keys its entity registry
// on unique_id keeps whichever arrived first. Home Assistant does, and
// the payload is retained, so the second CCU's entities stay missing
// until someone clears the topic by hand.
//
// Skipping is recoverable and visible; colliding is neither. The serial
// arrives with the hub bring-up, and the snapshot that follows it
// publishes what was skipped.
func (d *DefaultDiscoveryBuilder) scopedUniqueID(centralName, address, parameter, prefix string) (string, bool) {
	serial := d.serialSuffix(centralName)
	if serial == "" && routingkey.NeedsCentralScope(address) {
		return "", false
	}
	return routingkey.CanonicalUniqueID(serial, address, parameter, prefix), true
}
