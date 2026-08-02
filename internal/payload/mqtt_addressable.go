// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

// MQTTTopicSet bundles the MQTT topics a single model object owns.
// Every field is optional — a read-only sysvar has Set == "", a
// non-bridgeable sensor has every field empty.
//
// The model layer fills this struct so north-bound adapters never
// hand-roll topic strings. See [MQTTAddressable].
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

	// Config is the descriptor-companion /config topic. Empty when
	// the object exposes no descriptor payload separate from State.
	Config string

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

// IsZero reports whether the topic set is fully empty.
func (t MQTTTopicSet) IsZero() bool {
	return t.State == "" && t.Set == "" && t.Trigger == "" && t.Config == "" && t.Availability == ""
}

// MQTTAddressable is implemented by model objects that own their MQTT
// topic layout. The bridge consumes the result directly — it never
// concatenates topic segments itself.
//
// `base` and `central` are bridge-runtime context (broker prefix and
// CCU name); they live on the bridge config, not on the model.
//
// Legacy mirror topics are an operations-level concern (gated by
// LegacyAliasConfig in north/mqtt) and intentionally NOT part of
// this interface — the model exposes truth, the bridge decides how
// many copies it ships.
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
