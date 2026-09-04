// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package routingkey

import (
	"strconv"
	"strings"
)

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
// The marker is not decoration. The invariant it carries is that a
// calculated data point and a real VALUES parameter of the same name on the
// same channel must not produce the same key; without it they do.
//
// That the constraint is real and not theoretical is shown by any consumer
// that keys its entity registry on the key and migrates by exact string
// match: the Home Assistant drop-in rewrites the legacy key to
// `loom_calculated_…`, so a key emitted without the marker orphans the
// migrated entity and spawns a duplicate beside it.
const CalculatedFamilyPrefix = "calculated"

// EventGroupFamilyPrefix marks a device-trigger event group inside a
// routing key, ahead of the channel:
//
//	loom_event_group_<kind>_<channel>
//
// The layout is not [CanonicalUniqueID]'s. That helper puts the central-id
// slot in front of everything, which would yield
// `loom_<central>_event_group_<kind>_<channel>`; the reference builds
// `event_group_<kind>_<channel-unique-id>` and the central-id, where an
// address family needs one, lives inside that channel id. Consumers key
// their entity registry on the exact string, so the two spellings are not
// interchangeable.
const EventGroupFamilyPrefix = "event_group"

// EventGroupUniqueID builds the external unique_id for a device-trigger
// event group: the loom namespace, the family prefix, the short kind, then
// the channel-level routing key.
//
//	loom_event_group_keypress_vcu1234567_1
//	loom_event_group_keypress_11a0001234_bidcos_rf_1   (virtual remote)
//
// shortKind is the kind without its `homematic.` prefix — `keypress`,
// `impulse`, `device_error`.
func EventGroupUniqueID(centralID, channelAddress, shortKind string) string {
	if channelAddress == "" || shortKind == "" {
		return ""
	}
	channel := GenerateChannelUniqueID(centralID, channelAddress)
	if channel == "" {
		return ""
	}
	return strings.ToLower(loomNamespace + "_" + EventGroupFamilyPrefix + "_" + shortKind + "_" + channel)
}

// CalculatedUniqueID builds the external unique_id for a calculated data
// point. It is [CanonicalUniqueID] with [CalculatedFamilyPrefix] in the
// family slot, and exists so every producer - REST, WebSocket and MQTT
// discovery - spells the family the same way instead of each passing the
// literal.
func CalculatedUniqueID(serialSuffix, address, parameter string) string {
	return CanonicalUniqueID(serialSuffix, address, parameter, CalculatedFamilyPrefix)
}

// ZoneSlugStem is the fallback stem [UniqueSlug] uses for an alarm zone whose
// name yields nothing sluggable. It sits here rather than in either caller so
// the two cannot fall back to different stems and hand one zone two
// identities.
const ZoneSlugStem = "zone"

// EffectiveSlug is the slug a stored row actually answers to: its stored
// value when it has one, otherwise the slug derived from its name, otherwise
// the fallback stem.
//
// A row whose stored slug is blank still resolves to something at read time,
// so it must reserve that something when a sibling is being named — and a
// name that slugs to nothing resolves to the stem, which therefore has to be
// reserved too. Skipping the blank case hands a new row the identity an
// existing one already answers to.
func EffectiveSlug(stored, name, stem string) string {
	if stored != "" {
		return stored
	}
	if s := HubSlug(name); s != "" {
		return s
	}
	return stem
}

// UniqueSlug derives a stable external identifier for name, unique against
// taken, by appending "-2", "-3", … on collision. When the name yields nothing
// sluggable — a zone named only with emoji — it falls back to stem, because
// the thing still needs an identifier and its UUID is unusable in an entity
// id, which is the reason a slug exists at all.
//
// The rule lives here because two packages need it and neither can import the
// other: the REST handler assigns a slug when a zone is created, and the
// security index re-derives the effective slug of a row whose stored slug is
// blank. Two copies of a collision rule do not stay equal by luck — they
// diverge on the first edge case one of them meets, and the result is two
// zones answering to one identity.
func UniqueSlug(name, stem string, taken map[string]bool) string {
	base := HubSlug(name)
	if base == "" {
		base = stem
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
}
