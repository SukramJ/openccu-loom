// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strconv"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// The per-parameter discovery plane on the shared model (ADR 0070). It is
// the last and the largest of the eleven, and the only one whose payload is
// not finished when the render is: the Quantity tables, the HA registry
// rules and [dropForeignDeviceClass] all run afterwards and all of them read
// each other's verdicts, so the render produces the FRAME — device, origin,
// unique_id, name, the two topics, the availability list, the json-attribute
// pair and the envelope's value template — and the decoration chain in
// [DefaultDiscoveryBuilder.Build] finishes the body.
//
// What can be declared before the render is declared here, in
// [perDatapointVocabulary]. What is left in Build's own switch is exactly
// what keys on a value the chain resolves.

// perDatapointLayout renders this plane's topics from the strings
// [naming.PathData] and [TopicBuilder] already composed, so the render
// pipeline produces exactly what is retained on the broker rather than a
// second spelling of it.
//
// The slot arguments are unused for the same reason the other planes ignore
// theirs: one call builds one entity, whose four topics are already resolved
// by the authorities on this daemon's topic schema. Re-deriving them from
// the slot would be a second implementation of that schema with nothing
// keeping the two in step.
type perDatapointLayout struct {
	state   string
	command string
	device  string
	bridge  string
}

// State implements the shared model's topic layout.
func (l perDatapointLayout) State(hamodel.Slot) string { return l.state }

// Command implements the shared model's topic layout.
func (l perDatapointLayout) Command(hamodel.Slot) string { return l.command }

// Availability implements the shared model's topic layout with the owning
// device's reachability topic, resolved as [hamodel.LevelDevice].
func (l perDatapointLayout) Availability(hamodel.Slot) string { return l.device }

// Bridge implements the shared model's topic layout with the daemon's own
// LWT, resolved as [hamodel.LevelBridge].
func (l perDatapointLayout) Bridge() string { return l.bridge }

// perDatapointContext is the render context for this plane: the standard one
// with this daemon's identity strings substituted.
//
// All three are overridden, and Home Assistant has no migration path for any
// of them. The unique id is [routingkey.CanonicalUniqueID] — the one identity
// the REST and WebSocket planes spell the same way, which no derivation from
// the device block could reproduce. The node id is a slug of the central plus
// the device address, which for a sub-device split is deliberately NOT the
// device identifier the block carries. And the object id is EMPTY: this plane
// has never published a `default_entity_id`, and publishing one now would
// seed an entity id where Home Assistant currently derives its own — a rename
// of every per-parameter entity on every fleet at once.
type perDatapointContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes.
func (c perDatapointContext) UniqueID(*hamodel.Device, hamodel.Entity) string { return c.uniqueID }

// NodeID implements [hadiscovery.Context].
func (c perDatapointContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id` — see [perDatapointContext].
func (c perDatapointContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// perDatapointEntity is one per-parameter entity on the shared model: a
// [hamodel.Basic] plus the four things the description does not carry.
//
// Fields and command_template are Home Assistant vocabulary for one platform
// rather than model semantics, which is the case [hadiscovery.Builder] exists
// for. name:null is a Component-level statement — the difference between "no
// name" and "the device's name alone" — that the model has no place for. The
// fourth is the schema-gap restamp; see [perDatapointEntity.BuildDiscovery].
type perDatapointEntity struct {
	hamodel.Basic

	fields          any
	commandTemplate string
	nameNull        bool
	// comp exists only for the restamp below.
	comp HAComponent
}

// BuildDiscovery implements [hadiscovery.Builder].
//
// The restamp at the end is an escape hatch and is meant to read as one. It
// covers exactly one key on exactly one platform, and only because the
// projection rule and the Home Assistant schema disagree about it.
//
// `climate` declares `value_template` (go-ha-catalog v0.2.1, Home Assistant
// 2026.9.1) but declares no `state_topic` — it names a topic per role
// instead. The render pipeline projects `value_template` only inside the
// branch that projects `state_topic`, so a platform with the one key and not
// the other never receives it. Home Assistant would accept and keep the key,
// which makes dropping it a change an installed instance can see, so it is
// stamped back rather than lost to a projection rule it does not share.
//
// The three keys that used to be restamped alongside it — `state_topic` on
// climate, `value_template` on light and on siren — were the opposite case:
// undeclared on those platforms, dropped on receipt with no error on the
// wire and no log line, and so removed.
func (e *perDatapointEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	if e.fields != nil {
		comp.Fields = e.fields
	}
	if e.commandTemplate != "" {
		comp.CommandTemplate = e.commandTemplate
	}
	comp.NameNull = e.nameNull

	if e.comp == HAComponentClimate {
		comp.ValueTemplate = e.Description.ValueTemplate
	}
	return nil
}

// perDatapointSlot is the coordinate of one wire data point: the device
// address, its channel number, the paramset bucket the state topic lives
// under and the parameter name, inside the central and the wire interface.
//
// It is a complete coordinate even though [perDatapointLayout] renders the
// topics from strings resolved elsewhere — the render pipeline carries the
// slot into the availability and self-availability resolution, and a slot
// that addressed nothing would be a coordinate nobody could follow back.
func perDatapointSlot(ev Event, centralName string, bucket payload.Bucket) hamodel.Slot {
	slot := hamodel.S(ev.DeviceAddress, strconv.Itoa(ev.ChannelNo), bucket, ev.Parameter)
	scope := make([]string, 0, 2)
	// An empty scope segment would silently vanish from a rendered topic and
	// move the data point into another device's tree, so a missing central or
	// interface shortens the scope rather than punching a hole in it.
	for _, s := range []string{centralName, ev.Interface} {
		if s != "" {
			scope = append(scope, s)
		}
	}
	return slot.In(scope...)
}

// perDatapointBinds is the binding list for one per-parameter entity: a state
// role always, a command role when the component is one Home Assistant can
// write. Both name the same slot — the render pipeline resolves the two roles
// separately, so a single ReadWrite binding on one role would project only
// one topic.
func perDatapointBinds(slot hamodel.Slot, writable bool) []hamodel.Binding {
	binds := make([]hamodel.Binding, 0, 2)
	binds = append(binds, hamodel.Binding{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: slot})
	if writable {
		binds = append(binds, hamodel.Binding{Role: hamodel.RoleCommand, Mode: hamodel.Write, Slot: slot})
	}
	return binds
}

// perDatapointDeclaration is everything one component declares about itself
// before the decoration chain runs — the half of the old per-component switch
// that reads nothing the chain produces.
type perDatapointDeclaration struct {
	// fields is the platform's own vocabulary, carried through the Builder.
	fields any
	// valueTemplate and commandTemplate are the Jinja pair. The state side
	// goes onto the description, which the render pipeline projects only onto
	// the platforms that declare the key; the command side has no description
	// field and travels through the Builder.
	valueTemplate   string
	commandTemplate string
	// options is the enum vocabulary of a select.
	options []string
	// optimistic is projected onto the platforms whose schema declares it.
	optimistic *bool
	// min, max and step are the bounds of a number or a text entity. The
	// number's are seeded here and scaled by the multiplier afterwards.
	min, max, step *float64
	// writable declares the command binding.
	writable bool
}

// perDatapointVocabulary resolves what comp declares about itself.
//
// Everything here is a function of the event and the component alone, which
// is what makes it expressible as a [hamodel.Description] and a
// [hadiscovery.Builder] instead of a patch applied to a rendered body. The
// cases that are NOT — an enum sensor's options, either multiplier, the
// action-select category, the event device class, the motion binary sensor's
// force_update pair — key on a verdict the decoration chain produces and stay
// in [DefaultDiscoveryBuilder.Build]'s own switch.
func perDatapointVocabulary(ev Event, comp HAComponent) perDatapointDeclaration { //nolint:gocognit,funlen // one arm per Home Assistant platform this plane emits
	// The envelope's extractor, with the boolean-aware `| lower` variant for
	// the components that compare the rendered value against on/off tokens.
	// The `event` arm below replaces it outright; the platforms whose schema
	// declares no `value_template` never receive it.
	decl := perDatapointDeclaration{valueTemplate: jsonValueTemplate(comp)}

	switch comp { //nolint:exhaustive // climate / valve / siren / update declare nothing of their own here
	case HAComponentSwitch:
		// Writable boolean — switch carries the on/off payload contract.
		// binary_sensor is intentionally NOT in this case-arm: it is
		// read-only and `command_topic` / `state_on` / `state_off` are
		// switch-only fields.
		//
		// The PerDPState envelope carries the value as a JSON boolean
		// (`{"value":true,...}`). Jinja renders a Python boolean as
		// `True`/`False`, which would never match `state_on`/`state_off`
		// ("true"/"false") and would leave every switch stuck in `unknown`;
		// [jsonValueTemplate] pipes it through `| lower` for exactly that.
		decl.writable = true
		decl.fields = hadiscovery.SwitchFields{
			PayloadOn: "true", PayloadOff: "false",
			StateOn: "true", StateOff: "false",
		}
		// optimistic=false — without an explicit value HA defaults to true
		// and applies state changes locally before the CCU echoes them back.
		// Critical for switches, where a brief CCU outage would otherwise
		// leave HA showing the wrong state.
		decl.optimistic = hadiscovery.Ptr(false)
	case HAComponentLock:
		// Lock uses HA's lock-specific payload contract: `payload_lock` /
		// `payload_unlock` on the command topic, `state_locked` /
		// `state_unlocked` against the rendered value_template. Mirrors the
		// custom-DP aggregated lock path so the per-parameter and aggregated
		// discovery surfaces emit the same shape.
		//
		// Wire mapping for `LOCK_TARGET_LEVEL` matches `CustomDpLock`
		// semantics: `0.0` = locked, `1.0` = unlocked. `LOCK_STATE` is a
		// numeric enum (0=unknown, 1=locked, 2=unlocked) surfaced verbatim.
		decl.writable = true
		decl.optimistic = hadiscovery.Ptr(false)
		decl.fields = hadiscovery.LockFields{
			PayloadLock: "0", PayloadUnlock: "1",
			StateLocked: "0", StateUnlocked: "1",
		}
	case HAComponentBinarySensor:
		// Read-only — no command_topic / state_on / state_off. The declared
		// payloads must be what the state plane actually renders for THIS
		// descriptor; see [binarySensorPayloads].
		var binaryFields hadiscovery.BinarySensorFields
		binaryFields.PayloadOff, binaryFields.PayloadOn = binarySensorPayloads(ev)
		decl.fields = binaryFields
	case HAComponentSensor:
		// force_update ensures HA re-evaluates the state (advancing
		// last_changed) even when the value has not changed — useful for
		// periodic heartbeat-style sensors.
		//
		// NOTE on expire_after: deliberately NOT set. Availability is
		// governed by the reachability model — the per-device UNREACH topic
		// ([EventBridge.markAvailability]) plus each DP's `available` flag in
		// the slot-state envelope — not by value freshness. Many sensors
		// update far less than hourly (battery devices, OPERATING_VOLTAGE)
		// and a not-yet-observed sensor publishes
		// `{"value":null,"available":true}` and never receives a value until
		// the CCU pushes one; an `expire_after=3600` would falsely mark all
		// of those `unavailable` after an hour of inactivity even though the
		// device is perfectly reachable. This mirrors the binary_sensor case.
		decl.fields = hadiscovery.SensorFields{ForceUpdate: hadiscovery.Ptr(true)}
	case HAComponentLight, HAComponentCover:
		decl.writable = true
		decl.optimistic = hadiscovery.Ptr(false)
	case HAComponentNumber:
		decl.writable = true
		decl.optimistic = hadiscovery.Ptr(false)
		// Seed the wire-descriptor bounds here — [applyMultiplierNumber] only
		// scales values already present. Without the seed HA receives the
		// default range (0..100, step 1) regardless of the actual CCU bounds.
		decl.min, decl.max = ev.descMin(), ev.descMax()
		// step: mirrors the Python reference implementation's
		// `_attr_native_step = 1.0 if hmtype==INTEGER else 0.01 * multiplier`
		// (`number.py:235`). The wire ParameterData carries Type=INTEGER for
		// discrete parameters; default to 0.01 otherwise. The multiplier
		// scaling applies the `* multiplier` portion afterwards.
		if isIntegerParameter(ev) {
			decl.step = hadiscovery.Ptr(1.0)
		} else {
			decl.step = hadiscovery.Ptr(0.01)
		}
		// mode = "slider" when the range is small enough for a drag-bar to
		// feel useful; "box" otherwise. No bounds means no mode, and with no
		// mode the platform declares no fields at all.
		if mn, mx := ev.descMin(), ev.descMax(); mn != nil && mx != nil {
			mode := "box"
			if (*mx - *mn) <= 1000 {
				mode = "slider"
			}
			decl.fields = hadiscovery.NumberFields{Mode: mode}
		}
	case HAComponentSelect:
		decl.writable = true
		decl.optimistic = hadiscovery.Ptr(false)
		// HA `select` requires `options`; without it HA rejects the discovery
		// payload outright. Source: paramset descriptor's VALUE_LIST (e.g.
		// `SET_POINT_MODE` → ["AUTO_MODE", "MANU_MODE", "PARTY_MODE",
		// "BOOST_MODE"]).
		//
		// The reference stack lowercases select options and the current
		// option ("MANU_MODE" → "manu_mode") so they are translatable in HA,
		// and maps the chosen option back to its uppercase CCU token on
		// write. Mirror that: lowercase options, `| lower` on the state
		// template, `| upper` on the command template so the CCU receives the
		// exact VALUE_LIST entry.
		if vl := ev.descValueList(); len(vl) > 0 {
			if labels, ok := localisedEnumOptions(ev); ok {
				decl.options = labels
				decl.valueTemplate, decl.commandTemplate = enumOptionTemplates(vl, ev.descValueLabels())
			} else {
				decl.options = lowercasedOptions(vl)
				decl.valueTemplate = valueJSONValueLowerTemplate
				decl.commandTemplate = "{{ value | upper }}"
			}
		}
	case HAComponentButton:
		// payload_press="PRESS" mirrors the Python reference implementation's
		// button.py — without it HA sends an empty string on every button
		// press, which the CCU rejects.
		//
		// A button is stateless, and HA's mqtt.button declares neither
		// `state_topic` nor `value_template`. The render pipeline drops both
		// on its own now, which is what this plane used to clear by hand.
		decl.writable = true
		decl.fields = hadiscovery.ButtonFields{PayloadPress: "PRESS"}
	case HAComponentText:
		// HA `text` is a writable, free-form string — HmIP-WRCD display text
		// and similar. Its bounds are whole numbers of characters, so the
		// fractional part of a wire descriptor is truncated rather than
		// rounded.
		decl.writable = true
		decl.optimistic = hadiscovery.Ptr(false)
		decl.fields = hadiscovery.TextFields{Mode: "text"}
		if mn := ev.descMin(); mn != nil {
			decl.min = hadiscovery.Ptr(float64(int(*mn)))
		}
		if mx := ev.descMax(); mx != nil {
			decl.max = hadiscovery.Ptr(float64(int(*mx)))
		}
	case HAComponentEvent:
		// Press-type event entities: HA requires `event_types` listing all
		// press variants the channel can fire. The state topic receives
		// `{"event_type":"press_short"}` payloads when the button fires.
		//
		// HA's mqtt.event component parses the *post-value_template* payload
		// as JSON and reads `event_type` from it. The envelope extractor
		// yields a scalar — that breaks the JSON parsing and floods the HA
		// log with `No valid JSON event payload detected`. [hamodel.NoValueTemplate]
		// is how the model says "publish none", which an empty string cannot:
		// that is also what "no opinion" looks like.
		decl.valueTemplate = hamodel.NoValueTemplate
		decl.fields = hadiscovery.EventFields{
			EventTypes: MapDoorbellEventTypes(ev.Model, pressEventTypesFor(ev.Parameter)),
		}
	}
	return decl
}
