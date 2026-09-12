// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"log/slog"
	"testing"
)

// TestDiscoveryRuntimeStatusTopicIsChecked pins that the publisher
// runtime is handed a topic layout, not just a status-topic literal.
//
// The literal alone is unfalsifiable: it is the one string every entity's
// availability list references, and under the default
// `availability_mode: "all"` a single typo greys out the whole fleet with
// nothing on the wire naming the cause. With a layout configured,
// [hapublisher.New] derives the same string from
// [hatopic.Layout.Bridge] and refuses a disagreement at the composition
// root — so the failure moves from an operator asking why every entity is
// unavailable to a daemon that will not start.
//
// The layout has to be present AND has to agree. Asserting only the
// second would pass with no layout at all.
func TestDiscoveryRuntimeStatusTopicIsChecked(t *testing.T) {
	t.Parallel()
	bridge, _ := newTestBridge(t)
	cfg := discoveryRuntimeConfig(bridge, slog.Default())

	if cfg.Layout == nil {
		t.Fatal("the publisher runtime is configured with no topic layout, so its status topic is unchecked")
	}
	if got, want := cfg.Layout.Bridge(), cfg.StatusTopic; got != want {
		t.Errorf("Layout.Bridge() = %q, StatusTopic = %q — the declaring and publishing sides disagree", got, want)
	}
	if cfg.StatusTopic != bridge.topics.BridgeStatus() {
		t.Errorf("StatusTopic = %q, want the bridge's own %q", cfg.StatusTopic, bridge.topics.BridgeStatus())
	}
}

// TestDiscoveryRuntimeBridgeTopicIsTheDeclaredOne pins the round trip
// through the constructed runtime: the string the library reports as the
// bridge availability topic is the string this daemon's entities name.
func TestDiscoveryRuntimeBridgeTopicIsTheDeclaredOne(t *testing.T) {
	t.Parallel()
	bridge, _ := newTestBridge(t)
	if got, want := bridge.pub.BridgeTopic(), alarmBridgeStatusTopic(bridge.topics.Base); got != want {
		t.Errorf("Runtime.BridgeTopic() = %q, want the declaring side's %q", got, want)
	}
}
