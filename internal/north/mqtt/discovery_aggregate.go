// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// isPressParameter reports whether p is a click-event parameter, asking
// [event.Classify] rather than a list kept here. The set used to be
// duplicated in this file and exported for the EventBridge to share; a copy
// of an enumerable domain set caps its holder at the size its author knew
// about, which is why this plane published only keypresses while the model
// had known three event kinds all along.
func isPressParameter(p string) bool {
	kind, known := event.Classify(hmenum.Parameter(strings.ToUpper(p)))
	return known && kind == event.KindKeypress
}

// ChannelPressTypes returns the lower-cased HA event_types for all
// PRESS_* parameters present on ch. Returns nil only when the channel has
// no press parameter at all. The reference stack emits one channel-level
// keypress event entity per press channel regardless of how many press
// types it carries (a single PRESS_SHORT KEY channel gets the same
// channel-level `event` entity as a four-type remote), so a single press
// type is enough to materialise the aggregate. The order follows
// [event.Sources], the model's own source order.
func ChannelPressTypes(ch ChannelInspector) []string {
	if ch == nil {
		return nil
	}
	var found []string
	for _, p := range event.Sources(event.KindKeypress) {
		if ch.HasParameter(string(p)) {
			found = append(found, strings.ToLower(string(p)))
		}
	}
	return found
}

// channelParameterLister is an optional extension on [ChannelInspector].
// [ChannelInspector.HasParameter] answers an exact-name question; device-error
// parameters are prefix-matched and open-ended, so classifying them needs the
// list. A channel that does not implement it falls back to the known roots,
// which covers ERROR and SENSOR_ERROR but not ERROR_OVERHEAT and friends.
type channelParameterLister interface {
	ParameterNames() []string
}

// ChannelKindTypes returns the lower-cased wire parameters on the channel
// that belong to the given event kind, in sorted order — the `event_types`
// list of that kind's discovery entity.
//
// Home Assistant drops an event whose `event_type` is not in the announced
// list, so a parameter that can fire has to appear here or its pulses are
// discarded silently. That is why the device-error branch prefers the
// channel's actual parameter list over the known roots.
func ChannelKindTypes(ch ChannelInspector, kind event.Kind) []string {
	if ch == nil {
		return nil
	}
	if lister, ok := ch.(channelParameterLister); ok {
		var found []string
		for _, name := range lister.ParameterNames() {
			if k, known := event.Classify(hmenum.Parameter(name)); known && k == kind {
				found = append(found, strings.ToLower(name))
			}
		}
		sort.Strings(found)
		return found
	}
	// No list available: fall back to exact membership on the kind's known
	// parameters. Exhaustive for keypress and impulse; for device errors it
	// covers the roots only.
	var found []string
	for _, name := range event.Sources(kind) {
		if ch.HasParameter(string(name)) {
			found = append(found, strings.ToLower(string(name)))
		}
	}
	sort.Strings(found)
	return found
}

// channelPressTypes is the package-internal alias used by BuildChannelEvent.
func channelPressTypes(ch ChannelInspector) []string { return ChannelPressTypes(ch) }

// channelEventEntity is a channel-level event entity on the shared model: a
// [hamodel.Basic] with one description and a single read binding, plus the
// announced event vocabulary.
//
// `event_types` is the event platform's own vocabulary — the model describes
// what an entity is, not which strings one platform accepts as its triggers —
// so it is written through the typed [hadiscovery.EventFields] in
// [channelEventEntity.BuildDiscovery] rather than carried on the description.
type channelEventEntity struct {
	hamodel.Basic

	eventTypes []string
}

// BuildDiscovery implements [hadiscovery.Builder] for `event_types`.
func (e *channelEventEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.Fields = hadiscovery.EventFields{EventTypes: e.eventTypes}
	return nil
}

// channelEventTopicLayout renders this plane's topics through this daemon's
// own [TopicBuilder], so the render pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused. A channel-level event entity pins one
// channel and one kind, and [TopicBuilder] is the authority on how this
// daemon spells that kind's state topic — keypress, impulse and device error
// each have their own — so the state topic is resolved once by the caller and
// handed in. Deriving it from the slot again would be a second implementation
// of the same schema with nothing keeping the two in step.
type channelEventTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	ev      Event
	central string
	state   string
}

// State implements the shared model's topic layout.
func (l channelEventTopicLayout) State(hamodel.Slot) string { return l.state }

// Command implements the shared model's topic layout. A channel-level event
// entity is read-only and Home Assistant's event platform declares no
// `command_topic`.
func (l channelEventTopicLayout) Command(hamodel.Slot) string { return "" }

// Availability implements the shared model's topic layout: the per-device
// availability topic, which is the second of the two entries every channel
// entity on this daemon carries.
func (l channelEventTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l channelEventTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// channelEventDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// Both are overridden because Home Assistant has no migration path for either
// — the unique id keys the entity registry, the node id is a topic segment —
// and this plane derives them DIFFERENTLY from the same event: the unique id
// from the routing key's event-group layout (family first, central inside the
// channel slot), the node id from a slugged central plus the device address.
// No single derivation could produce both.
type channelEventDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes, central scoping and all. A virtual remote's address repeats
// verbatim on every CCU, so its id carries the central's serial; a real
// device's serial is globally unique and must NOT be scoped, or every
// existing event entity on every fleet is re-keyed at once.
func (c channelEventDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. It is central-scoped and derived
// from the PARENT device address even where the device block identifies a
// sub-device: only half the identity moves on a sub-device split, and
// deriving the node id from the device identity instead would re-home every
// such entity onto a topic nothing listens on.
func (c channelEventDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This plane has never published an entity-id
// seed; adding one now would rename every existing entity, and nothing
// downstream could undo it. The object id in the discovery TOPIC is a
// different string and is carried on the [DiscoveryItem].
func (c channelEventDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// channelEventSpec is what the two entry points on this plane resolve before
// the render: everything that differs between a keypress, an impulse and a
// device-error entity. The render itself is identical for all three, which is
// why it is one function rather than three.
type channelEventSpec struct {
	ev Event
	// key is the entity key inside the shared model. It is the discovery
	// object-id leaf, so the model names the entity the same way the topic
	// and the unique id do.
	key string
	// name is the friendly-name fragment; Home Assistant prepends the device
	// name itself.
	name string
	// stateTopic is the kind's channel-level topic, already spelled by
	// [TopicBuilder].
	stateTopic string
	// deviceClass is empty where the kind has no fitting Home Assistant
	// class, which omits the key.
	deviceClass string
	// types is the announced `event_types` vocabulary. Home Assistant drops
	// an event whose type is not in this list.
	types    []string
	uniqueID string
	nodeID   string
}

// renderChannelEvent renders one channel-level event entity through the
// shared model's per-entity discovery form ([hadiscovery.RenderComponent]),
// which attaches the device and origin blocks and omits `platform`.
//
// Returns ok=false when the device descriptor carries no identity or the
// render fails — the same "produce nothing" outcome the callers already have
// for an unscoped unique id.
func (d *DefaultDiscoveryBuilder) renderChannelEvent(s channelEventSpec) ([]byte, bool) {
	central := d.centralFor(s.ev)
	dev := modelDeviceFromInfo(deviceDescriptor(s.ev, d.hubURLFor(s.ev), d.SubDevicesEnabled))
	if dev == nil {
		return nil, false
	}

	entity := &channelEventEntity{
		Basic: hamodel.Basic{
			EntityKey:      s.key,
			EntityPlatform: hacatalog.PlatformEvent,
			Description: hamodel.Description{
				Name:        hamodel.L(s.name),
				DeviceClass: hamodel.DeviceClass(s.deviceClass),
				// Home Assistant's event platform requires the
				// post-template payload to be parseable as JSON and reads
				// `event_type` out of it itself. This daemon already
				// publishes that envelope on the channel topic, so a
				// template extracting the scalar would hand Home Assistant a
				// bare string it then fails to parse — `No valid JSON event
				// payload detected` in the log. The event platform DOES
				// declare `value_template`, so the default envelope encoding
				// would project one; suppress it explicitly.
				ValueTemplate: hamodel.NoValueTemplate,
			},
			Binds: []hamodel.Binding{{
				Role: hamodel.RoleState,
				Mode: hamodel.Read,
				Slot: hamodel.S(dev.UID(), strconv.Itoa(s.ev.ChannelNo),
					hamodel.BucketCustom, s.key).In(central, s.ev.Interface),
			}},
		},
		eventTypes: s.types,
	}

	ctx := channelEventDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: channelEventTopicLayout{
				d: d, ev: s.ev, central: central, state: s.stateTopic,
			},
			Lang:       d.Locale,
			Translator: d.tr,
		},
		uniqueID: s.uniqueID,
		nodeID:   s.nodeID,
	}

	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return nil, false
	}
	// EntityJSON, not json.Marshal: the component keeps its platform so the
	// caller can name the topic segment, and Home Assistant declares the key
	// on no platform -- its extra=REMOVE_EXTRA schemas would drop it with no
	// error on the wire and no log line.
	buf, err := comp.EntityJSON()
	if err != nil {
		return nil, false
	}
	return buf, true
}

// BuildChannelEvent produces the HA-Discovery `event` payload for a
// press channel: a single channel-level entity whose `event_types` list
// carries every PRESS_* type the channel exposes (one event entity per
// press channel, mirroring the reference stack — a single PRESS_SHORT KEY
// channel gets the same channel-level entity as a four-type remote).
//
// Returns (component, nodeID, objectID, payload, true) on success.
// Returns ("", "", "", nil, false) when ch is nil or carries no PRESS_*
// parameter — callers should fall through to the per-parameter path then.
func (d *DefaultDiscoveryBuilder) BuildChannelEvent(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) {
	types := channelPressTypes(ev.Channel)
	if len(types) == 0 {
		return "", "", "", nil, false
	}
	kindSlug := event.GenerateTranslationKey(event.KindKeypress)
	objectID = d.channelObjectID(ev, eventGroupLeaf(kindSlug))
	uniqueID, scoped := d.channelEventGroupUniqueID(ev, kindSlug)
	if !scoped {
		return "", "", "", nil, false
	}
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
	out, rendered := d.renderChannelEvent(channelEventSpec{
		ev:          ev,
		key:         eventGroupLeaf(kindSlug),
		name:        name,
		stateTopic:  stateTopic,
		deviceClass: EventDeviceClassForModel(ev.Model),
		types:       MapDoorbellEventTypes(ev.Model, types),
		uniqueID:    uniqueID,
		nodeID:      nodeID,
	})
	if !rendered {
		return "", "", "", nil, false
	}
	return string(HAComponentEvent), nodeID, objectID, out, true
}

// BuildChannelKindEvent produces the HA-Discovery `event` payload for the
// impulse and device-error kinds — one channel-level entity per kind, keyed
// and topicked as a sibling of the keypress entity.
//
// The keypress kind keeps [DefaultDiscoveryBuilder.BuildChannelEvent]: it
// carries the doorbell device-class mapping and an established identity that
// this must not disturb. Until 0.69.0 the other two kinds were not published
// on this plane at all, so everything here is additive — no key moves.
//
// Returns ("", "", "", nil, false) when the channel carries no parameter of
// the kind, or for a kind this builder does not serve.
func (d *DefaultDiscoveryBuilder) BuildChannelKindEvent(ev Event, kind event.Kind) (component, nodeID, objectID string, buf []byte, ok bool) {
	var leaf string
	switch kind {
	case event.KindImpulse:
		leaf = "impulse"
	case event.KindDeviceError:
		leaf = "device_error"
	default:
		return "", "", "", nil, false
	}
	types := ChannelKindTypes(ev.Channel, kind)
	if len(types) == 0 {
		return "", "", "", nil, false
	}
	kindSlug := event.GenerateTranslationKey(kind)
	objectID = d.channelObjectID(ev, eventGroupLeaf(kindSlug))
	uniqueID, scoped := d.channelEventGroupUniqueID(ev, kindSlug)
	if !scoped {
		return "", "", "", nil, false
	}
	nodeID = discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)

	var stateTopic string
	if kind == event.KindImpulse {
		stateTopic = d.TopicBuilder.ChannelImpulse(d.centralFor(ev), ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	} else {
		stateTopic = d.TopicBuilder.ChannelDeviceError(d.centralFor(ev), ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	}

	name := fmt.Sprintf("ch%d %s", ev.ChannelNo, leaf)
	if namer, nok := ev.Channel.(ChannelNamer); nok {
		if cn := namer.ChannelName(); cn != "" && !channelNameIsBareAddressNo(cn) {
			name = fmt.Sprintf("%s %s", cn, leaf)
		}
	}

	// A device error is a problem signal; the keypress entity has no
	// device_class and the impulse kind has no fitting one.
	deviceClass := ""
	if kind == event.KindDeviceError {
		deviceClass = "problem"
	}

	out, rendered := d.renderChannelEvent(channelEventSpec{
		ev:          ev,
		key:         eventGroupLeaf(kindSlug),
		name:        name,
		stateTopic:  stateTopic,
		deviceClass: deviceClass,
		types:       types,
		uniqueID:    uniqueID,
		nodeID:      nodeID,
	})
	if !rendered {
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

// errNoPlatform is what a builder that declined to produce anything yields:
// a component with no platform cannot name a topic, so it is not a payload.
var errNoPlatform = errors.New("discovery: component has no platform")

// discoveryItemFor renders a typed component into the item the publishers
// take, so a standalone builder assembles a [hadiscovery.Component] and hands
// it over rather than marshalling a map of its own.
//
// The body goes through [flattenComponent], which is also what drops the
// `platform` discriminator: this daemon publishes the per-entity form, where
// the platform is the topic segment and no schema declares it as a key.
func discoveryItemFor(comp hadiscovery.Component, nodeID, objectID string) DiscoveryItem {
	body, err := flattenComponent(comp)
	if err != nil {
		return DiscoveryItem{}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(comp.Platform),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// flattenComponent renders a typed component into the flat object Home
// Assistant receives.
//
// The platform is required on the way in and absent on the way out. It is
// the bundle discriminator, not a discovery key: the per-entity form this
// daemon publishes names the platform in its topic, and Home Assistant
// declares the key on no platform at all. [hadiscovery.Component.EntityJSON]
// is what drops it; the guard stays here because a component with no
// platform has no topic to be published to either.
func flattenComponent(comp hadiscovery.Component) (map[string]any, error) {
	if comp.Platform == "" {
		return nil, errNoPlatform
	}
	raw, err := comp.EntityJSON()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// aggregateTopicLayout renders a custom data point's slots through this
// daemon's own [TopicBuilder], so the render pipeline produces exactly the
// strings already retained on the broker rather than a second spelling of
// them.
//
// It is [payload.SlotLayout] over the per-event context the model-side
// builders already resolve their topics against, which is what keeps the
// mapping from coordinate to topic in one place: the model names a slot, this
// names the topic, and nothing in between spells either twice.
func (d *DefaultDiscoveryBuilder) aggregateTopicLayout(ev Event) payload.SlotLayout {
	return payload.SlotLayout{Topics: d.discoveryContext(ev)}
}

// aggregateDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// The unique id and the node id are overridden because Home Assistant has no
// migration path for either — the unique id keys the entity registry, the node
// id is a topic segment — and this plane derives them differently from the
// same event: the unique id from the channel address and the platform, the
// node id from a slugged central plus the device address. No single derivation
// produces both.
type aggregateDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes, central scoping and all.
func (c aggregateDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context].
func (c aggregateDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This plane has never published an entity-id
// seed; adding one now would rename every aggregate entity in the fleet, and
// nothing downstream could undo it. The object id in the discovery TOPIC is a
// different string and is carried on the [DiscoveryItem].
func (c aggregateDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// customDPEntity asks the event's custom data point to describe itself on the
// shared model.
//
// Returns ok=false when the source is not a custom data point at all, or
// declines — which is what sends the caller on to the per-parameter path.
func customDPEntity(ev Event) (hamodel.Entity, bool) {
	builder, is := ev.Source.(payload.HADiscoveryEntityBuilder)
	if !is || builder == nil {
		return nil, false
	}
	entity := builder.HADiscoveryEntity()
	if entity == nil || entity.Desc() == nil {
		return nil, false
	}
	return entity, entity.Platform() != ""
}

// aggregateEntityName resolves the entity's display name, empty when it has
// none of its own.
//
// Having none is a statement, not a gap: `name: null` is how Home Assistant is
// told the entity carries the device's name alone. An empty string is read as
// "derive one" and produces the double prefix this convention exists to
// avoid, which is why the caller nulls the key rather than leaving it blank.
//
// `translation_key` is a native-HA-integration concept: it resolves against
// that integration's own translations.json, which an MQTT-discovered entity
// has none of, so HA's MQTT schema strips the key on receipt and it never
// affects anything. An entity left nameless alongside a translation_key that
// would normally have supplied the display suffix therefore shows up in HA as
// the bare device name — indistinguishable from any other single-primary
// entity on the same device (canonical case: an HmIP-eTRV's climate and its
// BUTTON_LOCK child lock both landing on "<device name>"). Resolving the key
// through this daemon's own catalogue and using the result as the name is what
// tells them apart; a key with no catalogue entry keeps the null, same as
// before.
func (d *DefaultDiscoveryBuilder) aggregateEntityName(ev Event, desc *hamodel.Description) string {
	if name := displayChannelName(ev); name != "" {
		return name
	}
	tk, _ := desc.Extra["translation_key"].(string)
	if tk == "" {
		return ""
	}
	nameKey := "discovery.entity_name." + tk
	if resolved := d.tr(nameKey); resolved != nameKey {
		return resolved
	}
	return ""
}

// aggregateChannel collapses every parameter on a known custom-domain channel
// into a single HA entity (climate, cover, lock, light, valve, siren, switch).
// Returns (component, nodeID, objectID, payload, true) on a hit — the bridge
// uses the same dedup cache as the per-parameter path, so subsequent calls for
// the same channel (one per parameter event) deduplicate to a no-op.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin blocks
// and derives the availability list, the unique id and the topics from the
// entity's description and bindings. The custom data point contributes what it
// alone knows — its platform's vocabulary — and nothing else.
//
// Returns ok=false when ev.Source is not a custom data point, when it declines
// to describe itself, when the device descriptor carries no identity, or when
// the render or the encode fails.
func (d *DefaultDiscoveryBuilder) aggregateChannel(ev Event) (component, nodeID, objectID string, buf []byte, ok bool) {
	// Text-display custom-DPs (HmIP-WRCD) surface ONLY as a `notify` entity in
	// the reference stack — the TEXT_DISPLAY category maps to the notify
	// platform alone, not to a `text` entity. The notify companion is
	// published separately via [Bridge.publishTextDisplayNotify]; suppressing
	// the aggregate here keeps the loom plane from emitting a surplus `text`
	// entity (which HA would otherwise collide with the notify under a `_2`
	// suffix).
	//
	// The text-display source also declines on its own — it implements no
	// entity builder — so on the real source this check is redundant today.
	// It stays because the guarantee is the bridge's: a mixin that later grew
	// an HADiscoveryEntity method would put the surplus entity back on the
	// wire, and the `_2` collision this prevents has shipped once already.
	if isTextDisplayEvent(ev) {
		return "", "", "", nil, false
	}
	entity, described := customDPEntity(ev)
	if !described {
		return "", "", "", nil, false
	}
	component = string(entity.Platform())
	uniqueID, scoped := d.channelUniqueID(ev, component)
	if !scoped {
		return "", "", "", nil, false
	}
	dev := modelDeviceFromInfo(deviceDescriptor(ev, d.hubURLFor(ev), d.SubDevicesEnabled))
	if dev == nil {
		return "", "", "", nil, false
	}

	desc := entity.Desc()
	// Strict variant: when neither a rule nor a category-default matches,
	// every HA-attribute field is cleared so an unknown model gets no
	// `device_class` etc. (mirroring HA-native behaviour). Without it the
	// legacy openccu-loom table would keep emitting `device_class=shutter`
	// for models the HA integration has no cover rule for.
	//
	// Postfix propagation: the HA integration matches Lock variants
	// (BUTTON_LOCK, …) by `dp.data_point_name_postfix`. Pull it from the
	// custom data point when available so the postfix-keyed rules in
	// `entity_helpers/descriptions/locks.py` fire on the openccu-loom side
	// too.
	postfix := ""
	if pf, is := ev.Source.(interface{ NamePostfix() string }); is {
		postfix = pf.NamePostfix()
	}
	applyEntityDescriptionStrict(desc, component, "", ev.Model, ev.descUnit(), postfix)
	name := d.aggregateEntityName(ev, desc)
	desc.Name = hamodel.L(name)
	// CDP_SECONDARY entities (mirror channels declared via the profile's
	// `secondary_channels`) are hidden by default in HA
	// (model/data_point.py:399). The operator can re-enable them from the
	// device card; without this flag they show up as duplicate primary
	// entities and pollute the dashboard.
	if insp, is := ev.Channel.(CustomDPNamingInspector); is && insp.IsCustomDPSecondaryChannel() {
		desc.Enabled = hamodel.Ptr(false)
	}

	nodeID = discoveryNodeID(d.centralFor(ev), ev.DeviceAddress)
	objectID = d.channelObjectID(ev, component)
	ctx := aggregateDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: d.aggregateTopicLayout(ev),
			Lang:   d.Locale,
			// A custom data point's aggregate carries a curated document, not
			// the `{"value": …}` envelope the per-parameter plane publishes,
			// and every field of it names its own template. The envelope
			// default would project one onto entities that deliberately
			// publish none.
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
	}
	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return "", "", "", nil, false
	}
	// `name` is JSON-null when blank — that is HA's signal to render
	// `friendly_name` = device.name alone. The model has no way to say it: an
	// empty [hamodel.Localized] is the absence of an opinion, and the render
	// pipeline drops the key rather than nulling it, which makes HA derive a
	// name from the platform instead.
	comp.NameNull = comp.Name == ""
	// The climate preset list leaves the domain as slugs; the ones HA cannot
	// translate get labels here, where the catalogues are.
	if HAComponent(component) == HAComponentClimate {
		d.localiseClimatePresets(&comp)
	}
	body, err := flattenComponent(comp)
	if err != nil {
		return "", "", "", nil, false
	}
	// Lists a custom data point declared localisable — siren tones, light
	// effects — carry their labels on the event. This one stays on the
	// flattened body: the keys it rewrites are named by the platform
	// (effect_list, available_tones) and live on different platform structs,
	// which is what makes it the one genuinely dynamic step here.
	applySelectionLabels(body, ev.SelectionLabels)
	out, err := json.Marshal(body)
	if err != nil {
		return "", "", "", nil, false
	}
	return component, nodeID, objectID, out, true
}

// discoveryCtx is the bridge-side implementation of
// [payload.HADiscoveryTopics]. It carries the per-event scoping (central,
// interface, address, channel) so the shared model's layout can render a
// custom data point's slots without knowing the topology.
type discoveryCtx struct {
	d  *DefaultDiscoveryBuilder
	ev Event
}

var _ payload.HADiscoveryTopics = discoveryCtx{}

// CustomDPStateTopic returns the channel's custom-DP slot state topic
// `<addr>/<ch>/custom/<kind>`. The kind is read from the event's Source via
// the [payload.Slotted] interface — every custom-DP implements it. Empty when
// the source is missing or not a slotted custom-DP (e.g. a per-parameter
// discovery event).
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
	if slot.Bucket == payload.BucketUnset {
		slot.Bucket = payload.BucketCustom
	}
	return slot, true
}

// CustomDPCommandTopic is the base every named action hangs off: the shared
// context appends `/<method>` to it, which is exactly
// [TopicBuilder.CustomDPServiceMethod]'s `…/custom/<kind>/set/<method>` shape.
//
// It is derived by asking that builder for a method topic and dropping the
// method segment, rather than by appending "/set" to the state topic, so the
// two spellings cannot drift apart.
func (c discoveryCtx) CustomDPCommandTopic() string {
	slot, ok := customDPSlotForEvent(c.ev)
	if !ok {
		return ""
	}
	const probe = "method"
	full := c.d.TopicBuilder.CustomDPServiceMethod(c.d.centralFor(c.ev), c.ev.Interface, slot, probe)
	return strings.TrimSuffix(full, "/"+probe)
}

// ServiceMethodCommandTopic is the topic Home Assistant writes to in order to
// invoke method on this channel's custom data point.
//
// The aggregate plane reaches it through [discoveryCtx.CustomDPCommandTopic]
// and the shared render context, which appends the method segment itself. It
// stays a method of its own for the notify plane, whose entity has no custom-DP
// binding to derive the base from — the display's `write` service is the whole
// of what that entity addresses.
func (c discoveryCtx) ServiceMethodCommandTopic(method string) string {
	slot, ok := customDPSlotForEvent(c.ev)
	if !ok {
		return ""
	}
	return c.d.TopicBuilder.CustomDPServiceMethod(c.d.centralFor(c.ev), c.ev.Interface, slot, method)
}

// WireParameterCommandTopic is the per-parameter `/set` topic. An empty
// channelAddress means the channel this event names, which is what a slot
// built by [payload.WireSlot] leaves unsaid.
func (c discoveryCtx) WireParameterCommandTopic(channelAddress, parameter string) string {
	address, channel := c.channelOf(channelAddress)
	return c.d.TopicBuilder.DataPointCommand(
		c.d.centralFor(c.ev), c.ev.Interface, address, channel, parameter,
	)
}

// WireParameterStateTopic is the canonical per-parameter state topic — the
// same shape every consumer uses. Home Assistant reads the PerDPState envelope
// `{"value": …, "available": …, "modified_at": …, "type": …, "unit": …}`
// through a `value_json.value` template.
func (c discoveryCtx) WireParameterStateTopic(channelAddress, parameter string) string {
	address, channel := c.channelOf(channelAddress)
	return c.d.TopicBuilder.ParameterState(
		c.d.centralFor(c.ev), c.ev.Interface, address, channel,
		payload.BucketValues, parameter,
	)
}

// DeviceAvailabilityTopic is the owning device's retained reachability topic.
func (c discoveryCtx) DeviceAvailabilityTopic() string {
	return c.d.TopicBuilder.DeviceAvailability(c.d.centralFor(c.ev), c.ev.Interface, c.ev.DeviceAddress)
}

// BridgeStatusTopic is the daemon's own LWT.
func (c discoveryCtx) BridgeStatusTopic() string { return c.d.TopicBuilder.BridgeStatus() }

// channelOf resolves a `<device>:<n>` address to its parts, falling back to
// the channel this event names.
//
// A custom data point may compose a field from a sibling channel — the classic
// HM-CC-TC keeps its setpoint on the regulator channel while the thermostat is
// materialised on the weather channel — and the per-parameter state is
// published under the channel the parameter actually lives on. An address with
// no parsable channel suffix is the event's own channel, which is what a slot
// that named neither means.
func (c discoveryCtx) channelOf(channelAddress string) (address string, channel int) {
	if channelAddress == "" {
		return c.ev.DeviceAddress, c.ev.ChannelNo
	}
	address, channel, ok := hmtypes.SplitChannelAddress(channelAddress)
	if !ok {
		return c.ev.DeviceAddress, c.ev.ChannelNo
	}
	return address, channel
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

// eventGroupLeaf is the discovery object-id leaf of a channel-level event
// entity. It carries the same family marker as the entity's unique_id, which
// is what makes a re-key survivable: the object id is part of the discovery
// topic, so a changed leaf publishes the new entity on a new topic and leaves
// the old config behind as a genuine orphan.
//
// That matters more than the naming symmetry. Home Assistant reads unique_id
// only in its entity constructor, so a new id written onto the SAME topic
// reaches a running instance not at all — it would surface unannounced at the
// next restart. Moving the topic instead lets the existing discovery-orphan
// sweep retract the old config, which is what turns a silent zombie entity
// into a clean disappearance the operator can see and act on once.
func eventGroupLeaf(kindSlug string) string {
	return "event_group_" + kindSlug
}

// channelEventGroupUniqueID is the identity of a channel-level event entity:
// the routing key [event.Group.CanonicalUniqueID] publishes for the same
// channel and kind, so the MQTT entity, the REST projection and the model
// itself name it identically.
//
// It is deliberately not [DefaultDiscoveryBuilder.channelUniqueID] with a
// leaf suffix. That helper places the central scope in front of the whole
// key, which yields loom_<central>_<channel>_<leaf>; an event group's
// reference layout carries the family first and the central inside the
// channel slot. The two differ for every channel, and the family prefix is
// what lets a consumer recognise an event group at all.
func (d *DefaultDiscoveryBuilder) channelEventGroupUniqueID(ev Event, kindShort string) (string, bool) {
	channelAddress := ev.DeviceAddress + ":" + strconv.Itoa(ev.ChannelNo)
	serial := d.serialSuffix(d.centralFor(ev))
	if serial == "" && routingkey.NeedsCentralScope(channelAddress) {
		return "", false
	}
	id := routingkey.EventGroupUniqueID(serial, channelAddress, kindShort)
	return id, id != ""
}

// channelUniqueID is the cross-broker-stable id used for HA's
// `unique_id` payload field, and whether it is safe to publish — see
// [DefaultDiscoveryBuilder.scopedUniqueID] for what makes it unsafe.
func (d *DefaultDiscoveryBuilder) channelUniqueID(ev Event, suffix string) (string, bool) {
	return d.scopedUniqueID(ev.Central, ev.DeviceAddress+":"+strconv.Itoa(ev.ChannelNo), suffix, "")
}

// channelPathData builds the channel-scoped [naming.PathData] used
// by the discovery-identity helpers. Composed from the event's
// Interface/DeviceAddress/ChannelNo — the bucket and kind are
// irrelevant for identity derivation and stay zero.
func channelPathData(ev Event) naming.PathData {
	return naming.NewChannelPathData(
		hmtypes.ParseWireInterfaceID(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
	)
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
	if insp, ok := ev.Channel.(CustomDPNamingInspector); ok &&
		(insp.IsCustomDPPrimaryChannel() || insp.IsCustomDPSecondaryChannel()) {
		if namer, ok := ev.Channel.(CustomDPDisplayNamer); ok {
			return namer.CustomDPDisplayName()
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

// channelEnumValuesReader is an optional extension on ChannelInspector
// for reading a parameter's VALUE_LIST off a channel.
//
// Nothing in the discovery path asserts against it today: the `options`
// field of an enum sensor is populated from the event's own descriptor
// (descValueList / descValueLabels in discovery.go), which is a
// separate mechanism.
type channelEnumValuesReader interface {
	ParameterValueList(parameter string) []string
}
