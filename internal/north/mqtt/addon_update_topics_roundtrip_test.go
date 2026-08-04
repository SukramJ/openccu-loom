// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"
)

// TestAddonUpdatePlaneTopicsRoundTrip asserts that every topic the
// daemon-level add-on self-update entity declares is a topic the plane
// actually writes to (state / latest-version, which share one topic)
// or subscribes on (command).
//
// The plane is intentionally small — exactly one entity, no zones, no
// classes — but it carries the same risk as every other plane:
// [BuildAddonUpdateDiscovery] and [Bridge.PublishAddonUpdateState] must
// agree on the state topic, and [BuildAddonUpdateDiscovery] and
// [CommandSubscriber.Start]'s `<base>/system/addon_update/set`
// subscription must agree on the command topic.
//
// The state topic is checked via [TopicBuilder.AddonUpdateState] — the
// same method [Bridge.PublishAddonUpdateState] calls, so a rename that
// only touches one of the two call sites breaks the other and this
// test catches it. The command topic is deliberately NOT checked via
// [TopicBuilder.AddonUpdateCommand]: that method and the discovery
// builder are the same call path, so reusing it here would only prove
// discovery agrees with itself. It is checked against the literal
// string [CommandSubscriber.Start] subscribes, reproduced
// independently below — so a drift in either the topic-builder method
// or the hand-written subscription literal is what makes this test fail.
func TestAddonUpdatePlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	topics := NewTopicBuilder("gh")
	d := NewDefaultDiscoveryBuilder(topics, "")

	item := d.BuildAddonUpdateDiscovery()
	if !item.OK {
		t.Fatal("BuildAddonUpdateDiscovery returned OK=false")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("discovery payload is not JSON: %v", err)
	}

	declared := map[string]bool{}
	for _, field := range []string{"state_topic", "latest_version_topic", "command_topic"} {
		v, ok := body[field].(string)
		if !ok || v == "" {
			t.Fatalf("expected non-empty %q in add-on update discovery payload, got %v", field, body[field])
		}
		declared[v] = true
	}
	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payload and would pass vacuously")
	}

	published := map[string]bool{
		// PublishAddonUpdateState's own call site (bridge.go) —
		// b.topics.AddonUpdateState() — is the same TopicBuilder method
		// the discovery builder used above.
		topics.AddonUpdateState(): true,
		// Reproduces CommandSubscriber.Start's literal subscription
		// independently of TopicBuilder.AddonUpdateCommand — see the
		// doc comment above.
		"gh/system/addon_update/set": true,
	}
	if len(published) == 0 {
		t.Fatal("no topics published/subscribed; the walk found nothing and would pass vacuously")
	}

	for topic := range declared {
		if !published[topic] && !addonUpdateUndeclaredByDesign[topic] {
			t.Errorf("declared but never published/subscribed: %q — the update entity would stay "+
				"unavailable forever or its INSTALL command would vanish silently", topic)
		}
	}
	for topic := range published {
		if !declared[topic] {
			t.Logf("published but not declared: %q (no entity is created for it)", topic)
		}
	}
}

// addonUpdateUndeclaredByDesign is empty today — the add-on update
// entity declares exactly the two topics it writes/subscribes, plus the
// bridge/hub availability topics that are referenced from the nested
// `availability` list rather than as a top-level field. Kept as a named
// map (rather than an inline empty check) so a future exception reads
// the same way every other plane's exception map does.
var addonUpdateUndeclaredByDesign = map[string]bool{}
