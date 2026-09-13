// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"slices"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
)

// ownsFor builds the orphan sweep's ownership predicate for a bridge running
// under the given topic base, with every plane already declared — so what the
// predicate answers is about the node id, which is what these tests are about,
// and not about the declare gate, which has tests of its own.
//
// retractUnscoped is the operator's migration switch
// (`north.mqtt.discovery_retract_unscoped`). It is the difference between "my
// own scoped namespace" and "my own scoped namespace plus the unscoped one I
// wrote before the scope existed", and the whole point of these tests is that
// the second one is not free and is therefore not the default.
func ownsFor(t *testing.T, base, central string, retractUnscoped bool) func(string) bool {
	t.Helper()
	b := &Bridge{topics: NewTopicBuilder(base), cfg: BridgeConfig{RetractUnscopedDiscovery: retractUnscoped}}
	b.MarkHubPlaneDeclared(central)
	for nodeID := range daemonLevelNodeIDs {
		b.MarkPlaneDeclared(nodeID)
	}
	owns := b.ownsDiscoveryTopic(central)
	return func(nodeID string) bool {
		return owns(hapublisher.ConfigTopic{Platform: "sensor", NodeID: nodeID, ObjectID: "o"})
	}
}

// TestDiscoveryNodePrefixesCarryTheBaseScope pins the shape of the prefix set
// the orphan sweep matches on, in both directions that matter.
//
// The canonical prefix must lead — callers read `prefixes[0]` as "the
// spelling this build publishes" — and the unscoped spelling must be absent
// unless the operator asked for it. An unscoped node id is what a sibling
// daemon on the DEFAULT topic base publishes live; claiming it by default
// makes this daemon retract that sibling's configs, which takes its Home
// Assistant entities and the device-registry rows that lose their last
// entity, neither of which can be restored.
func TestDiscoveryNodePrefixesCarryTheBaseScope(t *testing.T) {
	t.Parallel()

	got := discoveryNodePrefixes("house_", false, "ccu-01")
	if len(got) == 0 || got[0] != "house_ccu-01_" {
		t.Fatalf("discoveryNodePrefixes = %v; the canonical `<base>_<central>_` prefix must lead", got)
	}
	if slices.Contains(got, "ccu-01_") {
		t.Errorf("discoveryNodePrefixes = %v, which claims the unscoped prefix %q without the operator "+
			"opting in — that prefix is what a default-base sibling publishes live, and the sweep would "+
			"retract every one of its configs", got, "ccu-01_")
	}
	// Opted in, the pre-scope spelling comes back, because that is the one
	// thing it is for: retracting what this daemon itself wrote before the
	// scope existed (ADR 0068 obligation 3).
	optedIn := discoveryNodePrefixes("house_", true, "ccu-01")
	if optedIn[0] != "house_ccu-01_" {
		t.Fatalf("discoveryNodePrefixes = %v; the canonical prefix must still lead", optedIn)
	}
	if !slices.Contains(optedIn, "ccu-01_") {
		t.Errorf("discoveryNodePrefixes with the migration switch on = %v, missing the pre-scope prefix "+
			"%q — nothing in the build spells those node ids again, so their retained configs could "+
			"never be retracted and each keeps a permanently unavailable entity in Home Assistant", optedIn, "ccu-01_")
	}
	// A daemon on the default base has no scope, so it must produce exactly
	// what it always produced: no second, duplicate spelling of its own
	// prefixes, and nothing new to sweep — with the switch either way, since
	// the unscoped namespace simply is its own.
	for _, unscoped := range []bool{false, true} {
		plain := discoveryNodePrefixes("", unscoped, "ccu-01")
		if !slices.Equal(plain, []string{"ccu-01_"}) {
			t.Errorf("discoveryNodePrefixes with no scope (retractUnscoped=%v) = %v, want exactly [ccu-01_]", unscoped, plain)
		}
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
//
// The sibling cases come in both shapes, because only one of them was ever
// tested and the other is the one that is reachable. A sibling under a second
// NON-default base (`garage_`) never shared a namespace with this daemon. A
// sibling under the DEFAULT base publishes exactly the unscoped node ids this
// daemon wrote before the scope existed — including the fixed literals
// `alarm`, `security` and `daemon`, which need no name coincidence at all.
func TestOrphanSweepOwnsItsOwnScopeAndNotASiblings(t *testing.T) {
	t.Parallel()

	owns := ownsFor(t, "house", "ccu-01", false)

	for _, nodeID := range []string{
		"house_ccu-01_000a0000000001", // per-device, this daemon's
		"house_ccu-01_sysvars",        // hub plane, this daemon's
		"house_alarm",                 // daemon-level, this daemon's
		"house_security",
		"house_daemon", // the add-on self-updater, missing from the map until now
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
		"garage_daemon",
	} {
		if owns(nodeID) {
			t.Errorf("the sweep claims %q, which belongs to a daemon under a different topic base — "+
				"it would retract a live entity of a process it knows nothing about", nodeID)
		}
	}

	// The default-base sibling: the case the previous version of this test
	// never covered, and the one that is actually reachable. Every node id
	// below is live traffic of another daemon, and nothing in the topic
	// distinguishes it from what this daemon published before the scope.
	for _, nodeID := range []string{
		"ccu-01_000a0000000001", // a default-base sibling's per-device config
		"ccu-01_sysvars",        // its hub config
		"alarm",                 // its alarm panels — a fixed literal, so ANY two daemons collide
		"security",
		"daemon", // its add-on self-update entity
	} {
		if owns(nodeID) {
			t.Errorf("the sweep claims %q, which a sibling daemon on the DEFAULT topic base publishes "+
				"live. Retracting it deletes that daemon's Home Assistant entities, and the device "+
				"registry rows that lose their last entity go with them — `identifiers` has no "+
				"migration path, so nothing restores them", nodeID)
		}
	}
}

// TestOrphanSweepReachesThePreScopeSpellingOnlyWhenOptedIn is obligation 3,
// and the price of discharging it, in one test.
//
// A daemon that ran under a non-default base before the node id was scoped
// wrote its configs unscoped, and ADR 0068 obligation 3 says it must retract
// them rather than leave a phantom entity behind for each. But an unscoped
// node id is INDISTINGUISHABLE from a live sibling's: the predicate is handed
// a node id and nothing else. The daemon cannot decide this; the operator
// can, because only the operator knows whether a default-base daemon shares
// the broker. So it is `north.mqtt.discovery_retract_unscoped`, default off,
// for one boot — and with it off, nothing of anyone else's is ever claimed.
func TestOrphanSweepReachesThePreScopeSpellingOnlyWhenOptedIn(t *testing.T) {
	t.Parallel()

	preScope := []string{
		"ccu-01_000a0000000001", // what this daemon wrote before the scope
		"ccu-01_sysvars",
		"alarm",
		"security",
		"daemon",
	}

	off := ownsFor(t, "house", "ccu-01", false)
	for _, nodeID := range preScope {
		if off(nodeID) {
			t.Errorf("the sweep claims the unscoped node id %q with the migration switch OFF — "+
				"that is a default-base sibling's live namespace, and claiming it deletes its entities", nodeID)
		}
	}

	on := ownsFor(t, "house", "ccu-01", true)
	for _, nodeID := range preScope {
		if !on(nodeID) {
			t.Errorf("with the migration switch ON the sweep does not recognise %q, the spelling this "+
				"daemon itself published before the base scope existed — every one of those retained "+
				"configs stays on the broker forever and Home Assistant keeps an unavailable entity "+
				"for each, which is the failure the switch exists to fix", nodeID)
		}
	}

	// Another integration's node id is still none of our business, opted in
	// or not. A parallel zigbee2mqtt publishes device documents that parse
	// perfectly well.
	for _, nodeID := range []string{"zigbee2mqtt_0x00124b", "ccu-02_000a0000000001", "some_other_thing"} {
		if on(nodeID) || off(nodeID) {
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
//
// The bare spelling is only reachable behind the operator's migration switch:
// `alarm`, `security` and `daemon` are fixed literals, so a bare one belongs
// as readily to a live default-base sibling as to this daemon's own past.
func TestDaemonLevelNodeIDRecoversTheBareName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope, nodeID   string
		retractUnscoped bool
		want            string
	}{
		{"house_", "house_alarm", false, alarmDiscoveryNodeID},
		{"house_", "house_security", false, securityDiscoveryNodeID},
		{"house_", "house_daemon", false, addonUpdateNodeID},
		// The pre-scope spelling: this daemon's own history and a live
		// sibling's present, in the same five characters. Only the operator
		// can tell them apart, so only the operator's switch unlocks it.
		{"house_", "alarm", false, ""},
		{"house_", "security", false, ""},
		{"house_", "daemon", false, ""},
		{"house_", "alarm", true, alarmDiscoveryNodeID},
		{"house_", "security", true, securityDiscoveryNodeID},
		{"house_", "daemon", true, addonUpdateNodeID},
		// Default base: one lookup, and the switch cannot change it — the
		// unscoped namespace is this daemon's own.
		{"", "alarm", false, alarmDiscoveryNodeID},
		{"", "security", false, securityDiscoveryNodeID},
		{"", "daemon", false, addonUpdateNodeID},
		{"", "alarm", true, alarmDiscoveryNodeID},
		{"house_", "garage_alarm", true, ""},  // a sibling daemon's
		{"", "house_alarm", true, ""},         // a sibling's, seen by a default-base daemon
		{"house_", "house_sysvars", true, ""}, // not a daemon-level plane at all
		{"house_", "", true, ""},
	}
	for _, tc := range cases {
		if got := daemonLevelNodeID(tc.scope, tc.retractUnscoped, tc.nodeID); got != tc.want {
			t.Errorf("daemonLevelNodeID(%q, %v, %q) = %q, want %q", tc.scope, tc.retractUnscoped, tc.nodeID, got, tc.want)
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

// daemonLevelUniqueIDs renders every daemon-level plane on one topic base and
// returns the published `unique_id` of each, keyed by a readable plane name.
//
// The three planes are built by three unrelated entry points with three
// different context types, so collecting them here is what makes the claim
// below a claim about the PLANE CLASS rather than three separate ones.
func daemonLevelUniqueIDs(t *testing.T, base string) map[string]string {
	t.Helper()
	out := map[string]string{}
	read := func(name string, item DiscoveryItem) {
		if !item.OK {
			t.Fatalf("%s on base %q: no discovery item built", name, base)
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s on base %q: unmarshal: %v", name, base, err)
		}
		uid, _ := body["unique_id"].(string)
		if uid == "" {
			t.Fatalf("%s on base %q: payload carries no unique_id", name, base)
		}
		out[name] = uid
	}

	read("alarm", BuildAlarmPanelDiscovery(base, alarmMasterZone, "Alarm", nil, true, false, false))
	// Indexed rather than ranged by value: the entity struct is large enough
	// that gocritic's rangeValCopy fires on the copy.
	secEntities := securitySystemEntities(func(_, fallback string) string { return fallback })
	for i := range secEntities {
		if secEntities[i].key == "state" {
			read("security", BuildSecurityDiscovery(base, "Security & Safety", "", secEntities[i]))
		}
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder(base), "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	read("addon-update", db.BuildAddonUpdateDiscovery())
	return out
}

// TestDaemonLevelUniqueIDsSeparateTwoDaemons is the half of the two-daemon
// collision that moving the discovery node id did not fix — and, until it was
// fixed, made worse.
//
// Home Assistant's MQTT integration keys its entity registry on `unique_id`
// and rejects a second config declaring one it has already seen ("Platform
// mqtt does not generate unique IDs"). The three daemon-level planes carry no
// `<central>` segment (ADR 0052) and their ids are fixed literals —
// `loom_addon_update`, `openccu-loom_alarm_<zone>`, `loom_security_<key>` —
// with nothing in them that differs between two daemons. So once the node id
// was scoped, two daemons wrote two DISTINCT config topics carrying the SAME
// unique id: the semantics went from "last writer wins, and a restart
// repoints the entity at the live daemon" to "first writer wins permanently",
// the second daemon's alarm, security and add-on-update entities never
// appeared at all, and its retained configs sat on the broker being rejected
// again on every Home Assistant restart.
func TestDaemonLevelUniqueIDsSeparateTwoDaemons(t *testing.T) {
	t.Parallel()

	haus := daemonLevelUniqueIDs(t, "haus")
	garage := daemonLevelUniqueIDs(t, "garage")
	if len(haus) != 3 {
		t.Fatalf("collected %d daemon-level planes (%v), want the three there are", len(haus), haus)
	}
	for plane, id := range haus {
		if other := garage[plane]; id == other {
			t.Errorf("the %s plane declares unique_id %q on BOTH topic bases — Home Assistant keeps "+
				"whichever daemon's config it saw first and rejects the other's outright, so the second "+
				"daemon's entity never appears at all", plane, id)
		}
	}
}

// TestDaemonLevelUniqueIDsAreUnchangedOnTheDefaultBase is the price side of
// the test above, and the reason the scope is conditional.
//
// Re-keying a `unique_id` is the one move Home Assistant has no migration path
// for at the entity level: the history, the long-term statistics, the entity
// id, the rename, the area and every automation that names the entity go with
// it. Applying the scope unconditionally would charge that to every
// single-daemon installation in the fleet for a collision it cannot have. A
// non-default topic base is the operator saying "there is more than one of
// me", so it is the configuration that pays — and it is exactly the
// configuration that is broken without it.
func TestDaemonLevelUniqueIDsAreUnchangedOnTheDefaultBase(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"alarm":        alarmpanel.PanelUniqueID(alarmMasterZone),
		"security":     "loom_security_state",
		"addon-update": addonUpdateUniqueID,
	}
	// The default base in every spelling that resolves to it, because the
	// comparison is on the slug: an operator who wrote `OpenCCU-Loom/` has
	// the default base and must pay nothing either.
	for _, base := range []string{"openccu-loom", "OpenCCU-Loom", "openccu-loom/", ""} {
		got := daemonLevelUniqueIDs(t, base)
		for plane, id := range want {
			if got[plane] != id {
				t.Errorf("base %q is the default, but the %s plane publishes unique_id %q instead of the "+
					"already-published %q — every installation that never set a topic base would lose that "+
					"entity's history, its entity id and every automation naming it", base, plane, got[plane], id)
			}
		}
	}
}

// TestSecurityIdentityIsUnchangedOnTheDefaultBase is named by
// security_discovery_test.go's scoped expectation, so it exists as its own
// statement: the key-by-key vocabulary of the Security & Safety plane keeps
// the ids it publishes today whenever the operator never set a topic base.
func TestSecurityIdentityIsUnchangedOnTheDefaultBase(t *testing.T) {
	t.Parallel()
	entities := securitySystemEntities(func(_, fallback string) string { return fallback })
	for i := range entities {
		e := &entities[i]
		item := BuildSecurityDiscovery("openccu-loom", "Security & Safety", "", *e)
		if !item.OK {
			t.Fatalf("BuildSecurityDiscovery(%q) returned OK=false", e.key)
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", e.key, err)
		}
		if want := "loom_security_" + e.key; body["unique_id"] != want {
			t.Errorf("%s: unique_id = %v on the default topic base, want the already-published %q",
				e.key, body["unique_id"], want)
		}
	}
}

// TestPublishingADaemonLevelPlaneDeclaresIt pins the half of the add-on
// self-updater's sweep repair that the node-id map alone does not give.
//
// The orphan sweep may not touch a daemon-level plane's namespace until that
// plane says it has published — otherwise the boot-time pass deletes the
// previous boot's entities moments before this boot re-announces them. The
// alarm and security planes call [Bridge.MarkPlaneDeclared] from their own
// publishers; the add-on self-updater publishes through the generic
// [Bridge.PublishHubDiscovery] and had no such call, so registering its node
// id in [daemonLevelNodeIDs] on its own would have left the sweep permanently
// deferred and the pre-scope config stranded exactly as before.
func TestPublishingADaemonLevelPlaneDeclaresIt(t *testing.T) {
	t.Parallel()

	broker := newFanoutBroker()
	b := NewBridge(BridgeConfig{
		Base: "house", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, broker).WithSubscriber(broker)

	if b.planeDeclared(addonUpdateNodeID) {
		t.Fatal("the add-on update plane counts as declared before it published anything; the sweep would delete its entity on every restart")
	}

	db := NewDefaultDiscoveryBuilder(b.Topics(), "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	if err := b.PublishHubDiscovery(t.Context(), db.BuildAddonUpdateDiscovery()); err != nil {
		t.Fatalf("PublishHubDiscovery: %v", err)
	}

	if !b.planeDeclared(addonUpdateNodeID) {
		t.Error("publishing the add-on self-update config did not declare its plane — the orphan sweep " +
			"defers that namespace forever, so neither the scoped config nor the pre-scope one it " +
			"replaced can ever be retracted")
	}
	// Publishing one daemon-level plane must not unlock the others.
	if b.planeDeclared(alarmDiscoveryNodeID) || b.planeDeclared(securityDiscoveryNodeID) {
		t.Error("publishing the add-on update plane also declared the alarm or security plane, whose entities the sweep would then delete before they are built")
	}
}
