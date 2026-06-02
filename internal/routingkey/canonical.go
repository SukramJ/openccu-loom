// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// SerialSuffix returns the per-CCU discriminator: the last
// [serialSuffixLen] characters of the CCU serial, lower-cased. Serials
// shorter than that are returned whole. Empty in, empty out.
//
// This feeds the central-id slot of [CanonicalUniqueID] for the address
// classes whose addresses repeat across CCUs (hub roots, INT000*,
// virtual remotes); normal device serials are globally unique and carry
// no prefix.
func SerialSuffix(serial string) string {
	serial = strings.ToLower(serial)
	if len(serial) <= serialSuffixLen {
		return serial
	}
	return serial[len(serial)-serialSuffixLen:]
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
