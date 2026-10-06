// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strconv"
	"strings"

	hatopic "github.com/SukramJ/go-hamqtt/topic"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TopicBuilder assembles topic strings from the raw plane components.
// Base defaults to "openccu-loom" but can be overridden per bridge.
//
// The grammar is mqtt-smarthome 2.0's `<name>/<function>/<item...>`
// (ADR 0083), with the configured base as `<name>`: status items under
// `status`, their commands under `set` on the same item path, descriptor
// companions under `meta`, and the instance topics `connected`, `info` and
// `maintenance/…` directly below the base.
//
// Every method that targets a model-relevant topic (data point, channel
// aggregate, device snapshot, hub item, …) delegates to [naming.PathData] or
// a free function in the naming package — the model layer owns those
// shapes. The bridge layer only fills in the runtime context (Base,
// Central) that the model has no natural access to.
//
// The shapes composed here are the instance topics ([TopicBuilder.Connected],
// [TopicBuilder.Info], [TopicBuilder.Maintenance]), the daemon-level add-on
// update pair, the central-wide `system/<metric>` items, and the combined-DP
// and schedule channel items that the naming package carries no helper for
// (see [TopicBuilder.CombinedState]). They go through
// [naming.StatusTopic] / [naming.SetTopic] like everything else, so the
// function level is spelled once.
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

// TopicBaseConformant reports whether base is an mqtt-smarthome 2.0
// instance name — a single topic level. A multi-level base (`home/loom`) is
// accepted verbatim and works, but runs outside spec §3 and is invisible to a
// `+/info` scan (ADR 0083); go-hamqtt's multi-level layout is the authority
// on the distinction.
func TopicBaseConformant(base string) bool {
	l, err := hatopic.NewSmartHomeMultiLevel(strings.Trim(base, "/"))
	return err == nil && l.Conformant()
}

// --- Instance topics (mqtt-smarthome §3.1, §6, §7) --------------------

// Connected is the instance's `<base>/connected` topic: a plain retained
// `0`/`1`/`2` (`0` by Last Will and on graceful stop, `1` while no central
// is reachable, `2` while at least one is). It replaces the former
// `bridge/status` online/offline marker.
func (b *TopicBuilder) Connected() string {
	return b.Base + "/" + hatopic.FunctionConnected
}

// Info is the retained instance-introspection document `<base>/info`
// (spec §6). The daemon's build and boot metadata that used to live on
// `bridge/health` are folded into it.
func (b *TopicBuilder) Info() string {
	return b.Base + "/" + hatopic.FunctionInfo
}

// Maintenance is `<base>/maintenance/<item...>` (spec §7):
// `maintenance/set/loglevel`, `maintenance/set/restart` and the retained
// `maintenance/stats`.
func (b *TopicBuilder) Maintenance(item ...string) string {
	return b.Base + "/" + hatopic.FunctionMaintenance + "/" + strings.Join(item, "/")
}

// AddonUpdateState is the status item of the daemon-level CCU add-on
// self-update entity (ADR 0057). Unlike every per-central hub item this
// carries no <central> segment: the self-updater is a property of the daemon
// process itself, not of any one CCU.
//
//	<base>/status/system/addon_update
func (b *TopicBuilder) AddonUpdateState() string {
	return naming.StatusTopic(b.Base, "system", "addon_update")
}

// AddonUpdateCommand is the subscribed `set` item pairing
// [TopicBuilder.AddonUpdateState] — HA's `update` entity publishes its
// install command here.
//
//	<base>/set/system/addon_update
func (b *TopicBuilder) AddonUpdateCommand() string {
	return naming.SetTopic(b.Base, "system", "addon_update")
}

// DiscoveryNodeScope is the `<base-slug>_` prefix this daemon's discovery
// node ids carry so a second daemon on the same broker cannot overwrite them.
// Empty on the default topic base — see [naming.DiscoveryBaseScope] for why
// only a base the operator changed earns a scope.
//
// Exported for the retained-config sweeps, which have to recognise the node
// ids this builder writes ([discoveryNodePrefixes], [Bridge.ownsDiscoveryTopic]).
func (b *TopicBuilder) DiscoveryNodeScope() string {
	return naming.DiscoveryBaseScope(b.Base)
}

// DiscoveryConfig is the HA Discovery retained config topic.
// Delegates to [naming.DiscoveryConfigTopic] — the model layer owns
// the format string.
//
//	homeassistant/<component>/<base-slug>_<node_id>/<object_id>/config
//
// The `<base-slug>_` scope is this builder's own contribution and the reason
// the method takes a receiver at all: ADR 0006 rule 4 has always said the
// discovery node derives from the topic base, and until this scope existed it
// did not — two daemons under different bases wrote the same config topics.
// It is empty on the default base, so the shape the topic-schema document
// names is unchanged and so is every topic a default-base daemon publishes.
// Applied HERE rather than at each of the thirteen node-id producers because
// this is the one function all of them funnel through on the way to a topic;
// the bundle plane is the single exception and scopes in [Bridge.routeToBundle].
func (b *TopicBuilder) DiscoveryConfig(component, nodeID, objectID string) string {
	return naming.DiscoveryConfigTopic(component, naming.ScopedDiscoveryNodeID(b.Base, nodeID), objectID)
}

// --- Channel-bound DP topics (delegate to naming.PathData) -----------

// ParameterState is the canonical retained value topic for one data
// point in one paramset bucket. Delegates to
// [naming.PathData.MQTTState].
func (b *TopicBuilder) ParameterState(centralName, iface, address string, channel int, bucket payload.Bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTState(b.Base, centralName)
}

// ParameterCommand returns the data point's `set` item.
func (b *TopicBuilder) ParameterCommand(centralName, iface, address string, channel int, bucket payload.Bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTCommand(b.Base, centralName)
}

// ParameterConfig returns the descriptor companion `<base>/meta/<item>`.
func (b *TopicBuilder) ParameterConfig(centralName, iface, address string, channel int, bucket payload.Bucket, parameter string) string {
	return b.parameterPathData(centralName, iface, address, channel, bucket, parameter).MQTTConfig(b.Base, centralName)
}

// DataPointState resolves to [TopicBuilder.ParameterState] on the
// VALUES bucket. Retained as a back-compat alias.
func (b *TopicBuilder) DataPointState(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterState(centralName, iface, address, channel, payload.BucketValues, parameter)
}

// DataPointCommand is the VALUES-bucket `set` alias.
func (b *TopicBuilder) DataPointCommand(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterCommand(centralName, iface, address, channel, payload.BucketValues, parameter)
}

// DataPointConfig is the VALUES-bucket `meta` alias.
func (b *TopicBuilder) DataPointConfig(centralName, iface, address string, channel int, parameter string) string {
	return b.ParameterConfig(centralName, iface, address, channel, payload.BucketValues, parameter)
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

// --- Slot helpers (delegate to ParameterState for non-custom buckets) ---

// SlotState resolves to the per-parameter
// [TopicBuilder.ParameterState] for VALUES/MASTER/CALCULATED buckets,
// and to [naming.PathData.MQTTCustomDPState] for [BucketCustom].
func (b *TopicBuilder) SlotState(centralName, iface string, slot payload.TopicSlot) string {
	if slot.Bucket == payload.BucketCustom {
		pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
		return pd.MQTTCustomDPState(b.Base, centralName)
	}
	return b.ParameterState(centralName, iface, slot.Address, slot.Channel, slot.Bucket, slot.Parameter)
}

// SlotConfig resolves to the matching descriptor-companion topic.
func (b *TopicBuilder) SlotConfig(centralName, iface string, slot payload.TopicSlot) string {
	if slot.Bucket == payload.BucketCustom {
		pd := naming.NewCustomDPPathData(hmtypes.ParseWireInterfaceID(iface), slot.Address, slot.Channel, slot.Parameter)
		return pd.MQTTCustomDPConfig(b.Base, centralName)
	}
	return b.ParameterConfig(centralName, iface, slot.Address, slot.Channel, slot.Bucket, slot.Parameter)
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
// item (`<base>/set/…/update`). Nothing subscribes to it and the update
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

// CombinedState returns the status item of a combined DP (HSColor, Timer,
// LevelCombined, …) on a channel. The kind disambiguates multiple combined
// DPs on the same channel ("duration", "hs_color", …).
//
//	<base>/status/<central>/<iface>/<addr>/<channel>/combined/<kind>
func (b *TopicBuilder) CombinedState(centralName, iface, address string, channel int, kind string) string {
	return naming.StatusTopic(b.Base, channelItem(centralName, iface, address, channel, "combined", naming.TopicSafe(kind))...)
}

// CombinedCommand returns the `set` item of a combined DP.
//
//	<base>/set/<central>/<iface>/<addr>/<channel>/combined/<kind>
func (b *TopicBuilder) CombinedCommand(centralName, iface, address string, channel int, kind string) string {
	return naming.SetTopic(b.Base, channelItem(centralName, iface, address, channel, "combined", naming.TopicSafe(kind))...)
}

// ScheduleEntityState returns the status item of the device-level Zeitplan
// sensor (one per schedule-relevant device): the count of active schedule
// entries.
//
//	<base>/status/<central>/<iface>/<addr>/<channel>/schedule/active_entries
func (b *TopicBuilder) ScheduleEntityState(centralName, iface, address string, channel int) string {
	return naming.StatusTopic(b.Base, channelItem(centralName, iface, address, channel, segSchedule, "active_entries")...)
}

// ScheduleEntityAttrs returns the status item carrying the Zeitplan
// sensor's attributes — schedule_type, max_entries,
// available_target_channels, schedule_enabled, schedule_data, …
//
//	<base>/status/<central>/<iface>/<addr>/<channel>/schedule/attributes
func (b *TopicBuilder) ScheduleEntityAttrs(centralName, iface, address string, channel int) string {
	return naming.StatusTopic(b.Base, channelItem(centralName, iface, address, channel, segSchedule, "attributes")...)
}

// ScheduleSwitchState returns the boolean status item of one schedule
// channel switch. The `switch` level keeps every key out of the namespace of
// the two items above, so no schedule key can shadow `active_entries`.
//
//	<base>/status/<central>/<iface>/<addr>/<channel>/schedule/switch/<key>
func (b *TopicBuilder) ScheduleSwitchState(centralName, iface, address string, channel int, key string) string {
	return naming.StatusTopic(b.Base, channelItem(centralName, iface, address, channel, segSchedule, segScheduleSwitch, naming.TopicSafe(key))...)
}

// ScheduleSwitchCommand returns the `set` item of one schedule channel
// switch.
//
//	<base>/set/<central>/<iface>/<addr>/<channel>/schedule/switch/<key>
func (b *TopicBuilder) ScheduleSwitchCommand(centralName, iface, address string, channel int, key string) string {
	return naming.SetTopic(b.Base, channelItem(centralName, iface, address, channel, segSchedule, segScheduleSwitch, naming.TopicSafe(key))...)
}

// channelItem returns the escaped `<central>/<iface>/<addr>/<channel>` item
// path followed by rest — the prefix of every bridge-local channel item that
// has no naming.PathData helper (combined-DP and schedule items).
func channelItem(centralName, iface, address string, channel int, rest ...string) []string {
	return append([]string{
		naming.TopicSafe(centralName),
		naming.TopicSafe(iface),
		naming.TopicSafe(address),
		intStr(channel),
	}, rest...)
}

func intStr(i int) string {
	return strconv.Itoa(i)
}

// --- Central-scope topics (delegate to naming free functions) -------

// SystemStatus is the non-retained per-central system-status event topic.
func (b *TopicBuilder) SystemStatus(centralName string) string {
	return naming.MQTTSystemStatus(b.Base, centralName)
}

// HubStatus renders the per-CCU availability gate
// `<base>/status/<central>/online`.
//
// **It is published**: a retained status item whose `val` is true while the
// CCU is reachable, folded from the CCU's per-interface reachability states
// and the ReGa probe, and written at QoS 1. Every CCU-scoped hub entity
// lists it in its discovery `availability` block alongside
// [TopicBuilder.Connected], so an unreachable CCU does not leave its
// sysvars, programs and system scores "available" with stale values. The
// two shapes that stay reserved are [TopicBuilder.HubInfo] and
// [TopicBuilder.HubDiagnostics].
func (b *TopicBuilder) HubStatus(centralName string) string {
	return naming.MQTTHubStatus(b.Base, centralName)
}

// HubInfo renders the reserved per-CCU info-snapshot shape
// `<base>/status/<central>/hub/info`.
//
// **Nothing publishes it**, and no consumer needs it: the fields it
// would carry (model, sw_version, serial_number, configuration_url) are
// in the HA discovery device block that hubDeviceBlock builds. Like
// [TopicBuilder.HubDiagnostics] the builder is kept rather than deleted,
// so the reserved shape stays pinned by
// tests/contract/mqtt_topic_schema_doctest_test.go and cannot drift if
// one of the two ever does gain a publisher —
// tests/contract/mqtt_topic_schema_producer_test.go fails if one does
// without its schema row and ADR note moving with it.
func (b *TopicBuilder) HubInfo(centralName string) string {
	return naming.MQTTHubInfo(b.Base, centralName)
}

// HubDiagnostics renders the reserved per-CCU diagnostics shape
// `<base>/status/<central>/hub/diagnostics`.
//
// **Nothing publishes it.** It went undocumented until 2026-09-12, when
// it was written into docs/mqtt-topic-schema.md's reserved table
// alongside [TopicBuilder.HubInfo] rather than left unmentioned. The
// radio and load figures it would have
// aggregated reach consumers as per-device data points (DUTY_CYCLE,
// CARRIER_SENSE_LEVEL, …) plus the central-wide metric topics
// [TopicBuilder.HubSystemHealthScore], [TopicBuilder.HubConnectionLatency]
// and [TopicBuilder.HubLastEventAge]. See [TopicBuilder.HubInfo].
func (b *TopicBuilder) HubDiagnostics(centralName string) string {
	return naming.MQTTHubDiagnostics(b.Base, centralName)
}

// HubSystemHealthScore is the retained system-health score item
// (`<base>/status/<central>/system/health_score`). Matches the state_topic
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
// item (`<base>/status/<central>/system/latency`). Matches the state_topic in
// BuildConnectionLatencyDiscovery — one central-wide latency sensor, not
// per-interface.
func (b *TopicBuilder) HubConnectionLatency(centralName string) string {
	return b.systemTopic(centralName, "latency")
}

// HubLastEventAge is the retained last-event-age status item
// (`<base>/status/<central>/system/last_event_age`). Matches the state_topic in
// BuildLastEventAgeDiscovery. The value is the age in seconds of the
// newest backend event — a liveness signal for the CCU connection.
func (b *TopicBuilder) HubLastEventAge(centralName string) string {
	return b.systemTopic(centralName, "last_event_age")
}

// systemTopic is the shared `<base>/status/<central>/system/<metric>` shape of
// the central-wide metric sensors. One builder so the three of them
// cannot drift apart from each other or from the discovery payloads,
// which derive their state topics from these methods.
func (b *TopicBuilder) systemTopic(centralName, metric string) string {
	return naming.StatusTopic(b.Base, naming.TopicSafe(centralName), "system", metric)
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

// HubUpdate is the retained firmware-update status item
// (`<base>/status/<central>/hub/update`). Matches the state_topic in
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
func (b *TopicBuilder) parameterPathData(centralName, iface, address string, channel int, bucket payload.Bucket, parameter string) naming.PathData {
	if bucket == payload.BucketUnset {
		bucket = payload.BucketValues
	}
	return naming.NewDataPointPathData(
		centralName,
		hmtypes.ParseWireInterfaceID(iface),
		address,
		channel,
		bucket,
		parameter,
	)
}

// safe is a package-local alias of [naming.TopicSafe], which it mirrors
// exactly.
//
// Its sole caller is the retained-topic address matcher in bridge.go.
func safe(s string) string {
	return naming.TopicSafe(s)
}

// scopedDaemonIdentity prefixes a daemon-level Home Assistant `unique_id`
// with [naming.DiscoveryBaseScope] of base.
//
// It lives in this package, not beside the topic rule in
// `internal/model/naming`, because that package deliberately owns no
// `unique_id` builder at all: the model layer owns the topic shapes, the ids
// Home Assistant keys its registry on are this package's business, and
// TestHmDevNamingDeclaresNoHADiscoveryUniqueIDBuilder enforces the split.
//
// # Why a unique id needs the scope at all, when the node id already has it
//
// Moving the discovery *topic* (which is what [naming.ScopedDiscoveryNodeID] does)
// separates two daemons on the broker. It does not separate them inside Home
// Assistant. HA's MQTT integration keys its entity registry on `unique_id`
// and rejects a second config that declares one it has already seen —
// "Platform mqtt does not generate unique IDs" — so two daemons writing two
// DISTINCT config topics that carry the SAME `unique_id` are worse off than
// before the topics were separated: the semantics move from "last writer
// wins, and a restart repoints the entity at the live daemon" to "first
// writer wins permanently", and the second daemon's entities never appear.
//
// The three daemon-level planes (alarm, Security & Safety, add-on
// self-update) are the ones that need this. They carry no `<central>`
// segment (ADR 0052) and their ids are fixed literals — `loom_addon_update`,
// `openccu-loom_alarm_<zone>`, `loom_security_<key>` — with nothing in them
// that differs between two daemons. Every per-device and hub plane is already
// keyed on the CCU serial or the ISE id and is left alone.
//
// # Why only a non-default base contributes, again
//
// Re-keying a `unique_id` is the one move Home Assistant has no migration
// path for at the ENTITY level: history, long-term statistics, the entity id,
// renames, the area and every automation that names the entity are lost.
// Applying this unconditionally would charge that to every single-daemon
// installation in the fleet for a collision it cannot have. A non-default
// base is the operator saying "there is more than one of me", so it is the
// configuration that pays — and it is exactly the configuration that is
// broken today.
func scopedDaemonIdentity(base, uniqueID string) string {
	if uniqueID == "" {
		return ""
	}
	return naming.DiscoveryBaseScope(base) + uniqueID
}
