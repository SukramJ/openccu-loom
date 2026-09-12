// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// textDisplayNotifyKey is the entity key of the notify entity a text-display
// custom-DP produces, and the suffix this daemon's object id and unique id
// are derived from. It is a model-side name, not a topic segment: the two
// identity strings are answered by [notifyDiscoveryContext] from this
// daemon's own spellings.
const textDisplayNotifyKey = "notify"

// textDisplayWriteMethod is the custom-DP service method the notify entity
// addresses. It is a method rather than a datapoint write: the display's
// `write` service takes a row number and a string, which no single parameter
// topic could carry.
const textDisplayWriteMethod = "write"

// textDisplayCommandTemplate wraps the raw message Home Assistant's notify
// component publishes into the payload the custom-DP's `write` service method
// expects (display row 1). tojson quotes and escapes the message safely.
const textDisplayCommandTemplate = `{"id": 1, "text": {{ value | tojson }}}`

// textDisplaySource is the narrow read-side contract used to recognise a
// text-display custom-DP (HmIP-WRCD) on the discovery path. The
// custom-DP reports DataPointCategoryTextDisplay via its Category method.
type textDisplaySource interface {
	Category() hmenum.DataPointCategory
}

// isTextDisplayEvent reports whether ev carries a text-display custom-DP
// as its Source.
func isTextDisplayEvent(ev Event) bool {
	if ev.Source == nil {
		return false
	}
	tds, ok := ev.Source.(textDisplaySource)
	return ok && tds.Category() == hmenum.DataPointCategoryTextDisplay
}

// textDisplayNotifyEntity is the text-display notify entity on the shared
// model: a [hamodel.Basic] with a description and no bindings at all.
//
// It binds no datapoint because it writes none. The display is driven by the
// custom-DP's `write` service method, which the entity declares as a
// [hamodel.Invoker]; a single method is unambiguous, so the render pipeline
// takes it as the command topic without a builder having to say which.
type textDisplayNotifyEntity struct {
	hamodel.Basic
}

// Methods implements [hamodel.Invoker].
func (e *textDisplayNotifyEntity) Methods() []string {
	return []string{textDisplayWriteMethod}
}

// BuildDiscovery implements [hadiscovery.Builder] for the two keys the shared
// model does not carry itself.
//
// `command_template` is notify's own vocabulary — the model describes what an
// entity is, not how a platform spells the payload it accepts — which is the
// designed use of a builder.
//
// `name: null` is the second, and it is not vocabulary. It is the difference
// between "this entity has no name of its own, render the device's" and
// "derive one from the platform", which Home Assistant reads differently; the
// description carries a name but has no way to ask for the null, so the
// component field is set here. An empty name reaching the payload as `""`
// instead of `null` re-seeds the entity id of every display in the fleet.
func (e *textDisplayNotifyEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.CommandTemplate = textDisplayCommandTemplate
	comp.NameNull = comp.Name == ""
	return nil
}

// notifyTopicLayout renders this plane's topics through this daemon's own
// [TopicBuilder], so the shared model's pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused: the only topics this plane needs are
// device-level ones the event already pins, and the notify entity binds no
// datapoint whose coordinate could say anything more.
type notifyTopicLayout struct {
	d  *DefaultDiscoveryBuilder
	ev Event
}

// State implements the shared model's topic layout. A notify entity publishes
// no state, and Home Assistant's notify platform declares no `state_topic`.
func (l notifyTopicLayout) State(hamodel.Slot) string { return "" }

// Command implements the shared model's topic layout. Every inbound
// instruction this entity takes arrives on the service-method topic, rendered
// by [notifyDiscoveryContext.MethodTopic].
func (l notifyTopicLayout) Command(hamodel.Slot) string { return "" }

// Availability implements the shared model's topic layout.
func (l notifyTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.d.centralFor(l.ev), l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l notifyTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// notifyDiscoveryContext is the render context for the notify plane: the
// standard one, with this daemon's three identity strings substituted.
//
// All three are overridden because Home Assistant has no migration path for
// any of them — the unique id keys the entity, the node id is a topic
// segment, the object id seeds the entity id — and this daemon's fleet is
// already published under its own spellings.
type notifyDiscoveryContext struct {
	hadiscovery.StdContext

	d        *DefaultDiscoveryBuilder
	ev       Event
	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes, central scoping and all.
func (c notifyDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. It is central-scoped and derived
// from the PARENT device address even where the device block identifies a
// sub-device: only half the identity moves on a sub-device split, and
// deriving the node id from the device identity instead would re-home every
// such entity onto a topic nothing listens on.
func (c notifyDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This daemon has never published an
// entity-id seed; adding one now would rename every existing entity, and
// nothing downstream could undo it.
func (c notifyDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// MethodTopic implements [hadiscovery.Context] with the custom-DP
// service-method topic, which is where the display's `write` service listens.
func (c notifyDiscoveryContext) MethodTopic(_ *hamodel.Device, _ hamodel.Entity, method string) string {
	return c.d.discoveryContext(c.ev).ServiceMethodCommandTopic(method)
}

// notifyModelDevice lifts the device descriptor this daemon harvests into the
// shared model's [hamodel.Device], so the render pipeline emits the device
// block instead of a builder stamping one on afterwards.
//
// The identifiers keep an EMPTY namespace, which the shared model renders
// verbatim. That is what lets the published `openccu-loom_<serial>` and
// `openccu-loom_central_<central>` spellings survive: Home Assistant keys its
// device registry on those strings and has no migration path for them either.
func notifyModelDevice(info *hadiscovery.DeviceInfo) *hamodel.Device {
	if info == nil {
		return nil
	}
	dev := &hamodel.Device{
		Name:          hamodel.L(info.Name),
		Manufacturer:  info.Manufacturer,
		Model:         info.Model,
		ModelID:       info.ModelID,
		SWVersion:     info.SWVersion,
		HWVersion:     info.HWVersion,
		SerialNumber:  info.SerialNumber,
		SuggestedArea: info.SuggestedArea,
		ConfigURL:     info.ConfigurationURL,
	}
	for _, id := range info.Identifiers {
		dev.Identity.IDs = append(dev.Identity.IDs, hamodel.Identifier{Value: id})
	}
	for _, conn := range info.Connections {
		dev.Identity.Connections = append(dev.Identity.Connections,
			hamodel.Connection{Type: conn[0], Value: conn[1]})
	}
	if info.ViaDevice != "" {
		dev.Via = &hamodel.Identity{IDs: []hamodel.Identifier{{Value: info.ViaDevice}}}
	}
	return dev
}

// BuildTextDisplayNotify emits the HA `notify` discovery payload for a
// text-display custom-DP (HmIP-WRCD). This is the SOLE entity the
// reference stack creates for a TEXT_DISPLAY custom-DP.
//
// The reference stack maps a TEXT_DISPLAY custom-DP onto a `notify`
// entity only (the integration's notify.py spawns one
// HmipTextDisplayNotifyEntity per CustomDpTextDisplay; no `text` entity
// is registered). The notify surface lets HA automations and the notify
// service push a message to the display. The aggregate `text` entity is
// suppressed in [DefaultDiscoveryBuilder.aggregateChannel].
//
// HA's notify component publishes the raw message string to the command
// topic. The `command_template` wraps it into the `{"id":1,"text":...}`
// payload the custom-DP's `write` service method expects (display row 1).
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`. The entity name mirrors the text entity
// (empty → `name: null`, so HA renders the display name alone) and the
// availability list is the description's default pair — bridge and device —
// rendered through this daemon's own topic builder.
//
// Returns the zero DiscoveryItem (OK=false) when ev does not carry a
// text-display custom-DP.
func (d *DefaultDiscoveryBuilder) BuildTextDisplayNotify(ev Event) DiscoveryItem {
	if !isTextDisplayEvent(ev) {
		return DiscoveryItem{}
	}
	objectID := d.channelObjectID(ev, textDisplayNotifyKey)
	uniqueID, scoped := d.channelUniqueID(ev, textDisplayNotifyKey)
	if !scoped {
		return DiscoveryItem{}
	}
	nodeID := discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)
	if d.discoveryContext(ev).ServiceMethodCommandTopic(textDisplayWriteMethod) == "" {
		return DiscoveryItem{}
	}
	// The same device block the channel's other entities hang off, so the
	// notify entity groups under the same HA device card as the text entity.
	dev := notifyModelDevice(deviceDescriptor(ev, d.hubURLFor(ev), d.SubDevicesEnabled))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &textDisplayNotifyEntity{
		Basic: hamodel.Basic{
			EntityKey:      textDisplayNotifyKey,
			EntityPlatform: hacatalog.PlatformNotify,
			Description: hamodel.Description{
				Name: hamodel.L(displayChannelName(ev)),
			},
		},
	}
	ctx := notifyDiscoveryContext{
		StdContext: hadiscovery.StdContext{Layout: notifyTopicLayout{d: d, ev: ev}},
		d:          d,
		ev:         ev,
		uniqueID:   uniqueID,
		nodeID:     nodeID,
	}
	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	buf, err := json.Marshal(comp)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentNotify), NodeID: nodeID, ObjectID: objectID, Payload: buf, OK: true}
}
