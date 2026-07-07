// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildScheduleEntityDiscovery
// ---------------------------------------------------------------------------

func TestBuildScheduleEntityDiscovery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ev      ScheduleEntityEvent
		wantOK  bool
		checkFn func(t *testing.T, item DiscoveryItem)
	}{
		{
			name: "happy path",
			ev: ScheduleEntityEvent{
				Central:       "ccu1",
				Interface:     "HmIP-RF",
				DeviceAddress: "0003CAFE",
				ChannelNo:     1,
				DeviceName:    "Heizkörperthermostat",
				Model:         "HmIP-eTRV-2",
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				if item.Component != string(HAComponentSensor) {
					t.Errorf("Component = %q, want sensor", item.Component)
				}
				if !strings.Contains(item.ObjectID, "schedule") {
					t.Errorf("ObjectID = %q, want it to contain 'schedule'", item.ObjectID)
				}
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if body["name"] != "Zeitplan" {
					t.Errorf("name = %v, want Zeitplan", body["name"])
				}
				if _, ok := body["json_attributes_topic"]; !ok {
					t.Error("json_attributes_topic missing from discovery payload")
				}
				device, ok := body["device"].(map[string]any)
				if !ok {
					t.Fatal("device block missing or not an object")
				}
				if device["via_device"] == nil {
					t.Error("schedule sub-device must set via_device to the parent device")
				}
			},
		},
		{
			name:   "empty device address rejected",
			ev:     ScheduleEntityEvent{DeviceAddress: ""},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
			item := builder.BuildScheduleEntityDiscovery("ccu1", tc.ev)
			if item.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", item.OK, tc.wantOK)
			}
			if tc.wantOK && tc.checkFn != nil {
				tc.checkFn(t, item)
			}
		})
	}
}

func TestPublishScheduleEntityDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("publishes when enabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishScheduleEntityDiscovery(context.Background(), "ccu1", ScheduleEntityEvent{
			DeviceAddress: "0003CAFE",
			ChannelNo:     1,
		})
		if err != nil {
			t.Fatalf("PublishScheduleEntityDiscovery: %v", err)
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
		err := b.PublishScheduleEntityDiscovery(context.Background(), "ccu1", ScheduleEntityEvent{
			DeviceAddress: "0003CAFE",
		})
		if err != nil {
			t.Fatalf("PublishScheduleEntityDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when HA discovery disabled, got %d", len(pub.sent))
		}
	})

	t.Run("no-op when builder declines event", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishScheduleEntityDiscovery(context.Background(), "ccu1", ScheduleEntityEvent{
			DeviceAddress: "", // declined
		})
		if err != nil {
			t.Fatalf("PublishScheduleEntityDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when builder declines, got %d", len(pub.sent))
		}
	})

	t.Run("propagates publisher error", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		pub.err = errors.New("broker unavailable")
		err := b.PublishScheduleEntityDiscovery(context.Background(), "ccu1", ScheduleEntityEvent{
			DeviceAddress: "0003CAFE",
		})
		if err == nil {
			t.Fatal("expected error from failing publisher to propagate")
		}
	})
}

func TestPublishScheduleEntityState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		rawEnabled bool
		count      int
		wantSent   bool
		wantBody   string
	}{
		{name: "publishes count", rawEnabled: true, count: 3, wantSent: true, wantBody: "3"},
		{name: "no-op when raw disabled", rawEnabled: false, count: 3, wantSent: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) {
				c.RawEnabled = tc.rawEnabled
			})
			err := b.PublishScheduleEntityState(context.Background(), "ccu1", "HmIP-RF", "0003CAFE", 1, tc.count)
			if err != nil {
				t.Fatalf("PublishScheduleEntityState: %v", err)
			}
			if tc.wantSent {
				if len(pub.sent) == 0 {
					t.Fatal("expected a state publish")
				}
				last := pub.sent[len(pub.sent)-1]
				if last.payload != tc.wantBody {
					t.Errorf("payload = %q, want %q", last.payload, tc.wantBody)
				}
			} else if len(pub.sent) != 0 {
				t.Fatalf("expected no publish when raw disabled, got %d", len(pub.sent))
			}
		})
	}
}

func TestPublishScheduleEntityAttrs(t *testing.T) {
	t.Parallel()

	t.Run("publishes attrs JSON", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishScheduleEntityAttrs(context.Background(), "ccu1", "HmIP-RF", "0003CAFE", 1, map[string]any{"p1": "07:00-09:00"})
		if err != nil {
			t.Fatalf("PublishScheduleEntityAttrs: %v", err)
		}
		if len(pub.sent) == 0 {
			t.Fatal("expected an attrs publish")
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(pub.sent[len(pub.sent)-1].payload), &got); err != nil {
			t.Fatalf("attrs payload not valid JSON: %v", err)
		}
		if got["p1"] != "07:00-09:00" {
			t.Errorf("p1 = %v, want 07:00-09:00", got["p1"])
		}
	})

	t.Run("nil attrs publishes empty object", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishScheduleEntityAttrs(context.Background(), "ccu1", "HmIP-RF", "0003CAFE", 1, nil)
		if err != nil {
			t.Fatalf("PublishScheduleEntityAttrs: %v", err)
		}
		if len(pub.sent) == 0 {
			t.Fatal("expected an attrs publish")
		}
		if pub.sent[len(pub.sent)-1].payload != "{}" {
			t.Errorf("payload = %q, want {}", pub.sent[len(pub.sent)-1].payload)
		}
	})

	t.Run("no-op when raw disabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t, func(c *BridgeConfig) {
			c.RawEnabled = false
		})
		err := b.PublishScheduleEntityAttrs(context.Background(), "ccu1", "HmIP-RF", "0003CAFE", 1, map[string]any{"p1": "x"})
		if err != nil {
			t.Fatalf("PublishScheduleEntityAttrs: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when raw disabled, got %d", len(pub.sent))
		}
	})

	t.Run("propagates publisher error", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		pub.err = errors.New("broker unavailable")
		err := b.PublishScheduleEntityAttrs(context.Background(), "ccu1", "HmIP-RF", "0003CAFE", 1, map[string]any{"p1": "x"})
		if err == nil {
			t.Fatal("expected error from failing publisher to propagate")
		}
	})
}

// ---------------------------------------------------------------------------
// BuildScheduleSwitchDiscovery — PublishScheduleSwitchDiscovery /
// PublishScheduleSwitchState behavioural coverage beyond the happy-path /
// empty-key cases already in schedule_switch_discovery_test.go.
// ---------------------------------------------------------------------------

func TestPublishScheduleSwitchDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("publishes when enabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishScheduleSwitchDiscovery(context.Background(), "ccu1", ScheduleSwitchEvent{
			DeviceAddress:     "0004FEED",
			ScheduleChannelNo: 1,
			Key:               "1_1",
			TargetChannelNo:   18,
			Label:             "Zeitplan Kanal 18",
		})
		if err != nil {
			t.Fatalf("PublishScheduleSwitchDiscovery: %v", err)
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
		err := b.PublishScheduleSwitchDiscovery(context.Background(), "ccu1", ScheduleSwitchEvent{
			DeviceAddress: "0004FEED",
			Key:           "1_1",
		})
		if err != nil {
			t.Fatalf("PublishScheduleSwitchDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when HA discovery disabled, got %d", len(pub.sent))
		}
	})

	t.Run("propagates publisher error", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		pub.err = errors.New("broker unavailable")
		err := b.PublishScheduleSwitchDiscovery(context.Background(), "ccu1", ScheduleSwitchEvent{
			DeviceAddress: "0004FEED",
			Key:           "1_1",
			Label:         "Zeitplan Kanal 18",
		})
		if err == nil {
			t.Fatal("expected error from failing publisher to propagate")
		}
	})
}

func TestPublishScheduleSwitchState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		rawEnabled bool
		enabled    bool
		wantSent   bool
		wantBody   string
	}{
		{name: "publishes true", rawEnabled: true, enabled: true, wantSent: true, wantBody: "true"},
		{name: "publishes false", rawEnabled: true, enabled: false, wantSent: true, wantBody: "false"},
		{name: "no-op when raw disabled", rawEnabled: false, enabled: true, wantSent: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) {
				c.RawEnabled = tc.rawEnabled
			})
			err := b.PublishScheduleSwitchState(context.Background(), "ccu1", "HmIP-RF", "0004FEED", 1, "1_1", tc.enabled)
			if err != nil {
				t.Fatalf("PublishScheduleSwitchState: %v", err)
			}
			if tc.wantSent {
				if len(pub.sent) == 0 {
					t.Fatal("expected a state publish")
				}
				last := pub.sent[len(pub.sent)-1]
				if last.payload != tc.wantBody {
					t.Errorf("payload = %q, want %q", last.payload, tc.wantBody)
				}
				if !last.retain {
					t.Error("expected the state publish to be retained")
				}
			} else if len(pub.sent) != 0 {
				t.Fatalf("expected no publish when raw disabled, got %d", len(pub.sent))
			}
		})
	}
}
