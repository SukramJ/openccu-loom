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
}

// IsZero reports whether the topic set is fully empty.
func (t MQTTTopicSet) IsZero() bool {
	return t.State == "" && t.Set == "" && t.Trigger == "" && t.Config == ""
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
