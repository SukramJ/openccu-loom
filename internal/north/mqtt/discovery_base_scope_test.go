// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"slices"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// ownsFor builds the orphan sweep's ownership predicate for a bridge running
// under the given topic base, with both the hub plane and the daemon-level
// planes already declared — so what the predicate answers is about the node
// id, which is what these tests are about, and not about the declare gate,
// which has tests of its own.
func ownsFor(t *testing.T, base, central string) func(string) bool {
	t.Helper()
	b := &Bridge{topics: NewTopicBuilder(base)}
	b.MarkHubPlaneDeclared(central)
	b.MarkPlaneDeclared(alarmDiscoveryNodeID)
	b.MarkPlaneDeclared(securityDiscoveryNodeID)
	owns := b.ownsDiscoveryTopic(central)
	return func(nodeID string) bool {
		return owns(hapublisher.ConfigTopic{Platform: "sensor", NodeID: nodeID, ObjectID: "o"})
	}
}

// TestDiscoveryNodePrefixesCarryTheBaseScope pins the shape of the prefix set
// the orphan sweep matches on, in both directions that matter.
//
// The canonical prefix must lead — callers read `prefixes[0]` as "the
// spelling this build publishes" — and the unscoped spellings must be present
// too, because they are what this daemon itself wrote before the topic base
// gained a node-id scope. Without them, every retained config a non-default-base
// daemon ever published becomes unreachable: nothing in the build spells those
// node ids again, so the sweep can never retract them and Home Assistant keeps
// a permanently unavailable twin of every entity forever. That is ADR 0068's
// obligation 3, and it is the same shape as the `legacyDiscoverySlug` entry
// #809 added for the slug unification.
func TestDiscoveryNodePrefixesCarryTheBaseScope(t *testing.T) {
	t.Parallel()

	got := discoveryNodePrefixes("house_", "ccu-01")
	if len(got) == 0 || got[0] != "house_ccu-01_" {
		t.Fatalf("discoveryNodePrefixes = %v; the canonical `<base>_<central>_` prefix must lead", got)
	}
	if !slices.Contains(got, "ccu-01_") {
		t.Errorf("discoveryNodePrefixes = %v, missing the pre-scope prefix %q — every retained config "+
			"this daemon wrote before the base scope existed would be unreachable, and each one keeps a "+
			"permanently unavailable entity in Home Assistant forever", got, "ccu-01_")
	}
	// A daemon on the default base has no scope, so it must produce exactly
	// what it always produced: no second, duplicate spelling of its own
	// prefixes, and nothing new to sweep.
	plain := discoveryNodePrefixes("", "ccu-01")
	if !slices.Equal(plain, []string{"ccu-01_"}) {
		t.Errorf("discoveryNodePrefixes with no scope = %v, want exactly [ccu-01_]", plain)
	}
}

// TestOrphanSweepOwnsItsOwnScopeAndNotASiblingsGoing is the collision this
// change exists to fix, stated as the sweep rather than as the publish.
//
// The publish half is the visible one — two daemons writing one config topic
// — but the sweep half is what deletes entities. `RunDiscoveryOrphanCleanupOnce`
// decides ownership from the node id alone ([hapublisher.ConfigTopic] carries no
// payload, so the `state_topic` that would name the owning daemon's base is
// not available to the predicate), and every config it owns but has not
// declared it retracts. Two daemons in one node-id namespace therefore did not
// merely race on publish: each ran a sweep that judged the other's live
// entities against its own claim set and deleted every one it did not itself
// publish, on every boot, with nothing in any log.
func TestOrphanSweepOwnsItsOwnScopeAndNotASiblings(t *testing.T) {
	t.Parallel()

	owns := ownsFor(t, "house", "ccu-01")

	for _, nodeID := range []string{
		"house_ccu-01_000a0000000001", // per-device, this daemon's
		"house_ccu-01_sysvars",        // hub plane, this daemon's
		"house_alarm",                 // daemon-level, this daemon's
		"house_security",
	} {
		if !owns(nodeID) {
			t.Errorf("the sweep does not recognise %q as its own — its orphans could never be retracted", nodeID)
		}
	}

	for _, nodeID := range []string{
		"garage_ccu-01_000a0000000001", // a sibling daemon's per-device config
		"garage_ccu-01_sysvars",        // a sibling daemon's hub config
		"garage_alarm",                 // a sibling daemon's daemon-level config
		"garage_security",
	} {
		if owns(nodeID) {
			t.Errorf("the sweep claims %q, which belongs to a daemon under a different topic base — "+
				"it would retract a live entity of a process it knows nothing about", nodeID)
		}
	}
}

// TestOrphanSweepStillReachesThePreScopeSpelling is obligation 3: the
// retraction half of the move.
//
// A daemon that ran under a non-default base before this change wrote its
// configs unscoped. It must still recognise them, once, so they can be
// retracted rather than left as phantom entities — and it must go on
// recognising the daemon-level planes in both spellings for the same reason.
func TestOrphanSweepStillReachesThePreScopeSpelling(t *testing.T) {
	t.Parallel()

	owns := ownsFor(t, "house", "ccu-01")
	for _, nodeID := range []string{
		"ccu-01_000a0000000001", // what this daemon wrote before the scope
		"ccu-01_sysvars",
		"alarm",
		"security",
	} {
		if !owns(nodeID) {
			t.Errorf("the sweep no longer recognises %q, the spelling this daemon itself published "+
				"before the base scope existed — every one of those retained configs is stranded on the "+
				"broker forever and Home Assistant keeps an unavailable entity for each", nodeID)
		}
	}

	// Another integration's node id is still none of our business. A parallel
	// zigbee2mqtt publishes device documents that parse perfectly well.
	for _, nodeID := range []string{"zigbee2mqtt_0x00124b", "ccu-02_000a0000000001", "some_other_thing"} {
		if owns(nodeID) {
			t.Errorf("the sweep claims %q, which belongs to another central or another integration", nodeID)
		}
	}
}

// TestDaemonLevelNodeIDRecoversTheBareName pins the scoped/unscoped lookup the
// daemon-level planes need.
//
// They carry no `<central>_` segment (ADR 0052), so [discoveryNodeIDBelongsTo]
// cannot see them and they are matched by name. The base scope prefixes them
// like everything else, so the name has to be recovered before the lookup — and
// the value recovered is what keys `planesDeclared`, which is a fact about this
// process rather than about the topic, so it must come back UNSCOPED.
func TestDaemonLevelNodeIDRecoversTheBareName(t *testing.T) {
	t.Parallel()

	cases := []struct{ scope, nodeID, want string }{
		{"house_", "house_alarm", alarmDiscoveryNodeID},
		{"house_", "house_security", securityDiscoveryNodeID},
		{"house_", "alarm", alarmDiscoveryNodeID}, // pre-scope spelling
		{"house_", "security", securityDiscoveryNodeID},
		{"", "alarm", alarmDiscoveryNodeID}, // default base: one lookup
		{"", "security", securityDiscoveryNodeID},
		{"house_", "garage_alarm", ""},  // a sibling daemon's
		{"", "house_alarm", ""},         // a sibling's, seen by a default-base daemon
		{"house_", "house_sysvars", ""}, // not a daemon-level plane at all
		{"house_", "", ""},
	}
	for _, tc := range cases {
		if got := daemonLevelNodeID(tc.scope, tc.nodeID); got != tc.want {
			t.Errorf("daemonLevelNodeID(%q, %q) = %q, want %q", tc.scope, tc.nodeID, got, tc.want)
		}
	}
}

// TestBundleModeCarriesTheBaseScopeToo pins the one discovery plane that does
// not reach [TopicBuilder.DiscoveryConfig].
//
// The bundle form renders its topic inside go-hamqtt, from the node id alone
// (`BundleConfigTopic`), so a scope applied in the topic builder never reaches
// it. [Bridge.publishDiscovery] diverts to [Bridge.routeToBundle] before it
// builds a topic at all — which means bundle mode would have kept writing
// `homeassistant/device/<central>_<addr>/config`, shared byte-for-byte with a
// sibling daemon, while per-entity mode was fixed. The two modes describe the
// same entities and must land in the same namespace; a fix that reached only
// one of them would be worse than none, because whether you are protected
// would depend on an expert-level config flag.
func TestBundleModeCarriesTheBaseScopeToo(t *testing.T) {
	broker := newFanoutBroker()
	b := NewBridge(BridgeConfig{
		Base: "house", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true, HADiscoveryBundles: true,
	}, broker).WithSubscriber(broker)

	if err := b.routeToBundle(t.Context(), "ccu-a", "sensor", "ccu-a_000a", "temperature",
		bundleComponent("temperature")); err != nil {
		t.Fatalf("routeToBundle: %v", err)
	}

	const want = "homeassistant/device/house_ccu-a_000a/config"
	const unscoped = "homeassistant/device/ccu-a_000a/config"
	broker.mu.Lock()
	got := make([]string, 0, len(broker.published))
	for _, p := range broker.published {
		got = append(got, p.topic)
	}
	broker.mu.Unlock()

	if slices.Contains(got, unscoped) {
		t.Errorf("bundle mode published the unscoped document topic %q — a sibling daemon under another "+
			"topic base writes the same one, and each daemon's rollback pass clears the other's", unscoped)
	}
	if !slices.Contains(got, want) {
		t.Errorf("bundle documents = %v, want the base-scoped topic %q", got, want)
	}
}
