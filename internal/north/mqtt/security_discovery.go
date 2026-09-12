// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// securityDiscoveryNodeID groups every Security & Safety entity under
// one discovery node. Like the alarm plane the domain is daemon-level,
// so the node carries no central segment.
const securityDiscoveryNodeID = "security"

// Security topic builders. The tree sits beside `alarm/` and
// `bridge/` as the third daemon-level plane — see ADR 0052 and its
// extension in ADR 0059.
func securityStateTopic(base, key string) string { return base + "/security/" + key }

func securityClassTopic(base string, class hmenum.SecurityClass) string {
	return base + "/security/class/" + string(class)
}

func securityZoneTopic(base, slug string) string { return base + "/security/zone/" + slug }

func securityAvailabilityTopic(base string) string { return base + "/security/availability" }

// securityDeviceIdentifier is the one synthetic HA device identifier the
// whole plane shares. It is carried as a [hamodel.Identifier] with an empty
// namespace, which renders the value verbatim — Home Assistant keys its
// device registry on this string and has no migration path for it.
const securityDeviceIdentifier = "openccu-loom_security"

// securityDevice is the single synthetic device every Security & Safety
// entity hangs off, in the shared model's terms.
//
// It is deliberately its own card rather than a reuse of the alarm card.
// The alarm block is rewritten on every panel discovery and on every broker
// reconnect; two publishers writing different blocks under one identifier
// set make the card name flap and would rename every existing alarm
// entity's friendly name. Two cards cost nothing and this is also the "own
// device" the domain was asked for.
func securityDevice(name, configURL string) *hamodel.Device {
	return &hamodel.Device{
		Identity: hamodel.Identity{
			IDs: []hamodel.Identifier{{Value: securityDeviceIdentifier}},
		},
		Name:         hamodel.L(name),
		Manufacturer: "OpenCCU-Loom",
		Model:        "Security & Safety",
		SWVersion:    build.Version,
		ConfigURL:    configURL,
	}
}

// securityDeviceBlock is the rendered `device` block of [securityDevice]. It
// goes through the shared renderer rather than being written out a second
// time, so the block the alarm plane is compared against cannot drift from
// the one the security payloads actually carry.
func securityDeviceBlock(name, configURL string) *hadiscovery.DeviceInfo {
	info := hadiscovery.NewDeviceInfo(securityDevice(name, configURL), "")
	return &info
}

// securityEventTypes is the announced vocabulary of the two event
// entities. A consumer rejects an event whose type was not announced,
// so the emitter and this list must never drift — they share this
// constant for that reason.
//
// SecurityVerbTest is deliberately absent: nothing constructs it yet.
// Announcing a type no producer emits is a promise the plane cannot
// keep, and a consumer that filters on the announced set would silently
// wait for an event that never arrives. It joins the list together with
// the endpoint that fires it.
var securityEventTypes = []string{
	string(hmenum.SecurityVerbTriggered),
	string(hmenum.SecurityVerbCleared),
	string(hmenum.SecurityVerbRaised),
}

// securityEntity is the shape of one discovery payload before
// marshalling.
type securityEntity struct {
	// component is the consumer entity type.
	component HAComponent
	// key is the topic suffix and the unique-id suffix.
	key string
	// name is the localized display name.
	name string
	// deviceClass is optional.
	deviceClass string
	// stateClass is optional (measurement counters).
	stateClass string
	// diagnostic marks a supporting entity rather than a primary one.
	diagnostic bool
	// enabledByDefault=false hides an entity until an operator asks.
	enabledByDefault bool
	// payloadOn/Off apply to binary sensors.
	payloadOn, payloadOff string
	// jsonAttributes publishes the state topic as the attribute source
	// too — the pattern hub discovery already uses.
	jsonAttributes bool
	// valueTemplate extracts the state from a JSON payload.
	valueTemplate string
	// event marks a non-retained event entity, which must carry no
	// value template and no device class.
	event bool
	// topic overrides the derived state topic. Class and zone entities
	// need it because their topics are nested (`security/class/<c>`)
	// while their keys are flat (`class_<c>`) — deriving the topic from
	// the key declared `security/class_smoke` while the publisher wrote
	// `security/class/smoke`, so every one of those entities appeared in
	// a consumer and stayed unavailable forever.
	topic string
	// options is the enum vocabulary. A sensor with device_class enum
	// and no options is refused outright, per the rule this package
	// already applies to CCU enum sensors.
	options []string
}

// securitySlot is the coordinate of one Security & Safety entity.
//
// The key is the address, not a device: every topic of this plane is keyed
// on the entity's own key, and the synthetic device exists only to group
// the entities under one Home Assistant card. The bucket is
// [hamodel.BucketUnset] because a daemon-level domain entity has no
// paramset to classify a datapoint into.
func securitySlot(key string) hamodel.Slot {
	return hamodel.S(key, "", hamodel.BucketUnset)
}

// securityTopicLayout renders this plane's topics from the strings the
// builder already resolved, so the render pipeline produces exactly what is
// retained on the broker rather than a second spelling of it.
//
// The slot arguments are unused. One call builds one entity, whose state
// topic is either derived from its key or overridden outright (see
// [securityEntity.topic]); re-deriving it from the slot would be a second
// implementation of the same schema with nothing keeping the two in step.
type securityTopicLayout struct {
	base  string
	state string
}

// State implements the shared model's topic layout.
func (l securityTopicLayout) State(hamodel.Slot) string { return l.state }

// Command implements the shared model's topic layout. Nothing on this plane
// is writable from Home Assistant: the domain is driven by the CCU and by
// the daemon's own REST surface.
func (l securityTopicLayout) Command(hamodel.Slot) string { return "" }

// Availability implements the shared model's topic layout with the domain's
// own flag, which is the second of the two sources every entity carries.
func (l securityTopicLayout) Availability(hamodel.Slot) string {
	return securityAvailabilityTopic(l.base)
}

// Bridge implements the shared model's topic layout. It goes through the
// topic builder instead of assembling the topic a second time: the bridge
// publishes its status on the builder's normalised base, and with
// `availability_mode: "all"` a source that differs from it by a single
// slash never receives a payload, which leaves every entity of the plane
// unavailable forever rather than costing one value.
func (l securityTopicLayout) Bridge() string { return alarmBridgeStatusTopic(l.base) }

// securityDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// Both the unique id and the entity-id seed are overridden because Home
// Assistant has no migration path for either, and this plane derives them
// DIFFERENTLY from the same key — the unique id prefixes "loom_security_",
// the discovery topic's object-id segment is the bare key. The node id is a
// constant rather than a device identifier, so the whole plane shares one
// discovery node.
type securityDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes.
func (c securityDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. Every entity of the plane shares
// one node, so a change to the constant orphans all of them at once.
func (c securityDiscoveryContext) NodeID(*hamodel.Device) string {
	return securityDiscoveryNodeID
}

// ObjectID implements [hadiscovery.Context]. This plane DOES publish an
// entity-id seed, and the seed is the UNIQUE ID rather than the discovery
// topic's object-id segment: `default_entity_id` has always been
// `<component>.<unique_id>` here, and Home Assistant derives the entity id
// from it once and will not rename afterwards. The pipeline prefixes the
// platform itself.
func (c securityDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// securityModelEntity is one Security & Safety entity on the shared model: a
// [hamodel.Basic] plus the handful of keys the model does not carry.
//
// The platform Fields and the two json-attributes keys are Home Assistant
// vocabulary rather than model semantics, which is the case
// [hadiscovery.Builder] exists for.
type securityModelEntity struct {
	hamodel.Basic

	fields                 any
	jsonAttributesTopic    string
	jsonAttributesTemplate string
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *securityModelEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	if e.fields != nil {
		comp.Fields = e.fields
	}
	if e.jsonAttributesTopic != "" {
		comp.JSONAttributesTopic = e.jsonAttributesTopic
		comp.JSONAttributesTemplate = e.jsonAttributesTemplate
	}
	return nil
}

// BuildSecurityDiscovery builds the discovery payload for one Security
// & Safety entity.
//
// deviceName and configURL identify the card; name is the entity's own
// localized label.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`.
func BuildSecurityDiscovery(base, deviceName, configURL string, e securityEntity) DiscoveryItem {
	if e.key == "" {
		return DiscoveryItem{}
	}
	uniqueID := "loom_security_" + e.key
	stateTopic := e.topic
	if stateTopic == "" {
		stateTopic = securityStateTopic(base, e.key)
	}

	entity := &securityModelEntity{
		Basic: hamodel.Basic{
			EntityKey:      e.key,
			EntityPlatform: hacatalog.Platform(e.component),
			Description: hamodel.Description{
				Name: hamodel.L(e.name),
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: securitySlot(e.key)},
			},
		},
	}
	desc := &entity.Description
	switch {
	case e.event:
		// An event entity must not carry a value template — a scalar
		// destroys the JSON parsing — and must not carry a device
		// class, whose vocabulary is limited to doorbell/button/motion.
		entity.fields = hadiscovery.EventFields{EventTypes: securityEventTypes}
	default:
		desc.DeviceClass = hamodel.DeviceClass(e.deviceClass)
		desc.StateClass = hacatalog.StateClass(e.stateClass)
		desc.ValueTemplate = e.valueTemplate
		if len(e.options) > 0 {
			// The security vocabulary is its own label: the daemon publishes
			// the raw tokens and Home Assistant stores one of them as the
			// entity's state, so codes without labels render verbatim.
			desc.Options = &hamodel.Enum{Codes: append([]string(nil), e.options...)}
		}
		if e.payloadOn != "" {
			// Only the binary sensor among the security entities carries a
			// payload pair, and its keys live on that platform's struct —
			// the typed form is what makes that visible.
			entity.fields = hadiscovery.BinarySensorFields{
				PayloadOn:  e.payloadOn,
				PayloadOff: e.payloadOff,
			}
		}
		if e.jsonAttributes {
			entity.jsonAttributesTopic = stateTopic
			entity.jsonAttributesTemplate = "{{ value_json | tojson }}"
		}
	}
	if e.diagnostic {
		desc.Category = EntityCategoryDiagnostic
	}
	if !e.enabledByDefault {
		desc.Enabled = hamodel.Ptr(false)
	}

	ctx := securityDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: securityTopicLayout{base: base, state: stateTopic},
			// The security state topics carry a bare token or a JSON document
			// read by the entity's own template, not the `{"value":…}`
			// envelope the datapoint planes publish, so an entity rendered
			// with the envelope's value template would read its state through
			// a filter that never matches and show as unknown forever.
			Enc: hadiscovery.RawEncoding,
		},
		uniqueID: uniqueID,
	}
	comp, err := hadiscovery.RenderComponent(ctx, securityDevice(deviceName, configURL), entity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	return discoveryItemFor(comp, securityDiscoveryNodeID, e.key)
}
