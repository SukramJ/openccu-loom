// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
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
// Both halves come from one run of the real production code against a
// recording broker: the state topic is read back from
// [Bridge.PublishAddonUpdateState]'s own write, and the command topic
// is matched against the filters the real [CommandSubscriber]
// registered — never by calling [TopicBuilder.AddonUpdateState] /
// [TopicBuilder.AddonUpdateCommand] a second time, which would only
// prove either call site agrees with itself.
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

	obs := runAddonUpdatePlane(t)
	planeRoundTrip(t, "addon update plane", declared, obs.publishedTopics(), obs.subscribedFilters(), nil)
}

// runAddonUpdatePlane drives [Bridge.PublishAddonUpdateState] and the
// real [CommandSubscriber] against a recording broker and returns
// everything carried.
func runAddonUpdatePlane(t *testing.T) *observedPlane {
	t.Helper()
	ctx := context.Background()
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: "gh", RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)

	if err := bridge.PublishAddonUpdateState(ctx, "1.0.0", "1.1.0", false); err != nil {
		t.Fatalf("publish addon update state: %v", err)
	}

	// The command half is observed rather than mirrored: the real
	// subscriber registers its own literal subscription, and the
	// declared command topic counts as heard only when it matches.
	cs := NewCommandSubscriber(obs, NewTopicBuilder("gh"), nil, slog.Default())
	if err := cs.Start(ctx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cs.Close)

	obs.settle(t)
	return obs
}
