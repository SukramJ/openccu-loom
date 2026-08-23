// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildCombinedDiscovery — the frame
// ---------------------------------------------------------------------------

func TestBuildCombinedDiscovery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ev      CombinedEvent
		wantOK  bool
		checkFn func(t *testing.T, item DiscoveryItem)
	}{
		{
			name: "happy path",
			ev: CombinedEvent{
				Central:       "ccu1",
				Interface:     "HmIP-RF",
				DeviceAddress: "0001ABCD",
				ChannelNo:     3,
				DeviceName:    "Alarmsirene FL",
				Model:         "HmIP-ASIR",
				Kind:          "duration",
				Component:     "number",
				Body: map[string]any{
					"name":            "Zeitdauer",
					"command_topic":   "gh/ccu1/HmIP-RF/0001ABCD/3/combined/duration/set",
					"entity_category": "config",
				},
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				if item.Component != "number" {
					t.Errorf("Component = %q, want number", item.Component)
				}
				if item.ObjectID != "openccu-loom_0001abcd_3_duration" {
					t.Errorf("ObjectID = %q", item.ObjectID)
				}
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("unmarshal payload: %v", err)
				}
				// The frame the bridge owns.
				for _, key := range []string{
					"unique_id", "state_topic", "availability",
					"availability_mode", "device", "origin",
				} {
					if _, ok := body[key]; !ok {
						t.Errorf("frame key %q missing from body", key)
					}
				}
				if got := body["state_topic"]; got != "gh/ccu1/HmIP-RF/0001ABCD/3/combined/duration" {
					t.Errorf("state_topic = %v", got)
				}
				// The half the projection owns, carried through verbatim.
				if got := body["name"]; got != "Zeitdauer" {
					t.Errorf("name = %v, want Zeitdauer", got)
				}
				if got := body["entity_category"]; got != "config" {
					t.Errorf("entity_category = %v, want config", got)
				}
			},
		},
		{
			name: "projection may override a frame key",
			ev: CombinedEvent{
				Central: "ccu1", DeviceAddress: "0001ABCD", ChannelNo: 3,
				Kind: "door_mode", Component: "select",
				Body: map[string]any{"state_topic": "custom/topic", "options": []string{"A"}},
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("unmarshal payload: %v", err)
				}
				// A projection that needs a different state topic must be
				// able to say so; silently discarding it would be the same
				// class of defect the projection seam exists to prevent.
				if got := body["state_topic"]; got != "custom/topic" {
					t.Errorf("state_topic = %v, want the projection's own value", got)
				}
			},
		},
		{
			name:   "declines without a kind",
			ev:     CombinedEvent{DeviceAddress: "0001ABCD", Component: "number", Body: map[string]any{"name": "x"}},
			wantOK: false,
		},
		{
			name:   "declines without a device address",
			ev:     CombinedEvent{Kind: "duration", Component: "number", Body: map[string]any{"name": "x"}},
			wantOK: false,
		},
		{
			name:   "declines when the projection returned no component",
			ev:     CombinedEvent{DeviceAddress: "0001ABCD", Kind: "duration", Body: map[string]any{"name": "x"}},
			wantOK: false,
		},
		{
			name:   "declines on an empty body",
			ev:     CombinedEvent{DeviceAddress: "0001ABCD", Kind: "duration", Component: "number"},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu1")
			item := d.BuildCombinedDiscovery("ccu1", tc.ev)
			if item.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", item.OK, tc.wantOK)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, item)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Publication
// ---------------------------------------------------------------------------

func TestPublishCombinedDiscovery(t *testing.T) {
	t.Parallel()

	validEvent := func() CombinedEvent {
		return CombinedEvent{
			DeviceAddress: "0001ABCD",
			Kind:          "duration",
			Component:     "number",
			Body:          map[string]any{"name": "Zeitdauer"},
		}
	}

	t.Run("publishes when enabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		if err := b.PublishCombinedDiscovery(context.Background(), "ccu1", validEvent()); err != nil {
			t.Fatalf("PublishCombinedDiscovery: %v", err)
		}
		if len(pub.sent) == 0 {
			t.Fatal("expected at least one discovery publish")
		}
	})

	t.Run("no-op when HA discovery disabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t, func(c *BridgeConfig) {
			c.HADiscoveryEnabled = false
		})
		if err := b.PublishCombinedDiscovery(context.Background(), "ccu1", validEvent()); err != nil {
			t.Fatalf("PublishCombinedDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when HA discovery disabled, got %d", len(pub.sent))
		}
	})

	t.Run("no-op when builder declines event", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		// No component: the projection declined.
		ev := validEvent()
		ev.Component = ""
		if err := b.PublishCombinedDiscovery(context.Background(), "ccu1", ev); err != nil {
			t.Fatalf("PublishCombinedDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish for a declined event, got %d", len(pub.sent))
		}
	})
}

func TestPublishCombinedState(t *testing.T) {
	t.Parallel()

	t.Run("publishes to the combined state topic", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		if err := b.PublishCombinedState(context.Background(), "ccu1", "HmIP-RF", "0001ABCD", 3, "door_mode", "OPEN"); err != nil {
			t.Fatalf("PublishCombinedState: %v", err)
		}
		var found bool
		for _, m := range pub.sent {
			if strings.HasSuffix(m.topic, "/combined/door_mode") {
				found = true
				if m.payload != "OPEN" {
					t.Errorf("payload = %q, want OPEN", m.payload)
				}
			}
		}
		if !found {
			t.Fatal("no publish on the combined state topic")
		}
	})

	t.Run("no-op when the raw plane is disabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t, func(c *BridgeConfig) {
			c.RawEnabled = false
		})
		if err := b.PublishCombinedState(context.Background(), "ccu1", "HmIP-RF", "0001ABCD", 3, "duration", "30"); err != nil {
			t.Fatalf("PublishCombinedState: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when the raw plane is disabled, got %d", len(pub.sent))
		}
	})

	t.Run("falls back to the configured central", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		if err := b.PublishCombinedState(context.Background(), "", "HmIP-RF", "0001ABCD", 3, "duration", "30"); err != nil {
			t.Fatalf("PublishCombinedState: %v", err)
		}
		if len(pub.sent) == 0 {
			t.Fatal("expected a publish with the fallback central")
		}
	})
}
