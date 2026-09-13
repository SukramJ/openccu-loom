// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import "strings"

// DefaultTopicBase is the `north.mqtt.topic_base` a daemon runs under when
// the operator sets none. It is duplicated from `config.applyDefaults`
// rather than imported because the model layer must not depend on the
// config package; [TestDiscoveryBaseScopeTracksTheConfigDefault] in
// `tests/contract` pins the two together, so a change to one fails on the
// other rather than silently splitting the fleet's node ids in half.
const DefaultTopicBase = "openccu-loom"

// DiscoveryBaseScope returns the `<base-slug>_` segment prefix that scopes a
// Home Assistant Discovery `node_id` to one daemon, or "" when the daemon
// runs under the default topic base.
//
// # Why the node id needs a scope at all
//
// `homeassistant/` is a single tree on a single broker, shared by every
// integration and every daemon that publishes into it. Everything else this
// daemon writes is namespaced by `north.mqtt.topic_base` — that is the whole
// point of the knob, and ADR 0006 rule 4 says the discovery node derives from
// it too. It did not: the node id was `<central-slug>_<address>` for device
// entities and a fixed literal (`alarm`, `security`, `daemon`) for the
// daemon-level planes, and neither reads the base. Two daemons that had been
// given distinct topic bases precisely so they would not collide still wrote
// byte-identical `homeassistant/.../config` topics, and the retained config
// the broker kept was whichever of them published last.
//
// The damage is not limited to the payload race. `RunDiscoveryOrphanCleanupOnce`
// decides what to retract from the node id alone — [publisher.ConfigTopic]
// carries no payload — so a shared node-id namespace makes each daemon's sweep
// judge the other daemon's live configs against its own claim set and retract
// every one it does not itself publish.
//
// # Why only a non-default base contributes
//
// A scope that always applied would move every retained config of every
// installation, single-daemon ones included, for a collision they cannot have.
// The base is what an operator sets to ask for a namespace of their own, so it
// is what earns one: a daemon on the default base keeps the node ids it has
// always written, and its upgrade is a no-op on this plane. The comparison is
// on the slug rather than the raw string so `OpenCCU-Loom` and `openccu-loom/`
// are the same default they look like.
//
// Two daemons that both leave the base at its default are not separated by
// this, and deliberately so: they already overwrite each other on every raw
// state topic, which is a misconfiguration the discovery plane cannot repair
// and must not paper over.
//
// # Known limitation: the scope and the central slug share a separator
//
// The scope is joined to the node id with `_`, and the node id's own first
// segment is the central slug, joined to the rest with `_` as well. The two
// boundaries are therefore indistinguishable: base `haus` with a CCU named
// `CCU` and the DEFAULT base with a CCU named `Haus CCU` both render
// `haus_ccu_<address>`. Two such daemons on one broker would collide exactly
// as they did before the scope existed.
//
// It is not fixed, and deliberately so. Disambiguating it means changing the
// separator (or escaping `_` inside the base slug), which moves the node id
// of every non-default-base installation a SECOND time, one release after
// the move that introduced the scope — a second unrecoverable migration for
// a collision that additionally requires the operator to have named their
// topic base and their CCU after the same thing. The cheap mitigation is
// documented instead: pick a topic base that is not a prefix of any CCU name
// on the broker. If a node-id move is ever required for another reason, the
// separator should be revisited in the same release rather than on its own.
func DiscoveryBaseScope(base string) string {
	slug := DiscoverySlug(strings.Trim(base, "/"))
	if slug == "" || slug == "x" || slug == DefaultTopicBase {
		return ""
	}
	return slug + "_"
}

// ScopedDiscoveryNodeID prefixes nodeID with [DiscoveryBaseScope] of base.
//
// It is the one place the two halves are joined, so the producers
// ([PathData.DiscoveryNodeID], the hub and daemon-level node ids) and the
// retained-config sweep that has to recognise the result again cannot drift
// into two spellings. An empty nodeID is returned untouched — a caller that
// has no node id must not be handed a bare scope that would match the whole
// namespace.
func ScopedDiscoveryNodeID(base, nodeID string) string {
	if nodeID == "" {
		return ""
	}
	return DiscoveryBaseScope(base) + nodeID
}

// ScopedDaemonUniqueID prefixes a daemon-level Home Assistant `unique_id`
// with [DiscoveryBaseScope] of base.
//
// # Why a unique id needs the scope at all, when the node id already has it
//
// Moving the discovery *topic* (which is what [ScopedDiscoveryNodeID] does)
// separates two daemons on the broker. It does not separate them inside Home
// Assistant. HA's MQTT integration keys its entity registry on `unique_id`
// and rejects a second config that declares one it has already seen —
// "Platform mqtt does not generate unique IDs" — so two daemons writing two
// DISTINCT config topics that carry the SAME `unique_id` are worse off than
// before the topics were separated: the semantics move from "last writer
// wins, and a restart repoints the entity at the live daemon" to "first
// writer wins permanently", and the second daemon's entities never appear.
//
// The three daemon-level planes (alarm, Security & Safety, add-on
// self-update) are the ones that need this. They carry no `<central>`
// segment (ADR 0052) and their ids are fixed literals — `loom_addon_update`,
// `openccu-loom_alarm_<zone>`, `loom_security_<key>` — with nothing in them
// that differs between two daemons. Every per-device and hub plane is already
// keyed on the CCU serial or the ISE id and is left alone.
//
// # Why only a non-default base contributes, again
//
// Re-keying a `unique_id` is the one move Home Assistant has no migration
// path for at the ENTITY level: history, long-term statistics, the entity id,
// renames, the area and every automation that names the entity are lost.
// Applying this unconditionally would charge that to every single-daemon
// installation in the fleet for a collision it cannot have. A non-default
// base is the operator saying "there is more than one of me", so it is the
// configuration that pays — and it is exactly the configuration that is
// broken today.
func ScopedDaemonUniqueID(base, uniqueID string) string {
	if uniqueID == "" {
		return ""
	}
	return DiscoveryBaseScope(base) + uniqueID
}
