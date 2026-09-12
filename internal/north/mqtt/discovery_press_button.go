// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"fmt"
	"strconv"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// pressButtonPayload is what Home Assistant publishes when the button is
// pressed. The command subscriber coerces it to the boolean `true` an ACTION
// parameter expects.
const pressButtonPayload = "PRESS"

// isPressButtonEvent reports whether ev is a click-event parameter the
// model classified as a clickable button entity: category=button AND
// usage=data_point. The reference stack's two-object model spawns BOTH a
// button (DpButton) and a keypress event for a writable press; the daemon
// carries one data point and signals the button surface through this
// category/usage pair. A writable press (a virtual-remote action, a plain
// KEY channel's PRESS_SHORT/PRESS_LONG, or an additional_data_points-
// promoted dimmer-input press) resolves to data_point; an event-only press
// (every KEY_TRANSCEIVER / MULTI_MODE_INPUT_TRANSMITTER transmitter, the
// central/long-press parameters) resolves to event and gets no button.
//
// The companion button is published IN ADDITION to the per-channel keypress
// `event` entity (which the regular [Build] press path emits): pressing the
// button from HA writes the action to the CCU, exactly as the keypress event
// observes a physical press.
func isPressButtonEvent(ev Event) bool {
	return ev.Category == hmenum.DataPointCategoryButton &&
		ev.Usage == hmenum.DataPointUsageDataPoint
}

// pressButtonEntity is the press button on the shared model: a
// [hamodel.Basic] with a description and one writable binding.
//
// The binding is what makes the render pipeline project `command_topic`
// itself. The button reads nothing, so it declares no state role — Home
// Assistant's button platform has no `state_topic` either.
type pressButtonEntity struct {
	hamodel.Basic
}

// BuildDiscovery implements [hadiscovery.Builder] for `payload_press`.
//
// That key is platform button's own vocabulary — the model describes what an
// entity is, not which string a platform accepts as its trigger — so it
// belongs in the platform's typed [hadiscovery.ButtonFields] rather than in
// the model. This is the designed use of a builder, not a correction of
// something the pipeline did.
func (e *pressButtonEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.Fields = hadiscovery.ButtonFields{PayloadPress: pressButtonPayload}
	return nil
}

// pressButtonTopicLayout renders this plane's topics through this daemon's
// own [TopicBuilder] and [naming.PathData], so the render pipeline produces
// exactly the strings already retained on the broker rather than a second
// spelling of them.
//
// The slot arguments are unused. The event pins a single data point, and its
// path data is the authority on how this daemon spells that data point's
// command and availability topics; deriving them from the slot again would
// be a second implementation of the same schema with nothing keeping the two
// in step.
type pressButtonTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	ev      Event
	pd      naming.PathData
	central string
}

// State implements the shared model's topic layout. A button publishes no
// state and Home Assistant's button platform declares no `state_topic`.
func (l pressButtonTopicLayout) State(hamodel.Slot) string { return "" }

// Command implements the shared model's topic layout: the press parameter's
// per-data-point command topic.
func (l pressButtonTopicLayout) Command(hamodel.Slot) string {
	return l.pd.MQTTCommand(l.d.TopicBuilder.Base, l.central)
}

// Availability implements the shared model's topic layout.
func (l pressButtonTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l pressButtonTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// pressButtonDiscoveryContext is the render context for the press-button
// plane: the standard one with this daemon's three identity strings
// substituted.
//
// All three are overridden because Home Assistant has no migration path for
// any of them — the unique id keys the entity, the node id is a topic
// segment, the object id seeds the entity id — and this daemon's fleet is
// already published under its own spellings.
type pressButtonDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes, central scoping and all. A virtual remote's address repeats
// verbatim on every CCU, so its id carries the central's serial; a real
// device's serial is globally unique and must NOT be scoped, or every
// existing button on every fleet is re-keyed at once.
func (c pressButtonDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. It is central-scoped and derived
// from the PARENT device address even where the device block identifies a
// sub-device: only half the identity moves on a sub-device split, and
// deriving the node id from the device identity instead would re-home every
// such button onto a topic nothing listens on.
func (c pressButtonDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This daemon has never published an
// entity-id seed; adding one now would rename every existing entity, and
// nothing downstream could undo it. The object id in the discovery TOPIC is
// a different string and is carried on the [DiscoveryItem].
func (c pressButtonDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// describePressButtonFromRegistry folds this daemon's entity-description
// table into a shared-model [hamodel.Description].
//
// The table is this daemon's catalogue, and the shared model's precedence
// rule puts a catalogue at the description stage — before the projection, so
// the pipeline renders a finished description instead of a builder editing
// the component afterwards. No matching rule leaves the description as it
// was, which is the same "no description-derived attributes" outcome
// [applyEntityDescription] produces on an otherwise untouched component.
func describePressButtonFromRegistry(
	desc *hamodel.Description, component, parameter, model, unit, postfix string,
) {
	rule := HARegistryDescriptionLookup(component, parameter, model, unit, postfix, "")
	if rule == nil {
		return
	}
	desc.DeviceClass = hamodel.DeviceClass(rule.DeviceClass)
	desc.StateClass = hacatalog.StateClass(rule.StateClass)
	desc.Category = hacatalog.EntityCategory(rule.EntityCategory)
	desc.Icon = rule.Icon
	if rule.UnitOfMeasurement != "" {
		// An empty `native_unit_of_measurement` in the rule means HA falls
		// back to the data point's own unit, so it is not an override.
		desc.Unit = hamodel.Unit(rule.UnitOfMeasurement)
	}
	if rule.SuggestedDisplayPrecision != nil {
		desc.Precision = hadiscovery.Ptr(*rule.SuggestedDisplayPrecision)
	}
	if rule.EnabledByDefault != nil {
		desc.Enabled = hadiscovery.Ptr(*rule.EnabledByDefault)
	}
	if len(rule.Options) > 0 {
		desc.Options = &hamodel.Enum{Codes: append([]string(nil), rule.Options...)}
	}
	if rule.TranslationKey != "" {
		// `translation_key` is declared by no Home Assistant platform and is
		// dropped on receipt; the daemon publishes it anyway so the parity
		// tooling can compare against the Python integration. It has no
		// typed home in the shared model for exactly that reason.
		if desc.Extra == nil {
			desc.Extra = map[string]any{}
		}
		desc.Extra["translation_key"] = rule.TranslationKey
	}
}

// BuildPressButton emits the HA `button` discovery payload for one
// click-event parameter the model marked as a button (category=button,
// usage=data_point). The reference stack renders every such press as a
// clickable button (disabled by default) NEXT TO the per-channel keypress
// `event` entity; the regular [Build] press path only produces the event
// entity, so the bridge publishes this companion through
// [Bridge.publishPressButton].
//
// The button's command topic is the press parameter's per-DP command
// topic; HA publishes `payload_press` ("PRESS") which the command
// subscriber coerces to the boolean `true` an ACTION parameter expects.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`. The entity-description rules mirror the
// reference factory defaults: PRESS_SHORT / PRESS_LONG buttons exist but are
// disabled by default (the keypress event entity is the primary surface),
// and the reset buttons additionally land in the `config` entity category.
//
// Returns the zero DiscoveryItem (OK=false) when ev is not a press-button
// parameter.
func (d *DefaultDiscoveryBuilder) BuildPressButton(ev Event) DiscoveryItem {
	if !isPressButtonEvent(ev) {
		return DiscoveryItem{}
	}
	central := d.centralFor(ev)
	pd := naming.NewDataPointPathData(
		central,
		hmtypes.ParseWireInterfaceID(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
		payload.BucketValues,
		ev.Parameter,
	)
	nodeID := pd.DiscoveryNodeID(central)
	objectID := pd.DiscoveryObjectID(ev.Parameter)
	uniqueID, scoped := d.scopedUniqueID(ev.Central, ev.DeviceAddress+":"+strconv.Itoa(ev.ChannelNo), ev.Parameter, "")
	if !scoped {
		return DiscoveryItem{}
	}
	dev := modelDeviceFromInfo(deviceDescriptor(ev, d.hubURLFor(ev), d.SubDevicesEnabled))
	if dev == nil {
		return DiscoveryItem{}
	}

	entity := &pressButtonEntity{
		Basic: hamodel.Basic{
			EntityKey:      strings.ToLower(ev.Parameter),
			EntityPlatform: hacatalog.PlatformButton,
			Description:    hamodel.Description{Name: hamodel.L(pressButtonName(ev))},
			Binds: []hamodel.Binding{{
				Role: hamodel.RoleCommand,
				Mode: hamodel.Write,
				Slot: hamodel.S(dev.UID(), strconv.Itoa(ev.ChannelNo),
					hamodel.BucketValues, ev.Parameter).In(central, ev.Interface),
			}},
		},
	}
	describePressButtonFromRegistry(&entity.Description,
		string(HAComponentButton), ev.Parameter, ev.Model, "", "")

	ctx := pressButtonDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: pressButtonTopicLayout{d: d, ev: ev, pd: pd, central: central},
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
	}
	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	// EntityJSON, not json.Marshal: the component keeps its platform so the
	// caller can name the topic segment, and Home Assistant declares the key
	// on no platform -- its extra=REMOVE_EXTRA schemas would drop it with no
	// error on the wire and no log line.
	buf, err := comp.EntityJSON()
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentButton),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// pressButtonName composes the friendly_name fragment for a press button
// so it is unique across channels and centrals once HA prepends the device
// name.
//
// The plain parameter label ("Press Short") is identical for every press
// channel, so two devices that share a device/channel name collapse onto
// the same friendly_name and HA disambiguates with `_2` / `_3` entity-id
// suffixes. Mirror the reference stack's get_event_name: prefix the
// press-type label with the operator channel name when present, otherwise
// with ` ch<N>` so the channel number always disambiguates. The device name
// is prepended by HA.
func pressButtonName(ev Event) string {
	label, omitted := naming.EntityDisplayName(ev.descLabel(), ev.descLabelOmitted(), ev.Parameter)
	if omitted {
		// Primary-parameter omission would leave the button nameless and
		// collide on the device name alone — never omit for press buttons;
		// fall back to the title-cased parameter as the press-type label.
		label = naming.TitleCaseParameter(ev.Parameter)
	}
	prefix := ""
	if namer, ok := ev.Channel.(ChannelNamer); ok {
		if cn := namer.ChannelName(); cn != "" && !channelNameIsBareAddressNo(cn) {
			prefix = cn
		}
	}
	if prefix == "" && ev.ChannelNo > 0 {
		prefix = fmt.Sprintf("ch%d", ev.ChannelNo)
	}
	if prefix == "" {
		return label
	}
	return prefix + " " + label
}
