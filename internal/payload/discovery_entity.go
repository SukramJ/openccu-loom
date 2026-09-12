// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import (
	"strconv"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"
	hatopic "github.com/SukramJ/go-hamqtt/topic"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// HADiscoveryEntityBuilder is the contract every custom data point
// implements to reach Home Assistant: it describes itself on the shared
// model — a [hamodel.Basic] with a description, bindings and the platform's
// own vocabulary — and the bridge renders that through
// [hadiscovery.RenderComponent].
//
// It replaces [HADiscoveryComponentBuilder] on the channel-aggregate plane.
// The predecessor returned the finished component and the bridge stamped the
// frame (unique_id, availability, device, origin, name) onto it afterwards,
// which meant every one of those five keys was spelled by hand at a point
// where nothing could check it. Home Assistant's discovery schemas are
// extra=REMOVE_EXTRA, so a key it does not declare is dropped with no error
// on the wire and no line in any log; and it keys the entity registry on
// unique_id and the device registry on identifiers, neither of which has a
// migration path. Rendering the frame from a [hamodel.Device] and a
// [hamodel.Description] is what puts those keys under the model's control
// instead of the builder's.
//
// A builder returns nil to decline — a data point with no Home Assistant
// surface at all. It must be a literal nil and not a typed nil pointer, or
// the bridge sees a non-nil interface holding nothing.
//
// The returned entity carries no topics. It names [hamodel.Slot]
// coordinates, and the bridge's own layout renders them, which is what keeps
// the topic schema the bridge's business: a model that formatted its own
// topics would have to know the central, the interface and the tree's root.
type HADiscoveryEntityBuilder interface {
	HADiscoveryEntity() hamodel.Entity
}

// CustomEntity is a custom data point on the shared model: a [hamodel.Basic]
// plus the Home Assistant vocabulary the model deliberately does not carry.
//
// Everything that says what an entity *is* — its name, device class, unit,
// bounds, category, `optimistic`, the json-attributes pair, an explicit
// value template — lives on [hamodel.Description] and is projected by the
// render pipeline onto the platforms whose schema declares it. What is left
// here is one platform's own key set, which is exactly the case
// [hadiscovery.Builder] exists for.
//
// A data point whose platform keys need a rendered topic — a climate's
// `mode_command_topic`, a cover's `set_position_topic` — embeds this in a
// type of its own and implements [hadiscovery.Builder] there, because only
// the render context can resolve a topic. One whose keys are static sets
// [CustomEntity.Fields] and needs no type of its own.
type CustomEntity struct {
	hamodel.Basic

	// Fields is the platform's typed key struct — [hadiscovery.SwitchFields]
	// and friends. Nil for a data point that declares no platform key, or one
	// that fills them in its own [hadiscovery.Builder].
	Fields any
}

// BuildDiscovery implements [hadiscovery.Builder] for the static case.
//
// A type embedding [CustomEntity] and declaring its own BuildDiscovery
// shadows this one; that is the intent, and such a type sets `comp.Fields`
// itself rather than through the embedded field.
func (e *CustomEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	if e.Fields != nil {
		comp.Fields = e.Fields
	}
	return nil
}

// MethodTopic is where Home Assistant writes to invoke one named action on
// this data point (ADR 0009's `…/custom/<kind>/set/<method>`).
//
// A named action is not a write to a datapoint — a cover's stop, a siren's
// turn_on, a lock's short-time open — and pointing Home Assistant at one of
// the parameters such an action happens to touch makes every other payload on
// that topic write nonsense to it. The shared context owns the spelling; this
// is the one call that reaches it from inside a builder.
//
// The device argument is nil on purpose: the bridge's layout resolves a
// custom-DP coordinate from the channel it is rendering, not from the device
// identity, and a device block that names a sub-device would otherwise move
// the topic onto a tree nothing subscribes to.
func (e *CustomEntity) MethodTopic(ctx hadiscovery.Context, method string) string {
	topic := ctx.MethodTopic(nil, e, method)
	// A context with no layout behind it renders no base, leaving the method
	// segment standing alone — which is not a topic but a fragment that would
	// publish the entity's commands at the broker root, where nothing
	// subscribes and anything might. Say there is none instead.
	if topic == "/"+method {
		return ""
	}
	return topic
}

// CustomSlot is the coordinate of a custom data point's own aggregate — the
// curated, model-computed document published at `…/<ch>/custom/<kind>`,
// which is the canonical read surface for every field Home Assistant cannot
// take from a single wire parameter.
func CustomSlot(s TopicSlot) hamodel.Slot {
	channel := ""
	if s.Channel != 0 {
		channel = strconv.Itoa(s.Channel)
	}
	return hamodel.Slot{
		Address: s.Address,
		Channel: channel,
		Bucket:  hamodel.BucketCustom,
		Path:    []string{s.Parameter},
	}
}

// WireSlot is the coordinate of a VALUES-bucket wire parameter on the custom
// data point's own channel.
//
// Address and channel are left empty rather than filled in from the data
// point: the bridge renders this plane for one channel event, and that event
// is what named the channel in every topic already retained on the broker.
// Naming it a second time from the model would be a second derivation of the
// same fact with nothing keeping the two in step.
func WireSlot(parameter string) hamodel.Slot {
	return hamodel.Slot{Bucket: hamodel.BucketValues, Path: []string{parameter}}
}

// WireSlotOn is [WireSlot] for a parameter that lives on a named channel of
// the device rather than on the data point's own.
//
// A custom data point may compose a field from a sibling channel — the
// classic HM-CC-TC keeps its setpoint on the regulator channel while the
// thermostat is materialised on the weather channel. The per-parameter state
// is published under the channel the parameter actually lives on, so a
// payload naming its own channel would point at a topic nothing ever writes.
//
// An address with no parsable channel suffix falls back to [WireSlot], which
// is the channel the bridge is rendering.
func WireSlotOn(channelAddress, parameter string) hamodel.Slot {
	address, channel, ok := hmtypes.SplitChannelAddress(channelAddress)
	if !ok {
		return WireSlot(parameter)
	}
	return hamodel.Slot{
		Address: address,
		Channel: strconv.Itoa(channel),
		Bucket:  hamodel.BucketValues,
		Path:    []string{parameter},
	}
}

// HADiscoveryTopics is the per-channel topic vocabulary a custom data
// point's slots resolve against.
//
// The bridge implements it over its own topic builder, so the render
// pipeline produces exactly the strings already retained on the broker
// rather than a second spelling of them. It is the whole of what the model
// needs from the transport, which is why it is six methods and not a
// reference to the bridge.
type HADiscoveryTopics interface {
	// CustomDPStateTopic is the channel's custom-DP aggregate state topic
	// (`…/<ch>/custom/<kind>`).
	CustomDPStateTopic() string
	// CustomDPCommandTopic is the base a service-method topic hangs off; the
	// render context appends `/<method>` to it.
	CustomDPCommandTopic() string
	// WireParameterStateTopic is the per-parameter VALUES state topic. An
	// empty channelAddress means the channel the bridge is rendering.
	WireParameterStateTopic(channelAddress, parameter string) string
	// WireParameterCommandTopic is the matching `/set` topic.
	WireParameterCommandTopic(channelAddress, parameter string) string
	// DeviceAvailabilityTopic is the owning device's reachability topic.
	DeviceAvailabilityTopic() string
	// BridgeStatusTopic is the daemon's own LWT.
	BridgeStatusTopic() string
}

// SlotLayout renders a custom data point's slots through a
// [HADiscoveryTopics].
//
// It is the one place that knows how a [hamodel.Slot] maps onto this
// daemon's topic tree on the channel-aggregate plane: the custom bucket is
// the aggregate, every other bucket is the wire parameter its path names.
type SlotLayout struct {
	// Topics resolves the strings. A nil Topics renders every topic empty,
	// which is what a caller with no transport wants and is never published.
	Topics HADiscoveryTopics
}

var _ hatopic.Layout = SlotLayout{}

// State implements [hatopic.Layout].
func (l SlotLayout) State(s hamodel.Slot) string {
	if l.Topics == nil {
		return ""
	}
	if s.Bucket == hamodel.BucketCustom {
		return l.Topics.CustomDPStateTopic()
	}
	return l.Topics.WireParameterStateTopic(slotChannelAddress(s), s.Leaf())
}

// Command implements [hatopic.Layout].
func (l SlotLayout) Command(s hamodel.Slot) string {
	if l.Topics == nil {
		return ""
	}
	if s.Bucket == hamodel.BucketCustom {
		return l.Topics.CustomDPCommandTopic()
	}
	return l.Topics.WireParameterCommandTopic(slotChannelAddress(s), s.Leaf())
}

// Availability implements [hatopic.Layout]. Every entity on this plane is
// gated on the one device the channel belongs to, so the slot is unused.
func (l SlotLayout) Availability(hamodel.Slot) string {
	if l.Topics == nil {
		return ""
	}
	return l.Topics.DeviceAvailabilityTopic()
}

// Bridge implements [hatopic.Layout].
func (l SlotLayout) Bridge() string {
	if l.Topics == nil {
		return ""
	}
	return l.Topics.BridgeStatusTopic()
}

// slotChannelAddress renders a slot's `<device>:<channel>` address, or the
// empty string when the slot names neither — which is what a slot built by
// [WireSlot] does, and means "the channel the bridge is rendering".
func slotChannelAddress(s hamodel.Slot) string {
	if s.Address == "" || s.Channel == "" {
		return ""
	}
	return s.Address + ":" + s.Channel
}
