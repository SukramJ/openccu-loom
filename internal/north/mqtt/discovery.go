// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// valueJSONValueTemplate is the canonical Jinja extractor for the
// PerDPState `value` field. The defensive `is defined` guard avoids
// the `'value_json' is undefined` template error HA otherwise raises
// when the state_topic carries an empty retained payload (the
// register-and-load-data eviction flow for unobserved DPs). Empty
// rendering combined with the per-device availability topic yields
// the expected "unavailable" entity badge.
const valueJSONValueTemplate = `{% if value_json is defined %}{{ value_json.value }}{% endif %}`

// valueJSONValueLowerTemplate is the boolean-aware variant used for
// switch / lock / binary_sensor entities. PerDPState carries Python-
// boolean rendering (`{"value":true}` → Jinja `True`/`False` with
// capitalised initial), but HA compares to lowercase tokens
// (`payload_on:"true"`, `payload_off:"false"`). Pipe through `| lower`
// so the comparison is case-stable. Same `is defined` guard semantics
// as the non-lower variant.
const valueJSONValueLowerTemplate = `{% if value_json is defined %}{{ value_json.value | lower }}{% endif %}`

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
)

// Origin metadata embedded in every Discovery payload (HA 2024+).
// Helps HA users see which integration produced an entity.
const (
	originName       = "openccu-loom"
	originSupportURL = "https://github.com/SukramJ/openccu-loom"
)

// originVersionStore is wired by main.SetVersion at startup so the
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
// The optional [PayloadFormat] selector aligns the produced payloads
// with the bridge's wire format. In `bare` mode (default) state
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
}

// NewDefaultDiscoveryBuilder constructs the default builder.
func NewDefaultDiscoveryBuilder(topics *TopicBuilder, centralName string) *DefaultDiscoveryBuilder {
	return &DefaultDiscoveryBuilder{TopicBuilder: topics, BridgeBase: topics.Base, Central: centralName}
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
func (d *DefaultDiscoveryBuilder) SetHubInfoFor(centralName string, info HubInfo) {
	if d == nil || centralName == "" {
		return
	}
	if d.hubs == nil {
		d.hubs = make(map[string]HubInfo)
	}
	d.hubs[centralName] = info
}

// hubFor returns the HubInfo to use for the named central. Falls
// back to the default [Hub] when no per-central entry is registered.
func (d *DefaultDiscoveryBuilder) hubFor(centralName string) HubInfo {
	if d != nil && d.hubs != nil {
		if hi, ok := d.hubs[centralName]; ok {
			return hi
		}
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

// Build translates ev into (component, objectID, payload).
//
// Three paths layered on top of each other:
// 1. Channel-aware aggregator (`aggregateChannel`): when the
// event carries a known custom-domain ChannelType, the whole
// channel collapses into one HA entity (climate, cover, lock,
// light, valve, siren). Per-parameter events on the same
// channel re-emit the same payload — dedup happens in the
// bridge's discovery cache.
// 2. Press-event aggregator (`BuildChannelEvent`): when the channel
// has 2+ PRESS_* parameters, all press types are collapsed into a
// single HA `event` entity with `event_types: [...]`. Per-
// parameter PRESS_* discovery is suppressed for these channels.
// 3. Per-parameter fallback via [resolveComponent]: uses ev.Category
// (model-driven). Drives sensor / binary_sensor / number entities
// that are not part of an aggregate, plus VALUES paramsets on
// channels we don't classify as a custom domain.
func (d *DefaultDiscoveryBuilder) Build(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) {
	if ev.ChannelType != "" {
		if comp, nid, oid, p, agg := d.aggregateChannel(ev); agg {
			return comp, nid, oid, p, true
		}
	}
	// Press-event aggregation: when the channel has 2+ PRESS_* parameters,
	// emit ONE event entity with all press types. Individual PRESS_* events
	// are suppressed — the aggregated entity is what HA receives.
	if isPressParameter(ev.Parameter) {
		if comp, nid, oid, p, agg := d.BuildChannelEvent(ev); agg {
			return comp, nid, oid, p, true
		}
		// Single-press channel (< 2 PRESS_* params) — fall through to the
		// per-parameter path below.
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
	bucket := string(payload.BucketValues)
	switch {
	case ev.descParamset() == hmenum.ParamsetKeyMaster:
		bucket = string(payload.BucketMaster)
	case ev.Calculated:
		// Calculated DPs publish their state under `calculated/<name>`;
		// discovery's `state_topic` must point at the same bucket or
		// HA reads the (empty) values/ topic and shows the sensor as
		// unavailable.
		bucket = string(payload.BucketCalculated)
	}
	pd := naming.NewDataPointPathData(
		hmenum.Interface(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
		naming.Bucket(bucket),
		ev.Parameter,
	)
	nodeID = pd.DiscoveryNodeID(d.Central)
	objectID = pd.DiscoveryObjectID(ev.Parameter)
	uniqueID := routingkey.CanonicalUniqueID(d.serialSuffix(ev.Central), ev.DeviceAddress+":"+strconv.Itoa(ev.ChannelNo), ev.Parameter, "")

	stateTopic := pd.MQTTState(d.TopicBuilder.Base, d.Central)
	commandTopic := pd.MQTTCommand(d.TopicBuilder.Base, d.Central)
	availability := []map[string]string{
		{
			"topic":                 d.TopicBuilder.BridgeStatus(),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 d.TopicBuilder.DeviceAvailability(d.Central, ev.Interface, ev.DeviceAddress),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}

	body := map[string]any{
		"name":        entityName(ev),
		"unique_id":   uniqueID,
		"state_topic": stateTopic,
		// state_topic carries the canonical PerDPState envelope
		// (`{"value": …, "available": …, "type": …, "unit": …,
		// "modified_at": …, "refreshed_at": …}`). HA reads the wire
		// value via the `.value` extractor.
		//
		// `value_json is defined` guards against the
		// register-and-load-data flow where unobserved DPs publish
		// an empty retained payload (eviction) so HA marks the
		// entity unavailable. Without the guard, applying
		// `{{ value_json.value }}` to "" raises the
		// `'value_json' is undefined` template error visible in HA
		// logs. The guard renders empty when no JSON is present
		// HA falls back to "unknown" for the entity, which combined
		// with the per-device availability topic produces the
		// expected "unavailable" badge.
		"value_template":    valueJSONValueTemplate,
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(ev, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}
	// json_attributes_topic + template — exposes the per-DP config payload
	// (min/max/value_list/unit/default/usage) as HA entity attributes for
	// diagnostics.
	body["json_attributes_topic"] = d.TopicBuilder.ParameterConfig(d.Central, ev.Interface, ev.DeviceAddress, ev.ChannelNo, bucket, ev.Parameter)
	body["json_attributes_template"] = "{{ value_json | tojson }}"
	// Device_class — Quantity-based resolution mirrors
	// (deviceModel, parameter, unit) → Quantity → HA device_class.
	// Falls back to the legacy parameter-name table when no Quantity
	// classification applies (rare but covers a few non-AHM-table
	// device-classes like "duration").
	if dc := componentDeviceClass(comp, ev.Model, ev.Parameter, ev.descUnit()); dc != "" {
		body["device_class"] = dc
	} else if dc, ok := deviceClassFor(ev.Parameter); ok {
		body["device_class"] = dc
	}
	if cat, ok := entityCategoryFor(ev.Parameter); ok {
		body["entity_category"] = cat
	}
	// MASTER-paramset default: all configuration parameters belong to HA's
	// "config" entity-category so they are relegated to the secondary
	// "Configuration" section in the HA UI instead of cluttering the primary
	// dashboard. Per-parameter overrides (via EntityDescriptionFor) applied
	// below can still promote a MASTER param to no-category or "diagnostic" when
	// semantically appropriate.
	if ev.descParamset() == hmenum.ParamsetKeyMaster {
		body["entity_category"] = EntityCategoryConfig
	}
	// state_class — only applies to sensor entities. The
	// ValueBehavior→state_class mapping is authoritative; the legacy
	// parameter-name fallback fills the gaps for parameters not yet in
	// the AHM metadata table.
	if comp == HAComponentSensor {
		if cls := resolveSensorStateClass(ev.Model, ev.Parameter, ev.descUnit()); cls != "" {
			body["state_class"] = cls
		} else if cls := stateClassFor(ev.Parameter); cls != "" {
			body["state_class"] = cls
		}
	}
	// `suggested_display_precision` is sourced exclusively from the
	// HA entity registry (HARegistryDescription.SuggestedPrecision) applied
	// below via applyEntityDescription. The discovery payload does not
	// derive precision from a parameter-name table to avoid over-emitting
	// vs. the HA-native integration.

	// MqttEntityDescription overrides — applied after the Quantity-based resolution
	// so the per-parameter/device table takes precedence over the AHM-derived defaults.
	if desc := EntityDescriptionFor(comp, ev.Model, ev.Parameter); desc != (MqttEntityDescription{}) {
		if desc.EntityCategory != "" {
			body["entity_category"] = desc.EntityCategory
		}
		if desc.EnabledDefault != nil {
			body["enabled_by_default"] = *desc.EnabledDefault
		}
		if desc.Icon != "" {
			body["icon"] = desc.Icon
		}
		if desc.SuggestedDisplayPrecision != nil {
			body["suggested_display_precision"] = *desc.SuggestedDisplayPrecision
		}
		if desc.UnitOfMeasurement != "" {
			body["unit_of_measurement"] = desc.UnitOfMeasurement
		}
		if desc.DeviceClass != "" {
			body["device_class"] = desc.DeviceClass
		}
		if desc.StateClass != "" {
			body["state_class"] = desc.StateClass
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
	applyEntityDescription(body, hmipCat, ev.Parameter, ev.Model, ev.descUnit(), "")
	// MASTER-paramset fallback: when neither EntityDescriptionFor nor
	// the HA integration sets an entity_category, force "config". This is a
	// openccu-loom-MQTT UX convention so MASTER parameters land in HA's
	// "Configuration" section instead of the primary dashboard. The
	// HA-native integration handles MASTER via a separate UI panel, so
	// Set-only-if-missing
	// preserves per-parameter overrides like RSSI_DEVICE → "diagnostic".
	if ev.descParamset() == hmenum.ParamsetKeyMaster {
		if _, has := body["entity_category"]; !has {
			body["entity_category"] = EntityCategoryConfig
		}
	}

	switch comp {
	case HAComponentSwitch:
		// Writable boolean — switch carries the on/off payload contract.
		// binary_sensor is intentionally NOT in this case-arm any more:
		// it is read-only and `command_topic` / `state_on` / `state_off`
		// are switch-only fields.
		body["command_topic"] = commandTopic
		body["payload_on"] = "true"
		body["payload_off"] = "false"
		body["state_on"] = "true"
		body["state_off"] = "false"
		// PerDPState envelope carries the value as a JSON boolean
		// (`{"value":true,...}`). Jinja's default rendering of a
		// Python boolean is `True`/`False` (capitalised) — that
		// would never match `state_on`/`state_off` ("true"/"false"),
		// leaving every switch entity stuck in `unknown`. Pipe the
		// scalar through `| lower` so the comparison is
		// case-insensitive. Same defensive `value_json is defined`
		// guard as the default template — without it HA logs
		// `'value_json' is undefined` against an empty retained
		// payload (eviction on unobserved DPs).
		body["value_template"] = valueJSONValueLowerTemplate
		// optimistic=false — without an explicit value HA defaults
		// to true and applies state changes locally before the CCU
		// echoes them back. Critical for switches where a brief CCU
		// outage would otherwise leave HA showing the wrong state.
		body["optimistic"] = false
	case HAComponentLock:
		// Lock component uses HA's lock-specific payload contract:
		// `payload_lock` / `payload_unlock` on the command topic,
		// `state_locked` / `state_unlocked` against the rendered
		// value_template. Mirrors the custom-DP aggregated lock path
		// (`internal/model/custom/lock/payload.go:122-129`) so the
		// per-parameter and aggregated discovery surfaces emit the
		// same shape (lock-shape consolidation).
		//
		// Wire mapping for `LOCK_TARGET_LEVEL` matches
		// `CustomDpLock` semantics: `0.0` = locked, `1.0` = unlocked.
		// `LOCK_STATE` is a numeric enum (0=unknown, 1=locked,
		// 2=unlocked) that we surface verbatim — HA's `state_locked`
		// / `state_unlocked` then match the numeric form.
		body["command_topic"] = commandTopic
		body["payload_lock"] = "0"
		body["payload_unlock"] = "1"
		body["state_locked"] = "0"
		body["state_unlocked"] = "1"
		body["value_template"] = valueJSONValueLowerTemplate
		body["optimistic"] = false
	case HAComponentBinarySensor:
		// Read-only — no command_topic / state_on / state_off.
		//
		// PerDPState envelope carries the value as a JSON boolean
		// (`{"value":true,...}`). Jinja's default rendering of a
		// Python boolean is `True`/`False` (capitalised). HA's
		// binary_sensor defaults are `payload_on:"ON"`
		// `payload_off:"OFF"` (uppercase). Without explicit
		// overrides every binary_sensor stuck in `unknown`. Mirror
		// the switch path: pipe the scalar through `| lower` and
		// declare `payload_on/off="true"/"false"` so the comparison
		// is case-stable.
		body["value_template"] = valueJSONValueLowerTemplate
		body["payload_on"] = "true"
		body["payload_off"] = "false"
		// NOTE on expire_after: deliberately NOT set for
		// binary_sensor. Door / window contacts, sabotage flags,
		// alarm bits and similar are event-driven — they only emit
		// when the state changes. A door that stays closed for a
		// week sends no events, but the sensor itself is fine; an
		// `expire_after=3600` would falsely mark it "unavailable"
		// after an hour of inactivity. Availability is already
		// covered by the per-device UNREACH topic the daemon
		// publishes via [EventBridge.markAvailability].
		//
		// force_update + off_delay are different concerns: HA's
		// last_changed should still advance for motion/presence
		// bursts, and the auto-reset is HA-side state-machine
		// behaviour that doesn't claim the sensor is offline.
		if dc, _ := body["device_class"].(string); isMotionDeviceClass(dc) {
			body["force_update"] = true
			// off_delay=300 → HA auto-resets the binary_sensor
			// after five minutes without a follow-up update,
			// motion/presence/occupancy. Without this motion
			// sensors stay "on" forever after the first trigger.
			body["off_delay"] = 300
		}
	case HAComponentSensor:
		// Enum-typed sensors (device_class=enum) require an `options` list —
		// without it HA refuses the discovery. Source: paramset descriptor's
		// VALUE_LIST.
		if dc, _ := body["device_class"].(string); dc == "enum" && len(ev.descValueList()) > 0 {
			vl := ev.descValueList()
			opts := make([]any, len(vl))
			for i, v := range vl {
				opts[i] = v
			}
			body["options"] = opts
		}
		// Apply
		// canonical unit string regardless of CCU firmware quirks
		// ("100%" → "%", "Lux" → "lx", "degree" → "°C", "" + ACTUAL_TEMPERATURE
		// → "°C", …). Mirrors `BaseDataPoint._cleanup_unit`.
		//
		// Set-only-when-missing — preserve the unit established by
		// the prior authoritative passes ([EntityDescriptionFor] +
		// [applyEntityDescription]). Without this guard the
		// Sensor branch would overwrite
		// override (e.g. GAS_FLOW: "m³/h" rule) with the raw
		// CleanupUnit result of the wire-descriptor unit ("m³"),
		// producing the
		// `device_class volume_flow_rate not valid with m³` HA
		// MQTT-Discovery error.
		// `BaseHmEntity` init where the wire unit is the LAST
		// resort, not the first.
		if _, has := body["unit_of_measurement"]; !has {
			if cleaned := generic.CleanupUnit(hmenum.Parameter(ev.Parameter), ev.descUnit()); cleaned != "" {
				body["unit_of_measurement"] = cleaned
			}
		}
		// H-028: sensors always carry force_update=true and expire_after=3600.
		// force_update ensures HA re-evaluates the state even when the value
		// has not changed (e.g., periodic heartbeat). expire_after=3600 marks
		// the entity unavailable when no update arrives within one hour,
		body["force_update"] = true
		body["expire_after"] = 3600
		// L6 — apply data_point.multiplier so HA receives the same scaled
		// Value would emit (`sensor.py:161-169`
		// `:201`: `new_value = self._data_point.value * self._multiplier`).
		// Without this template Energy/Power readings would be off by
		// the unit factor when the CCU firmware reports the raw count.
		applyMultiplierSensor(ev, body)
	case HAComponentLight, HAComponentNumber, HAComponentCover:
		body["command_topic"] = commandTopic
		body["optimistic"] = false
		if comp == HAComponentNumber {
			// Seed wire-descriptor min/max FIRST — applyMultiplierNumber only scales
			// values already present in body. Without the seed, HA receives the
			// default range (0..100, step 1) regardless of the actual CCU bounds.
			if mn := ev.descMin(); mn != nil {
				if _, has := body["min"]; !has {
					body["min"] = *mn
				}
			}
			if mx := ev.descMax(); mx != nil {
				if _, has := body["max"]; !has {
					body["max"] = *mx
				}
			}
			// Step
			// `_attr_native_step = 1.0 if hmtype==INTEGER else 0.01 *
			// multiplier` (`number.py:235`). The wire ParameterData
			// carries Type=INTEGER for discrete parameters; default to
			// 0.01 otherwise. The multiplier scaling below applies the
			// `* multiplier` portion when applicable.
			if _, has := body["step"]; !has {
				if isIntegerParameter(ev) {
					body["step"] = 1.0
				} else {
					body["step"] = 0.01
				}
			}
			// L6 — scale `min`/`max`/`step` by `data_point.multiplier`
			// and invert the scaling on writes (`value / multiplier`).
			// Run AFTER the seed above so the scaling actually has values
			// to multiply.
			applyMultiplierNumber(ev, body, stateTopic, commandTopic)
			// Unit_of_measurement
			// `data_point.unit` when the EntityDescription doesn't
			// override (`number.py:236-237`). Mirror that here so wire
			// units like "s" / "%" / "°C" propagate to HA.
			if _, has := body["unit_of_measurement"]; !has {
				if cleaned := generic.CleanupUnit(hmenum.Parameter(ev.Parameter), ev.descUnit()); cleaned != "" {
					body["unit_of_measurement"] = cleaned
				}
			}
			// mode = "slider" when the range is small enough for a drag-bar to feel
			// useful; "box" otherwise.
			if mn, mx := ev.descMin(), ev.descMax(); mn != nil && mx != nil {
				if _, has := body["mode"]; !has {
					if (*mx - *mn) <= 1000 {
						body["mode"] = "slider"
					} else {
						body["mode"] = "box"
					}
				}
			}
		}
	case HAComponentSelect:
		body["command_topic"] = commandTopic
		body["optimistic"] = false
		// HA `select` requires `options`; without it HA rejects the discovery
		// payload outright. Source: paramset descriptor's VALUE_LIST (e.g.
		// `SET_POINT_MODE` → ["AUTO_MODE", "MANU_MODE", "PARTY_MODE",
		// "BOOST_MODE"]).
		if vl := ev.descValueList(); len(vl) > 0 {
			opts := make([]any, len(vl))
			for i, v := range vl {
				opts[i] = v
			}
			body["options"] = opts
		}
	case HAComponentButton:
		// Payload_press="PRESS" mirrors
		// button.py — without it HA sends an empty string on every
		// button press, which the CCU rejects.
		body["command_topic"] = commandTopic
		body["payload_press"] = "PRESS"
	case HAComponentText:
		// HA `text` is a writable, free-form string — used for HmIP-WRCD display
		// text and similar.
		body["command_topic"] = commandTopic
		body["mode"] = "text"
		if mn := ev.descMin(); mn != nil {
			body["min"] = int(*mn)
		}
		if mx := ev.descMax(); mx != nil {
			body["max"] = int(*mx)
		}
		body["optimistic"] = false
	case HAComponentEvent:
		// Press-type event entities: HA requires `event_types` listing all
		// press variants the channel can fire. The per-parameter sub-event
		// carries a single press type; the state topic receives
		// `{"event_type":"press_short"}` payloads when the button fires.
		// `device_class: button` is the canonical HA choice for key-press events.
		//
		// HA's mqtt.event component parses the *post-value_template*
		// payload as JSON and reads `event_type` from it. The default
		// `valueJSONValueTemplate` set above extracts `value_json.value`
		// (a scalar) — that breaks JSON parsing and floods the HA log
		// with `No valid JSON event payload detected, value after
		// processing payload 'press_long'`. Drop value_template so HA
		// receives the raw `{"event_type":...}` envelope.
		body["event_types"] = pressEventTypesFor(ev.Parameter)
		body["device_class"] = "button"
		delete(body, "value_template")
	default:
		// Climate / valve / siren / select / button / update / text are rendered
		// by the HADiscoveryPayloadBuilder fast path in aggregateChannel and never reach this switch.
	}

	// All state topics carry the JSON envelope (ADR 0011). Patch the
	// discovery payload so HA pulls the scalar via value_template and
	// uses the per-DP availability flag from the same JSON document.
	// The bridge_status + device_availability entries stay in the
	// availability list — they cover the broker / device-gone cases
	// the per-DP flag cannot represent.
	//
	// Event-type entities parse the raw JSON envelope themselves and
	// must NOT carry a value_template. Skip the default assignment
	// when the per-component branch already wrote a tailored template
	// (multiplier scaling for sensor/number).
	if comp != HAComponentEvent {
		if _, has := body["value_template"]; !has {
			body["value_template"] = jsonValueTemplate(comp)
		}
	}
	availabilityList, ok := body["availability"].([]map[string]string)
	if !ok {
		// body["availability"] is not the expected slice type — skip patching
		// to avoid panic; the bridge-level availability entries still work.
		return string(comp), nodeID, objectID, nil, false
	}
	availabilityList = append(availabilityList, map[string]string{
		"topic":                 stateTopic,
		"value_template":        `{{ value_json.available | lower }}`,
		"payload_available":     "true",
		"payload_not_available": "false",
	})
	body["availability"] = availabilityList

	buf, err := json.Marshal(body)
	if err != nil {
		return "", "", "", nil, false
	}
	return string(comp), nodeID, objectID, buf, true
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

// discoveryNodeID is retained as a thin alias to the canonical
// [naming.PathData.DiscoveryNodeID] form. Used by the few discovery
// helpers (week-profile, update-entity) that don't yet build their
// PathData up front.
func discoveryNodeID(centralName, deviceAddress string) string {
	pd := naming.NewDevicePathData("", deviceAddress)
	return pd.DiscoveryNodeID(centralName)
}

// applyMultiplierSensor patches body["value_template"] when ev.Channel
// reports a non-trivial multiplier for ev.Parameter. The emitted Jinja
// template multiplies the wire scalar (raw or value_json.value,
// depending on PayloadFormat) by the multiplier, mirroring the math
// Does in `sensor.py:201`.
func applyMultiplierSensor(ev Event, body map[string]any) {
	r, ok := ev.Channel.(channelMultiplierReader)
	if !ok {
		return
	}
	m, nontrivial := r.ParameterMultiplier(ev.Parameter)
	if !nontrivial {
		return
	}
	// State topics carry the JSON envelope (ADR 0011); the multiplied
	// template pulls value_json.value, wrapped in the `is defined`
	// guard so HA renders empty (entity "unknown") when the slot has
	// no observed payload yet rather than logging Jinja errors.
	body["value_template"] = fmt.Sprintf("{%% if value_json is defined %%}{{ (value_json.value | float * %s) }}{%% endif %%}", formatMultiplier(m))
}

// applyMultiplierNumber patches body so HA scales `min`/`max`/`step` to the
// multiplied range and inverts the multiplier on writes.
func applyMultiplierNumber(ev Event, body map[string]any, stateTopic, commandTopic string) {
	r, ok := ev.Channel.(channelMultiplierReader)
	if !ok {
		return
	}
	m, nontrivial := r.ParameterMultiplier(ev.Parameter)
	if !nontrivial {
		return
	}
	mStr := formatMultiplier(m)
	// State topics carry JSON; the multiplied template pulls
	// value_json.value (ADR 0011 — JSON is the only supported shape).
	body["value_template"] = fmt.Sprintf("{{ (value_json.value | float * %s) }}", mStr)
	// Write template — invert (HA-supplied value / multiplier).
	body["command_template"] = fmt.Sprintf("{{ (value | float / %s) }}", mStr)
	// Bounds — multiply min/max/step if the discovery payload already
	// carries them (number-bound population is descriptor-driven).
	if v, has := body["min"].(float64); has {
		body["min"] = v * m
	}
	if v, has := body["max"].(float64); has {
		body["max"] = v * m
	}
	if v, has := body["step"].(float64); has {
		body["step"] = v * m
	}
	_ = stateTopic
	_ = commandTopic
}

// formatMultiplier returns m rendered without trailing zeros so the
// emitted Jinja template stays readable (`0.1` instead of `0.100000`).
func formatMultiplier(m float64) string {
	s := strconv.FormatFloat(m, 'f', -1, 64)
	return s
}

// pressEventTypesFor returns the HA `event_types` list for a button-press
// parameter. HA's `event` platform requires the full list of possible event
// Types upfront.
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

// displayName returns the per-entity `name` HA shows in the UI. HA
// automatically prepends the device name (from the `device` block)
// when rendering, so this should be the parameter portion only
// duplicating the device name here produces "Wandthermostat
// Wandthermostat RSSI_DEVICE" in the HA frontend.
//
// Resolution order
// 1. `ev.descLabel()` — the locale-aware OCCU translation (prefers Descriptor.Label,
// falls back to ev.ParameterLabel).
// 2. Title-cased parameter (`RSSI_DEVICE` → `Rssi Device`) when no
// Translation is available — the same fallback
// applies in `support.py::get_data_point_name_data`.
func displayName(ev Event) string {
	if lbl := ev.descLabel(); lbl != "" {
		return lbl
	}
	return titleCaseParameter(ev.Parameter)
}

// entityName returns the value to assign to the HA Discovery `name`
// field. Returns the literal `nil` (HA's signal for "use the device
// name alone for friendly_name and entity_id") when the parameter is
// flagged primary by the embedded translation_custom catalogue (see
// [GenericConfig.LabelOmitted]). Otherwise delegates to
// [displayName] for the locale-aware label.
func entityName(ev Event) any {
	if ev.descLabelOmitted() {
		return nil
	}
	return displayName(ev)
}

// titleCaseParameter mirrors Python's `str.title().replace("_", " ")`
// for HM parameter names: each underscore-delimited segment becomes
// title-cased and joined with spaces. Numeric segments stay as-is.
//
//	"RSSI_DEVICE" → "Rssi Device"
//	"SET_POINT_TEMPERATURE" → "Set Point Temperature"
//	"LEVEL_2" → "Level 2"
func titleCaseParameter(parameter string) string {
	if parameter == "" {
		return ""
	}
	parts := strings.Split(parameter, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(strings.ToLower(p))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// haDeviceFields is the closed set of keys HA accepts inside an MQTT
// Discovery `device` block (HA 2024.x, see
// https://www.home-assistant.io/integrations/mqtt#discovery-payload).
// Anything outside this set causes HA to reject the entire discovery
// message with `extra keys not allowed @ data['device'][...]` — the
// device's `payload:"info"` partition contains HM-specific fields
// (`interface`, `interfaceid`, `model_icon`, `model_label`, `rooms`,
// `functions`, `product_group`) that must therefore be filtered out
// before the block leaves this package.
var haDeviceFields = map[string]struct{}{
	"identifiers":       {},
	"connections":       {},
	"manufacturer":      {},
	"model":             {},
	"model_id":          {},
	"name":              {},
	"serial_number":     {},
	"sw_version":        {},
	"hw_version":        {},
	"via_device":        {},
	"suggested_area":    {},
	"configuration_url": {},
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
func deviceDescriptor(ev Event, hubURL string, subDevices bool) map[string]any {
	parentID := "openccu-loom_" + strings.ToLower(ev.DeviceAddress)
	desc := map[string]any{
		"identifiers":  []string{parentID},
		"manufacturer": "eQ-3",
	}
	// Stamp via_device so HA renders this device as a child of the
	// OpenCCU-Loom central — same hierarchy as
	// (`generic_entity.py:142`) and
	// (`platforms/generic_entity.py:118`). A device without
	// via_device floats at the top level, mixed with the central
	// itself — confusing in the HA Devices view.
	if ev.Central != "" {
		desc["via_device"] = "openccu-loom_central_" + strings.ToLower(ev.Central)
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
					desc["identifiers"] = []string{subDeviceID}
					desc["via_device"] = parentID
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
		for k, v := range info {
			if _, ok := haDeviceFields[k]; !ok {
				continue
			}
			desc[k] = v
		}
		// Capture the singular room (set by the model when exactly
		// one room is assigned) for the suggested_area fallback
		// below. Multi-room and unassigned devices intentionally
		// produce no suggested_area: HA accepts only a single string
		// there, and silently picking any entry from a multi-room
		// list mis-attributes devices that span rooms.
		if r, ok := info["room"].(string); ok {
			room = r
		}
		// Capture the translated, human-readable model label for the
		// model_id fallback below. Filtered out of the main loop
		// because `model_label` is not in HA's whitelist — only
		// `model_id` is, and we deliberately route the label there.
		if ml, ok := info["model_label"].(string); ok {
			modelLabel = ml
		}
	}
	// Sub-device naming wins over both the harvested info name and
	// the event-level default — the sub-device represents only the
	// channel-group slice of the physical device.
	if subDeviceName != "" {
		desc["name"] = subDeviceName
	} else if _, has := desc["name"]; !has {
		switch {
		case ev.DeviceName != "":
			desc["name"] = ev.DeviceName
		default:
			// HA requires a name; fall back to the address so the
			// entity surfaces with a recognisable label rather than
			// being rejected with `required key not provided`.
			desc["name"] = ev.DeviceAddress
		}
	}
	if _, has := desc["model"]; !has && ev.Model != "" {
		desc["model"] = ev.Model
	}
	// "HmIP-eTRV-2") and HA `model_id` carries the translated, human-readable
	// label (e.g. "Heizkörperthermo- stat"). Without this, HA only sees the
	// cryptic wire type. `Device.ModelLabel` is filled by
	// [DevicePipeline.WithTranslations] during ingest from the openccu
	// translation catalogue; an empty label (no translation match) leaves
	// model_id unset rather than duplicating the wire type, so HA falls back to
	// its own model rendering.
	if _, has := desc["model_id"]; !has && modelLabel != "" {
		desc["model_id"] = modelLabel
	}
	// Stamp sw_version from the device's firmware tracker. Empty firmware
	// strings (CCU has not reported one yet) leave the field unset rather than
	// emitting "" — HA renders "Unknown" cleanly when sw_version is absent.
	if _, has := desc["sw_version"]; !has && ev.Device != nil {
		if dwsv, ok := ev.Device.(deviceWithSwVersion); ok {
			if v := dwsv.SwVersion(); v != "" {
				desc["sw_version"] = v
			}
		}
	}
	// configuration_url points HA at the CCU's WebUI. Same value as the
	// synthetic hub device (hubDeviceBlock embeds info.URL there) so HA's "Visit
	// device" button on the per-device card opens the same operator console.
	if _, has := desc["configuration_url"]; !has && hubURL != "" {
		desc["configuration_url"] = hubURL
	}
	// Stamp suggested_area from the device's singular room when the
	// model has resolved exactly one assignment. Multi-room devices
	// (Device.Room == "") produce no suggested_area on purpose — HA
	// only accepts a single string and an arbitrary pick would
	// mis-attribute the device. The `room` field itself is not part
	// of haDeviceFields (HA would reject the bare key), so this is
	// the only path the per-device room reaches HA Discovery.
	if _, has := desc["suggested_area"]; !has && room != "" {
		desc["suggested_area"] = room
	}
	return desc
}
