// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TopicBuilder assembles topic strings from the raw plane components.
// Base defaults to "openccu-loom" but can be overridden per bridge.
//
// Every method that targets a model-relevant topic (data point,
// channel aggregate, device snapshot, hub topic, …) delegates to
// [naming.PathData] or a free function in the naming package — the
// model layer owns every format string. The bridge layer only fills
// in the runtime context (Base, Central) that the model has no
// natural access to. The few topics that stay defined here
// ([TopicBuilder.BridgeStatus], [TopicBuilder.BridgeHealth],
// [TopicBuilder.DiscoveryConfig]) are bridge-internal operational
// concerns with no model representation.
type TopicBuilder struct {
	Base string
}

// NewTopicBuilder returns a builder with base as the prefix.
func NewTopicBuilder(base string) *TopicBuilder {
	if base == "" {
		base = "openccu-loom"
	}
	return &TopicBuilder{Base: strings.Trim(base, "/")}
}

// --- Bridge-internal (not model-driven) -------------------------------

// BridgeStatus is the LWT / retained status topic.
func (b *TopicBuilder) BridgeStatus() string {
	return b.Base + "/bridge/status"
}

// BridgeHealth is the retained daemon-level health topic.
//
//	<base>/bridge/health
func (b *TopicBuilder) BridgeHealth() string {
	return b.Base + "/bridge/health"
}

// AddonUpdateState is the retained state topic for the daemon-level
// CCU add-on self-update entity (ADR 0057). Unlike every per-central
// hub topic this carries no <central> segment: the self-updater is a
// property of the daemon process itself, not of any one CCU.
//
//	<base>/system/addon_update/state
func (b *TopicBuilder) AddonUpdateState() string {
	return b.Base + "/system/addon_update/state"
}

// AddonUpdateCommand is the subscribed command topic pairing
// [TopicBuilder.AddonUpdateState] — HA's `update` entity publishes its
// install command here.
//
//	<base>/system/addon_update/set
func (b *TopicBuilder) AddonUpdateCommand() string {
	return b.Base + "/system/addon_update/set"
}

// DiscoveryConfig is the HA Discovery retained config topic.
// Delegates to [naming.DiscoveryConfigTopic] — the model layer owns
// the format string.
//
//	homeassistant/<component>/<node_id>/<object_id>/config
func (b *TopicBuilder) DiscoveryConfig(component, nodeID, objectID string) string {
	return naming.DiscoveryConfigTopic(component, nodeID, objectID)
}

// --- Channel-bound DP topics (delegate to naming.PathData) -----------

// ParameterState is the canonical retained value topic for one data
// point in one paramset bucket. Delegates to
// [naming.PathData.MQTTState].
func (b *TopicBuilder) ParameterState(centralName, iface, address string, channel int, bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTState(b.Base, centralName)
}

// ParameterCommand returns the subscribed `/set` topic.
func (b *TopicBuilder) ParameterCommand(centralName, iface, address string, channel int, bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTCommand(b.Base, centralName)
}

// ParameterConfig returns the descriptor-companion `/config` topic.
func (b *TopicBuilder) ParameterConfig(centralName, iface, address string, channel int, bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTConfig(b.Base, centralName)
}

// DataPointState resolves to [TopicBuilder.ParameterState] on the
// VALUES bucket. Retained as a back-compat alias.
func (b *TopicBuilder) DataPointState(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterState(centralName, iface, address, channel, string(payload.BucketValues), parameter)
}

// DataPointCommand is the VALUES-bucket /set alias.
func (b *TopicBuilder) DataPointCommand(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterCommand(centralName, iface, address, channel, string(payload.BucketValues), parameter)
}

// DataPointConfig is the VALUES-bucket /config alias.
func (b *TopicBuilder) DataPointConfig(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterConfig(centralName, iface, address, channel, string(payload.BucketValues), parameter)
}

// DataPointEvent is the legacy per-event-type pulse topic. Delegates
// to [naming.PathData.MQTTDataPointEvent].
func (b *TopicBuilder) DataPointEvent(centralName, iface, address string, channel int, etype string) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTDataPointEvent(b.Base, centralName, etype)
}

// ChannelEvent is the non-retained per-channel aggregate-event
// topic. Delegates to [naming.PathData.MQTTChannelEvent].
func (b *TopicBuilder) ChannelEvent(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTChannelEvent(b.Base, centralName)
}

// ChannelImpulse is the non-retained per-channel impulse-event topic.
// Delegates to [naming.PathData.MQTTChannelImpulse].
func (b *TopicBuilder) ChannelImpulse(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTChannelImpulse(b.Base, centralName)
}

// ChannelDeviceError is the non-retained per-channel device-error topic.
// Delegates to [naming.PathData.MQTTChannelDeviceError].
func (b *TopicBuilder) ChannelDeviceError(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTChannelDeviceError(b.Base, centralName)
}

// AggregatedState is the retained per-Source state topic introduced
// by ADR 0007. Delegates to [naming.PathData.MQTTChannelAggregateState].
func (b *TopicBuilder) AggregatedState(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTChannelAggregateState(b.Base, centralName)
}

// --- Slot helpers (delegate to ParameterState for non-custom buckets) ---

// SlotState resolves to the per-parameter
// [TopicBuilder.ParameterState] for VALUES/MASTER/CALCULATED buckets,
// and to [naming.PathData.MQTTCustomDPState] for [BucketCustom].
func (b *TopicBuilder) SlotState(centralName, iface string, slot payload.TopicSlot) string {
	if slot.Bucket == payload.BucketCustom {
		pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
		return pd.MQTTCustomDPState(b.Base, centralName)
	}
	return b.ParameterState(centralName, iface, slot.Address, slot.Channel, string(slot.Bucket), slot.Parameter)
}

// SlotConfig resolves to the matching descriptor-companion topic.
func (b *TopicBuilder) SlotConfig(centralName, iface string, slot payload.TopicSlot) string {
	if slot.Bucket == payload.BucketCustom {
		pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
		return pd.MQTTCustomDPConfig(b.Base, centralName)
	}
	return b.ParameterConfig(centralName, iface, slot.Address, slot.Channel, string(slot.Bucket), slot.Parameter)
}

// SlotCommand resolves to the matching `set` topic. Custom-DP slots
// expose a single `set` topic (per-method shape via
// [TopicBuilder.CustomDPServiceMethod]).
func (b *TopicBuilder) SlotCommand(centralName, iface string, slot payload.TopicSlot) string {
	if slot.Bucket == payload.BucketCustom {
		pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
		state := pd.MQTTCustomDPState(b.Base, centralName)
		if state == "" {
			return ""
		}
		return state + "/set"
	}
	return b.ParameterCommand(centralName, iface, slot.Address, slot.Channel, string(slot.Bucket), slot.Parameter)
}

// CustomDPServiceMethod returns the per-method command topic for a
// custom-DP slot. Delegates to
// [naming.PathData.MQTTCustomDPServiceMethod].
func (b *TopicBuilder) CustomDPServiceMethod(centralName, iface string, slot payload.TopicSlot, method string) string {
	pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
	return pd.MQTTCustomDPServiceMethod(b.Base, centralName, method)
}

// CustomDPInvoke is the subscribed invoke-topic for a custom DP
// operation. Delegates to [naming.MQTTCustomDPInvoke].
func (b *TopicBuilder) CustomDPInvoke(centralName, deviceAddress, name, operation string) string {
	return naming.MQTTCustomDPInvoke(b.Base, centralName, deviceAddress, name, operation)
}

// --- Device-scope topics (delegate to naming.PathData) --------------

// DeviceAvailability is the per-device retained availability topic.
func (b *TopicBuilder) DeviceAvailability(centralName, iface, address string) string {
	pd := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), address)
	return pd.MQTTDeviceAvailability(b.Base, centralName)
}

// DeviceInfo is the retained per-device device-info topic.
func (b *TopicBuilder) DeviceInfo(centralName, iface, address string) string {
	pd := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), address)
	return pd.MQTTDeviceInfo(b.Base, centralName)
}

// DeviceDiagnostics is the retained per-device aggregated-diagnostics
// topic.
func (b *TopicBuilder) DeviceDiagnostics(centralName, iface, address string) string {
	pd := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), address)
	return pd.MQTTDeviceDiagnostics(b.Base, centralName)
}

// DeviceUpdateState is the retained JSON state topic for the HA
// `update` entity.
func (b *TopicBuilder) DeviceUpdateState(centralName, iface, address string) string {
	pd := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), address)
	return pd.MQTTDeviceUpdateState(b.Base, centralName)
}

// DeviceUpdateCommand is the canonical spelling of the install-command
// topic (`.../update/set`). Nothing subscribes to it and the update
// entity declares no command_topic — flashing firmware from a possibly
// retained broker payload is unsafe. See
// [naming.PathData.MQTTDeviceUpdateCommand] for the full rationale.
func (b *TopicBuilder) DeviceUpdateCommand(centralName, iface, address string) string {
	pd := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), address)
	return pd.MQTTDeviceUpdateCommand(b.Base, centralName)
}

// WeekProfileState is the retained state topic for the week-profile
// select entity.
func (b *TopicBuilder) WeekProfileState(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTWeekProfileState(b.Base, centralName)
}

// WeekProfileCommand is the subscribed set topic.
func (b *TopicBuilder) WeekProfileCommand(centralName, iface, address string, channel int) string {
	pd := naming.NewChannelPathData(hmtypes.ParseWireInterfaceID(iface), address, channel)
	return pd.MQTTWeekProfileCommand(b.Base, centralName)
}

// CombinedState returns the retained state topic for a combined DP
// (HSColor, Timer, LevelCombined, …) on a channel. The kind disambiguates
// multiple combined DPs on the same channel ("duration", "hs_color", …).
//
//	<base>/<central>/<iface>/<addr>/<channel>/combined/<kind>
func (b *TopicBuilder) CombinedState(centralName, iface, address string, channel int, kind string) string {
	// The combined-DP topology is local to the bridge layer (the naming
	// package does not yet carry a helper for it). Construct via the
	// shared channelScopedTopic prefix using the same TopicSafe contract
	// every channel topic uses.
	return b.channelScopedTopic(centralName, iface, address, channel) + "/combined/" + naming.TopicSafe(kind)
}

// CombinedCommand returns the subscribed set topic for a combined DP.
//
//	<base>/<central>/<iface>/<addr>/<channel>/combined/<kind>/set
func (b *TopicBuilder) CombinedCommand(centralName, iface, address string, channel int, kind string) string {
	return b.CombinedState(centralName, iface, address, channel, kind) + "/set"
}

// ScheduleEntityState returns the retained state topic for the
// device-level Zeitplan sensor (one per schedule-relevant device). The
// state payload is the count of active schedule entries.
//
//	<base>/<central>/<iface>/<addr>/<channel>/schedule/state
func (b *TopicBuilder) ScheduleEntityState(centralName, iface, address string, channel int) string {
	return b.channelScopedTopic(centralName, iface, address, channel) + "/schedule/state"
}

// ScheduleEntityAttrs returns the retained json_attributes topic for the
// Zeitplan sensor — schedule_type, max_entries, available_target_channels,
// schedule_enabled, schedule_data, …
//
//	<base>/<central>/<iface>/<addr>/<channel>/schedule/attrs
func (b *TopicBuilder) ScheduleEntityAttrs(centralName, iface, address string, channel int) string {
	return b.channelScopedTopic(centralName, iface, address, channel) + "/schedule/attrs"
}

// ScheduleSwitchState returns the retained boolean state topic for one
// schedule channel switch.
//
//	<base>/<central>/<iface>/<addr>/<channel>/schedule/<key>/state
func (b *TopicBuilder) ScheduleSwitchState(centralName, iface, address string, channel int, key string) string {
	return b.channelScopedTopic(centralName, iface, address, channel) + "/schedule/" + naming.TopicSafe(key) + "/state"
}

// ScheduleSwitchCommand returns the subscribed set topic for one
// schedule channel switch.
//
//	<base>/<central>/<iface>/<addr>/<channel>/schedule/<key>/set
func (b *TopicBuilder) ScheduleSwitchCommand(centralName, iface, address string, channel int, key string) string {
	return b.channelScopedTopic(centralName, iface, address, channel) + "/schedule/" + naming.TopicSafe(key) + "/set"
}

// channelScopedTopic returns the shared "<base>/<central>/<iface>/<addr>/<channel>"
// prefix used by every bridge-internal channel topic that has no
// naming.PathData helper yet (combined-DP and schedule topics).
//
// The base is only slash-trimmed, never [naming.TopicSafe]d: a base is a
// topic *prefix*, and an operator may configure it with levels
// ("home/loom"). Escaping it here collapsed those to "home_loom" while
// every naming.MQTT* helper kept them, so the combined-DP and schedule
// topics of such an installation landed on a prefix of their own — one
// no subscriber and no retain sweep looks at.
func (b *TopicBuilder) channelScopedTopic(centralName, iface, address string, channel int) string {
	return strings.Trim(b.Base, "/") + "/" +
		naming.TopicSafe(centralName) + "/" +
		naming.TopicSafe(iface) + "/" +
		naming.TopicSafe(address) + "/" +
		intStr(channel)
}

func intStr(i int) string {
	return strconv.Itoa(i)
}

// --- Central-scope topics (delegate to naming free functions) -------

// SystemStatus is the non-retained per-central system-status event topic.
func (b *TopicBuilder) SystemStatus(centralName string) string {
	return naming.MQTTSystemStatus(b.Base, centralName)
}

// HubStatus is the retained CCU connection state topic. Central-weite
// aggregate that is not bound to a specific model object.
func (b *TopicBuilder) HubStatus(centralName string) string {
	return naming.MQTTHubStatus(b.Base, centralName)
}

// HubInfo is the retained CCU info-snapshot topic.
func (b *TopicBuilder) HubInfo(centralName string) string {
	return naming.MQTTHubInfo(b.Base, centralName)
}

// HubDiagnostics is the retained per-CCU diagnostics topic.
func (b *TopicBuilder) HubDiagnostics(centralName string) string {
	return naming.MQTTHubDiagnostics(b.Base, centralName)
}

// HubSystemHealthScore is the retained system-health score topic
// (`<base>/<central>/system/health_score`). Matches the state_topic
// in BuildSystemHealthDiscovery.
//
// The central segment is [naming.TopicSafe]d like every other topic on
// the plane. It used to be lower-cased instead, which put the metric
// sensors of a central whose name carries an upper-case letter, a dot
// or an umlaut on a different segment than the rest of its topics — and
// on a different segment than the discovery payload declared.
func (b *TopicBuilder) HubSystemHealthScore(centralName string) string {
	return b.systemTopic(centralName, "health_score")
}

// HubConnectionLatency is the retained aggregated connection-latency
// topic (`<base>/<central>/system/latency`). Matches the state_topic in
// BuildConnectionLatencyDiscovery — one central-wide latency sensor, not
// per-interface.
func (b *TopicBuilder) HubConnectionLatency(centralName string) string {
	return b.systemTopic(centralName, "latency")
}

// HubLastEventAge is the retained last-event-age state topic
// (`<base>/<central>/system/last_event_age`). Matches the state_topic in
// BuildLastEventAgeDiscovery. The value is the age in seconds of the
// newest backend event — a liveness signal for the CCU connection.
func (b *TopicBuilder) HubLastEventAge(centralName string) string {
	return b.systemTopic(centralName, "last_event_age")
}

// systemTopic is the shared `<base>/<central>/system/<metric>` shape of
// the central-wide metric sensors. One builder so the three of them
// cannot drift apart from each other or from the discovery payloads,
// which derive their state topics from these methods.
func (b *TopicBuilder) systemTopic(centralName, metric string) string {
	return b.Base + "/" + naming.TopicSafe(centralName) + "/system/" + metric
}

// systemMetricTopics returns the retained topics of all central-wide
// metric sensors of one central.
//
// One list so a caller that reasons about the group as a whole — the
// retained-orphan sweep, which has to tell a retired spelling of these
// topics from a live one — cannot enumerate a different set than the
// publishers do.
func (b *TopicBuilder) systemMetricTopics(centralName string) []string {
	return []string{
		b.HubSystemHealthScore(centralName),
		b.HubConnectionLatency(centralName),
		b.HubLastEventAge(centralName),
	}
}

// HubUpdate is the retained firmware-update state topic
// (`<base>/<central>/hub/update`). Matches the state_topic in
// BuildHubUpdateDiscovery.
func (b *TopicBuilder) HubUpdate(centralName string) string {
	return naming.MQTTHubUpdate(b.Base, centralName)
}

// --- Helpers ---------------------------------------------------------

// parameterPathData composes the [naming.PathData] for a per-DP
// topic. Centralised so the bucket-empty fallback (→ VALUES) and
// the iface-string conversion stay in one place.
//
// centralName is passed through because the data-point constructor needs it to
// recover the bare interface from the wire id every caller hands in.
func (b *TopicBuilder) parameterPathData(centralName, iface, address string, channel int, bucket, parameter string) naming.PathData {
	if bucket == "" {
		bucket = string(payload.BucketValues)
	}
	return naming.NewDataPointPathData(
		centralName,
		hmtypes.ParseWireInterfaceID(iface),
		address,
		channel,
		naming.Bucket(bucket),
		parameter,
	)
}

// safe is a thin wrapper kept for the bridge-internal topics
// (BridgeStatus, BridgeHealth, DiscoveryConfig, ServiceMethodCommand)
// that don't go through naming.PathData. Mirrors
// [naming.TopicSafe] exactly.
func safe(s string) string {
	return naming.TopicSafe(s)
}
