// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Path-root constants. They are the canonical first segment of every
// set/state path; north-bound adapters (REST, MQTT, WS) use them to route
// requests back to the appropriate handler family without having to inspect
// the trailing segments.
const (
	// SetPathRoot is the first segment for channel-bound write paths.
	SetPathRoot = "device/set"
	// StatePathRoot is the first segment for channel-bound read paths.
	StatePathRoot = "device/status"
	// VirtDevSetPathRoot replaces SetPathRoot for the VirtualDevices / CCU-Jack
	// interface family.
	VirtDevSetPathRoot = "virtdev/set"
	// VirtDevStatePathRoot is the read-side counterpart.
	VirtDevStatePathRoot = "virtdev/status"
	// ProgramSetPathRoot is the first segment for CCU-program DPs.
	ProgramSetPathRoot = "program/set"
	// ProgramStatePathRoot is the read-side counterpart.
	ProgramStatePathRoot = "program/status"
	// SysvarSetPathRoot is the first segment for system-variable DPs.
	SysvarSetPathRoot = "sysvar/set"
	// SysvarStatePathRoot is the read-side counterpart.
	SysvarStatePathRoot = "sysvar/status"
)

// Bucket identifies the paramset family a data point belongs to. The
// bucket is the disambiguator between MASTER (config) and VALUES
// (runtime state) parameters that share the same wire name on the
// same channel — a real conflict on the CCU XML-RPC API and on the
// north-bound MQTT topology where the older bucket-less shape
// `<addr>/<ch>/<param>` could not distinguish the two.
//
// String values match the [internal/payload.Bucket] enum verbatim so
// neither layer needs translation.
type Bucket string

// Bucket constants.
const (
	// BucketUnset is the zero value — used by Hub/Program/Sysvar path
	// data points that do not live on a channel and therefore have no
	// paramset context.
	BucketUnset Bucket = ""
	// BucketValues is the runtime VALUES paramset.
	BucketValues Bucket = "values"
	// BucketMaster is the operator-tunable MASTER paramset.
	BucketMaster Bucket = "master"
	// BucketCalculated is the synthetic / calculated DP family.
	BucketCalculated Bucket = "calculated"
	// BucketCustom is the custom-DP aggregate (climate, lock, cover,
	// …) — the model-level source-of-truth slot.
	BucketCustom Bucket = "custom"
)

// PathData is the model-layer descriptor for a single data point's
// canonical paths. It is the single source of truth across the
// north-bound adapters (MQTT, REST, WS):
//
//   - logical strings (`SetPath`, `StatePath`)
//     are pre-computed for parity with the Python reference.
//   - The structured fields (Interface, Address, ChannelNo, Bucket,
//     Kind) let an adapter compose its own native shape — e.g. the
//     MQTT bridge derives its bucket-aware topic
//     `<base>/<central>/<iface>/<addr>/<ch>/<bucket>/<param>` via
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
	Interface hmenum.Interface
	Address   string // upper-cased CCU device address ("" for non-channel families)
	ChannelNo int
	Bucket    Bucket
	// Kind is the wire-parameter name (VALUES / MASTER) or the
	// custom-DP type label ("CLIMATE", "LIGHT", …) for BucketCustom
	// DPs. For non-channel families it is the program/sysvar id /
	// hub-DP name.
	Kind string
}

// EmptyPathData is the zero-value sentinel returned when path
// computation is not possible (missing address, empty parameter).
var EmptyPathData = PathData{}

// IsZero reports whether the path data is empty.
func (p PathData) IsZero() bool { return p.SetPath == "" && p.StatePath == "" }

// MQTTState returns the canonical MQTT state-topic for the data
// point: `<base>/<central>/<iface>/<addr>/<ch>/<bucket>/<kind>`.
// Empty on non-channel-bound DPs (programs, sysvars, hub) — those
// have their own bridge-side topic builders.
//
// `central` and `base` are bridge-side scoping that does not live on
// the model; the model declares the shape, the bridge prepends its
// runtime context.
//
// All inputs are MQTT-safe-escaped via [TopicSafe]. Wire parameters
// are upper-case by convention; bucket labels are lower-case.
func (p PathData) MQTTState(base, centralName string) string {
	if p.Address == "" || p.Kind == "" || p.Bucket == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/%s/%s",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
		TopicSafe(string(p.Bucket)),
		TopicSafe(p.Kind),
	)
}

// MQTTCommand returns the matching `/set` topic for a writable data
// point. Empty for non-channel-bound DPs.
func (p PathData) MQTTCommand(base, centralName string) string {
	state := p.MQTTState(base, centralName)
	if state == "" {
		return ""
	}
	return state + "/set"
}

// MQTTConfig returns the descriptor-companion `/config` topic.
func (p PathData) MQTTConfig(base, centralName string) string {
	state := p.MQTTState(base, centralName)
	if state == "" {
		return ""
	}
	return state + "/config"
}

// MQTTChannelAggregateState returns the channel-rollup retained
// state topic — `<base>/<central>/<iface>/<addr>/<ch>/state`. Used
// by the bridge for the custom-DP-aggregated payload (climate / lock
// / cover / valve / siren). Uses only Address + ChannelNo +
// Interface; Bucket and Kind are ignored. Empty when Address is
// missing.
func (p PathData) MQTTChannelAggregateState(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/state",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
	)
}

// MQTTChannelEvent returns the per-channel event-aggregate topic
// `<base>/<central>/<iface>/<addr>/<ch>/event`. Non-retained;
// carries the multi-press JSON `{"event_type": ...}` payload.
func (p PathData) MQTTChannelEvent(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/event",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
	)
}

// MQTTDataPointEvent returns the legacy per-event-type pulse topic
// `<base>/<central>/<iface>/<addr>/<ch>/event/<etype>`.
func (p PathData) MQTTDataPointEvent(base, centralName, etype string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/event/%s",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
		TopicSafe(etype),
	)
}

// MQTTDeviceAvailability returns the per-device retained
// availability topic `<base>/<central>/<iface>/<addr>/availability`.
// Uses only Address + Interface; ChannelNo / Bucket / Kind are
// ignored. Empty when Address is missing.
func (p PathData) MQTTDeviceAvailability(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/availability",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
	)
}

// MQTTDeviceInfo returns the per-device retained info-snapshot topic
// `<base>/<central>/<iface>/<addr>/info`.
func (p PathData) MQTTDeviceInfo(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/info",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
	)
}

// MQTTDeviceDiagnostics returns the per-device retained diagnostics
// topic `<base>/<central>/<iface>/<addr>/diagnostics`.
func (p PathData) MQTTDeviceDiagnostics(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/diagnostics",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
	)
}

// MQTTDeviceUpdateState returns the HA `update`-entity retained
// state topic `<base>/<central>/<iface>/<addr>/update`. Follows the
// device-scope convention (`/availability`, `/info`, `/diagnostics`)
// — no `/state` suffix because the topic IS the state.
func (p PathData) MQTTDeviceUpdateState(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/update",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
	)
}

// MQTTDeviceUpdateCommand builds the install-command topic
// `<base>/<central>/<iface>/<addr>/update/set`, following the `/set`
// convention used for inbound writes.
//
// Nothing subscribes to it. The command subscriber's wildcards are all
// deeper or shallower than this shape, so the topic has never been
// inbound, and the device update entity is declared read-only for the
// separate reason that flashing firmware from an unconfirmed — possibly
// retained and replayed — broker payload is unsafe. Kept as the
// canonical spelling should the command path ever be wired; the doc
// comment used to call it "subscribed", which no test contradicted.
func (p PathData) MQTTDeviceUpdateCommand(base, centralName string) string {
	state := p.MQTTDeviceUpdateState(base, centralName)
	if state == "" {
		return ""
	}
	return state + "/set"
}

// MQTTWeekProfileState returns the week-profile select-entity state
// topic `<base>/<central>/<iface>/<addr>/<ch>/week_profile/state`.
func (p PathData) MQTTWeekProfileState(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/week_profile/state",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
	)
}

// MQTTWeekProfileCommand returns the subscribed `set` topic
// `<base>/<central>/<iface>/<addr>/<ch>/week_profile/set`.
func (p PathData) MQTTWeekProfileCommand(base, centralName string) string {
	if p.Address == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/week_profile/set",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
	)
}

// MQTTCustomDPState returns the custom-DP slot state topic
// `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>` for the
// climate / cover / lock / light / siren / valve / textdisplay
// aggregate. The PathData must have been constructed with
// [BucketCustom] and `Kind` set to the lowercase domain label
// (e.g. "climate"). Empty when Bucket != BucketCustom or Address /
// Kind is missing.
func (p PathData) MQTTCustomDPState(base, centralName string) string {
	if p.Address == "" || p.Kind == "" || p.Bucket != BucketCustom {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/%s/%s/%d/custom/%s",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(string(p.Interface)),
		TopicSafe(p.Address),
		p.ChannelNo,
		TopicSafe(strings.ToLower(p.Kind)),
	)
}

// MQTTCustomDPConfig is the descriptor companion `/config` topic
// for the custom-DP slot.
func (p PathData) MQTTCustomDPConfig(base, centralName string) string {
	state := p.MQTTCustomDPState(base, centralName)
	if state == "" {
		return ""
	}
	return state + "/config"
}

// MQTTCustomDPServiceMethod returns the per-method command topic
// `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>`
// for a custom-DP service-method invocation (open, close, set_mode,
// turn_on, …). Mirrors ADR 0011's `…/custom/<kind>/set/<method>`
// shape. Empty when the PathData is not a custom-DP slot.
func (p PathData) MQTTCustomDPServiceMethod(base, centralName, method string) string {
	state := p.MQTTCustomDPState(base, centralName)
	if state == "" {
		return ""
	}
	return state + "/set/" + TopicSafe(method)
}

// DiscoveryNodeID returns the HA-Discovery `<node_id>` segment that
// groups every entity belonging to one physical device. Format:
// `<central-lower>_<address-lower>` — matching HA's convention that
// `node_id` distinguishes one device from another, not one
// integration from another. When `central` is empty the node id
// collapses to just the lower-cased address.
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
	return strings.ToLower(TopicSafe(centralName)) + "_" + addr
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

// DiscoveryUniqueID returns the cross-broker-stable `unique_id`
// payload field. Format:
// `<daemonPrefix>_<central-lower>_<address-lower>_<channel>_<suffix-lower>`.
// HA persists the value in its registry; changing the format
// orphans every entity HA already knows about, so the format is
// pinned via this method.
//
// `daemonPrefix` is the daemon identity (typically `"openccu-loom"`)
// — kept as a parameter so multi-daemon test setups can produce
// per-daemon unique-ids without collision.
//
// Empty when Address is missing or the suffix is empty.
func (p PathData) DiscoveryUniqueID(daemonPrefix, centralName, suffix string) string {
	if p.Address == "" || suffix == "" {
		return ""
	}
	addr := strings.ToLower(p.Address)
	suf := strings.ToLower(TopicSafe(suffix))
	prefix := strings.ToLower(TopicSafe(daemonPrefix))
	if prefix == "" {
		prefix = "openccu-loom"
	}
	if centralName == "" {
		return fmt.Sprintf("%s_%s_%d_%s", prefix, addr, p.ChannelNo, suf)
	}
	return fmt.Sprintf(
		"%s_%s_%s_%d_%s",
		prefix, strings.ToLower(TopicSafe(centralName)), addr, p.ChannelNo, suf,
	)
}

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
		"homeassistant/%s/%s/%s/config",
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
func NewChannelPathData(iface hmenum.Interface, address string, channelNo int) PathData {
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
func NewDevicePathData(iface hmenum.Interface, address string) PathData {
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
// Bucket is forced to [BucketCustom] so [MQTTCustomDPState] etc. can
// guard against accidental misuse with a generic VALUES bucket.
//
// SetPath and StatePath stay empty — custom-DP slots are an
// MQTT-bridge concept and don't carry an logical
// path.
func NewCustomDPPathData(iface hmenum.Interface, address string, channelNo int, kind string) PathData {
	if address == "" || kind == "" {
		return EmptyPathData
	}
	return PathData{
		Interface: iface,
		Address:   strings.ToUpper(address),
		ChannelNo: channelNo,
		Bucket:    BucketCustom,
		Kind:      strings.ToLower(kind),
	}
}

// TopicSafe replaces characters MQTT disallows in a single topic
// level (`+`, `#`, `/`, space) with underscores. The raw plane uses
// `/` as the separator, so topic components with interior slashes
// would otherwise split the path incorrectly.
//
// Exported because adapter packages (north/mqtt) escape their own
// non-PathData segments (bridge status, hub topics) and the same
// rule must apply consistently.
func TopicSafe(s string) string {
	replacer := strings.NewReplacer("+", "_", "#", "_", "/", "_", " ", "_")
	return replacer.Replace(s)
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
// ("CLIMATE", "LIGHT") for BucketCustom DPs.
//
// OpenCCU-Loom adds the bucket segment to the
// the same address+channel+kind combination on different paramsets
// no longer aliases — a real conflict for parameters that exist in
// both VALUES and MASTER on the same channel.
func NewDataPointPathData(iface hmenum.Interface, address string, channelNo int, bucket Bucket, kind string) PathData {
	if address == "" || kind == "" {
		return EmptyPathData
	}
	if bucket == "" {
		// Default to VALUES — preserves the historic single-bucket
		// behaviour for callers that have not yet been migrated.
		bucket = BucketValues
	}
	upperAddr := strings.ToUpper(address)
	upperKind := strings.ToUpper(kind)

	var sb strings.Builder
	sb.Grow(len(upperAddr) + len(upperKind) + len(bucket) + 8)
	sb.WriteString(upperAddr)
	sb.WriteByte('/')
	sb.WriteString(strconv.Itoa(channelNo))
	sb.WriteByte('/')
	sb.WriteString(string(bucket))
	sb.WriteByte('/')
	sb.WriteString(upperKind)
	item := sb.String()

	setRoot := SetPathRoot
	stateRoot := StatePathRoot
	if iface == hmenum.InterfaceVirtualDevices {
		setRoot = VirtDevSetPathRoot
		stateRoot = VirtDevStatePathRoot
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

// NewProgramPathData builds the path data for a CCU-program data point.
func NewProgramPathData(pid string) PathData {
	if pid == "" {
		return EmptyPathData
	}
	return PathData{
		SetPath:   ProgramSetPathRoot + "/" + pid,
		StatePath: ProgramStatePathRoot + "/" + pid,
		Kind:      pid,
	}
}

// NewSysvarPathData builds the path data for a CCU system-variable data
// point.
func NewSysvarPathData(vid string) PathData {
	if vid == "" {
		return EmptyPathData
	}
	return PathData{
		SetPath:   SysvarSetPathRoot + "/" + vid,
		StatePath: SysvarStatePathRoot + "/" + vid,
		Kind:      vid,
	}
}

// --- ADR 0011 hub-topic free functions -------------------------------

// MQTTHubStatus returns the retained CCU connection-state topic
// `<base>/<central>/hub/status`.
func MQTTHubStatus(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/status", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubInfo returns the retained CCU info-snapshot topic
// `<base>/<central>/hub/info`.
func MQTTHubInfo(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/info", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubDiagnostics returns the retained per-CCU diagnostics topic
// `<base>/<central>/hub/diagnostics`.
func MQTTHubDiagnostics(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/diagnostics", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubSysvarState returns the canonical ADR-0011 sysvar state
// topic `<base>/<central>/hub/sysvars/<name>/state`.
func MQTTHubSysvarState(base, centralName, name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/sysvars/%s/state",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(name),
	)
}

// MQTTHubSysvarCommand returns the matching `set` topic.
func MQTTHubSysvarCommand(base, centralName, name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/sysvars/%s/set",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(name),
	)
}

// MQTTHubProgramTrigger returns the canonical ADR-0011 program-
// trigger topic `<base>/<central>/hub/programs/<id>/trigger`.
func MQTTHubProgramTrigger(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/programs/%s/trigger",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(id),
	)
}

// MQTTHubProgramSet returns the program-activation command topic
// `<base>/<central>/hub/programs/<id>/set`.
//
// Distinct from the trigger topic: `set` decides whether the CCU lets the
// program run at all, `trigger` runs it once. They are two controls
// because the CCU treats them as two things — a deactivated program
// ignores a trigger.
func MQTTHubProgramSet(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/programs/%s/set",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(id),
	)
}

// MQTTHubProgramExecuteAvailability returns the availability topic for a
// program's execute action: `<base>/<central>/hub/programs/<id>/execute_available`.
// The daemon publishes online/offline there so a consumer greys out the
// execute control while the program is deactivated, without having to
// derive that rule itself.
func MQTTHubProgramExecuteAvailability(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/programs/%s/execute_available",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(id),
	)
}

// MQTTHubProgramState returns the canonical ADR-0011 program-
// state topic `<base>/<central>/hub/programs/<id>/state`. Mirrors
// the sysvar `state`/`set` symmetry — HA's `switch` entity for a
// program needs a retained state topic to render its on/off pip.
func MQTTHubProgramState(base, centralName, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/hub/programs/%s/state",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(id),
	)
}

// MQTTHubInstallMode is the canonical install-mode countdown topic
// `<base>/<central>/hub/install_mode`. The legacy form lives at
// `<base>/<central>/install_mode` and is gated by LegacyAliasConfig.
func MQTTHubInstallMode(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/install_mode", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubInstallModeForInterface is the per-interface install-mode
// countdown state topic `<base>/<central>/hub/install_mode/<iface>`.
// The reference stack exposes one remaining-seconds sensor per
// interface (HmIP-RF, BidCos-RF) rather than a single central-wide
// aggregate, so each interface carries its own retained state topic.
func MQTTHubInstallModeForInterface(base, centralName, iface string) string {
	return fmt.Sprintf(
		"%s/%s/hub/install_mode/%s",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(iface),
	)
}

// MQTTHubInstallModeCommand is the per-interface install-mode activation
// command topic `<base>/<central>/hub/install_mode/<iface>/set`. The
// reference stack pairs each per-interface remaining-seconds sensor with
// a button that activates pairing on that interface; HA publishes
// "PRESS" here and the command subscriber translates it into a
// POST install-mode for the named interface.
func MQTTHubInstallModeCommand(base, centralName, iface string) string {
	return fmt.Sprintf(
		"%s/%s/hub/install_mode/%s/set",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(iface),
	)
}

// MQTTHubAlarmMessages is the canonical alarm-messages topic
// `<base>/<central>/hub/alarm_messages`.
func MQTTHubAlarmMessages(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/alarm_messages", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubServiceMessages is the canonical service-messages topic
// `<base>/<central>/hub/service_messages`.
func MQTTHubServiceMessages(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/service_messages", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubInbox is the canonical inbox topic
// `<base>/<central>/hub/inbox`.
func MQTTHubInbox(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/inbox", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubUpdate is the canonical hub firmware-update state topic
// `<base>/<central>/hub/update`.
func MQTTHubUpdate(base, centralName string) string {
	return fmt.Sprintf("%s/%s/hub/update", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTHubConnectivity is the canonical per-interface connectivity
// topic `<base>/<central>/hub/connectivity/<iface>`.
func MQTTHubConnectivity(base, centralName, iface string) string {
	return fmt.Sprintf(
		"%s/%s/hub/connectivity/%s",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(iface),
	)
}

// MQTTSystemStatus returns the non-retained per-central
// system-status event topic `<base>/<central>/system/status`.
func MQTTSystemStatus(base, centralName string) string {
	return fmt.Sprintf("%s/%s/system/status", strings.Trim(base, "/"), TopicSafe(centralName))
}

// MQTTCustomDPInvoke returns the device-scoped custom-DP invocation
// topic `<base>/<central>/devices/<addr>/cdps/<name>/<op>/invoke`.
func MQTTCustomDPInvoke(base, centralName, deviceAddress, name, operation string) string {
	if deviceAddress == "" || name == "" || operation == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/devices/%s/cdps/%s/%s/invoke",
		strings.Trim(base, "/"),
		TopicSafe(centralName),
		TopicSafe(deviceAddress),
		TopicSafe(name),
		TopicSafe(operation),
	)
}
