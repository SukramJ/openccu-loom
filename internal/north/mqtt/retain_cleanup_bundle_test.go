// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strings"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// TestDiscoveryNodeIDFromTopic pins both discovery topic shapes. Before the
// bundle form was recognised, a three-segment topic was dropped on the way in
// — so a device bundle was never inspected, never evicted, and stayed on the
// broker for good.
//
// The parser is [hapublisher.ParseConfigTopic] now, and this table is what
// holds it to the reading this daemon's ownership check depends on. The one
// thing it does not do is fold the node id — ownership is the caller's job
// there, and so is the case — so the fold is applied here, exactly where
// [Bridge.ownsDiscoveryTopic] applies it.
func TestDiscoveryNodeIDFromTopic(t *testing.T) {
	t.Parallel()

	const prefix = "homeassistant/"
	cases := []struct {
		name       string
		topic      string
		want       string
		wantOK     bool
		wantBundle bool
		// wantOwned is whether the bundle sweep would claim the topic —
		// the property the production path actually depends on, composed
		// here from the same three conditions it applies. Asserted
		// directly because the library's parse result is its own contract
		// and has widened once already.
		wantOwned bool
	}{
		{
			name:   "per-entity form",
			topic:  "homeassistant/sensor/loom_ccu_0001abc/temperature/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
		},
		{
			name:       "device bundle",
			topic:      "homeassistant/device/loom_ccu_0001abc/config",
			wantOwned:  true,
			want:       "loom_ccu_0001abc",
			wantOK:     true,
			wantBundle: true,
		},
		{
			name:       "node id is folded, as the ownership check expects",
			topic:      "homeassistant/device/LOOM_CCU_0001ABC/config",
			wantOwned:  true,
			want:       "loom_ccu_0001abc",
			wantOK:     true,
			wantBundle: true,
		},
		{
			// device_automation and device_tracker are real platforms and
			// produce four segments, so the literal `device` guard cannot
			// swallow them.
			name:   "device_tracker is a platform, not a bundle",
			topic:  "homeassistant/device_tracker/loom_ccu_0001abc/phone/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
		},
		{
			// Home Assistant permits omitting the node id, and go-hamqtt
			// v0.27.0 started parsing that legacy form instead of refusing
			// it — it is a real shape on a real broker, and a snapshot that
			// cannot name it cannot reason about it.
			//
			// It stays unreachable for this daemon's ownership check all the
			// same, which is why the parse widening is safe here: the node
			// id is empty, so `discoveryNodeIDBelongsTo` matches no prefix,
			// and the bundle sweep additionally requires `Bundle`. Reading
			// the object id as a node id would be the dangerous reading, and
			// nothing does.
			name:       "per-entity form without a node id carries no node id",
			topic:      "homeassistant/sensor/some_object/config",
			want:       "",
			wantOK:     true,
			wantBundle: false,
		},
		{
			// The row that makes the node-id guard load-bearing. Every
			// other non-claimed row above is already settled by the
			// prefix, by `Bundle`, or by an empty node id — so without
			// this one, a guard that claimed EVERY node id would pass the
			// whole table. That is not hypothetical: mutating
			// `discoveryNodeIDBelongsTo` to return true unconditionally
			// was verified to pass before this row existed and to fail
			// after it. Claiming a foreign integration's device bundle
			// means sweeping away somebody else's entities.
			name:       "a bundle under a foreign node id parses but is not this daemon's",
			topic:      "homeassistant/device/zigbee2mqtt_0x00124b/config",
			want:       "zigbee2mqtt_0x00124b",
			wantOK:     true,
			wantBundle: true,
			wantOwned:  false,
		},
		{
			name:   "another prefix",
			topic:  "hass/device/loom_ccu_0001abc/config",
			wantOK: false,
		},
		{
			name:   "not a config topic",
			topic:  "homeassistant/device/loom_ccu_0001abc/state",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, ok := hapublisher.ParseConfigTopic(prefix, tc.topic)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got := strings.ToLower(parsed.NodeID); ok && got != tc.want {
				t.Errorf("node id = %q, want %q", got, tc.want)
			}
			if ok && parsed.Bundle != tc.wantBundle {
				t.Errorf("bundle = %v, want %v", parsed.Bundle, tc.wantBundle)
			}
			owned := ok && parsed.Bundle &&
				discoveryNodeIDBelongsTo(strings.ToLower(parsed.NodeID), []string{"loom_ccu_"})
			if owned != tc.wantOwned {
				t.Errorf("claimed by the bundle sweep = %v, want %v", owned, tc.wantOwned)
			}
		})
	}
}
