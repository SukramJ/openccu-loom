// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

// MQTTTopicSet bundles the MQTT topics a single model object owns.
// Every field is optional — a read-only sysvar has Set == "", a
// non-bridgeable sensor has every field empty.
//
// The model layer fills this struct so north-bound adapters never
// hand-roll topic strings. See [MQTTAddressable].
//
// It carries four kinds and not five: a `Config` field was declared here
// for a "descriptor-companion" topic that no implementation ever filled and
// no caller ever read. A declared-but-unwritten topic is worse than a
// missing one, because the next builder to see it fills it and the next
// reader never does.
type MQTTTopicSet struct {
	// State is the retained state topic. Empty for objects that have
	// no state to publish.
	State string

	// Set is the inbound /set topic (HA → daemon). Empty for
	// read-only objects.
	Set string

	// Trigger is the inbound /trigger topic used by CCU programs.
	// Empty for objects that do not expose a trigger semantic.
	Trigger string

	// Availability is the per-object availability topic the daemon
	// publishes online/offline on. Empty for objects whose availability
	// is fully covered by the bridge- and device-level topics.
	//
	// It exists for sources whose usability depends on their own state
	// rather than on reachability — a CCU program's execution is refused
	// while the program is deactivated, and the consumer should render
	// that control as unavailable rather than let it fail on use.
	Availability string
}

// MQTTAddressable is implemented by model objects that own their MQTT
// topic layout. The bridge consumes the result directly — it never
// concatenates topic segments itself.
//
// `base` and `central` are bridge-runtime context (broker prefix and
// CCU name); they live on the bridge config, not on the model.
//
// The model exposes truth and the bridge decides how many copies it
// ships, so a mirrored topology would be an operations-level concern
// and not part of this interface. None exists: the legacy-alias
// opt-in that was the standing example was never reachable and has
// been removed (see ADR 0006's amendment).
//
// # Why this is not a [hatopic.Layout]
//
// The shared module arranges the same seam the other way round: a model
// returns a [hamodel.Slot] coordinate and a layout renders it. That
// arrangement is right for the datapoint planes, where it is what this
// daemon already does, and it was measured against this interface as step D
// of ADR 0070's move-up. The decision is that this one keeps its shape, for
// four reasons that are properties of the hub plane rather than preferences:
//
//   - The experiment has already run here. `hubTopicLayout` in
//     internal/north/mqtt IS a [hatopic.Layout], the hub plane renders every
//     config through it, and every one of its slot-taking methods ignores
//     the slot and returns a string the builder composed. Removing this
//     interface relocates that composition; it does not remove it.
//   - A hub object needs four topic kinds and [hatopic.Layout] names two of
//     them. State and Command have a home; a program's `trigger` and its
//     per-role `execute_available` gate do not, and the shared module's own
//     [hatopic.PulseLayout] doc rules out a fifth Layout method because it
//     would break all six consumers at once. A slot arrangement would still
//     need a loom-local four-kind carrier — this struct, renamed.
//   - [hamodel.Slot] is device-shaped and a hub object is not a device.
//     Slot.Valid needs a non-empty Address and at least one Path segment; a
//     system variable has no device and no paramset, and
//     `<base>/<central>/hub/alarm_messages` has no leaf at all.
//   - The two runtime facts a hub topic needs — the broker base and the
//     resolved central — are parameters here, so the compiler refuses a call
//     that omits them. On a [hamodel.Slot] they live in Scope, which nothing
//     in this daemon fills; a slot built without it renders a short topic
//     and nothing catches it. That is measured, not hypothetical: see
//     `deviceAvailabilitySlot` in internal/north/mqtt, which exists to inject
//     the two segments on the way in.
//
// The cost of keeping it is that these four names stay in this package
// rather than resolving upwards, and that every topic on the hub plane has
// two spellings — the model's here and the discovery builder's in
// hub_discovery.go. Both go through internal/model/naming, and
// TestHubModelAndDiscoveryDeclareOneTopic pins them equal.
type MQTTAddressable interface {
	MQTTTopics(base, centralName string) MQTTTopicSet
}

// MQTTRole is one operator-facing control a source surfaces.
//
// Most sources are a single control and need none of this — a sysvar is
// one entity, a data point is one entity. Some are genuinely two: a CCU
// program has an activity flag the operator toggles and an execution the
// operator invokes, and the CCU treats them as separate things (a
// deactivated program refuses the execution).
//
// Which controls a source surfaces is model knowledge, so it is declared
// here rather than discovered by the bridge. See ADR 0011: "every fact
// about a source — what it is, what topics it owns, which HA component it
// surfaces as — is declared on the model object itself".
type MQTTRole struct {
	// Key distinguishes the role within its source and is appended to the
	// source's identity to keep each control separately addressable.
	// Empty marks the source's principal control, which keeps the
	// identity it had when it was the only one.
	Key string

	// Component is the consumer-facing control kind ("switch", "button",
	// …). The model decides; the bridge only transcribes it.
	Component string

	// Topics this role owns. A role that only accepts commands leaves
	// State empty; one that only reports leaves Set and Trigger empty.
	Topics MQTTTopicSet

	// NameSuffix distinguishes the role in a display name. Empty for the
	// principal role, which carries the source's plain name.
	NameSuffix string
}

// MQTTRoleAddressable is implemented by sources that surface more than
// one control. A source that does not implement it surfaces exactly one,
// described by [MQTTAddressable.MQTTTopics] — which is why this is a
// separate interface rather than a widened one: nothing that is a single
// control has to say so.
type MQTTRoleAddressable interface {
	MQTTAddressable

	// MQTTRoles returns every control the source surfaces, principal role
	// first. Returning fewer than two is legal and equivalent to not
	// implementing the interface.
	MQTTRoles(base, centralName string) []MQTTRole
}
