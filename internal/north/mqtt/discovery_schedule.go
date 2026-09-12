// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// ScheduleEntityEvent carries the device + channel context the discovery
// builder needs to emit the Zeitplan-sensor HA entity — an HA sensor
// with rich json_attributes carrying the week-profile state.
type ScheduleEntityEvent struct {
	// Central is the CCU identifier (required for topic scoping).
	Central string
	// Interface is the CCU interface identifier (e.g. "HmIP-RF").
	Interface string
	// DeviceAddress is the base device address (without channel suffix).
	DeviceAddress string
	// ChannelNo is the schedule channel number on the device — the
	// channel that carries the WEEK_PROFILE / week_program data.
	ChannelNo int
	// DeviceName is the human-readable device name for the HA device block.
	DeviceName string
	// Model is the CCU device model string (e.g. "HmIP-MIO16-PCB").
	Model string
	// Device, when non-nil, is consulted by deviceDescriptor for the
	// `payload:"info"` map — same as Event.Device.
	Device any
}

// scheduleLabelKey is the catalogue key for the word "Zeitplan". It names
// both the sub-device card and the sensor that reports how many entries the
// schedule holds, which is why it is a key rather than two call sites.
const scheduleLabelKey = "discovery.schedule"

// scheduleTopicLayout renders this plane's topics through this daemon's own
// [TopicBuilder], so the render pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused. One call builds one schedule entity, whose
// channel — and, for a switch, whose channel key — the event already pins;
// deriving them from the slot again would be a second implementation of the
// same schema with nothing keeping the two in step.
//
// The availability topic is the PARENT device's, not the schedule
// sub-device's. The sub-device is a Home Assistant card, not a CCU device:
// nothing publishes a reachability flag for it, and pointing at one would
// leave every schedule entity unavailable forever.
type scheduleTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	central string
	iface   string
	address string
	state   string
	command string
}

// State implements the shared model's topic layout.
func (l scheduleTopicLayout) State(hamodel.Slot) string { return l.state }

// Command implements the shared model's topic layout.
func (l scheduleTopicLayout) Command(hamodel.Slot) string { return l.command }

// Availability implements the shared model's topic layout.
func (l scheduleTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.iface, l.address)
}

// Bridge implements the shared model's topic layout.
func (l scheduleTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// scheduleDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// The unique id and the node id are derived two different ways from the
// same address — [discoveryNodeID] slugs the central and lower-cases the
// address, [physicalDeviceIdentifier] prefixes "openccu-loom_" and folds
// the central in only for an address family that repeats across CCUs — and
// the node id is the PARENT's even though the device block identifies the
// schedule sub-device, so no single derivation produces both.
type scheduleDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes, central scoping and all.
func (c scheduleDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context].
func (c scheduleDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This plane has never published an
// entity-id seed; adding one now would rename every schedule entity in the
// fleet, and nothing downstream could undo it.
func (c scheduleDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// scheduleEntity is one schedule entity on the shared model: a
// [hamodel.Basic] plus the keys the model does not carry.
//
// The platform Fields are Home Assistant vocabulary for one platform
// rather than model semantics, which is the case [hadiscovery.Builder]
// exists for. `optimistic` and the json-attributes pair used to live here
// too; go-hamqtt v0.24.0 carries all three on the description, so they are
// declared with the rest of the entity.
type scheduleEntity struct {
	hamodel.Basic

	fields any
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *scheduleEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	if e.fields != nil {
		comp.Fields = e.fields
	}
	return nil
}

// scheduleSlot is the coordinate of one schedule entity.
//
// The bucket is [hamodel.BucketCustom]: a schedule surface is an aggregate
// this daemon composes out of a channel's week-profile state, not a
// datapoint of either paramset. The layout renders every topic from the
// strings the builder already resolved, so the slot's job is to be a stable
// coordinate carrying the read and write modes the pipeline projects the
// two topics from.
func scheduleSlot(dev *hamodel.Device, central, iface string, channel int, path ...string) hamodel.Slot {
	return hamodel.S(dev.UID(), strconv.Itoa(channel), hamodel.BucketCustom, path...).
		In(central, iface)
}

// renderScheduleItem renders one schedule entity through the shared
// per-entity pipeline and packages it as the item the publishers take.
//
// [hadiscovery.RawEncoding] is not a preference: a schedule state topic
// carries the bare entry count or the bare boolean, not the `{"value":…}`
// envelope the datapoint planes publish, so an entity rendered with the
// envelope's value template would read its state through a filter that
// never matches and show as unknown forever.
func (d *DefaultDiscoveryBuilder) renderScheduleItem(
	dev *hamodel.Device, e *scheduleEntity, layout scheduleTopicLayout, uniqueID, nodeID, objectID string,
) DiscoveryItem {
	if dev == nil {
		return DiscoveryItem{}
	}
	ctx := scheduleDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout:     layout,
			Lang:       d.Locale,
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
	}
	comp, err := hadiscovery.RenderComponent(ctx, dev, e, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	return discoveryItemFor(comp, nodeID, objectID)
}

// BuildScheduleEntityDiscovery builds the HA Discovery `sensor` payload
// for a device's Zeitplan entity. The native state is the count of
// active schedule entries; the rich schedule structure is exposed via
// json_attributes_topic.
//
// The entity lives on a **sub-device** "<device-name> Zeitplan" linked
// to the parent device via HA's `via_device` mechanism so each
// schedule surface gets its own HA device card.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`.
func (d *DefaultDiscoveryBuilder) BuildScheduleEntityDiscovery(centralName string, ev ScheduleEntityEvent) DiscoveryItem {
	if ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.ScheduleEntityState(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	attrsTopic := d.TopicBuilder.ScheduleEntityAttrs(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("%s_%d_schedule",
		physicalDeviceIdentifier(centralName, ev.DeviceAddress), ev.ChannelNo)

	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		ChannelNo:     ev.ChannelNo,
		Device:        ev.Device,
	}
	dev := modelDeviceFromInfo(
		scheduleSubDeviceDescriptor(mockEv, d.hubURLFor(mockEv), d.tr(scheduleLabelKey)),
	)
	if dev == nil {
		return DiscoveryItem{}
	}

	entity := &scheduleEntity{
		Basic: hamodel.Basic{
			EntityKey:      "schedule",
			EntityPlatform: hacatalog.PlatformSensor,
			Description: hamodel.Description{
				NameKey:  scheduleLabelKey,
				Icon:     "mdi:calendar-clock",
				Category: EntityCategoryDiagnostic,
				// The rich week-profile structure rides beside the count
				// rather than as the state: Home Assistant caps a sensor's
				// state at 255 characters and a schedule document is far
				// longer, so the count is the state and the document is
				// attached as attributes.
				JSONAttributesTopic:    attrsTopic,
				JSONAttributesTemplate: "{{ value_json | tojson }}",
			},
			Binds: []hamodel.Binding{
				{
					Role: hamodel.RoleState, Mode: hamodel.Read,
					Slot: scheduleSlot(dev, centralName, ev.Interface, ev.ChannelNo, "schedule"),
				},
			},
		},
	}
	layout := scheduleTopicLayout{
		d: d, central: centralName, iface: ev.Interface, address: ev.DeviceAddress,
		state: stateTopic,
	}
	return d.renderScheduleItem(dev, entity, layout, objectID, nodeID, objectID)
}

// PublishScheduleEntityDiscovery publishes the Zeitplan-sensor HA
// Discovery payload. Retained and deduplicated through the shared
// discovery cache.
//
// No-ops when HA discovery is disabled.
func (b *Bridge) PublishScheduleEntityDiscovery(ctx context.Context, centralName string, ev ScheduleEntityEvent) error {
	if !b.cfg.HADiscoveryEnabled {
		return nil
	}
	if b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	builder, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder)
	if !ok {
		return nil
	}
	item := builder.BuildScheduleEntityDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishScheduleEntityState publishes the active-entry count to the
// Zeitplan sensor's state topic.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishScheduleEntityState(
	ctx context.Context,
	centralName, iface, address string,
	channel, count int,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.ScheduleEntityState(centralName, iface, address, channel)
	return b.publishRawRetained(ctx, topic, fmt.Appendf(nil, "%d", count))
}

// PublishScheduleEntityAttrs publishes the rich schedule structure to
// the Zeitplan sensor's json_attributes topic. attrs is JSON-marshalled
// verbatim — callers populate it from
// [weekprofile.ProfileDataPoint] state.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishScheduleEntityAttrs(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	attrs map[string]any,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	if attrs == nil {
		attrs = map[string]any{}
	}
	body, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	topic := b.topics.ScheduleEntityAttrs(centralName, iface, address, channel)
	return b.publishRawRetained(ctx, topic, body)
}

// ScheduleSwitchEvent carries the context the discovery builder needs
// to emit one HA `switch` entity per ScheduleChannelSwitch on a
// device.
type ScheduleSwitchEvent struct {
	Central       string
	Interface     string
	DeviceAddress string
	// ScheduleChannelNo is the channel that hosts the WeekProfile /
	// COMBINED_PARAMETER write target.
	ScheduleChannelNo int
	DeviceName        string
	Model             string
	Device            any
	// Key is the channel key ("<actor>_<sub>", e.g. "1_1").
	Key string
	// TargetChannelNo is the receiver channel this switch governs.
	TargetChannelNo int
	// Label is the operator-facing entity name (e.g. "Zeitplan Kanal 18").
	Label string
}

// BuildScheduleSwitchDiscovery builds the HA Discovery `switch` payload
// for one ScheduleChannelSwitch. Each schedule device emits N switches
// (one per available target channel); HA renders them as a row of
// toggles for enabling / disabling the schedule per receiver.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`.
func (d *DefaultDiscoveryBuilder) BuildScheduleSwitchDiscovery(centralName string, ev ScheduleSwitchEvent) DiscoveryItem {
	if ev.Key == "" || ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.ScheduleSwitchState(centralName, ev.Interface, ev.DeviceAddress, ev.ScheduleChannelNo, ev.Key)
	commandTopic := d.TopicBuilder.ScheduleSwitchCommand(centralName, ev.Interface, ev.DeviceAddress, ev.ScheduleChannelNo, ev.Key)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	// The object id is built from the same identifier the device card uses, so
	// an address class that repeats across CCUs — INT000*, CUxD, the virtual
	// remotes — carries its central here too. Spelling it by hand from the bare
	// address produced byte-identical ids for two CCUs' heating groups, and
	// Home Assistant keeps whichever discovery config arrived first.
	objectID := fmt.Sprintf("%s_%d_schedule_%s",
		physicalDeviceIdentifier(centralName, ev.DeviceAddress), ev.ScheduleChannelNo, ev.Key)

	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		ChannelNo:     ev.ScheduleChannelNo,
		Device:        ev.Device,
	}
	dev := modelDeviceFromInfo(
		scheduleSubDeviceDescriptor(mockEv, d.hubURLFor(mockEv), d.tr(scheduleLabelKey)),
	)
	if dev == nil {
		return DiscoveryItem{}
	}

	// One slot, bound twice. The switch is read on the state topic and
	// written on the command topic, and the render pipeline resolves the two
	// roles separately, so a single ReadWrite binding on one role would
	// project only one of the two topics.
	slot := scheduleSlot(dev, centralName, ev.Interface, ev.ScheduleChannelNo, "schedule", ev.Key)
	entity := &scheduleEntity{
		Basic: hamodel.Basic{
			EntityKey:      "schedule_" + ev.Key,
			EntityPlatform: hacatalog.PlatformSwitch,
			Description: hamodel.Description{
				Name:       hamodel.L(ev.Label),
				Icon:       "mdi:calendar-check",
				Category:   EntityCategoryConfig,
				Optimistic: hadiscovery.Ptr(false),
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: slot},
				{Role: hamodel.RoleCommand, Mode: hamodel.Write, Slot: slot},
			},
		},
		fields: hadiscovery.SwitchFields{
			PayloadOn:  "true",
			PayloadOff: "false",
			StateOn:    "true",
			StateOff:   "false",
		},
	}
	layout := scheduleTopicLayout{
		d: d, central: centralName, iface: ev.Interface, address: ev.DeviceAddress,
		state: stateTopic, command: commandTopic,
	}
	return d.renderScheduleItem(dev, entity, layout, objectID, nodeID, objectID)
}

// PublishScheduleSwitchDiscovery publishes the HA Discovery payload for
// one ScheduleChannelSwitch entity. Retained + deduplicated.
func (b *Bridge) PublishScheduleSwitchDiscovery(ctx context.Context, centralName string, ev ScheduleSwitchEvent) error {
	if !b.cfg.HADiscoveryEnabled {
		return nil
	}
	if b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	builder, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder)
	if !ok {
		return nil
	}
	item := builder.BuildScheduleSwitchDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// scheduleSubDeviceDescriptor builds the HA `device` block for the
// schedule sub-device: the Zeitplan sensor + ScheduleChannelSwitches
// all land on a dedicated HA device card "<parent-name> Zeitplan",
// `via_device`-linked to the parent. This keeps the parent device card
// uncluttered (sensors / switches that control the real outputs stay
// there; schedule administration lives in its own card).
//
// Identifiers diverge from the parent (`openccu-loom_<addr>_schedule`
// vs. `openccu-loom_<addr>`), so HA creates a separate device. The
// parent device is referenced via the `via_device` field so HA renders
// the schedule sub-device as a child entry under the parent in the
// Devices view.
//
// Manufacturer / model / model_id / sw_version are copied from the
// parent device descriptor so the schedule card carries the same
// hardware identity as the parent — HA shows them as related units.
// suggested_area is also inherited so the sub-device falls into the
// same room.
func scheduleSubDeviceDescriptor(ev Event, hubURL, scheduleLabel string) *hadiscovery.DeviceInfo {
	// Through the same helper the parent card declares its own identifier with:
	// a hand-built "openccu-loom_<addr>" misses the central prefix that helper
	// adds for the repeating address classes, so via_device pointed at an
	// identifier no device declares and the schedule sub-device floated
	// unparented in Home Assistant — visible on a single CCU too.
	parentID := physicalDeviceIdentifier(ev.Central, ev.DeviceAddress)
	subID := parentID + "_schedule"
	parentName := ev.DeviceName
	if parentName == "" {
		parentName = ev.DeviceAddress
	}
	dev := &hadiscovery.DeviceInfo{
		Identifiers:  []string{subID},
		Name:         parentName + " " + scheduleLabel,
		Manufacturer: "eQ-3",
		ViaDevice:    parentID,
	}
	if hubURL != "" {
		dev.ConfigurationURL = hubURL
	}
	// Pull the parent's model / sw_version / serial / area info from
	// the device-info `payload:"info"` map so the schedule card carries
	// the same hardware identity. Only the keys Home Assistant accepts
	// exist on the typed block, so nothing has to be filtered.
	if ev.Device != nil {
		info := payload.ForWith(ev.Device, payload.KindInfo, payload.Options{UseAltNames: true})
		// `name` is reserved for the sub-device label.
		name := dev.Name
		assignDeviceInfo(dev, info)
		dev.Name = name
		// suggested_area fallback: copy the parent's room if present
		// and not already set by the info map. The parent device
		// resolves the singular room behind its own lock, so it is
		// asked rather than reflected over (see [deviceWithRoom]).
		if dev.SuggestedArea == "" {
			if dwr, ok := ev.Device.(deviceWithRoom); ok && dwr.Room() != "" {
				dev.SuggestedArea = dwr.Room()
			}
		}
	}
	return dev
}

// PublishScheduleSwitchState publishes the boolean state of one
// ScheduleChannelSwitch (true=enabled, false=disabled). Retained.
func (b *Bridge) PublishScheduleSwitchState(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	key string,
	enabled bool,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	msg := []byte("false")
	if enabled {
		msg = []byte("true")
	}
	topic := b.topics.ScheduleSwitchState(centralName, iface, address, channel, key)
	return b.publishRawRetained(ctx, topic, msg)
}
