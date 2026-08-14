// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHubPlaneTopicsRoundTrip asserts that every topic a hub-entity
// discovery payload declares (sysvars, programs, alarm/service
// messages, inbox, install-mode, connectivity, the three metric
// sensors, and the firmware-update entity) is a topic the plane
// actually writes to or subscribes on.
//
// It exists for the same reason as [TestSecurityPlaneTopicsRoundTrip]:
// each hub builder in hub_discovery.go derives its topics from a
// [naming.MQTTHub*] free function or a [TopicBuilder] method, and the
// real publish call sites (hub_mqtt_publisher.go, bridge.go) are
// supposed to call the very same function. Nothing today compares the
// two, so a discovery-side helper that starts hand-building a topic
// string instead of calling the shared function would leave that
// entity unavailable forever without either half's own tests noticing.
//
// The command half (sysvar/program/install-mode `set` and program
// `trigger` topics) is checked against a topic mirrored independently
// from [CommandSubscriber.Start]'s own wildcard registrations, not by
// calling the naming helper a second time — see [hubPublishedTopics].
//
// The comparison is one-directional: declared-but-unpublished fails;
// published-but-undeclared (the nested availability topics, and the
// program execute-availability gate) is reported via t.Logf only.
func TestHubPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	// The fixtures deliberately exercise the escaping: a central name
	// with a space and an umlaut, and a sysvar name with a space. Every
	// segment on this plane goes through naming.TopicSafe (topics) or
	// safeLower (discovery ids), and both used to be bypassed by
	// hand-assembled topic strings. With an invariant fixture like
	// `ccu-01` a hand-built segment is indistinguishable from the
	// shared builder's output, so the guard could not see the drift.
	const (
		base       = "openccu-loom"
		central    = "Haus CCÜ"
		sysvarName = "Außen Temperatur"
	)
	db := newHubBuilder()
	// The hub builders gate every payload on a known CCU serial.
	db.SetHubInfoFor(central, HubInfo{Serial: "3014F711A0001234"})

	declared := map[string]bool{}
	collectHubDeclaredTopics(
		t, declared,
		db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: sysvarName, ValueType: hmenum.HubValueTypeLogic, Writable: true, IsExtended: true,
		}),
		db.BuildAlarmMessagesDiscovery(central),
		db.BuildServiceMessagesDiscovery(central),
		db.BuildInboxDiscovery(central),
		db.BuildInstallModeSensorDiscovery(central, "HmIP-RF"),
		db.BuildInstallModeButtonDiscovery(central, "HmIP-RF"),
		db.BuildConnectivityDiscovery(central, "HmIP-RF"),
		db.BuildSystemHealthDiscovery(central),
		db.BuildConnectionLatencyDiscovery(central),
		db.BuildLastEventAgeDiscovery(central),
		db.BuildHubUpdateDiscovery(central),
	)

	// Programs declare two roles (principal switch + execute button);
	// BuildProgramDiscoveryRoles fans out to one DiscoveryItem per role.
	// Roles are built through the real production method
	// (*hub.Program).MQTTRoles — the same call site
	// internal/central/adapter/hub_mqtt_publisher.go uses via
	// Bridge.ProgramRoles — so this test also exercises the real
	// role-declaration path, not a hand-rolled substitute.
	prog := hub.NewProgram(central, "PRG_1", "Morning", "", false, nil)
	roles := prog.MQTTRoles(base, central)
	for _, item := range db.BuildProgramDiscoveryRoles(central, HubProgramSpec{ID: prog.ID, Name: prog.Name}, roles) {
		collectHubDeclaredTopics(t, declared, item)
	}

	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
	}

	published := hubPublishedTopics(base, central, sysvarName)
	if len(published) == 0 {
		t.Fatal("no topics published/subscribed; the walk found nothing and would pass vacuously")
	}

	for topic := range declared {
		if !published[topic] {
			t.Errorf("declared but never published/subscribed: %q — a consumer creates this entity "+
				"and it either stays unavailable forever (state) or its commands vanish silently (command)", topic)
		}
	}
	for topic := range published {
		if !declared[topic] {
			t.Logf("published but not declared: %q (no entity is created for it)", topic)
		}
	}
}

// TestHubSystemTopicsShareTheCentralSegmentOfTheRestOfThePlane asserts
// that the three central-wide metric sensors live in the same per-CCU
// subtree as every other topic of that CCU.
//
// The round-trip guard above cannot see this: declaration and publish
// both go through [TopicBuilder.systemTopic], so the two agree with
// each other no matter how the segment is spelled. What broke was the
// spelling against the rest of the plane — the metric topics
// lower-cased the central while `hub/status`, `hub/info` and the
// sysvar topics escape it with [naming.TopicSafe]. One CCU then
// occupied two subtrees, and an operator subscribing the documented
// `<base>/<central>/#` (docs/mqtt-topic-schema.md) never received the
// health sensors. The prefix is therefore compared against a second,
// independent producer rather than against a literal.
func TestHubSystemTopicsShareTheCentralSegmentOfTheRestOfThePlane(t *testing.T) {
	t.Parallel()
	const (
		base    = "openccu-loom"
		central = "Haus CCÜ"
	)
	topics := NewTopicBuilder(base)
	// naming.MQTTHubStatus is the plane's other producer of the per-CCU
	// prefix; every hub topic of this CCU starts with it.
	prefix := strings.TrimSuffix(naming.MQTTHubStatus(base, central), "hub/status")
	for _, topic := range []string{
		topics.HubSystemHealthScore(central),
		topics.HubConnectionLatency(central),
		topics.HubLastEventAge(central),
	} {
		if !strings.HasPrefix(topic, prefix) {
			t.Errorf("%q is outside the CCU subtree %q — a consumer subscribing the documented per-CCU wildcard never sees it", topic, prefix+"#")
		}
	}
}

// collectHubDeclaredTopics extracts the top-level state/command/
// json-attributes/latest-version topic fields from each item into out.
// Items with OK=false are ignored so an optional, not-yet-applicable
// builder (none in this test's fixture) would not fail the walk.
func collectHubDeclaredTopics(t *testing.T, out map[string]bool, items ...DiscoveryItem) {
	t.Helper()
	for _, item := range items {
		if !item.OK {
			t.Errorf("hub discovery builder returned OK=false for a valid fixture (component=%q, objectID=%q)",
				item.Component, item.ObjectID)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("discovery payload for %q/%q is not JSON: %v", item.Component, item.ObjectID, err)
		}
		for _, field := range []string{"state_topic", "command_topic", "json_attributes_topic", "latest_version_topic"} {
			if v, ok := body[field].(string); ok && v != "" {
				out[v] = true
			}
		}
	}
}

// hubPublishedTopics is the set of topics the hub plane actually
// writes to or subscribes on for the fixture built above.
//
// The state/json-attributes/latest-version half is derived from the
// same [naming.MQTTHub*] free functions and [TopicBuilder] methods the
// real publish call sites use (internal/central/adapter/hub_mqtt_publisher.go,
// bridge.go's PublishHubSystemHealthScore/PublishHubConnectionLatency/
// PublishHubLastEventAge/PublishHubUpdate). The command half mirrors
// [CommandSubscriber.Start]'s own wildcard registrations
// (`<base>/+/hub/sysvars/+/set`, `<base>/+/hub/programs/+/set`,
// `<base>/+/hub/programs/+/trigger`, `<base>/+/hub/install_mode/+/set`)
// with the wildcard segment substituted by the fixture's concrete
// name/id/interface — not by calling the naming helper a second time —
// so a drift in either side's topic shape is what makes this test fail.
func hubPublishedTopics(base, central, sysvarName string) map[string]bool {
	topics := NewTopicBuilder(base)
	// The command half substitutes the wildcard of the subscriber's own
	// filters with the segment that reaches the wire — i.e. the escaped
	// name, exactly as the broker sees it.
	seg := naming.TopicSafe(central)
	return map[string]bool{
		naming.MQTTHubSysvarState(base, central, sysvarName):                       true,
		base + "/" + seg + "/hub/sysvars/" + naming.TopicSafe(sysvarName) + "/set": true,
		naming.MQTTHubProgramState(base, central, "PRG_1"):                         true,
		base + "/" + seg + "/hub/programs/PRG_1/set":                               true,
		base + "/" + seg + "/hub/programs/PRG_1/trigger":                           true,
		naming.MQTTHubAlarmMessages(base, central):                                 true,
		naming.MQTTHubServiceMessages(base, central):                               true,
		naming.MQTTHubInbox(base, central):                                         true,
		naming.MQTTHubInstallModeForInterface(base, central, "HmIP-RF"):            true,
		base + "/" + seg + "/hub/install_mode/HmIP-RF/set":                         true,
		naming.MQTTHubConnectivity(base, central, "HmIP-RF"):                       true,
		topics.HubSystemHealthScore(central):                                       true,
		topics.HubConnectionLatency(central):                                       true,
		topics.HubLastEventAge(central):                                            true,
		naming.MQTTHubUpdate(base, central):                                        true,
		// Published, but never a top-level discovery field — the
		// program execute-availability gate rides the nested
		// `availability` list (see t.Logf output for this run).
		naming.MQTTHubProgramExecuteAvailability(base, central, "PRG_1"): true,
	}
}
