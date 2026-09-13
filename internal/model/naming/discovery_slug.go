// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import "github.com/SukramJ/go-hamqtt/topic"

// DiscoverySlug turns s into an HA-Discovery-safe identifier suitable
// for the `<node_id>` and `<object_id>` segments of
// `homeassistant/<component>/<node_id>/<object_id>/config` as well as
// the device-identifier fields in the payload. HA only accepts
// `[A-Za-z0-9_-]+` for these segments — `:`, umlauts, spaces, and other
// punctuation that CCU names routinely carry (`Watchdog:_CCU-Jack`,
// `s0_Sensoren_Hülle_EG`, …) get HA to drop the discovery message with
// a warning, so the entity never appears.
//
// It is the one normaliser for the identifiers that carry a central
// name: per-device node ids ([PathData.DiscoveryNodeID]), hub node ids,
// the device-block `identifiers`, and the retained-config orphan sweep
// that has to match them again. When a producer and the sweep disagree
// about the spelling of the same central, the sweep matches nothing and
// every retired entity keeps its retained config forever.
//
// # One rule, not two
//
// This is a delegation to `topic.Slug`, the shared model's rule, exactly
// as its sibling [TopicSafe] delegates to `topic.Safe`. It used to be a
// second implementation, and the two disagreed on eight of a 23-case
// probe in two classes — both of which were defects here rather than
// differences:
//
//   - non-German accented Latin was DROPPED rather than transliterated,
//     so `Café` and `Caf` collapsed to one node id `caf` and one
//     retained config. Now `cafe` and `caf`, two identities.
//   - a LITERAL `__` passed through where only generated underscore runs
//     collapsed, so `Watchdog:_CCU-Jack` — this function's own documented
//     example of a real CCU name — slugged to `watchdog__ccu-jack`. Now
//     `watchdog_ccu-jack`.
//
// Adopting the shared rule moved published node ids, object ids and
// device `identifiers` for the affected names; see ADR 0068's process and
// `docs/external-clients/ha-unique-id-migration.md`. It moved no
// `unique_id` and no `default_entity_id` — those are keyed on the CCU
// serial and the ISE id, not on a slugged name.
//
// The name is kept rather than the call sites rewritten because this is
// loom's vocabulary for "the discovery identifier normaliser", and the
// orphan sweep's doc comments refer to it as the single spelling
// authority. The retraction of the pre-unification spelling lives in
// `internal/north/mqtt`, not here — see `legacyDiscoverySlug`.
//
// Object ids on the per-datapoint plane do NOT go through it.
// [PathData.DiscoveryObjectID] takes the weaker [TopicSafe] route, which
// replaces only `+ # /` and space and leaves `:` and umlauts intact. That
// is safe exactly as long as object-id suffixes stay inside
// `[A-Za-z0-9_-]` — they are wire parameter names and component labels
// today.
//
// Rules (the shared rule's, verbatim):
//   - German umlauts and ß are transliterated (ü→ue, ö→oe, ä→ae, ß→ss),
//     and so is the rest of the accented Latin range (é→e, ç→c, ø→oe,
//     å→a, æ→ae, ñ→n, …), before the case fold.
//   - All remaining bytes outside `[a-z0-9-]` collapse to a single `_`;
//     runs are de-duplicated whether generated or literal; leading and
//     trailing `_` are trimmed. The hyphen survives.
//   - Empty input or input that reduces to "" returns "x" so callers
//     never emit a zero-length segment that HA would reject.
func DiscoverySlug(s string) string { return topic.Slug(s) }
