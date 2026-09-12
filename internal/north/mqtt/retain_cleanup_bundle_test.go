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
		name   string
		topic  string
		want   string
		wantOK bool
		// wantOwned is whether this daemon's sweep would claim the topic.
		// Defaulting to false would make every existing row assert the
		// opposite of the truth, so each row states it.
		wantOwned bool
	}{
		{
			name:   "per-entity form",
			topic:  "homeassistant/sensor/loom_ccu_0001abc/temperature/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
			// Parsed and node-id-matched, but the sweep in question only
			// judges bundle topics, so the per-entity form is not its
			// business even when the node id is this daemon's.
			wantOwned: false,
		},
		{
			name:      "device bundle",
			topic:     "homeassistant/device/loom_ccu_0001abc/config",
			want:      "loom_ccu_0001abc",
			wantOK:    true,
			wantOwned: true,
		},
		{
			name:      "node id is folded, as the ownership check expects",
			topic:     "homeassistant/device/LOOM_CCU_0001ABC/config",
			want:      "loom_ccu_0001abc",
			wantOK:    true,
			wantOwned: true,
		},
		{
			// device_automation and device_tracker are real platforms and
			// produce four segments, so the literal `device` guard cannot
			// swallow them.
			name:   "device_tracker is a platform, not a bundle",
			topic:  "homeassistant/device_tracker/loom_ccu_0001abc/phone/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
			// A real platform producing four segments: the literal `device`
			// guard must not swallow it, and it is not a bundle either.
			wantOwned: false,
		},
		{
			// Home Assistant permits omitting the node id, and go-hamqtt
			// v0.27.0 started parsing that form — before it, the topic was
			// invisible to any sweep and retained forever. It parses with an
			// EMPTY node id, which is what keeps it out of this daemon's
			// reach: the ownership check below requires a bundle topic and a
			// node id carrying one of this daemon's prefixes, and an empty
			// one matches no prefix. The property that matters here is
			// therefore "not claimed", not "not parsed" — reading such a
			// topic's object id as a node id would be worse than ignoring
			// it, and that is asserted directly rather than inferred from
			// the library's return value.
			name:      "per-entity form without a node id parses but is not claimed",
			topic:     "homeassistant/sensor/some_object/config",
			want:      "",
			wantOK:    true,
			wantOwned: false,
		},
		{
			// The row that makes the node-id guard load-bearing. Every
			// other non-claimed row is already settled by `Bundle` or by
			// the prefix, so without this one a guard that claimed every
			// node id would pass the table — and claiming a foreign
			// integration's device bundle means sweeping away somebody
			// else's entities.
			name:      "a bundle under a foreign node id is not claimed",
			topic:     "homeassistant/device/zigbee2mqtt_0x00124b/config",
			want:      "zigbee2mqtt_0x00124b",
			wantOK:    true,
			wantOwned: false,
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
			// The property the sweep actually depends on. Asserted
			// directly because the library's parse result is its own
			// contract and has widened once already: v0.27.0 began
			// parsing the node-id-less form, which had been invisible to
			// every sweep and therefore retained forever.
			owned := ok && parsed.Bundle &&
				discoveryNodeIDBelongsTo(strings.ToLower(parsed.NodeID), []string{"loom_ccu_"})
			if owned != tc.wantOwned {
				t.Errorf("claimed by the bundle sweep = %v, want %v", owned, tc.wantOwned)
			}
		})
	}
}
