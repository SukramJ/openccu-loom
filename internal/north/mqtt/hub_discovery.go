// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strconv"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Hub-entity discovery — sysvars, programs, alarm/service messages,
// install-mode, per-interface connectivity. Each maps to a single HA
// entity rooted on a synthetic device that represents the central
// Itself. The
// device block uses a stable identifier `openccu-loom_central_<name>`
// so HA groups every hub entity under one card.
//
// All builders return the canonical (component, nodeID, objectID,
// payload, ok) shape consumed by [Bridge.publishDiscovery].
//
// Every payload on this plane is rendered by the shared model's per-entity
// discovery form (ADR 0070): a [hamodel.Device] lifted from the block the
// plane composes, a [hamodel.Basic] entity carrying the description and its
// bindings, and [hadiscovery.RenderComponent] for the body — which attaches
// the device and origin blocks and omits `platform`. The builders below
// still own every identity string (unique id, node id, object id) and every
// topic, because Home Assistant has no migration path for any of them; see
// [hubDiscoveryContext] and [hubTopicLayout].

// DiscoveryItem packages a built HA Discovery message so it can be
// passed as a single value (Go doesn't spread multi-return into a
// function call). `OK=false` is a quietly-skipped no-op when handed
// to [Bridge.PublishHubDiscovery].
type DiscoveryItem struct {
	Component string
	NodeID    string
	ObjectID  string
	Payload   []byte
	OK        bool
}

// HubSysvarSpec is the narrow read-side contract on a sysvar that the
// discovery builder needs. Mirrors the fields a sysvar would carry
// regardless of where it lives in the model layer — the bridge stays
// free of the `internal/model/hub` import.
type HubSysvarSpec struct {
	Name string
	// Vid is the CCU-internal numeric variable id (ReGa ise_id). It is the
	// sysvar's identity for the HA `unique_id`, because the display name is
	// not one: [routingkey.HubSlug] collapses punctuation, so "Alarm: Küche"
	// and "Alarm Küche" — two different variables an operator may well have —
	// slug to the same string and produce byte-identical unique_ids. Home
	// Assistant keeps whichever config arrived first and silently drops the
	// other, and because the discovery payload is retained the loss outlives
	// the daemon that caused it.
	//
	// Zero means the id was not resolved; the builder then falls back to the
	// slug rather than emitting an entity keyed on 0.
	Vid         int
	Description string
	Unit        string
	ValueList   []string
	ValueType   hmenum.HubValueType
	// Writable reports whether the daemon holds a write path for the
	// sysvar. It is a safety gate only — the HA component selection is
	// keyed on IsExtended (see [DefaultDiscoveryBuilder.BuildSysvarDiscovery]).
	Writable bool
	// IsExtended marks a sysvar whose ReGa description carries the
	// extended-sysvar marker. The reference stack renders only extended
	// sysvars as writable HA entities (switch / select / number / text);
	// everything else is a read-only sensor or binary_sensor.
	IsExtended bool
	// EnabledDefault carries the marker-derived enabled-by-default flag
	// into HA's `enabled_by_default` registry hint: the entity registry
	// entry is created disabled unless the sysvar's CCU description
	// matched a configured marker token. HA applies the hint only when
	// the entity is first added, so an operator's later enable/disable
	// choice sticks. Mirrors the reference stack's entity-registry
	// default for hub data points.
	EnabledDefault bool
	Min            *float64
	Max            *float64
	// DeviceAddress, when non-empty, is the physical CCU device this sysvar is
	// linked to because its name carries the device's (or one of its channels')
	// identifier. It moves the HA entity from the synthetic central hub card
	// onto that device's card (see [hubEntityDeviceBlock]). Empty for an
	// unlinked, hub-level sysvar.
	DeviceAddress string
}

// HubInfo carries the optional CCU metadata that enriches the synthetic
// HA device block for hub entities. All fields are optional — zero
// values fall back to static defaults. Populate via [DefaultDiscoveryBuilder.WithHubInfo]
// after querying the CCU's getVersion / system.getSystemInfo call.
type HubInfo struct {
	// Name overrides the central identifier as the HA device name.
	Name string
	// Model overrides the default "HomeMatic Central" model string.
	Model string
	// Version is included as `sw_version` when non-empty.
	Version string
	// Serial is included as `serial_number` when non-empty.
	Serial string
	// URL is included as `configuration_url` when non-empty.
	URL string
}

// hubDeviceBlock builds the synthetic HA `device` block that groups
// every hub entity belonging to one central. Identifier matches the
// `<central>_<addr>` shape we use elsewhere; here `<addr>` is the
// literal "central" so HA can distinguish the hub-device card from
// the per-physical-device cards.
// When info carries non-zero fields they override the static defaults.
func hubDeviceBlock(centralName string, info HubInfo) *hadiscovery.DeviceInfo {
	name := centralName
	if info.Name != "" {
		name = info.Name
	}
	model := "HomeMatic Central"
	if info.Model != "" {
		model = info.Model
	}
	return &hadiscovery.DeviceInfo{
		Identifiers:  []string{centralDeviceIdentifier(centralName)},
		Name:         name,
		Manufacturer: "eQ-3",
		Model:        model,
		// The three below stay absent when the CCU has not reported them;
		// their `omitempty` tags carry the conditional the map form spelled
		// out, and Home Assistant renders "Unknown" for an absent field
		// rather than the empty string a present one would show.
		SWVersion:        info.Version,
		SerialNumber:     info.Serial,
		ConfigurationURL: info.URL,
	}
}

// hubEntityDeviceBlock chooses the HA `device` block for a hub entity (sysvar
// or program). When deviceAddress is set the entity is linked to a physical
// CCU device — carrying its name/channel identifier — so the block references
// that device (by the shared [physicalDeviceIdentifier]) instead of the
// synthetic central hub card. HA then merges the entity into the physical
// device's card, inheriting the name/model/via_device the per-DP discovery
// already published for it; only `identifiers` (plus `via_device` for the
// device-not-yet-published case) is needed here. When deviceAddress is empty
// the entity stays on the central hub card via [hubDeviceBlock].
//
// This is the north-bound consumer of the sysvar-to-device association
// (the Python reference's `model/hub/data_point.py:84` via channel.device).
func hubEntityDeviceBlock(centralName, deviceAddress string, info HubInfo) *hadiscovery.DeviceInfo {
	if deviceAddress == "" {
		return hubDeviceBlock(centralName, info)
	}
	return &hadiscovery.DeviceInfo{
		Identifiers: []string{physicalDeviceIdentifier(centralName, deviceAddress)},
		ViaDevice:   centralDeviceIdentifier(centralName),
	}
}

func hubAvailability(t *TopicBuilder) []hadiscovery.AvailabilityEntry {
	return []hadiscovery.AvailabilityEntry{
		{
			Topic:               t.BridgeStatus(),
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
		},
	}
}

func hubNodeID(centralName, kind string) string {
	return safeLower(centralName) + "_" + kind
}

// safeLower is the package-local spelling of the shared discovery slug
// [naming.DiscoverySlug]. Hub node ids, object ids and identifier
// fields, per-device node ids and the retained-config orphan sweep all
// go through that one function, so a central name can never appear
// under two different discovery spellings.
func safeLower(s string) string {
	return naming.DiscoverySlug(s)
}

// hubSerial returns the per-central serial discriminator for hub
// unique_ids and whether one is available. Hub-entity unique_ids embed
// the serial suffix to disambiguate identical slots across CCUs
// (`loom_<serial10>_alarm_messages`); without a serial two centrals
// would collide on the SAME unique_id (`loom__alarm_messages`) and HA
// silently discards the duplicate. Callers MUST skip the discovery
// publish when ok=false — the daemon re-publishes the hub plane once
// the CCU's serial has been registered via [Bridge.SetHubInfoFor].
func (d *DefaultDiscoveryBuilder) hubSerial(centralName string) (serial10 string, ok bool) {
	s := d.serialSuffix(centralName)
	return s, s != ""
}

// ------------------- Shared model (ADR 0070) ----------------------

// hubTopicLayout renders this plane's topics from the strings its builders
// already compose, so the render pipeline produces exactly what is retained
// on the broker rather than a second spelling of it.
//
// The slot arguments are unused. A hub entity names one datapoint whose
// topic [naming] and [TopicBuilder] are the authority on; deriving it from
// the slot again would be a second implementation of the same schema with
// nothing keeping the two in step.
type hubTopicLayout struct {
	state   string
	command string
	// device is the second availability source a program role may declare.
	// Empty for every other hub entity, which is gated by the bridge alone.
	device string
	bridge string
	// ccu is the per-CCU reachability gate `<base>/<central>/hub/status`,
	// the availability source every CCU-scoped hub entity carries ALONGSIDE
	// the bridge one. Rendered by [hubDiscoveryContext.Availability] rather
	// than by a level, because the model's three levels are already spoken
	// for here — bridge, and device for the program-role gate — and a hub
	// entity's "device" is the synthetic central card, which has no
	// reachability topic of its own to borrow.
	ccu string
}

// State implements the shared model's topic layout.
func (l hubTopicLayout) State(hamodel.Slot) string { return l.state }

// Command implements the shared model's topic layout.
func (l hubTopicLayout) Command(hamodel.Slot) string { return l.command }

// Availability implements the shared model's topic layout: the per-entity
// gate a program role declares, resolved as [hamodel.LevelDevice].
func (l hubTopicLayout) Availability(hamodel.Slot) string { return l.device }

// Bridge implements the shared model's topic layout.
func (l hubTopicLayout) Bridge() string { return l.bridge }

// hubDiscoveryContext is the render context for this plane: the standard one
// with this daemon's identity strings substituted.
//
// Both are overridden because Home Assistant has no migration path for
// either. The unique id is central-scoped by the CCU's SERIAL while the node
// id is a slug of the central's NAME, so no single derivation produces both
// — and the default would derive the node id from the device identifier,
// which for a device-linked sysvar is a physical device card rather than the
// `<central>_sysvars` segment the retained configs actually live under.
type hubDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
	// ccuStatus is the per-CCU reachability gate appended to every gated
	// hub entity's availability list. Empty suppresses the append.
	ccuStatus string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes.
func (c hubDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string { return c.uniqueID }

// NodeID implements [hadiscovery.Context].
func (c hubDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// Availability implements [hadiscovery.Context], appending the per-CCU
// reachability gate to whatever the standard resolution produced.
//
// Home Assistant's default `availability_mode: "all"` is a conjunction over
// the whole list, so appending is exactly the semantics wanted: an entity is
// available when the daemon is up AND its CCU is on the bus. The existing
// `bridge/status` entry is kept rather than replaced, because the two say
// different things and neither implies the other — a dead daemon publishes
// nothing about its CCUs, and a live daemon with a dead CCU says nothing
// about itself.
//
// It appends rather than declaring a fourth [hamodel.AvailabilityLevel]
// because the model's levels are resolved against a slot and a device, and a
// hub entity's device is the synthetic central card: it has no reachability
// topic of its own for [hamodel.LevelDevice] to render, and that level is
// already spent on the program-role gate. The gate is a property of the
// RENDER CONTEXT — one topic per CCU, identical for every entity of that CCU
// — which is where it is put.
//
// Two cases are skipped, and both are skipped on purpose:
//
//   - An entity that resolved to NO availability sources at all. That is
//     [hamodel.NoAvailability], which this plane uses for exactly one entity,
//     the daemon-status sensor, whose whole point is to stay visible when
//     everything else has gone quiet. Giving it a source here would undo
//     that, and "an entity that declares no gate acquires none" is a rule
//     that needs no per-entity list to maintain.
//   - An entity marked [hubEntity.selfReports] — the per-interface
//     connectivity sensors, which are the fold's own inputs.
func (c hubDiscoveryContext) Availability(dev *hamodel.Device, e hamodel.Entity) []hadiscovery.AvailabilityEntry {
	entries := c.StdContext.Availability(dev, e)
	if c.ccuStatus == "" || len(entries) == 0 {
		return entries
	}
	return append(entries, hadiscovery.AvailabilityEntry{
		Topic:               c.ccuStatus,
		PayloadAvailable:    hadiscovery.PayloadOnline,
		PayloadNotAvailable: hadiscovery.PayloadOffline,
	})
}

// ObjectID implements [hadiscovery.Context]. This plane DOES publish an
// entity-id seed, and the seed is the UNIQUE ID rather than the discovery
// topic's object-id segment: `default_entity_id` has always been
// `<component>.<unique_id>` here, and Home Assistant derives the entity id
// from it once and will not rename afterwards. The pipeline prefixes the
// platform itself.
func (c hubDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return c.uniqueID }

// hubEntity is any hub entity on the shared model: a [hamodel.Basic] plus
// the handful of keys the model does not carry.
//
// The platform Fields are Home Assistant vocabulary rather than model
// semantics, which is the case [hadiscovery.Builder] exists for.
type hubEntity struct {
	hamodel.Basic

	fields any
	// selfReports marks an entity whose own STATE is the CCU-reachability
	// signal the per-CCU gate is folded from. Such an entity is the one
	// place an operator reads why a CCU went quiet, so it must not be gated
	// on the answer it is reporting — pointing its availability at the fold
	// makes it unavailable in exactly the situation it exists for, which is
	// the same reasoning [DefaultDiscoveryBuilder.BuildDaemonStatusDiscovery]
	// applies one level up, against `bridge/status`.
	selfReports bool
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *hubEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	if e.fields != nil {
		comp.Fields = e.fields
	}
	return nil
}

// hubSlot is the coordinate of one hub datapoint.
//
// [hamodel.BucketUnset] because a hub datapoint has no paramset: a system
// variable, a program or a central-wide metric is not on a channel and has
// no configuration/runtime distinction to make. The context renders every
// topic from [hubTopicLayout], so the slot's job is to be a stable
// coordinate carrying the read and write modes the pipeline projects the
// two topics from.
func hubSlot(dev *hamodel.Device, centralName string, path ...string) hamodel.Slot {
	slot := hamodel.S(dev.UID(), "", hamodel.BucketUnset, path...)
	if centralName != "" {
		slot = slot.In(centralName)
	}
	return slot
}

// hubBinds is the binding list for a hub entity: a state role when the
// entity reads, a command role when Home Assistant can write it. Both name
// the same slot — the render pipeline resolves the two roles separately, so
// a single ReadWrite binding on one role would project only one topic.
func hubBinds(slot hamodel.Slot, reads, writes bool) []hamodel.Binding {
	binds := make([]hamodel.Binding, 0, 2)
	if reads {
		binds = append(binds, hamodel.Binding{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: slot})
	}
	if writes {
		binds = append(binds, hamodel.Binding{Role: hamodel.RoleCommand, Mode: hamodel.Write, Slot: slot})
	}
	return binds
}

// renderHubItem renders one hub entity through the shared per-entity
// pipeline and packages it as the item the publishers take.
//
// [hadiscovery.RawEncoding] is not a preference: hub state topics carry the
// bare value, not the `{"value":…}` envelope the datapoint planes publish,
// so an entity rendered with the envelope's value template would read its
// state through a filter that never matches and show as unknown forever.
func (d *DefaultDiscoveryBuilder) renderHubItem(
	dev *hamodel.Device, e *hubEntity, layout hubTopicLayout, uniqueID, nodeID, objectID string,
) DiscoveryItem {
	if dev == nil {
		return DiscoveryItem{}
	}
	ccu := layout.ccu
	if e.selfReports {
		ccu = ""
	}
	ctx := hubDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout:     layout,
			Lang:       d.Locale,
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID:  uniqueID,
		nodeID:    nodeID,
		ccuStatus: ccu,
	}
	comp, err := hadiscovery.RenderComponent(ctx, dev, e, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	return discoveryItemFor(comp, nodeID, objectID)
}

// hubLayout is the topic layout every central-scoped hub entity renders
// under: one state topic and the daemon's own LWT as the sole availability
// source.
func (d *DefaultDiscoveryBuilder) hubLayout(centralName, stateTopic string) hubTopicLayout {
	return hubTopicLayout{
		state:  stateTopic,
		bridge: d.TopicBuilder.BridgeStatus(),
		ccu:    d.TopicBuilder.HubStatus(centralName),
	}
}

// ----------------------------- Sysvar -----------------------------

// BuildSysvarDiscovery emits the HA Discovery payload for one sysvar.
// Component selection mirrors the reference stack's mapping, which is
// keyed on the extended-sysvar marker (a ReGa-description flag), NOT
// on writability:
//
// - LOGIC, ALARM → switch (extended) / binary_sensor (read-only;
// ALARM adds the problem device_class)
// - LIST → select (extended) / sensor with enum options
// - STRING → text (extended) / sensor
// - NUMBER, FLOAT, INTEGER → number (extended) / sensor
//
// A spec that is extended but carries no write path (Writable=false)
// falls back to the read-only shape so HA never renders a control
// whose commands would fail.
//
// The stable HA `unique_id` is `loom_<serial10>_sysvar_<ise_id>` — see
// [sysvarUniqueID] for why it is keyed on the numeric id rather than the
// name. Skipped (OK=false) until the central's serial is known — see
// [DefaultDiscoveryBuilder.hubSerial].
func (d *DefaultDiscoveryBuilder) BuildSysvarDiscovery(centralName string, sv HubSysvarSpec) DiscoveryItem { //nolint:funlen,gocognit // single-purpose sysvar discovery builder with many type branches
	if sv.Name == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	dev := modelDeviceFromInfo(hubEntityDeviceBlock(centralName, sv.DeviceAddress, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	var component string
	stateTopic := naming.MQTTHubSysvarState(d.BridgeBase, centralName, sv.Name)
	commandTopic := naming.MQTTHubSysvarCommand(d.BridgeBase, centralName, sv.Name)
	uniqueID := sysvarUniqueID(serial10, sv)
	objectID := safeLower(sv.Name)

	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey: objectID,
			Description: hamodel.Description{
				Name:         hamodel.L(displaySysvarName(sv)),
				Enabled:      hamodel.Ptr(sv.EnabledDefault),
				Availability: hamodel.BridgeOnly(),
			},
		},
	}
	desc := &entity.Description
	writes := false

	// `editable` selects the writable HA surface. The reference stack
	// keys the component on the extended-sysvar marker alone; Writable
	// is ANDed in as a daemon-side safety so an extended sysvar without
	// a write path still renders read-only.
	editable := sv.IsExtended && sv.Writable

	switch sv.ValueType {
	case hmenum.HubValueTypeLogic, hmenum.HubValueTypeAlarm:
		if editable {
			component = string(HAComponentSwitch)
			writes = true
			desc.Optimistic = hamodel.Ptr(false)
			entity.fields = hadiscovery.SwitchFields{
				PayloadOn: "true", PayloadOff: "false",
				StateOn: "true", StateOff: "false",
			}
		} else {
			component = string(HAComponentBinarySensor)
			entity.fields = hadiscovery.BinarySensorFields{PayloadOn: "true", PayloadOff: "false"}
			if sv.ValueType == hmenum.HubValueTypeAlarm {
				desc.DeviceClass = "problem"
			}
		}
	case hmenum.HubValueTypeList:
		if editable && len(sv.ValueList) > 0 {
			component = string(HAComponentSelect)
			writes = true
			// The CCU's own value list is both the code and the label: the
			// daemon publishes and accepts the raw tokens, so an [hamodel.Enum]
			// without labels renders them verbatim.
			desc.Options = &hamodel.Enum{Codes: append([]string(nil), sv.ValueList...)}
			desc.Optimistic = hamodel.Ptr(false)
			desc.Category = EntityCategoryConfig
		} else {
			component = string(HAComponentSensor)
			if len(sv.ValueList) > 0 {
				desc.DeviceClass = "enum"
				desc.Options = &hamodel.Enum{Codes: append([]string(nil), sv.ValueList...)}
			}
		}
	case hmenum.HubValueTypeString:
		if editable {
			// Extended string sysvars are operator-declared HA inputs —
			// render them writable like the reference stack does.
			component = string(HAComponentText)
			writes = true
			desc.Optimistic = hamodel.Ptr(false)
			entity.fields = hadiscovery.TextFields{Mode: "text"}
		} else {
			// HA's `text` entity caps state payloads at 255 chars and warns
			// loudly on every overrun. CCU string sysvars (e.g.
			// `AlleServicemeldungen`) routinely exceed that with multi-line
			// service-message dumps, so non-extended string sysvars render
			// as a read-only `sensor` — no length cap, no truncation, no
			// inbound-write surface from HA. Operators who need to write a
			// string sysvar still have the CCU UI and our REST API.
			component = string(HAComponentSensor)
		}
	case hmenum.HubValueTypeNumber, hmenum.HubValueTypeFloat, hmenum.HubValueTypeInteger:
		if editable {
			component = string(HAComponentNumber)
			writes = true
			desc.Optimistic = hamodel.Ptr(false)
			if sv.ValueType == hmenum.HubValueTypeInteger {
				entity.fields = hadiscovery.NumberFields{Mode: "box"}
				desc.Step = hamodel.Ptr(float64(1))
			} else {
				entity.fields = hadiscovery.NumberFields{Mode: "auto"}
				desc.Step = hamodel.Ptr(0.01)
			}
		} else {
			component = string(HAComponentSensor)
			desc.StateClass = hacatalog.StateClassMeasurement
		}
		// HA's `number` entity defaults to min=1, max=100 when the
		// discovery payload omits these fields — which is what
		// triggered the "Invalid value … (range 0.0 - 100.0)" warnings
		// for energy / sunshine counters delivering 10 ⁶+ readings.
		// Send a wide fallback range whenever the model does not carry
		// a declared bound; the sensor path is unaffected because HA
		// sensors have no min/max contract — and the render pipeline
		// now enforces that, projecting `min`/`max`/`step` only onto the
		// platforms whose schema declares them.
		switch {
		case sv.Min != nil:
			desc.Min = hamodel.Ptr(*sv.Min)
		case editable:
			desc.Min = hamodel.Ptr(-1e9)
		}
		switch {
		case sv.Max != nil:
			desc.Max = hamodel.Ptr(*sv.Max)
		case editable:
			desc.Max = hamodel.Ptr(1e9)
		}
		desc.Unit = hamodel.Unit(sv.Unit)
	default:
		// Unknown value-type; surface as a plain sensor so the data
		// is at least visible — better than dropping the entity.
		component = string(HAComponentSensor)
	}

	// CCU-auto-generated counter sysvars (svEnergyCounter, svHmIPRainCounter,
	// svHmIPSunshineCounter and their FeedIn/Today/Yesterday variants) carry a
	// machine-token name (`svEnergyCounter_<ise_id>_<addr>:<ch>`). Give them the
	// friendly localized name plus the energy/rain/sunshine sensor semantics HA
	// needs — a cumulative `total_increasing` counter that feeds long-term
	// statistics — so the entity reads e.g. "Energiezähler Gesamt" (and its
	// entity_id follows) instead of the raw token. The stable unique_id is
	// untouched. Sensor-only: these are read-only numeric counters. Mirrors the
	// reference HA integration's hub entity-description rules.
	if component == string(HAComponentSensor) {
		if cls, ok := classifyAutoSysvar(sv.Name); ok {
			// The literal name wins over the catalogue key in the shared
			// model, so the machine token has to be cleared rather than just
			// overridden — otherwise `svEnergyCounter_1234_ABC0123456:6`
			// would stay the display name and the key would never be read.
			desc.Name = hamodel.Localized{}
			desc.NameKey = cls.translationKey
			desc.StateClass = hacatalog.StateClass(cls.stateClass)
			if cls.deviceClass != "" {
				desc.DeviceClass = hamodel.DeviceClass(cls.deviceClass)
			}
			if cls.unit != "" {
				desc.Unit = hamodel.Unit(cls.unit)
			}
		}
	}

	entity.EntityPlatform = hacatalog.Platform(component)
	entity.Binds = hubBinds(hubSlot(dev, centralName, "sysvar", sv.Name), true, writes)
	layout := d.hubLayout(centralName, stateTopic)
	layout.command = commandTopic
	return d.renderHubItem(dev, entity, layout, uniqueID, hubNodeID(centralName, "sysvars"), objectID)
}

func displaySysvarName(sv HubSysvarSpec) string {
	if sv.Description != "" {
		return sv.Description
	}
	return sv.Name
}

// ----------------------------- Program ----------------------------

// HubProgramSpec is the narrow read-side contract on a CCU program that
// the discovery builder needs — mirrors [HubSysvarSpec] so the bridge
// stays free of the `internal/model/hub` import.
type HubProgramSpec struct {
	ID   string
	Name string
	// DeviceAddress, when non-empty, links the program to a physical CCU
	// device (see [hubEntityDeviceBlock]). Empty for an unlinked,
	// hub-level program.
	DeviceAddress string
	// EnabledDefault carries the marker-derived enabled-by-default flag
	// into HA's `enabled_by_default` registry hint (see
	// [HubSysvarSpec.EnabledDefault]).
	EnabledDefault bool
}

// BuildProgramDiscoveryRoles emits the discovery payloads for one CCU program.
//
// Which controls a program surfaces, and on which topics, is declared by
// the model ([payload.MQTTRoleAddressable]) — this function transcribes
// that declaration into HA discovery bodies and adds nothing of its own.
// A program that declares no roles falls back to the single-switch shape,
// so a source that is one control needs to say nothing.
func (d *DefaultDiscoveryBuilder) BuildProgramDiscoveryRoles(
	centralName string, p HubProgramSpec, roles []payload.MQTTRole,
) []DiscoveryItem {
	if len(roles) == 0 {
		item := d.BuildProgramDiscovery(centralName, p)
		if !item.OK {
			return nil
		}
		return []DiscoveryItem{item}
	}
	out := make([]DiscoveryItem, 0, len(roles))
	for i := range roles {
		if item := d.buildProgramRole(centralName, p, &roles[i]); item.OK {
			out = append(out, item)
		}
	}
	return out
}

// buildProgramRole renders one declared role. The role supplies the
// component, the topics and the availability gate; everything else is the
// program's shared identity.
func (d *DefaultDiscoveryBuilder) buildProgramRole(
	centralName string, p HubProgramSpec, role *payload.MQTTRole,
) DiscoveryItem {
	serial10, ok := d.hubSerial(centralName)
	if !ok || p.ID == "" || role.Component == "" {
		return DiscoveryItem{}
	}
	uniqueID := routingkey.CanonicalUniqueID(serial10, "program", programUniqueSlug(p), "")
	objectID := safeLower(p.ID)
	displayName := p.Name
	if displayName == "" {
		displayName = p.ID
	}
	if role.Key != "" {
		// A secondary control needs an identity of its own; the principal
		// role keeps the one the program always had.
		uniqueID += "_" + role.Key
		objectID += "_" + safeLower(role.Key)
	}
	if role.NameSuffix != "" {
		displayName += " " + role.NameSuffix
	}

	dev := modelDeviceFromInfo(hubEntityDeviceBlock(centralName, p.DeviceAddress, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}

	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      objectID,
			EntityPlatform: hacatalog.Platform(role.Component),
			Description: hamodel.Description{
				Name:         hamodel.L(displayName),
				Enabled:      hamodel.Ptr(p.EnabledDefault),
				Availability: hamodel.BridgeOnly(),
			},
		},
	}
	layout := d.hubLayout(centralName, role.Topics.State)
	if role.Topics.Availability != "" {
		// A role that declares its own gate adds it as the model's
		// [hamodel.LevelDevice] source — the default level pair is bridge
		// plus device, so clearing the BridgeOnly restriction is what picks
		// both up, in that order. availability_mode stays "all", the model's
		// default, so every listed topic must report online.
		layout.device = role.Topics.Availability
		entity.Description.Availability = hamodel.Availability{}
	}
	if role.Topics.State != "" {
		entity.Description.Optimistic = hamodel.Ptr(false)
	}
	// The command shape decides the platform's own keys, and the two roles a
	// program declares are disjoint: the switch carries State plus Set, the
	// execute button carries Trigger alone. That is why state_on/state_off
	// belong on the switch branch — mqtt.button declares neither, so a button
	// carrying them would have had both dropped on receipt.
	switch {
	case role.Topics.Set != "":
		layout.command = role.Topics.Set
		entity.fields = hadiscovery.SwitchFields{
			PayloadOn: "true", PayloadOff: "false",
			StateOn: "true", StateOff: "false",
		}
	case role.Topics.Trigger != "":
		layout.command = role.Topics.Trigger
		entity.fields = hadiscovery.ButtonFields{PayloadPress: "true"}
	}
	entity.Binds = hubBinds(hubSlot(dev, centralName, "program", p.ID, role.Component),
		role.Topics.State != "", layout.command != "")
	return d.renderHubItem(dev, entity, layout, uniqueID, hubNodeID(centralName, "programs"), objectID)
}

// BuildProgramDiscovery emits one HA `switch` per CCU program.
// `turn_on` triggers the program (write to /trigger); state reflects
// the most recent execution active flag.
//
// Deprecated: kept for sources that declare no roles. Prefer
// [DefaultDiscoveryBuilder.BuildProgramDiscoveryRoles].
func (d *DefaultDiscoveryBuilder) BuildProgramDiscovery(centralName string, p HubProgramSpec) DiscoveryItem {
	if p.ID == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	stateTopic := naming.MQTTHubProgramState(d.BridgeBase, centralName, p.ID)
	commandTopic := naming.MQTTHubProgramTrigger(d.BridgeBase, centralName, p.ID)
	uniqueID := routingkey.CanonicalUniqueID(serial10, "program", programUniqueSlug(p), "")
	displayName := p.Name
	if displayName == "" {
		displayName = p.ID
	}
	dev := modelDeviceFromInfo(hubEntityDeviceBlock(centralName, p.DeviceAddress, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	objectID := safeLower(p.ID)
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      objectID,
			EntityPlatform: hacatalog.PlatformSwitch,
			Description: hamodel.Description{
				Name:         hamodel.L(displayName),
				Enabled:      hamodel.Ptr(p.EnabledDefault),
				Availability: hamodel.BridgeOnly(),
				Optimistic:   hamodel.Ptr(false),
			},
			Binds: hubBinds(hubSlot(dev, centralName, "program", p.ID), true, true),
		},
		fields: hadiscovery.SwitchFields{
			PayloadOn:  "true",
			PayloadOff: "false",
			StateOn:    "true",
			StateOff:   "false",
		},
	}
	layout := d.hubLayout(centralName, stateTopic)
	layout.command = commandTopic
	return d.renderHubItem(dev, entity, layout, uniqueID, hubNodeID(centralName, "programs"), objectID)
}

// ------------------- AlarmMessages / ServiceMessages -------------

// BuildAlarmMessagesDiscovery exposes the CCU alarm-message list as a HA
// `sensor` whose value is the message count and whose `json_attributes_topic`
// carries the full list. `device_class: problem` belongs to binary_sensor and
// would be rejected on a sensor entity, so it is intentionally omitted.
func (d *DefaultDiscoveryBuilder) BuildAlarmMessagesDiscovery(centralName string) DiscoveryItem {
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubAlarmMessages(d.BridgeBase, centralName)
	uniqueID := hubAggregateUniqueID(serial10, "alarm_messages")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "alarm_messages",
			EntityPlatform: hacatalog.PlatformSensor,
			Description: hamodel.Description{
				NameKey:      "discovery.alarm_messages",
				StateClass:   "measurement",
				Category:     "diagnostic",
				Availability: hamodel.BridgeOnly(),
				// The state topic carries the message LIST, not a scalar, so
				// the count is read out of it by template. That is a per-entity
				// answer the context's encoding cannot give.
				ValueTemplate:          "{{ value_json | length }}",
				JSONAttributesTopic:    topic,
				JSONAttributesTemplate: `{"messages": {{ value_json | tojson }} }`,
			},
			Binds: hubBinds(hubSlot(dev, centralName, "alarm_messages"), true, false),
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID, hubNodeID(centralName, "messages"), "alarm")
}

// BuildServiceMessagesDiscovery is the maintenance-list counterpart
// to [BuildAlarmMessagesDiscovery]. Diagnostic category, no
// device_class (HA shows a neutral icon).
func (d *DefaultDiscoveryBuilder) BuildServiceMessagesDiscovery(centralName string) DiscoveryItem {
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubServiceMessages(d.BridgeBase, centralName)
	uniqueID := hubAggregateUniqueID(serial10, "service_messages")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "service_messages",
			EntityPlatform: hacatalog.PlatformSensor,
			Description: hamodel.Description{
				NameKey:                "discovery.service_messages",
				Category:               "diagnostic",
				Availability:           hamodel.BridgeOnly(),
				ValueTemplate:          "{{ value_json | length }}",
				JSONAttributesTopic:    topic,
				JSONAttributesTemplate: `{"messages": {{ value_json | tojson }} }`,
			},
			Binds: hubBinds(hubSlot(dev, centralName, "service_messages"), true, false),
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID, hubNodeID(centralName, "messages"), "service")
}

// ------------------------- Inbox ----------------------------------

// BuildInboxDiscovery exposes the pending-device inbox count as a HA
// sensor. The state topic carries the full inbox list; a
// value_template extracts the count. A json_attributes_topic exposes
// the raw device list for automations that need the details.
// Mirrors the inbox sensor in the hub model (translation_key="inbox",
// state_class="measurement", enabled_default=True).
func (d *DefaultDiscoveryBuilder) BuildInboxDiscovery(centralName string) DiscoveryItem {
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubInbox(d.BridgeBase, centralName)
	uniqueID := hubAggregateUniqueID(serial10, "inbox")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "inbox",
			EntityPlatform: hacatalog.PlatformSensor,
			Description: hamodel.Description{
				NameKey:                "discovery.inbox",
				StateClass:             "measurement",
				Icon:                   "mdi:tray-arrow-down",
				Availability:           hamodel.BridgeOnly(),
				ValueTemplate:          "{{ value_json | length }}",
				JSONAttributesTopic:    topic,
				JSONAttributesTemplate: `{"devices": {{ value_json | tojson }} }`,
			},
			Binds: hubBinds(hubSlot(dev, centralName, "inbox"), true, false),
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID, hubNodeID(centralName, "messages"), "inbox")
}

// ----------------------- InstallMode ------------------------------

// installModeInterfaceSuffix maps a CCU interface identifier to the
// short suffix used for per-interface install-mode entities: "hmip" for
// HmIP-RF and "bidcos" for BidCos-RF, matching the reference registry
// entity names (`install_mode_hmip`, `install_mode_bidcos`). The suffix
// feeds the unique_id slot, the object_id and the friendly-name infix.
//
// Every other pairing-capable interface — BidCos-Wired above all — gets
// its own slot instead of falling into the "bidcos" bucket. The state
// and command topics are built per interface, so two interfaces sharing
// a suffix share one retained discovery topic and one unique_id while
// declaring two different topic sets: only the payload published last
// survives, and its pairing button opens the window on whichever bus
// won that race.
func installModeInterfaceSuffix(iface string) string {
	switch {
	case strings.EqualFold(iface, "HmIP-RF"):
		return "hmip"
	case strings.EqualFold(iface, "BidCos-RF"):
		return "bidcos"
	default:
		return safeLower(iface)
	}
}

// installModeInterfaceLabel returns the human-readable interface label
// used in the friendly name ("HmIP-RF" / "BidCos-RF"). Falls back to the
// raw interface id for interface families with no canonical short label.
func installModeInterfaceLabel(iface string) string {
	switch {
	case strings.EqualFold(iface, "HmIP-RF"):
		return "HmIP-RF"
	case strings.EqualFold(iface, "BidCos-RF"):
		return "BidCos-RF"
	default:
		return iface
	}
}

// BuildInstallModeSensorDiscovery is the remaining-seconds counter for
// the CCU's pairing-install mode on one interface. The reference stack
// renders one sensor per interface (`install_mode_hmip`,
// `install_mode_bidcos`) rather than a single central-wide aggregate.
// Surfaced as a `sensor` (not a number) because the value is read-only —
// activation happens through the paired button (see
// [DefaultDiscoveryBuilder.BuildInstallModeButtonDiscovery]).
func (d *DefaultDiscoveryBuilder) BuildInstallModeSensorDiscovery(centralName, iface string) DiscoveryItem {
	if iface == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	suffix := installModeInterfaceSuffix(iface)
	topic := naming.MQTTHubInstallModeForInterface(d.BridgeBase, centralName, iface)
	uniqueID := routingkey.CanonicalUniqueID(serial10, "install_mode", suffix, "")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "install_mode_" + suffix,
			EntityPlatform: hacatalog.PlatformSensor,
			Description: hamodel.Description{
				NameKey:      "discovery.install_mode_duration",
				NameArgs:     map[string]string{"iface": installModeInterfaceLabel(iface)},
				DeviceClass:  "duration",
				Unit:         "s",
				StateClass:   "measurement",
				Category:     "diagnostic",
				Availability: hamodel.BridgeOnly(),
				// translation_key is the cross-stack parity marker Home
				// Assistant declares nowhere and drops on receipt; see
				// discoveryKeysHomeAssistantIgnores. It has no typed home in
				// the shared model for exactly that reason.
				Extra: map[string]any{"translation_key": "install_mode_" + suffix},
			},
			Binds: hubBinds(hubSlot(dev, centralName, "install_mode", suffix), true, false),
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID,
		hubNodeID(centralName, "central"), "install_mode_"+suffix)
}

// BuildInstallModeButtonDiscovery emits the HA `button` that activates
// install/pairing mode on one interface. The reference stack pairs each
// per-interface remaining-seconds sensor with a button
// (`install_mode_hmip-button`, `install_mode_bidcos-button`); HA
// publishes the press token to the command topic and the command
// subscriber translates it into a POST install-mode for the interface.
func (d *DefaultDiscoveryBuilder) BuildInstallModeButtonDiscovery(centralName, iface string) DiscoveryItem {
	if iface == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	suffix := installModeInterfaceSuffix(iface)
	commandTopic := naming.MQTTHubInstallModeCommand(d.BridgeBase, centralName, iface)
	// The reference unique_id slugifies the "<suffix>_button" parameter
	// to "<suffix>-button"; mirror that exact shape so the loom button
	// lines up with the reference registry (`install_mode_hmip-button`).
	uniqueID := routingkey.CanonicalUniqueID(serial10, "install_mode", suffix+"-button", "")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "install_mode_" + suffix + "_button",
			EntityPlatform: hacatalog.PlatformButton,
			Description: hamodel.Description{
				NameKey:      "discovery.install_mode_activate",
				NameArgs:     map[string]string{"iface": installModeInterfaceLabel(iface)},
				Category:     EntityCategoryConfig,
				Availability: hamodel.BridgeOnly(),
				// translation_key is the cross-stack parity marker Home
				// Assistant declares nowhere and drops on receipt; see
				// discoveryKeysHomeAssistantIgnores.
				Extra: map[string]any{"translation_key": "install_mode_" + suffix + "_button"},
			},
			// The button reads nothing and Home Assistant's button platform
			// declares no `state_topic`; one writable binding is the whole
			// entity.
			Binds: hubBinds(hubSlot(dev, centralName, "install_mode", suffix), false, true),
		},
		fields: hadiscovery.ButtonFields{PayloadPress: "PRESS"},
	}
	layout := d.hubLayout(centralName, "")
	layout.command = commandTopic
	return d.renderHubItem(dev, entity, layout, uniqueID,
		hubNodeID(centralName, "central"), "install_mode_"+suffix+"_button")
}

// ---------------------- Connectivity ------------------------------

// BuildConnectivityDiscovery is one HA `binary_sensor` per
// CCU-interface (HmIP-RF, BidCos-RF, …). `device_class: connectivity`
// flips the icon between connected / disconnected.
func (d *DefaultDiscoveryBuilder) BuildConnectivityDiscovery(centralName, iface string) DiscoveryItem {
	if iface == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubConnectivity(d.BridgeBase, centralName, iface)
	uniqueID := hubAggregateUniqueID(serial10, "connectivity_"+safeLower(iface))
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "connectivity_" + safeLower(iface),
			EntityPlatform: hacatalog.PlatformBinarySensor,
			Description: hamodel.Description{
				NameKey:     "discovery.connectivity",
				NameArgs:    map[string]string{"iface": iface},
				DeviceClass: "connectivity",
				Category:    "diagnostic",
				// Bridge only, and no per-CCU gate either (see
				// [hubEntity.selfReports] below). This sensor's whole job is
				// to report that an interface is down, so gating it on that
				// interface — or on the fold its own state feeds — would make
				// it unavailable in the one situation it exists for.
				Availability: hamodel.BridgeOnly(),
			},
			Binds: hubBinds(hubSlot(dev, centralName, "connectivity", iface), true, false),
		},
		fields: hadiscovery.BinarySensorFields{
			PayloadOn:  "true",
			PayloadOff: "false",
		},
		// This sensor's state is an input to the per-CCU reachability fold,
		// so it takes no availability from it — see [hubEntity.selfReports].
		selfReports: true,
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID,
		hubNodeID(centralName, "connectivity"), safeLower(iface))
}

// BuildDaemonStatusDiscovery is the HA `binary_sensor` that reports
// whether the daemon itself is reachable, read straight off the retained
// bridge status topic the broker also carries the last will on.
//
// That topic existed from the start but only ever appeared inside other
// entities' `availability` blocks, where it can express "this entity's
// data is stale" and nothing else. A daemon that goes away — a CCU reboot,
// an add-on restart, a killed process — therefore left every entity
// `unavailable` with no entity anywhere saying why, and nothing an
// automation could act on. This is that entity.
//
// It deliberately carries NO availability block, which is the one thing
// separating it from every other hub entity here: pointing an entity's
// availability at the same topic it reads its state from makes it go
// `unavailable` in exactly the situation it exists to report, so the
// disconnect would once again be visible only as an absence.
func (d *DefaultDiscoveryBuilder) BuildDaemonStatusDiscovery(centralName string) DiscoveryItem {
	if centralName == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	uniqueID := hubAggregateUniqueID(serial10, "daemon_status")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "daemon_status",
			EntityPlatform: hacatalog.PlatformBinarySensor,
			Description: hamodel.Description{
				NameKey:     "discovery.daemon_status",
				DeviceClass: "connectivity",
				Category:    "diagnostic",
				Enabled:     hamodel.Ptr(true),
				// The absence IS the feature — see this builder's doc
				// comment. [hamodel.NoAvailability] is the model's way to
				// say it: [hamodel.LevelNone] suppresses the `availability`
				// list and `availability_mode` together, where an empty
				// level list would still resolve to the default pair.
				Availability: hamodel.NoAvailability(),
			},
			Binds: hubBinds(hubSlot(dev, centralName, "daemon_status"), true, false),
		},
		fields: hadiscovery.BinarySensorFields{
			PayloadOn:  "online",
			PayloadOff: "offline",
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, d.TopicBuilder.BridgeStatus()), uniqueID,
		hubNodeID(centralName, "system"), "daemon_status")
}

// ----------------------- System-Health / Latency ------------------

// BuildSystemHealthDiscovery emits a HA `sensor` for the openccu-loom
// system-health score (0–100). The score is published on
// `<base>/<central>/system/health_score`. Mirrors the entry in
// `hubDescriptionsByKind["system_health"]` (entity_descriptions.go:404).
func (d *DefaultDiscoveryBuilder) BuildSystemHealthDiscovery(centralName string) DiscoveryItem {
	if centralName == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	// Reference parity: the canonical hub sensor is "system_health"
	// (translation_key system_health, German "Systemzustand"). The
	// earlier loom slug "system_health_score" diverged from the reference
	// uid/translation. The retained STATE topic stays at
	// `/system/health_score` (publisher contract unchanged).
	uniqueID := hubAggregateUniqueID(serial10, "system_health")
	topic := d.TopicBuilder.HubSystemHealthScore(centralName)
	return d.hubMetricItem(centralName, uniqueID, "system_health", topic, hamodel.Description{
		NameKey:      "discovery.system_health",
		Unit:         "%",
		StateClass:   "measurement",
		Category:     "diagnostic",
		Icon:         "mdi:heart-pulse",
		Precision:    hamodel.Ptr(1),
		Enabled:      hamodel.Ptr(true),
		Availability: hamodel.BridgeOnly(),
		Extra:        map[string]any{"translation_key": "system_health"},
	})
}

// hubMetricItem renders one central-wide diagnostic sensor. The three
// system metrics differ only in their description, so the entity, the
// context and the node id are assembled once here rather than three times.
func (d *DefaultDiscoveryBuilder) hubMetricItem(
	centralName, uniqueID, key, stateTopic string, desc hamodel.Description,
) DiscoveryItem {
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      key,
			EntityPlatform: hacatalog.PlatformSensor,
			Description:    desc,
			Binds:          hubBinds(hubSlot(dev, centralName, key), true, false),
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, stateTopic), uniqueID,
		hubNodeID(centralName, "system"), key)
}

// BuildConnectionLatencyDiscovery emits a single aggregated HA `sensor`
// (duration, ms) for the CCU's round-trip latency. The reference stack
// exposes ONE central-wide connection-latency sensor
// (`<central>_hub_connection-latency`, translation_key connection_latency)
// derived from the aggregated ping/pong metric — not one sensor per
// interface. The measurement topic is `<base>/<central>/system/latency`.
func (d *DefaultDiscoveryBuilder) BuildConnectionLatencyDiscovery(centralName string) DiscoveryItem {
	if centralName == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	uniqueID := hubAggregateUniqueID(serial10, "connection_latency")
	topic := d.TopicBuilder.HubConnectionLatency(centralName)
	return d.hubMetricItem(centralName, uniqueID, "connection_latency", topic, hamodel.Description{
		NameKey:      "discovery.connection_latency",
		Unit:         "ms",
		StateClass:   "measurement",
		Category:     "diagnostic",
		Icon:         "mdi:timer",
		Precision:    hamodel.Ptr(1),
		Enabled:      hamodel.Ptr(true),
		Availability: hamodel.BridgeOnly(),
		Extra:        map[string]any{"translation_key": "connection_latency"},
	})
}

// ----------------------- Last-Event-Age --------------------------

// BuildLastEventAgeDiscovery emits a HA `sensor` (duration, s) for the
// age of the newest backend event — a liveness signal for the CCU
// connection. The measurement topic is
// `<base>/<central>/system/last_event_age`. Reference parity:
// hub_last-event-age (translation_key last_event_age, German "Alter
// letztes Ereignis").
func (d *DefaultDiscoveryBuilder) BuildLastEventAgeDiscovery(centralName string) DiscoveryItem {
	if centralName == "" {
		return DiscoveryItem{}
	}
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	uniqueID := hubAggregateUniqueID(serial10, "last_event_age")
	topic := d.TopicBuilder.HubLastEventAge(centralName)
	return d.hubMetricItem(centralName, uniqueID, "last_event_age", topic, hamodel.Description{
		NameKey:      "discovery.last_event_age",
		DeviceClass:  "duration",
		Unit:         "s",
		StateClass:   "measurement",
		Category:     "diagnostic",
		Icon:         "mdi:clock-alert-outline",
		Precision:    hamodel.Ptr(1),
		Enabled:      hamodel.Ptr(true),
		Availability: hamodel.BridgeOnly(),
		Extra:        map[string]any{"translation_key": "last_event_age"},
	})
}

// ----------------------- System Update ---------------------------

// BuildHubUpdateDiscovery exposes the CCU's firmware-update state as
// a HA `update` entity. The state topic carries a JSON object with
// `installed_version`, `latest_version`, and `in_progress` fields.
// Mirrors the hub update entity (Category=HubUpdate,
// translation_key="update", enabled_default=True).
func (d *DefaultDiscoveryBuilder) BuildHubUpdateDiscovery(centralName string) DiscoveryItem {
	serial10, ok := d.hubSerial(centralName)
	if !ok {
		return DiscoveryItem{}
	}
	topic := naming.MQTTHubUpdate(d.BridgeBase, centralName)
	// Reference parity: the hub firmware-update entity is "system_update"
	// (hub_system-update). The bare "update" slug collided conceptually
	// with per-device firmware-update entities (loom_<addr>_update) so HA
	// rendered a "_2"-suffixed entity_id when names matched. Scope the
	// uid/object_id to "system_update".
	uniqueID := hubAggregateUniqueID(serial10, "system_update")
	dev := modelDeviceFromInfo(hubDeviceBlock(centralName, d.hubFor(centralName)))
	if dev == nil {
		return DiscoveryItem{}
	}
	entity := &hubEntity{
		Basic: hamodel.Basic{
			EntityKey:      "system_update",
			EntityPlatform: hacatalog.PlatformUpdate,
			Description: hamodel.Description{
				NameKey:      "discovery.system_update",
				Category:     "diagnostic",
				Enabled:      hamodel.Ptr(true),
				Availability: hamodel.BridgeOnly(),
				// No `value_template`: HA's MQTT update platform parses the
				// raw state_topic payload natively against its state-payload
				// schema (installed_version, latest_version, in_progress)
				// when no value_template narrows it to a scalar first.
				// `in_progress_template` is not a schema option at all — HA
				// reads `in_progress` only from that native parse — so
				// setting either one here left the entity showing no
				// install-in-progress indication.
				ValueTemplate: hamodel.NoValueTemplate,
			},
			Binds: hubBinds(hubSlot(dev, centralName, "system_update"), true, false),
		},
		fields: hadiscovery.UpdateFields{
			LatestVersionTopic:    topic,
			LatestVersionTemplate: "{{ value_json.latest_version }}",
		},
	}
	return d.renderHubItem(dev, entity, d.hubLayout(centralName, topic), uniqueID,
		hubNodeID(centralName, "system"), "system_update")
}

// ----------------------- Bridge plumbing --------------------------

// PublishHubDiscovery is the Bridge-side helper that takes the
// builder output and pushes it to the broker through the same
// dedup-cache path as the per-DP discovery. An [DiscoveryItem] with
// `OK=false` is a quiet no-op — the caller can pipe builder output
// straight in without checking.
func (b *Bridge) PublishHubDiscovery(ctx context.Context, item DiscoveryItem) error {
	if !item.OK || !b.cfg.HADiscoveryEnabled {
		return nil
	}
	// DiscoveryItem carries no central — most hub builders fold it into
	// NodeID already, and the one daemon-level caller (the add-on
	// self-update entity) has none at all — so a publish_errors
	// increment from here goes unlabeled rather than guessing.
	return b.publishDiscovery(ctx, "", item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// sysvarUniqueID builds the HA `unique_id` for one system variable.
//
// It is keyed on the CCU's own numeric variable id, not on the display name.
// The name is not an identity: [routingkey.HubSlug] collapses punctuation and
// case, so two variables whose names differ only there — "Alarm: Küche" and
// "Alarm Küche" — produced byte-identical unique_ids. Home Assistant keeps the
// config that arrived first and drops the second variable's entity entirely,
// and since the discovery payload is retained on the broker, the loss survives
// a restart of the daemon that caused it. Nothing in the daemon noticed,
// because both variables published happily to their own distinct state topics;
// only the entity registry on the far side had one fewer row than it should.
//
// A sysvar whose id has not been resolved yet (Vid == 0, e.g. a spec built
// before the first hub scan) falls back to the slug. That is the pre-existing
// behaviour and can still collide, but an entity keyed on the literal 0 would
// collide with *every* other unresolved sysvar, which is worse.
// programUniqueSlug is the identity half of a program's unique_id.
//
// Keyed on the CCU program id, for the reason the sysvar id is: the name is
// editable in the WebUI, and a key built from it re-keys the consumer's
// entity on every rename — taking its history, its area and every automation
// with it. The name slug stands in only while the id is unresolved.
//
// It mirrors [hub.Program.CanonicalUniqueID], which serves the same key on
// the REST/WS plane; TestHubProgramUniqueIDMatchesAcrossPlanes pins the two
// against each other.
func programUniqueSlug(p HubProgramSpec) string {
	if slug := routingkey.HubSlug(p.ID); slug != "" {
		return slug
	}
	return routingkey.HubSlug(p.Name)
}

func sysvarUniqueID(serial10 string, sv HubSysvarSpec) string {
	if sv.Vid > 0 {
		return routingkey.CanonicalUniqueID(serial10, "sysvar", strconv.Itoa(sv.Vid), "")
	}
	return routingkey.CanonicalUniqueID(serial10, "sysvar", routingkey.HubSlug(sv.Name), "")
}
