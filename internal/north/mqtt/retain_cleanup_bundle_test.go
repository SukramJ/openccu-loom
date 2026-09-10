// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// TestDiscoveryNodeIDFromTopic pins both discovery topic shapes. Before the
// bundle form was recognised, a three-segment topic was dropped on the way in
// — so a device bundle was never inspected, never evicted, and stayed on the
// broker for good.
func TestDiscoveryNodeIDFromTopic(t *testing.T) {
	t.Parallel()

	const prefix = "homeassistant/"
	cases := []struct {
		name   string
		topic  string
		want   string
		wantOK bool
	}{
		{
			name:   "per-entity form",
			topic:  "homeassistant/sensor/loom_ccu_0001abc/temperature/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
		},
		{
			name:   "device bundle",
			topic:  "homeassistant/device/loom_ccu_0001abc/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
		},
		{
			name:   "node id is folded, as the ownership check expects",
			topic:  "homeassistant/device/LOOM_CCU_0001ABC/config",
			want:   "loom_ccu_0001abc",
			wantOK: true,
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
			// Home Assistant permits omitting the node id. Such a topic
			// names no node this daemon could own, so it is not ours to
			// judge — reading its object id as a node id would be worse
			// than ignoring it.
			name:   "per-entity form without a node id is not claimed",
			topic:  "homeassistant/sensor/some_object/config",
			wantOK: false,
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
			got, ok := discoveryNodeIDFromTopic(tc.topic, prefix)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("node id = %q, want %q", got, tc.want)
			}
		})
	}
}
