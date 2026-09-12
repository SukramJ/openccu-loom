// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HAComponentAlarmControlPanel is the HA MQTT-Discovery component prefix
// for the alarm panel. Every zone (and the aggregate master panel) maps
// onto one alarm_control_panel entity.
const HAComponentAlarmControlPanel HAComponent = "alarm_control_panel"

// alarmDiscoveryNodeID groups every alarm panel under one HA discovery
// node (`homeassistant/alarm_control_panel/alarm/<zone>/config`). Zones
// are daemon-level, so the node carries no `<central>` segment.
const alarmDiscoveryNodeID = "alarm"

// alarmMasterZone is the reserved zone segment of the aggregate panel
// that arms/disarms every zone at once. A real zone ID is a UUID, so it
// can never collide with this token.
const alarmMasterZone = alarmpanel.MasterZoneID

// alarmDeviceIdentifier is the single synthetic HA device identifier every
// alarm entity hangs off. It is carried as a [hamodel.Identifier] with an
// empty namespace, which renders the value verbatim — Home Assistant keys
// its device registry on this string and has no migration path for it.
const alarmDeviceIdentifier = "openccu-loom_alarm"

// alarm slot leaves. A [hamodel.Slot] is a coordinate, never a topic: the
// leaf is what [alarmContext] dispatches on to reach the topic builders
// below, so the plane's topic schema stays spelled out exactly once.
const (
	alarmSlotState           = "state"
	alarmSlotCommand         = "set"
	alarmSlotTriggeredMotion = "triggered-motion"
)

// HA alarm_control_panel supported-feature tokens. HA reads these from
// the discovery payload to decide which arm buttons the panel renders.

// alarm topic builders. Zones are daemon-level, so the alarm plane omits
// the `<central>` segment every per-device topic carries — a deliberate
// extension of the topic schema precedented only by `<base>/bridge/*`
// (docs/mqtt-topic-schema.md, notes/concepts/alarm-concept.md §13.3).
func alarmStateTopic(base, zone string) string { return base + "/alarm/" + zone + "/state" }

func alarmAvailabilityTopic(base, zone string) string {
	return base + "/alarm/" + zone + "/availability"
}
func alarmEventTopic(base, zone string) string   { return base + "/alarm/" + zone + "/event" }
func alarmCommandTopic(base, zone string) string { return base + "/alarm/" + zone + "/set" }

// alarmBridgeStatusTopic is the retained bridge LWT topic the panel's
// availability list references as the first (transport-level) source.
// The security plane declares the same source.
//
// It goes through the topic builder instead of assembling the topic a
// second time: the bridge publishes its status on the builder's
// normalised base, and with `availability_mode: "all"` an availability
// source that differs from it by a single slash never receives a
// payload, which leaves every entity of both planes unavailable
// forever rather than costing one value.
func alarmBridgeStatusTopic(base string) string { return NewTopicBuilder(base).BridgeStatus() }

// alarmDevice is the single synthetic device that groups every zone panel
// (and the master panel) under one card, in the shared model's terms.
func alarmDevice() *hamodel.Device {
	return &hamodel.Device{
		Identity: hamodel.Identity{
			IDs: []hamodel.Identifier{{Value: alarmDeviceIdentifier}},
		},
		Name:         hamodel.L("OpenCCU-Loom Alarm"),
		Manufacturer: "OpenCCU-Loom",
	}
}

// alarmDeviceBlock is the rendered `device` block of [alarmDevice]. It goes
// through the shared renderer rather than being written out a second time,
// so the block other planes compare against cannot drift from the one the
// alarm payloads actually carry.
func alarmDeviceBlock() *hadiscovery.DeviceInfo {
	info := hadiscovery.NewDeviceInfo(alarmDevice(), "")
	return &info
}

// alarmAvailability is the two-source availability list every alarm panel
// carries: the bridge LWT plus the per-zone alarm availability topic. With
// availability_mode "all" HA marks the panel available only when both are
// online (notes/concepts/alarm-concept.md §13.3).
func alarmAvailability(base, zone string) []hadiscovery.AvailabilityEntry {
	return []hadiscovery.AvailabilityEntry{
		{
			Topic:               alarmBridgeStatusTopic(base),
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
		},
		{
			Topic:               alarmAvailabilityTopic(base, zone),
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
		},
	}
}

// alarmContext is the alarm plane's [hadiscovery.Context]: the render
// pipeline asks it for every string the model must not build itself.
//
// It embeds [hadiscovery.StdContext] and overrides the answers this plane
// spells its own way — the three identity strings Home Assistant has no
// migration path for, and the topics, which follow the daemon-level
// `<base>/alarm/<zone>/...` schema rather than a device layout.
type alarmContext struct {
	hadiscovery.StdContext
	base string
}

// newAlarmContext binds a context to one topic base.
//
// [hadiscovery.RawEncoding] is not a preference here: the alarm plane
// publishes a retained plain state token, not the `{"value":…}` envelope
// the datapoint planes use, so an entity rendered with the envelope's value
// template would read its state through a filter that never matches and
// show as unknown forever.
func newAlarmContext(base string) alarmContext {
	return alarmContext{
		StdContext: hadiscovery.StdContext{Enc: hadiscovery.RawEncoding},
		base:       base,
	}
}

// StateTopic implements [hadiscovery.Context], dispatching on the slot's
// leaf so both state-shaped topics of the plane resolve through the topic
// builders above.
func (c alarmContext) StateTopic(s hamodel.Slot) string {
	if s.Leaf() == alarmSlotTriggeredMotion {
		return alarmTriggeredMotionTopic(c.base, s.Address)
	}
	return alarmStateTopic(c.base, s.Address)
}

// CommandTopic implements [hadiscovery.Context]. Every alarm entity writes
// to the one per-zone command topic — the reset button rides it with its
// own press payload rather than opening a second command plane.
func (c alarmContext) CommandTopic(s hamodel.Slot) string {
	return alarmCommandTopic(c.base, s.Address)
}

// Availability implements [hadiscovery.Context]. The plane's two sources
// are the bridge LWT and the per-zone alarm availability topic, neither of
// which is a device-layout topic the standard levels could render.
func (c alarmContext) Availability(_ *hamodel.Device, e hamodel.Entity) []hadiscovery.AvailabilityEntry {
	return alarmAvailability(c.base, alarmZoneOf(e))
}

// UniqueID implements [hadiscovery.Context]. The entity key *is* the
// identity: [alarmpanel.PanelUniqueID] is what internal/alarm stamps on the
// same panel, and the two derived entities hang their suffixes off it.
func (c alarmContext) UniqueID(_ *hamodel.Device, e hamodel.Entity) string { return e.Key() }

// NodeID implements [hadiscovery.Context]. Every alarm entity sits under
// the fixed "alarm" node: zones are daemon-level, so the node is not
// derived from anything device-shaped.
func (c alarmContext) NodeID(_ *hamodel.Device) string { return alarmDiscoveryNodeID }

// ObjectID implements [hadiscovery.Context]. The entity-id seed this plane
// publishes is the unique id verbatim; the default seed would prefix the
// device slug a second time and rename every entity.
func (c alarmContext) ObjectID(_ *hamodel.Device, e hamodel.Entity) string { return e.Key() }

// alarmZoneOf reads the zone back out of an entity's bindings. Every alarm
// entity binds at least one slot, and a slot's address is the zone.
func alarmZoneOf(e hamodel.Entity) string {
	if binds := e.Bindings(); len(binds) > 0 {
		return binds[0].Slot.Address
	}
	return ""
}

// alarmSlot is one coordinate of a zone.
//
// The zone is the address, not the synthetic device: every topic of this
// plane is keyed on the zone, and the device exists only to group the
// entities under one Home Assistant card. Putting the zone in Channel
// instead would read more literally but lose it — the shared context
// derives a device-level coordinate from Address and Scope alone.
//
// The bucket is [hamodel.BucketUnset] because an alarm zone is
// daemon-level: it has no paramset to classify a datapoint into.
func alarmSlot(zone, leaf string) hamodel.Slot {
	return hamodel.S(zone, "", hamodel.BucketUnset, leaf)
}

// alarmPanelEntity is the panel plus the alarm_control_panel vocabulary the
// shared model does not carry: the per-verb code policy, the
// supported-feature list and the command template. Those are platform
// keys, which is the case [hadiscovery.Builder] exists for.
type alarmPanelEntity struct {
	hamodel.Basic
	fields          hadiscovery.AlarmControlPanelFields
	commandTemplate string
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *alarmPanelEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.Fields = e.fields
	if e.commandTemplate != "" {
		comp.CommandTemplate = e.commandTemplate
	}
	return nil
}

// alarmButtonEntity is the reset button plus its press payload — the button
// platform's own vocabulary.
type alarmButtonEntity struct {
	hamodel.Basic
	fields hadiscovery.ButtonFields
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *alarmButtonEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.Fields = e.fields
	return nil
}

// renderAlarmItem renders one alarm entity through the shared per-entity
// pipeline and packages it as the item the publishers take.
//
// [hadiscovery.RenderComponent] drops the `platform` discriminator, which
// is right for the payload — the per-entity form carries the platform in
// its topic — but [discoveryItemFor] reads it to name that topic segment,
// so it is restored on the component and dropped again by the flattener.
func renderAlarmItem(base string, e hamodel.Entity, objectID string) DiscoveryItem {
	comp, err := hadiscovery.RenderComponent(newAlarmContext(base), alarmDevice(), e, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	comp.Platform = e.Platform()
	return discoveryItemFor(comp, alarmDiscoveryNodeID, objectID)
}

// alarmCommandTemplate is the HA command template used when a panel
// requires a code: it wraps the plain HA action and the entered code into
// the JSON envelope the raw command plane parses
// (`{"action":"ARM_AWAY","code":"1234"}`). Verified against the HA
// alarm_control_panel.mqtt docs (notes/reference/alarm-assumptions.md, Alarmo/HA-app
// section). Paired with code:"REMOTE_CODE" so HA prompts for a free-form
// code rather than a fixed on-device PIN.
const alarmCommandTemplate = `{"action":"{{ action }}","code":"{{ code }}"}`

// alarmRemoteCode is HA's sentinel for "the code is validated remotely
// (by loom), not fixed in the discovery config".
const alarmRemoteCode = "REMOTE_CODE"

// alarmFeatureTrigger advertises the HA TRIGGER capability so the panel
// exposes a panic/trigger affordance; the raw command plane routes a
// TRIGGER payload onto the engine's loud panic path
// (notes/concepts/alarm-concept.md §7).
const alarmFeatureTrigger = "trigger"

// BuildAlarmPanelDiscovery builds the HA Discovery payload for one alarm
// zone (master==false) or the aggregate master panel (master==true). The
// caller resolves the display name — for the master panel it passes the
// i18n-localized "Alarm system" string, mirroring how hub discovery names
// its synthetic entities. When master is set the topic/unique-id segment
// is forced to the reserved master token regardless of zoneID.
//
// codeArmRequired / codeDisarmRequired reflect the zone's per-verb code
// policy (notes/concepts/alarm-concept.md §11). When either is set the panel
// advertises code:"REMOTE_CODE" and a command template that folds the
// entered code into the raw command JSON, so HA prompts for the code and
// loom validates it.
func BuildAlarmPanelDiscovery(base, zoneID, zoneName string, modes []hmenum.AlarmMode, master, codeArmRequired, codeDisarmRequired bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	// The panel's identity is the model's — [alarmpanel.PanelUniqueID] is what
	// internal/alarm stamps on the same panel, and the two derived entities
	// below hang their suffixes off it. An entity id spelled out here as well
	// is one rename away from two entities for one zone.
	uniqueID := alarmpanel.PanelUniqueID(zone)
	entity := &alarmPanelEntity{
		Basic: hamodel.Basic{
			EntityKey:      uniqueID,
			EntityPlatform: hacatalog.PlatformAlarmControlPanel,
			Description:    hamodel.Description{Name: hamodel.L(zoneName)},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Slot: alarmSlot(zone, alarmSlotState), Mode: hamodel.Read},
				{Role: hamodel.RoleCommand, Slot: alarmSlot(zone, alarmSlotCommand), Mode: hamodel.Write},
			},
		},
		fields: hadiscovery.AlarmControlPanelFields{
			CodeArmRequired:    hadiscovery.Ptr(codeArmRequired),
			CodeDisarmRequired: hadiscovery.Ptr(codeDisarmRequired),
			// Panic is the case where nobody can type. Home Assistant defaults
			// code_trigger_required to true, so a zone that gates arming or
			// disarming used to gate the panic affordance as well — a rule this
			// daemon never chose and has no verb for: EffectiveCodePolicy
			// returns arm and disarm and nothing else. Left unset, an HA default
			// decided a safety policy on the operator's behalf.
			//
			// Arming and disarming keep their gates. The trade is deliberate: a
			// mis-tap on a dashboard now sounds the alarm immediately, which is
			// the lesser failure — the other direction is an alarm that cannot
			// be raised by the person who needs it.
			CodeTriggerRequired: hadiscovery.Ptr(false),
			SupportedFeatures:   append(alarmpanel.SupportedFeatures(modes), alarmFeatureTrigger),
		},
	}
	// A code-gated panel folds the entered code into the JSON command the
	// raw plane parses; without a template HA sends the bare action and
	// the code never reaches loom's validator.
	if codeArmRequired || codeDisarmRequired {
		entity.fields.Code = alarmRemoteCode
		entity.commandTemplate = alarmCommandTemplate
	}
	return renderAlarmItem(base, entity, zone)
}

// alarmTriggeredMotionTopic carries the number of latched motion
// detectors of a zone. It is a state topic like the panel's, so the
// round-trip guard covers it the same way.
func alarmTriggeredMotionTopic(base, zone string) string {
	return base + "/alarm/" + zone + "/triggered-motion"
}

// BuildAlarmMotionResetDiscovery builds the "clear latched motion
// detectors" button for one zone (or the master aggregate).
//
// It rides the panel's existing command topic with a `RESET_MOTION`
// press payload rather than opening a second command plane: the
// subscriber already wildcards `<base>/alarm/+/set`, so one plane keeps
// one subscription and the round-trip guard keeps checking one shape.
//
// The button is an entity in its own right rather than a panel feature
// because HA's alarm_control_panel has no vocabulary for it — without a
// separate entity there is nothing for an automation to press.
func BuildAlarmMotionResetDiscovery(base, zoneID, zoneName, label string, master bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	uniqueID := alarmpanel.PanelUniqueID(zone) + "_reset_motion"
	entity := &alarmButtonEntity{
		Basic: hamodel.Basic{
			EntityKey:      uniqueID,
			EntityPlatform: hacatalog.PlatformButton,
			Description: hamodel.Description{
				Name: hamodel.L(zoneName + " — " + label),
				Icon: "mdi:motion-sensor-off",
				// No Category on purpose. Home Assistant files `config`
				// entities away in a collapsed section of the device page and
				// keeps them out of dashboards and the entity picker's default
				// view — right for a knob that tunes behaviour, wrong for
				// something an operator presses during an incident. This is a
				// control belonging to the panel's main purpose, like the panel
				// entity itself, which carries no category either. The
				// latched-detector count next to it stays `diagnostic`; that one
				// really is a readout.
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleCommand, Slot: alarmSlot(zone, alarmSlotCommand), Mode: hamodel.Write},
			},
		},
		fields: hadiscovery.ButtonFields{PayloadPress: alarmCommandResetMotion},
	}
	return renderAlarmItem(base, entity, zone+"_reset_motion")
}

// BuildAlarmTriggeredMotionDiscovery builds the sensor that reports how
// many detectors the reset button would clear.
//
// It exists so an automation can decide rather than guess: pressing the
// button blindly writes to the radio for nothing, and a non-zero count
// on a disarmed zone is usually the reason an arm refuses.
func BuildAlarmTriggeredMotionDiscovery(base, zoneID, zoneName, label string, master bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	uniqueID := alarmpanel.PanelUniqueID(zone) + "_triggered_motion"
	entity := &hamodel.Basic{
		EntityKey:      uniqueID,
		EntityPlatform: hacatalog.PlatformSensor,
		Description: hamodel.Description{
			Name:       hamodel.L(zoneName + " — " + label),
			StateClass: hacatalog.StateClassMeasurement,
			Unit:       "detectors",
			Icon:       "mdi:motion-sensor",
			Category:   hacatalog.EntityCategoryDiagnostic,
		},
		Binds: []hamodel.Binding{
			{Role: hamodel.RoleState, Slot: alarmSlot(zone, alarmSlotTriggeredMotion), Mode: hamodel.Read},
		},
	}
	return renderAlarmItem(base, entity, zone+"_triggered_motion")
}
