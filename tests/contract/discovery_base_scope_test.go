// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestDiscoveryBaseScopeTracksTheConfigDefault pins [naming.DefaultTopicBase]
// against the value `config.applyDefaults` actually fills in for an absent
// `north.mqtt.topic_base`.
//
// The two are duplicated rather than shared because the model layer must not
// import the config package, and the duplication is only safe while something
// fails when they split. Nothing would, otherwise: a config default renamed
// without this constant following makes [naming.DiscoveryBaseScope] treat the
// NEW default as a custom base, so every installation that never configured
// anything silently scopes its discovery node ids and orphans every retained
// config it has. The failure mode is a fleet-wide entity re-key triggered by a
// string literal in an unrelated file, which is exactly the class of change
// nobody re-reads this constant for.
func TestDiscoveryBaseScopeTracksTheConfigDefault(t *testing.T) {
	t.Parallel()

	// Parsed through the real loader rather than reading the literal: what
	// matters is the value a daemon actually runs with when the operator
	// configures nothing, which is applyDefaults' output and not a constant
	// somebody could move without moving the behaviour.
	cfg, err := config.Parse([]byte(`
centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF]
north:
  mqtt:
    enabled: true
    broker_url: tcp://192.168.1.5:1883
`))
	if err != nil {
		t.Fatalf("parse a config with no topic_base: %v", err)
	}
	applied := cfg.North.MQTT.TopicBase
	if applied == "" {
		t.Fatal("config applied an empty default topic base")
	}
	if applied != naming.DefaultTopicBase {
		t.Fatalf("config defaults topic_base to %q, naming.DefaultTopicBase is %q — "+
			"DiscoveryBaseScope would treat the real default as a custom base and scope the discovery "+
			"node ids of every installation that never set one, orphaning their retained configs",
			applied, naming.DefaultTopicBase)
	}
	if scope := naming.DiscoveryBaseScope(applied); scope != "" {
		t.Fatalf("the default topic base %q produced discovery node scope %q, want none", applied, scope)
	}
}

// TestDiscoveryBaseScopeSeparatesTwoDaemons is the collision this scope
// exists to prevent, written as the two-daemon case rather than as a property
// of one builder.
//
// ADR 0006 rule 4 has said since the initial release that the HA Discovery
// node derives from the topic base "not a hardcoded literal", so that
// "non-default Bases give multi-daemon installations distinct namespaces".
// It did not. Two daemons bridging one CCU under two bases wrote byte-identical
// `homeassistant/.../config` topics on all three planes — per-device, hub and
// daemon-level — and the retained config the broker kept was whichever of them
// published last. Worse than the race: `RunDiscoveryOrphanCleanupOnce` decides
// what to retract from the node id alone, so each daemon judged the other's
// live configs against its own claim set and retracted every one it did not
// itself publish.
func TestDiscoveryBaseScopeSeparatesTwoDaemons(t *testing.T) {
	t.Parallel()

	first := mqtt.NewTopicBuilder("house")
	second := mqtt.NewTopicBuilder("garage")

	// One node id per plane, spelled the way its producer spells it.
	for _, tc := range []struct{ plane, component, nodeID, objectID string }{
		{"per-device", "switch", "ccu-01_000a0000000001", "4_state"},
		{"hub", "sensor", "ccu-01_sysvars", "4711"},
		{"daemon-level (alarm)", "alarm_control_panel", "alarm", "master"},
		{"daemon-level (security)", "event", "security", "fault"},
		{"daemon-level (add-on update)", "update", "daemon", "addon_update"},
	} {
		a := first.DiscoveryConfig(tc.component, tc.nodeID, tc.objectID)
		b := second.DiscoveryConfig(tc.component, tc.nodeID, tc.objectID)
		if a == b {
			t.Errorf("%s: two daemons under topic bases %q and %q both write %q — "+
				"the second daemon's retained config replaces the first's, and each daemon's orphan "+
				"sweep retracts the other's entities",
				tc.plane, "house", "garage", a)
		}
		if !strings.HasPrefix(a, "homeassistant/"+tc.component+"/house_") {
			t.Errorf("%s: %q does not carry the first daemon's base scope", tc.plane, a)
		}
	}
}
