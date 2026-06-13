// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// pressParameters is the closed set of PRESS_* parameter names a button
// channel can expose.
var pressParameters = []string{
	"PRESS_SHORT",
	"PRESS_LONG",
	"PRESS_LONG_RELEASE",
	"PRESS_LONG_START",
}

// IsPressParameter reports whether p is one of the canonical PRESS_*
// parameter names (case-insensitive). Exported so the EventBridge can
// use the same check without duplicating the set.
func IsPressParameter(p string) bool {
	up := strings.ToUpper(p)
	return slices.Contains(pressParameters, up)
}

// isPressParameter is the package-internal alias used by Build.
func isPressParameter(p string) bool { return IsPressParameter(p) }

// ChannelPressTypes returns the lower-cased HA event_types for all
// PRESS_* parameters present on ch. Returns nil when fewer than 2
// press parameters are found (single-press channels use the
// per-parameter path). The result order mirrors pressParameters.
// Exported so the EventBridge can detect multi-press channels.
func ChannelPressTypes(ch ChannelInspector) []string {
	if ch == nil {
		return nil
	}
	var found []string
	for _, pp := range pressParameters {
		if ch.HasParameter(pp) {
			found = append(found, strings.ToLower(pp))
		}
	}
	if len(found) < 2 {
		return nil
	}
	return found
}

// channelPressTypes is the package-internal alias used by BuildChannelEvent.
func channelPressTypes(ch ChannelInspector) []string { return ChannelPressTypes(ch) }

// BuildChannelEvent produces the HA-Discovery `event` payload for a
// button channel that exposes 2 or more PRESS_* parameters. It is the
// aggregated counterpart of the per-parameter `resolveComponent` →
// HAComponentEvent path: instead of N separate event entities (one
// per PRESS_* parameter), a single entity carries all event types in
// its `event_types` list.
//
// Returns (component, nodeID, objectID, payload, true) on success.
// Returns ("", "", "", nil, false) when ch carries fewer than 2
// PRESS_* parameters — callers should fall through to the per-
// parameter path in that case.
func (d *DefaultDiscoveryBuilder) BuildChannelEvent(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) {
	types := channelPressTypes(ev.Channel)
	if len(types) == 0 {
		return "", "", "", nil, false
	}
	objectID = d.channelObjectID(ev, "event")
	uniqueID := d.channelUniqueID(ev, "event")
	nodeID = discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)
	stateTopic := d.TopicBuilder.ChannelEvent(d.centralFor(ev), ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	// Press-event entity name prefers the CCU-operator-assigned
	// channel name ("Taster Wohnzimmer oben links") when present,
	// falling back to `ch<N>` when the channel has no operator name.
	// The device card then surfaces the human label the operator
	// already maintains in the CCU instead of an opaque channel
	// number.
	var name string
	if namer, ok := ev.Channel.(ChannelNamer); ok {
		if cn := namer.ChannelName(); cn != "" {
			name = cn
		}
	}
	// Reference parity (get_event_name): when the operator channel name
	// is the bare `<base>:<channel_no>` form (no human-assigned label),
	// HA slugifies it down to the base and two channels of the same VR
	// collapse onto one entity_id (the `_2` / `_3` dedup suffixes). Drop
	// it to `ch<N>` so each channel stays distinct.
	if name == "" || channelNameIsBareAddressNo(name) {
		name = fmt.Sprintf("ch%d", ev.ChannelNo)
	}
	base := d.channelBaseBody(ev, name, uniqueID)
	// HA's mqtt.event component requires the post-template payload to
	// be parseable as JSON and reads `event_type` from it directly. A
	// `value_template` that extracts the scalar (`{{ value_json.event_type }}`)
	// turns the JSON envelope into a bare string that HA then fails to
	// parse — surfacing as `No valid JSON event payload detected` in
	// the HA log. The bridge already publishes a JSON envelope to the
	// channel's `/event` topic, so no template is needed.
	body := map[string]any{
		"state_topic":  stateTopic,
		"event_types":  toAnySlice(types),
		"device_class": "button",
	}
	maps.Copy(body, base)
	out, err := json.Marshal(body)
	if err != nil {
		return "", "", "", nil, false
	}
	return string(HAComponentEvent), nodeID, objectID, out, true
}

// channelNameIsBareAddressNo reports whether name is the unlabelled
// `<base>:<channel_no>` form a CCU reports for a channel the operator
// never renamed (e.g. "KearneyIP:2"). Mirrors the reference stack's
// _check_channel_name_with_channel_no: exactly one ':' separator and an
// integer trailing part. Such names slugify to the base alone in HA, so
// sibling channels collide on the same entity_id and must instead fall
// back to the `ch<N>` discriminator.
func channelNameIsBareAddressNo(name string) bool {
	idx := strings.LastIndex(name, ":")
	if idx < 0 || strings.Count(name, ":") != 1 {
		return false
	}
	suffix := name[idx+1:]
	if suffix == "" {
		return false
	}
	if _, err := strconv.Atoi(suffix); err != nil {
		return false
	}
	return true
}

// toAnySlice converts []string to []any for JSON marshalling so that
// `event_types` is emitted as a JSON array of strings rather than a
// Go-specific type.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// aggregateChannel collapses every parameter on a known custom-
// domain channel into a single HA entity (climate, cover, lock,
// light, valve, siren). Returns (component, objectID, payload,
// true) on a hit — the bridge uses the same dedup cache as the
// per-parameter path, so subsequent calls for the same channel
// (one per parameter event) deduplicate to a no-op.
//
// Returns ok=false when ev.Source does not implement
// [payload.HADiscoveryPayloadBuilder], the builder returns a nil
// body, or JSON marshalling fails.
//
// ADR 0010: all custom-DP types implement HADiscoveryPayloadBuilder.
// The legacy buildX path has been removed.
func (d *DefaultDiscoveryBuilder) aggregateChannel(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) {
	builder, ok := ev.Source.(payload.HADiscoveryPayloadBuilder)
	if !ok || builder == nil {
		return "", "", "", nil, false
	}
	comp, body := builder.HADiscoveryPayload(d.discoveryContext(ev))
	if body == nil {
		return "", "", "", nil, false
	}
	objectID = d.channelObjectID(ev, comp)
	uniqueID := d.channelUniqueID(ev, comp)
	nodeID = discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)
	base := d.channelBaseBody(ev, displayChannelName(ev), uniqueID)
	maps.Copy(body, base)
	// Strict variant: when neither a rule nor a category-default matches, every
	// HA-attribute field is stripped from the body so an unknown model gets no
	// `device_class` etc. (mirrors HA-native behaviour). Without this the legacy
	// openccu-loom table would keep emitting `device_class=shutter` for models
	// the HA integration has no cover rule for.
	//
	// Postfix propagation: the HA integration matches Lock variants
	// (BUTTON_LOCK, …) by `dp.data_point_name_postfix`. Pull it from the
	// Custom-DP when available so the postfix-keyed rules in
	// `entity_helpers/descriptions/locks.py` fire on the openccu-loom side too.
	postfix := ""
	if pf, ok := ev.Source.(interface{ NamePostfix() string }); ok {
		postfix = pf.NamePostfix()
	}
	applyEntityDescriptionStrict(body, comp, "", ev.Model, ev.descUnit(), postfix)
	// CDP_SECONDARY entities (mirror channels declared via the
	// profile's `secondary_channels`) are hidden by default in HA —
	// (model/data_point.py:399). The operator can re-enable them
	// from the device card; without this flag they show up as
	// duplicate primary entities and pollute the dashboard.
	if insp, ok := ev.Channel.(CustomDPNamingInspector); ok && insp.IsCustomDPSecondaryChannel() {
		body["enabled_by_default"] = false
	}
	out, err := json.Marshal(body)
	if err != nil {
		return "", "", "", nil, false
	}
	return comp, nodeID, objectID, out, true
}

// discoveryCtx is the bridge-side implementation of
// [payload.HADiscoveryContext]. It carries the per-event scoping
// (central, interface, address, channel) so model-side builders can
// request topic strings without knowing the topology.
type discoveryCtx struct {
	d  *DefaultDiscoveryBuilder
	ev Event
}

func (c discoveryCtx) AggregatedStateTopic() string {
	// Deprecated: kept as an alias to CustomDPStateTopic so callers
	// in transition compile. The bridge no longer publishes content
	// to the bare `<addr>/<ch>/state` shape — it has been retired in
	// favour of the per-source `<addr>/<ch>/custom/<kind>` slot.
	return c.CustomDPStateTopic()
}

// CustomDPStateTopic returns the channel's custom-DP slot state
// topic `<addr>/<ch>/custom/<kind>`. The kind is read from the
// event's Source via the [payload.Slotted] interface — every
// custom-DP implements it. Empty when the source is missing or not
// a slotted custom-DP (e.g. a per-parameter discovery event).
func (c discoveryCtx) CustomDPStateTopic() string {
	slot, ok := customDPSlotForEvent(c.ev)
	if !ok {
		return ""
	}
	return c.d.TopicBuilder.SlotState(c.d.centralFor(c.ev), c.ev.Interface, slot)
}

// customDPSlotForEvent extracts the [payload.TopicSlot] declared by
// the event's Source. Returns ok=false when the source is nil, not
// slotted, or carries an empty slot (defensive — a custom-DP that
// declares an empty parameter would otherwise produce a
// `…/custom/` topic with a trailing slash).
func customDPSlotForEvent(ev Event) (payload.TopicSlot, bool) {
	if ev.Source == nil {
		return payload.TopicSlot{}, false
	}
	slotted, ok := ev.Source.(payload.Slotted)
	if !ok {
		return payload.TopicSlot{}, false
	}
	slot := slotted.TopicSlot()
	if slot.Parameter == "" {
		return payload.TopicSlot{}, false
	}
	// Trust the source-declared address+channel (model knows the
	// canonical CCU address shape) — but make sure the channel
	// matches what the channel-context expects so a misconfigured
	// model can't accidentally write to the wrong slot.
	if slot.Channel == 0 && ev.ChannelNo != 0 {
		slot.Channel = ev.ChannelNo
	}
	if slot.Address == "" {
		slot.Address = ev.DeviceAddress
	}
	if slot.Bucket == "" {
		slot.Bucket = payload.BucketCustom
	}
	return slot, true
}

func (c discoveryCtx) ServiceMethodCommandTopic(method string) string {
	slot, ok := customDPSlotForEvent(c.ev)
	if !ok {
		return ""
	}
	return c.d.TopicBuilder.CustomDPServiceMethod(c.d.centralFor(c.ev), c.ev.Interface, slot, method)
}

func (c discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return c.d.TopicBuilder.DataPointCommand(c.d.centralFor(c.ev), c.ev.Interface, c.ev.DeviceAddress, c.ev.ChannelNo, parameter)
}

func (c discoveryCtx) WireParameterStateTopic(parameter string) string {
	// Canonical per-parameter state topic — same shape every consumer
	// uses (HA value-template extractors, slot-state publishes,
	// Legacy HA reads
	// the PerDPState envelope `{"value": ..., "available": ...,
	// "modified_at": ..., "type": ..., "unit": ...}` via
	// `value_template "{{ value_json.value }}"`.
	return c.d.TopicBuilder.ParameterState(
		c.d.centralFor(c.ev), c.ev.Interface, c.ev.DeviceAddress, c.ev.ChannelNo,
		string(payload.BucketValues), parameter,
	)
}

func (d *DefaultDiscoveryBuilder) discoveryContext(ev Event) discoveryCtx {
	return discoveryCtx{d: d, ev: ev}
}

// channelObjectID returns the per-channel object_id (the part after
// the device-grouping node_id in the discovery topic). Delegates to
// [naming.PathData.DiscoveryObjectID] — the model layer owns the
// `<channel>_<suffix>` derivation.
func (d *DefaultDiscoveryBuilder) channelObjectID(ev Event, suffix string) string {
	return channelPathData(ev).DiscoveryObjectID(suffix)
}

// channelUniqueID is the cross-broker-stable id used for HA's
// `unique_id` payload field. Uses [routingkey.CanonicalUniqueID] with
// the channel address (device:channel) and suffix as the parameter.
func (d *DefaultDiscoveryBuilder) channelUniqueID(ev Event, suffix string) string {
	return routingkey.CanonicalUniqueID(d.serialSuffix(ev.Central), ev.DeviceAddress+":"+strconv.Itoa(ev.ChannelNo), suffix, "")
}

// channelPathData builds the channel-scoped [naming.PathData] used
// by the discovery-identity helpers. Composed from the event's
// Interface/DeviceAddress/ChannelNo — the bucket and kind are
// irrelevant for identity derivation and stay zero.
func channelPathData(ev Event) naming.PathData {
	return naming.NewChannelPathData(
		hmenum.Interface(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
	)
}

// channelBaseBody returns the shared HA-Discovery scaffolding:
// availability, device descriptor, origin block. Concrete builders
// extend it with platform-specific fields.
//
// Per-availability-entry `payload_available`/`payload_not_available`
// match the strings the bridge actually publishes ("online" /
// "offline") — HA's defaults are the same but pinning them avoids a
// surprise if the bridge contract ever changes.
func (d *DefaultDiscoveryBuilder) channelBaseBody(ev Event, name, uniqueID string) map[string]any {
	availability := []map[string]string{
		{
			"topic":                 d.TopicBuilder.BridgeStatus(),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 d.TopicBuilder.DeviceAvailability(d.centralFor(ev), ev.Interface, ev.DeviceAddress),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
	// `name` is JSON-null when blank — that is HA's signal to render
	// `friendly_name` = device.name alone. An empty string is treated
	// as "default" (entity-id derived), which produces the same
	// double-prefix the bug we are fixing originally surfaced.
	body := map[string]any{
		"unique_id":         uniqueID,
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(ev, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}
	if name == "" {
		body["name"] = nil
	} else {
		body["name"] = name
	}
	return body
}

// displayChannelName returns the entity-name string for an aggregated
// channel discovery payload. HA's MQTT integration prepends the
// device-level `device.name` automatically when computing
// `friendly_name`, so the entity-side `name` MUST NOT repeat the
// device name.
//
// Naming convention mirrors
// `get_custom_data_point_name` (model/support.py:443):
//
//   - **Single primary custom-DP** (`HasSinglePrimaryCustomDP=true`):
//     entity name is empty — HA falls back to `device.name` alone
//     ("Wandthermostat AK" for HmIP-BWTH).
//   - **Multiple primary custom-DPs** (e.g. HmIP-PSM with three
//     switch outputs): each primary gets `ch<N>` so HA renders
//     "Steckdose ch3" / "Steckdose ch4" / "Steckdose ch5".
//   - **Secondary custom-DP** (mirrored channel via the profile's
//     `secondary_channels`): name is `vch<N>` so HA renders
//     "Bicolor BSL vch1" — the entity is also marked
//     `enabled_by_default: false` upstream so HA hides it by default.
//   - **Channels without custom-DP context** (the historic fallback
//     path): channel 0 → empty, channel N > 0 → "<N>".
func displayChannelName(ev Event) string {
	if insp, ok := ev.Channel.(CustomDPNamingInspector); ok {
		// Secondary channels always carry the `vch<N>` suffix
		// regardless of how many primaries the device hosts —
		// HmIP-PSM ch4/ch5 are secondaries even though the device
		// has only one SWITCH primary, so the secondary check must
		// run BEFORE [HasSinglePrimaryCustomDP] (which only counts
		// primary channels and therefore returns true even when
		// invoked from a secondary).
		if insp.IsCustomDPSecondaryChannel() {
			return fmt.Sprintf("vch%d", ev.ChannelNo)
		}
		if insp.IsCustomDPPrimaryChannel() {
			if insp.HasSinglePrimaryCustomDP() {
				return ""
			}
			// _ignore_multiple_channels_for_name override (Python
			// data_point.py:542 + custom/lock.py:65). When the
			// channel's custom DP opts in, the discovery name builder
			// drops the `ch<N>` suffix even for multi-primary devices.
			if ig, ok := ev.Channel.(IgnoreMultipleChannelsForNameInspector); ok && ig.IgnoreMultipleChannelsForName() {
				return ""
			}
			return fmt.Sprintf("ch%d", ev.ChannelNo)
		}
	}
	if ev.ChannelNo > 0 {
		return strconv.Itoa(ev.ChannelNo)
	}
	return ""
}

// channelMultiplierReader is an optional extension on ChannelInspector. When
// a channel implements it, sensor / number builders multiply the raw CCU
// value by the reported multiplier before emitting to HA, and invert the
// multiplier when forwarding writes back to the CCU.
type channelMultiplierReader interface {
	ParameterMultiplier(name string) (float64, bool)
}

// channelEnumValuesReader is an optional extension on ChannelInspector. When
// a channel implements it, the sensor builder calls ParameterValueList to
// populate the HA `options` field for `device_class: enum` sensors (H-029).
type channelEnumValuesReader interface {
	ParameterValueList(parameter string) []string
}
