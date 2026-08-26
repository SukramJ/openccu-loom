// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package routingkey

import "strings"

// loomNamespace is the constant prefix applied to every external
// unique_id. It segregates loom entities from other integrations'
// entities in a shared registry (most importantly on the MQTT plane,
// where loom entities mix with every other MQTT device).
const loomNamespace = "loom"

// serialSuffixLen is how many trailing characters of the CCU serial
// form the per-CCU discriminator for hub-level, internal, and
// virtual-remote keys. Ten characters mirror the legacy entry_id[-10:]
// width and stay comfortably collision-free across CCUs.
const serialSuffixLen = 10

// CanonicalSerial reduces a CCU serial to its canonical discriminator: the
// last [serialSuffixLen] characters, with the original case preserved. Serials
// shorter than that are returned whole. Empty in, empty out.
//
// This is the single source of truth for "the per-CCU serial identity". Every
// producer of a CCU serial — the ReGa `system.GetSerial` reader and SSDP/UPnP
// discovery — funnels through here so a CCU's runtime identity and its
// discovery identity are the same string (which is what lets discovery flag a
// configured central as already-configured by serial). Case is preserved so the
// stored / displayed serial matches what the CCU's own UI shows; callers that
// need a case-insensitive key (e.g. unique_id generation) use [SerialSuffix].
func CanonicalSerial(serial string) string {
	if len(serial) <= serialSuffixLen {
		return serial
	}
	return serial[len(serial)-serialSuffixLen:]
}

// SerialSuffix returns the per-CCU discriminator: [CanonicalSerial] lower-cased.
//
// This feeds the central-id slot of [CanonicalUniqueID] for the address
// classes whose addresses repeat across CCUs (hub roots, INT000*,
// virtual remotes); normal device serials are globally unique and carry
// no prefix.
func SerialSuffix(serial string) string {
	return strings.ToLower(CanonicalSerial(serial))
}

// CanonicalUniqueID builds the external, loom-namespaced unique_id:
//
//	loom_<routing-key>
//
// where the routing key is [GenerateUniqueID] with serialSuffix in the
// central-id slot. Devices come out unprefixed within the routing key
// (e.g. loom_vcu1234567_1_state); hub / internal / virtual-remote
// addresses carry the serial suffix (e.g.
// loom_<serial10>_sysvar_<hub-slug>).
//
// For hub data points the caller passes the pseudo-address
// ("sysvar" / "program" / "install_mode") and the [HubSlug]-ed name as
// the parameter.
func CanonicalUniqueID(serialSuffix, address, parameter, eventPrefix string) string {
	return loomNamespace + "_" + GenerateUniqueID(serialSuffix, address, parameter, eventPrefix)
}

// CalculatedFamilyPrefix marks the synthetic / calculated data-point family
// inside a routing key, ahead of the address:
//
//	loom_calculated_<device>_<channel>_<parameter>
//
// The marker is not decoration. A calculated data point and a real VALUES
// parameter of the same name on the same channel would otherwise produce
// the identical key, and consumers that key their entity registry on it -
// the Home Assistant drop-in above all - migrate by exact string match.
// Its migration rewrites the legacy key to `loom_calculated_…`, so a key
// emitted without the marker orphans the migrated entity and spawns a
// duplicate beside it.
const CalculatedFamilyPrefix = "calculated"

// CalculatedUniqueID builds the external unique_id for a calculated data
// point. It is [CanonicalUniqueID] with [CalculatedFamilyPrefix] in the
// family slot, and exists so every producer - REST, WebSocket and MQTT
// discovery - spells the family the same way instead of each passing the
// literal.
func CalculatedUniqueID(serialSuffix, address, parameter string) string {
	return CanonicalUniqueID(serialSuffix, address, parameter, CalculatedFamilyPrefix)
}
