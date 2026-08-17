// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
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
// supposed to call the very same function. A discovery-side helper
// that starts hand-building a topic string instead of calling the
// shared function would leave that entity unavailable forever, and a
// "published" set built by calling the same helpers a second time
// cannot see it: both halves would move together by construction.
//
// The state half of "published" therefore comes from one run of the
// real [Bridge] publish call sites (PublishSysvar, PublishProgram,
// PublishAlarmMessages, PublishServiceMessages, PublishInbox,
// PublishInstallMode, PublishConnectivity, the three metric
// publishers, PublishHubUpdate) against a recording broker, driven
// with real [hub] model objects so the topic comes out of each type's
// own MQTTTopics resolver. The command half is checked against the
// filters the real [CommandSubscriber] registered.
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
		programID  = "PRG_1"
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
	prog := hub.NewProgram(central, programID, "Morning", "", false, nil)
	roles := prog.MQTTRoles(base, central)
	for _, item := range db.BuildProgramDiscoveryRoles(central, HubProgramSpec{ID: prog.ID, Name: prog.Name}, roles) {
		collectHubDeclaredTopics(t, declared, item)
	}

	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
	}

	obs := runHubPlane(t, base, central, sysvarName, programID)
	planeRoundTrip(t, "hub plane", declared, obs.publishedTopics(), obs.subscribedFilters(), nil)
}

// runHubPlane drives the real hub plane's publish call sites and the
// real [CommandSubscriber] against a recording broker and returns
// everything carried.
//
// Every state write goes through a real [hub] model object — a
// Sysvar, a Program, the AlarmMessages/ServiceMessages/Inbox
// aggregates, a Connectivity tracker — so the topic comes from that
// type's own MQTTTopics resolver, the same one the discovery builders
// call. The command half is registered by starting the real
// [CommandSubscriber]; a declared command topic counts as carried
// only when one of its real wildcard subscriptions matches it.
func runHubPlane(t *testing.T, base, central, sysvarName, programID string) *observedPlane {
	t.Helper()
	ctx := context.Background()
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: central,
		RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)

	sv := hub.NewSysvar(central, sysvarName, "", hmenum.HubValueTypeLogic, nil)
	if err := bridge.PublishSysvar(ctx, central, sv, true); err != nil {
		t.Fatalf("publish sysvar: %v", err)
	}

	prog := hub.NewProgram(central, programID, "Morning", "", false, nil)
	if err := bridge.PublishProgram(ctx, central, prog, false); err != nil {
		t.Fatalf("publish program: %v", err)
	}

	alarmMessages := hub.NewAlarmMessagesWithCentral(central, nil)
	if err := bridge.PublishAlarmMessages(ctx, central, alarmMessages, alarmMessages.List()); err != nil {
		t.Fatalf("publish alarm messages: %v", err)
	}

	serviceMessages := hub.NewServiceMessagesWithCentral(central, nil)
	if err := bridge.PublishServiceMessages(ctx, central, serviceMessages, serviceMessages.List()); err != nil {
		t.Fatalf("publish service messages: %v", err)
	}

	inbox := hub.NewInboxWithCentral(central)
	if err := bridge.PublishInbox(ctx, central, inbox, inbox.List()); err != nil {
		t.Fatalf("publish inbox: %v", err)
	}

	if err := bridge.PublishInstallMode(ctx, central, "HmIP-RF", 120); err != nil {
		t.Fatalf("publish install mode: %v", err)
	}

	connectivity := hub.NewConnectivity()
	if err := bridge.PublishConnectivity(ctx, central, connectivity, "HmIP-RF", true); err != nil {
		t.Fatalf("publish connectivity: %v", err)
	}

	if err := bridge.PublishHubSystemHealthScore(ctx, central, 97.5); err != nil {
		t.Fatalf("publish system health score: %v", err)
	}
	if err := bridge.PublishHubConnectionLatency(ctx, central, 42); err != nil {
		t.Fatalf("publish connection latency: %v", err)
	}
	if err := bridge.PublishHubLastEventAge(ctx, central, 3); err != nil {
		t.Fatalf("publish last event age: %v", err)
	}
	if err := bridge.PublishHubUpdate(ctx, central, "1.0.0", "1.1.0", false); err != nil {
		t.Fatalf("publish hub update: %v", err)
	}

	// The command half is observed rather than mirrored: the real
	// subscriber registers its own wildcards, and a declared command
	// topic counts as heard only when one of them matches it.
	cs := NewCommandSubscriber(obs, NewTopicBuilder(base), nil, slog.Default())
	if err := cs.Start(ctx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cs.Close)

	obs.settle(t)
	return obs
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
