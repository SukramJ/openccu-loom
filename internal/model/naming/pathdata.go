// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SukramJ/go-hamqtt/model"
	"github.com/SukramJ/go-hamqtt/topic"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Path-root constants. They are the canonical first segment of every
// set/state path, and their only readers are [NewDataPointPathData] and
// [NewSysvarPathData] at the bottom of this file — the roots reach a north-bound adapter as the prefix of a rendered
// SetPath / StatePath, never as constants of their own, so they are not
// exported.
const (
	// setPathRoot is the first segment for channel-bound write paths.
	setPathRoot = "device/set"
	// statePathRoot is the first segment for channel-bound read paths.
	statePathRoot = "device/status"
	// virtDevSetPathRoot replaces setPathRoot for the VirtualDevices / CCU-Jack
	// interface family.
	virtDevSetPathRoot = "virtdev/set"
	// virtDevStatePathRoot is the read-side counterpart.
	virtDevStatePathRoot = "virtdev/status"
	// sysvarSetPathRoot is the first segment for system-variable DPs.
	sysvarSetPathRoot = "sysvar/set"
	// sysvarStatePathRoot is the read-side counterpart.
	sysvarStatePathRoot = "sysvar/status"
)

// PathData is the model-layer descriptor for a single data point's
// canonical paths. It is the single source of truth across the
// north-bound adapters (MQTT, REST, WS):
//
//   - logical strings (`SetPath`, `StatePath`)
//     are pre-computed for parity with the Python reference.
//   - The structured fields (Interface, Address, ChannelNo, Bucket,
//     Kind) let an adapter compose its own native shape — e.g. the
//     MQTT bridge derives its bucket-aware status item
//     `<base>/status/<central>/<iface>/<addr>/<ch>/<bucket>/<param>` via
//     [PathData.MQTTState].
//
// The strings are field-cached on the data point at construction time
// — north-bound adapters consume them in hot paths (every event,
// every REST request) so on-demand recomputation would amplify
// allocator pressure for no semantic gain.
//
// (`DataPointPathData`, `ProgramPathData`, `SysvarPathData`,
// `HubPathData`) in `model/support.py:240-332`. The structured
// Fields are an additive openccu-loom extension
// the strings only, but a Go-typed bridge layer benefits from
// strongly-typed access to the components.
type PathData struct {
	// SetPath is the write path
	// (`device/set/<ADDR>/<CH>/<BUCKET>/<KIND>`). Empty for
	// non-channel-bound DPs (programs, sysvars, hub).
	SetPath string
	// StatePath is the read path
	// (`device/status/<ADDR>/<CH>/<BUCKET>/<KIND>` for channel DPs;
	// `program/status/<id>` etc. for non-channel families).
	StatePath string

	// Structured components — additive over
	// string-only PathData. Adapters that need to compose
	// transport-specific topic strings read them straight off the
	// struct without re-parsing SetPath/StatePath.
	//
	// Interface is the canonical `<central>-<interface>` wire id, which is
	// what every producer of path data holds (device.Device.InterfaceID,
	// hmtypes.DataPointKey.InterfaceID) and what the north-bound topic
	// segment has always carried. It is typed so that a decision which is
	// really about the radio technology — the `virtdev/` path roots — has to
	// go through [hmtypes.WireInterfaceID.Bare] instead of comparing a wire
	// id against a bare interface constant, which never matched on a named
	// central.
	Interface hmtypes.WireInterfaceID
	Address   string // upper-cased CCU device address ("" for non-channel families)
	ChannelNo int
	Bucket    model.Bucket
	// Kind is the wire-parameter name (VALUES / MASTER) or the
	// custom-DP type label ("CLIMATE", "LIGHT", …) for model.BucketCustom
	// DPs. For non-channel families it is the program/sysvar id /
	// hub-DP name.
	Kind string
}

// EmptyPathData is the zero-value sentinel returned when path
// computation is not possible (missing address, empty parameter).
var EmptyPathData = PathData{}

// IsZero reports whether the path data is empty.
func (p PathData) IsZero() bool { return p.SetPath == "" && p.StatePath == "" }

// The MQTT topic grammar is mqtt-smarthome 2.0's
// `<name>/<function>/<item...>` (ADR 0083): the function — `status`, `set`,
// `meta` — is the second level, and the item path below it is the same for
// a status item, its `set` and its descriptor companion. `<name>` is the
// configured topic base, kept verbatim (a multi-level base such as
// `home/loom` stays accepted; see ADR 0083 §Grammar). The function names
// are go-hamqtt's, so this package and the shared layout cannot spell them
// differently.

// mqttTopic composes `<base>/<function>/<item...>`. Every item segment must
// already be escaped by the caller; the base is only slash-trimmed, never
// escaped, because it is a prefix and may carry levels of its own.
func mqttTopic(base, function string, item ...string) string {
	return strings.Trim(base, "/") + "/" + function + "/" + strings.Join(item, "/")
}

// StatusTopic is `<base>/status/<item...>`. Exported for the adapters that
// compose an item path the model owns no helper for (the combined-DP and
// schedule items, the daemon-level alarm and security trees), so every
// status topic of the daemon goes through one spelling of the function.
func StatusTopic(base string, item ...string) string {
	return mqttTopic(base, topic.FunctionStatus, item...)
}

// SetTopic is `<base>/set/<item...>`, the command twin of [StatusTopic].
func SetTopic(base string, item ...string) string {
	return mqttTopic(base, topic.FunctionSet, item...)
}

// MetaTopic is `<base>/meta/<item...>`, the descriptor companion of the
// status item with the same path.
func MetaTopic(base string, item ...string) string {
	return mqttTopic(base, topic.FunctionMeta, item...)
}

// deviceItem is the item path of a device: `<central>/<iface>/<addr>`.
func (p PathData) deviceItem(centralName string) []string {
	return []string{TopicSafe(centralName), TopicSafe(string(p.Interface)), TopicSafe(p.Address)}
}

// channelItem is the item path of a channel:
// `<central>/<iface>/<addr>/<ch>`, followed by rest.
func (p PathData) channelItem(centralName string, rest ...string) []string {
	return append(append(p.deviceItem(centralName), strconv.Itoa(p.ChannelNo)), rest...)
}

// dataPointItem is the item path of a wire data point:
// `<central>/<iface>/<addr>/<ch>/<bucket>/<kind>`. Nil when the path data
// does not describe one.
func (p PathData) dataPointItem(centralName string) []string {
	if p.Address == "" || p.Kind == "" || p.Bucket == model.BucketUnset {
		return nil
	}
	return p.channelItem(centralName, TopicSafe(p.Bucket.String()), TopicSafe(p.Kind))
}

// MQTTState returns the data point's status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/<bucket>/<kind>`.
// Empty on non-channel-bound DPs (programs, sysvars, hub) — those have
// their own topic builders.
//
// `central` and `base` are bridge-side scoping that does not live on the
// model; the model declares the shape, the bridge prepends its runtime
// context.
//
// All item inputs are MQTT-safe-escaped via [TopicSafe]. Wire parameters
// are upper-case by convention; bucket labels are lower-case.
func (p PathData) MQTTState(base, centralName string) string {
	item := p.dataPointItem(centralName)
	if item == nil {
		return ""
	}
	return StatusTopic(base, item...)
}

// MQTTCommand returns the data point's `set` item — the same item path as
// [PathData.MQTTState] under the `set` function. Empty for
// non-channel-bound DPs.
func (p PathData) MQTTCommand(base, centralName string) string {
	item := p.dataPointItem(centralName)
	if item == nil {
		return ""
	}
	return SetTopic(base, item...)
}

// MQTTConfig returns the descriptor companion `<base>/meta/<item>` of the
// data point's status item.
func (p PathData) MQTTConfig(base, centralName string) string {
	item := p.dataPointItem(centralName)
	if item == nil {
		return ""
	}
	return MetaTopic(base, item...)
}

// MQTTChannelImpulse returns the per-channel impulse-event status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/impulse`. Not retained.
//
// A sibling leaf rather than `…/event/impulse`: the event item is already a
// leaf, and nesting under it would make every `…/event` subscriber receive
// impulses too.
func (p PathData) MQTTChannelImpulse(base, centralName string) string {
	return p.channelEventLeaf(base, centralName, "impulse")
}

// MQTTChannelDeviceError returns the per-channel device-error status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/device_error`. Not retained;
// same sibling-leaf reasoning as [PathData.MQTTChannelImpulse].
func (p PathData) MQTTChannelDeviceError(base, centralName string) string {
	return p.channelEventLeaf(base, centralName, "device_error")
}

// MQTTChannelEvent returns the per-channel keypress status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/event`. Not retained; the
// event type travels in the status object's `val`.
func (p PathData) MQTTChannelEvent(base, centralName string) string {
	return p.channelEventLeaf(base, centralName, "event")
}

// channelEventLeaf builds one of the per-channel event items.
func (p PathData) channelEventLeaf(base, centralName, leaf string) string {
	if p.Address == "" {
		return ""
	}
	return StatusTopic(base, p.channelItem(centralName, leaf)...)
}

// deviceLeaf builds one of the device-scope status items below
// `<base>/status/<central>/<iface>/<addr>/`.
func (p PathData) deviceLeaf(base, centralName, leaf string) string {
	if p.Address == "" {
		return ""
	}
	return StatusTopic(base, append(p.deviceItem(centralName), leaf)...)
}

// MQTTDeviceAvailability returns the device's reachability status item
// `<base>/status/<central>/<iface>/<addr>/online` (`val` true/false). Uses
// only Address + Interface; ChannelNo / Bucket / Kind are ignored. Empty
// when Address is missing.
func (p PathData) MQTTDeviceAvailability(base, centralName string) string {
	return p.deviceLeaf(base, centralName, "online")
}

// MQTTDeviceInfo returns the device's info-snapshot status item
// `<base>/status/<central>/<iface>/<addr>/info`.
func (p PathData) MQTTDeviceInfo(base, centralName string) string {
	return p.deviceLeaf(base, centralName, "info")
}

// MQTTDeviceDiagnostics returns the device's diagnostics status item
// `<base>/status/<central>/<iface>/<addr>/diagnostics`.
func (p PathData) MQTTDeviceDiagnostics(base, centralName string) string {
	return p.deviceLeaf(base, centralName, "diagnostics")
}

// MQTTDeviceUpdateState returns the HA `update`-entity status item
// `<base>/status/<central>/<iface>/<addr>/update`.
func (p PathData) MQTTDeviceUpdateState(base, centralName string) string {
	return p.deviceLeaf(base, centralName, "update")
}

// MQTTDeviceUpdateCommand builds the install-command item
// `<base>/set/<central>/<iface>/<addr>/update`.
//
// Nothing subscribes to it. No command route matches its shape, and the
// device update entity is declared read-only because flashing firmware from
// an unconfirmed — possibly retained and replayed — broker payload is
// unsafe. Kept as the canonical spelling should the command path ever be
// wired.
func (p PathData) MQTTDeviceUpdateCommand(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return SetTopic(base, append(p.deviceItem(centralName), "update")...)
}

// MQTTWeekProfileState returns the week-profile select status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/week_profile`.
func (p PathData) MQTTWeekProfileState(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return StatusTopic(base, p.channelItem(centralName, "week_profile")...)
}

// MQTTWeekProfileCommand returns the matching `set` item
// `<base>/set/<central>/<iface>/<addr>/<ch>/week_profile`.
func (p PathData) MQTTWeekProfileCommand(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return SetTopic(base, p.channelItem(centralName, "week_profile")...)
}

// customDPItem is the item path of a custom-DP slot, nil when the path data
// is not one.
func (p PathData) customDPItem(centralName string) []string {
	if p.Address == "" || p.Kind == "" || p.Bucket != model.BucketCustom {
		return nil
	}
	return p.channelItem(centralName, "custom", TopicSafe(strings.ToLower(p.Kind)))
}

// MQTTCustomDPState returns the custom-DP slot status item
// `<base>/status/<central>/<iface>/<addr>/<ch>/custom/<kind>` for the
// climate / cover / lock / light / siren / valve / textdisplay aggregate.
// The PathData must have been constructed with [model.BucketCustom] and
// `Kind` set to the lowercase domain label (e.g. "climate"). Empty when
// Bucket != model.BucketCustom or Address / Kind is missing.
func (p PathData) MQTTCustomDPState(base, centralName string) string {
	item := p.customDPItem(centralName)
	if item == nil {
		return ""
	}
	return StatusTopic(base, item...)
}

// MQTTCustomDPConfig is the descriptor companion `<base>/meta/<item>` of
// the custom-DP slot.
func (p PathData) MQTTCustomDPConfig(base, centralName string) string {
	item := p.customDPItem(centralName)
	if item == nil {
		return ""
	}
	return MetaTopic(base, item...)
}

// MQTTCustomDPServiceMethod returns the per-method command item
// `<base>/set/<central>/<iface>/<addr>/<ch>/custom/<kind>/<method>` for a
// custom-DP service-method invocation (open, close, set_mode, turn_on, …):
// a method level below the aggregate's item (ADR 0083 §set, ADR 0009's
// bodies unchanged). Empty when the PathData is not a custom-DP slot.
func (p PathData) MQTTCustomDPServiceMethod(base, centralName, method string) string {
	item := p.customDPItem(centralName)
	if item == nil {
		return ""
	}
	return SetTopic(base, append(item, TopicSafe(method))...)
}

// DiscoveryNodeID returns the HA-Discovery `<node_id>` segment that
// groups every entity belonging to one physical device. Format:
// `<central-slug>_<address-lower>` — matching HA's convention that
// `node_id` distinguishes one device from another, not one
// integration from another. When `central` is empty the node id
// collapses to just the lower-cased address.
//
// The central segment goes through [DiscoverySlug], the same
// normaliser the hub node ids and the retained-config orphan sweep
// use: a name carrying a dot or an umlaut would otherwise reach the
// wire in a spelling HA rejects, and in a spelling the sweep cannot
// match — leaving retired entities retained forever.
//
// Empty when the PathData has no Address.
func (p PathData) DiscoveryNodeID(centralName string) string {
	if p.Address == "" {
		return ""
	}
	addr := strings.ToLower(p.Address)
	if centralName == "" {
		return addr
	}
	return DiscoverySlug(centralName) + "_" + addr
}

// DiscoveryObjectID returns the HA-Discovery `<object_id>` segment
// disambiguating one entity within a device. Format:
// `<channel>_<suffix-lower>` — the device address is intentionally
// NOT included because it lives in `node_id` already.
//
// Suffix is the per-entity discriminator: for per-parameter
// entities it is the wire parameter name (lower-cased), for
// custom-DP aggregates the HA component label (`climate`, `lock`,
// …), for press-event aggregates the literal `event`.
//
// Empty when suffix is empty.
func (p PathData) DiscoveryObjectID(suffix string) string {
	if suffix == "" {
		return ""
	}
	return fmt.Sprintf("%d_%s", p.ChannelNo, strings.ToLower(TopicSafe(suffix)))
}

// DiscoveryTopicPrefix is HA's MQTT-Discovery root. The producer below
// and the retained-config sweeps in the MQTT adapter must agree on it
// byte for byte: a sweep spelled one character differently matches
// nothing, and every retired entity keeps its retained config forever.
const DiscoveryTopicPrefix = "homeassistant/"

// DiscoveryConfigTopic returns the canonical HA-Discovery retained
// config topic `homeassistant/<component>/<node_id>/<object_id>/config`.
//
// `homeassistant/` is HA's MQTT-Discovery prefix — configurable on
// the HA side but conventionally fixed; openccu-loom mirrors the
// convention. The model layer owns the format string so the bridge
// stays a thin wiring layer.
//
// All three components are MQTT-safe-escaped via [TopicSafe]. Empty
// inputs produce a malformed topic — the caller must validate first.
func DiscoveryConfigTopic(component, nodeID, objectID string) string {
	return fmt.Sprintf(
		DiscoveryTopicPrefix+"%s/%s/%s/config",
		TopicSafe(component), TopicSafe(nodeID), TopicSafe(objectID),
	)
}

// NewChannelPathData builds a channel-scoped PathData carrying just
// the channel-identifying components (Interface, Address, ChannelNo).
// Used by the bridge for channel-aggregate state, channel events,
// week-profile entities — topics that target a channel rather than a
// specific data point.
//
// Bucket and Kind stay empty; SetPath / StatePath stay empty
// (channel-aggregate has no logical path of its
// own — it is an MQTT-bridge convenience).
func NewChannelPathData(iface hmtypes.WireInterfaceID, address string, channelNo int) PathData {
	if address == "" {
		return EmptyPathData
	}
	return PathData{
		Interface: iface,
		Address:   strings.ToUpper(address),
		ChannelNo: channelNo,
	}
}

// NewDevicePathData builds a device-scoped PathData carrying the
// device-identifying components only. Used by the bridge for
// availability / info / diagnostics / firmware-update topics.
//
// ChannelNo / Bucket / Kind stay zero; the structured helpers that
// only need Address + Interface (MQTTDeviceAvailability,
// MQTTDeviceInfo, …) work with this minimal form.
func NewDevicePathData(iface hmtypes.WireInterfaceID, address string) PathData {
	if address == "" {
		return EmptyPathData
	}
	return PathData{
		Interface: iface,
		Address:   strings.ToUpper(address),
	}
}

// NewCustomDPPathData builds the path data for a custom-DP slot
// (climate / lock / cover / siren / valve / textdisplay). `kind` is
// the lowercase domain label that becomes the trailing path segment
// — e.g. "climate" for a HmIP-BWTH thermostat aggregate.
//
// Bucket is forced to [model.BucketCustom] so [MQTTCustomDPState] etc. can
// guard against accidental misuse with a generic VALUES bucket.
//
// SetPath and StatePath stay empty — custom-DP slots are an
// MQTT-bridge concept and don't carry an logical
// path.
func NewCustomDPPathData(iface hmtypes.WireInterfaceID, address string, channelNo int, kind string) PathData {
	if address == "" || kind == "" {
		return EmptyPathData
	}
	return PathData{
		Interface: iface,
		Address:   strings.ToUpper(address),
		ChannelNo: channelNo,
		Bucket:    model.BucketCustom,
		Kind:      strings.ToLower(kind),
	}
}

// TopicSafe replaces characters MQTT disallows in a single topic
// level (`+`, `#`, `/`, space) with underscores. The raw plane uses
// `/` as the separator, so topic components with interior slashes
// would otherwise split the path incorrectly.
//
// Exported because the adapters escape segments the model never sees,
// and the same rule must apply consistently: the combined-DP and
// schedule keys, the central/iface/address prefix of the bridge-local
// channel topics, the central segment of the `system/<metric>` sensors,
// and the segments the retained sweep and the command subscriber
// rebuild. Not "bridge status, hub topics", which an earlier version of
// this comment named: BridgeStatus and BridgeHealth escape nothing, and
// the hub topics are escaped inside the MQTTHub* functions here.
//
// Delegates to the shared model. That one also folds tab, newline, carriage
// return and NUL, which this never did — strictly safer, and a device name
// containing one of those was already producing a topic no broker would
// route sensibly.
//
// The sibling [DiscoverySlug] now delegates too, to topic.Slug. It used to
// carry a second implementation that dropped non-German accented Latin
// (colliding "Café" and "Caf" into one node id) and let a literal "__"
// through. Both were defects rather than differences, and both are fixed;
// the change moved published node ids, object ids and device identifiers
// for the affected names and shipped under ADR 0068's process. Nothing in
// this package spells a discovery identifier any other way.
func TopicSafe(s string) string {
	return topic.Safe(s)
}

// NewDataPointPathData builds the path data for a channel-bound data
// Point.
// (`model/support.py:254-281`):
//
//	path_item  = f"{address.upper()}/{channel_no}/{bucket}/{kind.upper()}"
//	set_path   = f"{set_root}/{path_item}"
//	state_path = f"{state_root}/{path_item}"
//
// `iface` selects the path roots: VirtualDevices uses the `virtdev/`
// prefix, every other interface uses the `device/` prefix. `bucket`
// disambiguates VALUES / MASTER / CALCULATED / CUSTOM. `kind` is the
// wire-parameter name (typical case) or the custom-DP type label
// ("CLIMATE", "LIGHT") for model.BucketCustom DPs.
//
// `centralName` is required for that one decision and for nothing else: the
// interface arrives as the `<central>-<interface>` wire id every producer
// holds, and the separator is an ordinary hyphen, so only the owning central's
// name can split the id back into the bare interface the root selection asks
// about. Comparing the wire id itself against the bare constant compiled and
// ran, and picked the `device/` roots for every virtual-remote data point on
// any central that has a name.
//
// OpenCCU-Loom adds the bucket segment to the
// the same address+channel+kind combination on different paramsets
// no longer aliases — a real conflict for parameters that exist in
// both VALUES and MASTER on the same channel.
func NewDataPointPathData(centralName string, iface hmtypes.WireInterfaceID, address string, channelNo int, bucket model.Bucket, kind string) PathData {
	if address == "" || kind == "" {
		return EmptyPathData
	}
	if bucket == model.BucketUnset {
		// Default to VALUES — preserves the historic single-bucket
		// behaviour for callers that have not yet been migrated.
		bucket = model.BucketValues
	}
	upperAddr := strings.ToUpper(address)
	upperKind := strings.ToUpper(kind)

	var sb strings.Builder
	sb.Grow(len(upperAddr) + len(upperKind) + len(bucket.String()) + 8)
	sb.WriteString(upperAddr)
	sb.WriteByte('/')
	sb.WriteString(strconv.Itoa(channelNo))
	sb.WriteByte('/')
	sb.WriteString(bucket.String())
	sb.WriteByte('/')
	sb.WriteString(upperKind)
	item := sb.String()

	setRoot := setPathRoot
	stateRoot := statePathRoot
	if iface.Bare(centralName) == hmenum.InterfaceVirtualDevices {
		setRoot = virtDevSetPathRoot
		stateRoot = virtDevStatePathRoot
	}
	return PathData{
		SetPath:   setRoot + "/" + item,
		StatePath: stateRoot + "/" + item,
		Interface: iface,
		Address:   upperAddr,
		ChannelNo: channelNo,
		Bucket:    bucket,
		Kind:      upperKind,
	}
}

// NewSysvarPathData builds the path data for a CCU system-variable data
// point.
func NewSysvarPathData(vid string) PathData {
	if vid == "" {
		return EmptyPathData
	}
	return PathData{
		SetPath:   sysvarSetPathRoot + "/" + vid,
		StatePath: sysvarStatePathRoot + "/" + vid,
		Kind:      vid,
	}
}

// --- Central-scope (hub) items ----------------------------------------

// centralItem is the item path below one central: `<central>/<rest...>`.
func centralItem(centralName string, rest ...string) []string {
	return append([]string{TopicSafe(centralName)}, rest...)
}

// MQTTHubStatus returns the per-CCU availability gate
// `<base>/status/<central>/online`, a status item whose `val` is true while
// the CCU is reachable (ADR 0083: the per-central `online` item).
//
// Reached through `TopicBuilder.HubStatus` in internal/north/mqtt, which the
// per-CCU availability publisher and the hub discovery builder both call.
func MQTTHubStatus(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "online")...)
}

// MQTTHubInfo returns the reserved CCU info-snapshot item
// `<base>/status/<central>/hub/info`. Nothing publishes it: its fields reach
// consumers in the HA discovery device block instead. Documented as a
// reserved shape in docs/mqtt-topic-schema.md.
func MQTTHubInfo(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "info")...)
}

// MQTTHubDiagnostics returns the reserved per-CCU diagnostics item
// `<base>/status/<central>/hub/diagnostics`. Nothing publishes it:
// per-device data points and the `<central>/system/*` metric items carry the
// same figures. Documented as a reserved shape in docs/mqtt-topic-schema.md.
func MQTTHubDiagnostics(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "diagnostics")...)
}

// MQTTHubSysvarState returns the sysvar status item
// `<base>/status/<central>/hub/sysvars/<name>`.
func MQTTHubSysvarState(base, centralName, name string) string {
	if name == "" {
		return ""
	}
	return StatusTopic(base, centralItem(centralName, "hub", "sysvars", TopicSafe(name))...)
}

// MQTTHubSysvarCommand returns the matching `set` item
// `<base>/set/<central>/hub/sysvars/<name>`.
func MQTTHubSysvarCommand(base, centralName, name string) string {
	if name == "" {
		return ""
	}
	return SetTopic(base, centralItem(centralName, "hub", "sysvars", TopicSafe(name))...)
}

// MQTTHubProgramTrigger returns the program's run-once action item
// `<base>/set/<central>/hub/programs/<id>/trigger`. An action item has no
// status; any non-empty payload fires it.
func MQTTHubProgramTrigger(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return SetTopic(base, centralItem(centralName, "hub", "programs", TopicSafe(id), "trigger")...)
}

// MQTTHubProgramSet returns the program-activation command item
// `<base>/set/<central>/hub/programs/<id>/active`.
//
// Distinct from the trigger item: `active` decides whether the CCU lets the
// program run at all, `trigger` runs it once. They are two controls because
// the CCU treats them as two things — a deactivated program ignores a
// trigger.
func MQTTHubProgramSet(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return SetTopic(base, centralItem(centralName, "hub", "programs", TopicSafe(id), "active")...)
}

// MQTTHubProgramExecuteAvailability returns the status item
// `<base>/status/<central>/hub/programs/<id>/execute_available` (`val`
// true/false). A consumer greys out the execute control while the program
// is deactivated, without having to derive that rule itself.
func MQTTHubProgramExecuteAvailability(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return StatusTopic(base, centralItem(centralName, "hub", "programs", TopicSafe(id), "execute_available")...)
}

// MQTTHubProgramState returns the program's activation status item
// `<base>/status/<central>/hub/programs/<id>/active` — the status twin of
// [MQTTHubProgramSet], which HA's `switch` entity renders its on/off pip
// from.
func MQTTHubProgramState(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return StatusTopic(base, centralItem(centralName, "hub", "programs", TopicSafe(id), "active")...)
}

// MQTTHubInstallModeForInterface is the per-interface install-mode
// countdown status item `<base>/status/<central>/hub/install_mode/<iface>`.
// The reference stack exposes one remaining-seconds sensor per interface
// (HmIP-RF, BidCos-RF) rather than a single central-wide aggregate.
func MQTTHubInstallModeForInterface(base, centralName, iface string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "install_mode", TopicSafe(iface))...)
}

// MQTTHubInstallModeCommand is the per-interface install-mode activation
// item `<base>/set/<central>/hub/install_mode/<iface>`. HA's button
// publishes "PRESS" here and the command subscriber translates it into an
// install-mode activation for the named interface.
func MQTTHubInstallModeCommand(base, centralName, iface string) string {
	return SetTopic(base, centralItem(centralName, "hub", "install_mode", TopicSafe(iface))...)
}

// MQTTHubAlarmMessages is the alarm-messages status item
// `<base>/status/<central>/hub/alarm_messages`.
func MQTTHubAlarmMessages(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "alarm_messages")...)
}

// MQTTHubServiceMessages is the service-messages status item
// `<base>/status/<central>/hub/service_messages`.
func MQTTHubServiceMessages(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "service_messages")...)
}

// MQTTHubInbox is the inbox status item `<base>/status/<central>/hub/inbox`.
func MQTTHubInbox(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "inbox")...)
}

// MQTTHubUpdate is the hub firmware-update status item
// `<base>/status/<central>/hub/update`.
func MQTTHubUpdate(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "update")...)
}

// MQTTHubConnectivity is the per-interface connectivity status item
// `<base>/status/<central>/hub/connectivity/<iface>`.
func MQTTHubConnectivity(base, centralName, iface string) string {
	return StatusTopic(base, centralItem(centralName, "hub", "connectivity", TopicSafe(iface))...)
}

// MQTTSystemStatus returns the per-central system-status event item
// `<base>/status/<central>/system/status`. Not retained.
func MQTTSystemStatus(base, centralName string) string {
	return StatusTopic(base, centralItem(centralName, "system", "status")...)
}

// MQTTCustomDPInvoke returns the device-scoped custom-DP operation action
// item `<base>/set/<central>/devices/<addr>/cdps/<name>/<op>`.
func MQTTCustomDPInvoke(base, centralName, deviceAddress, name, operation string) string {
	if deviceAddress == "" || name == "" || operation == "" {
		return ""
	}
	return SetTopic(base, centralItem(centralName, "devices", TopicSafe(deviceAddress), "cdps",
		TopicSafe(name), TopicSafe(operation))...)
}
