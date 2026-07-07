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
// BuildCombinedTimerDiscovery
// ---------------------------------------------------------------------------

func TestBuildCombinedTimerDiscovery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ev      CombinedTimerEvent
		wantOK  bool
		checkFn func(t *testing.T, item DiscoveryItem)
	}{
		{
			name: "happy path",
			ev: CombinedTimerEvent{
				Central:       "ccu1",
				Interface:     "HmIP-RF",
				DeviceAddress: "0001ABCD",
				ChannelNo:     1,
				DeviceName:    "Test Device",
				Model:         "HmIP-ASIR",
				Kind:          "duration",
				Label:         "Zeitdauer",
				Unit:          "s",
				MinSeconds:    0,
				MaxSeconds:    3600,
				Step:          1,
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				if item.Component != string(HAComponentNumber) {
					t.Errorf("Component = %q, want number", item.Component)
				}
				if !strings.Contains(item.ObjectID, "duration") {
					t.Errorf("ObjectID = %q, want it to contain kind", item.ObjectID)
				}
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if body["name"] != "Zeitdauer" {
					t.Errorf("name = %v, want Zeitdauer", body["name"])
				}
				if body["max"] != float64(3600) {
					t.Errorf("max = %v, want 3600", body["max"])
				}
				if body["unit_of_measurement"] != "s" {
					t.Errorf("unit_of_measurement = %v, want s", body["unit_of_measurement"])
				}
			},
		},
		{
			name: "empty kind rejected",
			ev: CombinedTimerEvent{
				DeviceAddress: "0001ABCD",
				Kind:          "",
			},
			wantOK: false,
		},
		{
			name: "empty device address rejected",
			ev: CombinedTimerEvent{
				DeviceAddress: "",
				Kind:          "duration",
			},
			wantOK: false,
		},
		{
			name: "zero MaxSeconds omits max field",
			ev: CombinedTimerEvent{
				DeviceAddress: "0001ABCD",
				Kind:          "duration",
				Label:         "Zeitdauer",
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if _, ok := body["max"]; ok {
					t.Error("max must be omitted when MaxSeconds <= 0")
				}
			},
		},
		{
			name: "zero step defaults to 1",
			ev: CombinedTimerEvent{
				DeviceAddress: "0001ABCD",
				Kind:          "duration",
				Label:         "Zeitdauer",
				Step:          0,
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if body["step"] != float64(1) {
					t.Errorf("step = %v, want 1 (default)", body["step"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
			item := builder.BuildCombinedTimerDiscovery("ccu1", tc.ev)
			if item.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", item.OK, tc.wantOK)
			}
			if tc.wantOK && tc.checkFn != nil {
				tc.checkFn(t, item)
			}
		})
	}
}

func TestPublishCombinedTimerDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("publishes when enabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishCombinedTimerDiscovery(context.Background(), "ccu1", CombinedTimerEvent{
			DeviceAddress: "0001ABCD",
			Kind:          "duration",
			Label:         "Zeitdauer",
		})
		if err != nil {
			t.Fatalf("PublishCombinedTimerDiscovery: %v", err)
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
		err := b.PublishCombinedTimerDiscovery(context.Background(), "ccu1", CombinedTimerEvent{
			DeviceAddress: "0001ABCD",
			Kind:          "duration",
		})
		if err != nil {
			t.Fatalf("PublishCombinedTimerDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when HA discovery disabled, got %d", len(pub.sent))
		}
	})

	t.Run("no-op when builder declines event", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishCombinedTimerDiscovery(context.Background(), "ccu1", CombinedTimerEvent{
			DeviceAddress: "", // declined: BuildCombinedTimerDiscovery returns OK=false
			Kind:          "duration",
		})
		if err != nil {
			t.Fatalf("PublishCombinedTimerDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when builder declines, got %d", len(pub.sent))
		}
	})

	t.Run("propagates publisher error", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		pub.err = errors.New("broker unavailable")
		err := b.PublishCombinedTimerDiscovery(context.Background(), "ccu1", CombinedTimerEvent{
			DeviceAddress: "0001ABCD",
			Kind:          "duration",
			Label:         "Zeitdauer",
		})
		if err == nil {
			t.Fatal("expected error from failing publisher to propagate")
		}
	})
}

func TestPublishCombinedTimerState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		rawEnabled bool
		seconds    float64
		wantSent   bool
		wantBody   string
	}{
		{name: "publishes positive value", rawEnabled: true, seconds: 42, wantSent: true, wantBody: "42"},
		{name: "clamps negative to zero", rawEnabled: true, seconds: -5, wantSent: true, wantBody: "0"},
		{name: "no-op when raw disabled", rawEnabled: false, seconds: 10, wantSent: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) {
				c.RawEnabled = tc.rawEnabled
			})
			err := b.PublishCombinedTimerState(context.Background(), "ccu1", "HmIP-RF", "0001ABCD", 1, "duration", tc.seconds)
			if err != nil {
				t.Fatalf("PublishCombinedTimerState: %v", err)
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

func TestFormatSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{42.5, "42.5"},
		{100.25, "100.25"},
	}
	for _, tc := range cases {
		got := formatSeconds(tc.in)
		if got != tc.want {
			t.Errorf("formatSeconds(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildCombinedSensorDiscovery
// ---------------------------------------------------------------------------

func TestBuildCombinedSensorDiscovery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ev      CombinedSensorEvent
		wantOK  bool
		checkFn func(t *testing.T, item DiscoveryItem)
	}{
		{
			name: "happy path with value template and unit",
			ev: CombinedSensorEvent{
				DeviceAddress: "0002BEEF",
				ChannelNo:     3,
				Kind:          "level_combined",
				Label:         "Rollladen Position",
				ValueTemplate: "{{ value_json.level }}",
				Unit:          "%",
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				if item.Component != string(HAComponentSensor) {
					t.Errorf("Component = %q, want sensor", item.Component)
				}
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if body["value_template"] != "{{ value_json.level }}" {
					t.Errorf("value_template = %v", body["value_template"])
				}
				if body["unit_of_measurement"] != "%" {
					t.Errorf("unit_of_measurement = %v, want %%", body["unit_of_measurement"])
				}
				if _, ok := body["command_topic"]; ok {
					t.Error("sensor entity must not carry a command_topic")
				}
			},
		},
		{
			name: "no value template or unit omits fields",
			ev: CombinedSensorEvent{
				DeviceAddress: "0002BEEF",
				Kind:          "hs_color",
				Label:         "Farbe",
			},
			wantOK: true,
			checkFn: func(t *testing.T, item DiscoveryItem) {
				t.Helper()
				var body map[string]any
				if err := json.Unmarshal(item.Payload, &body); err != nil {
					t.Fatalf("payload is not valid JSON: %v", err)
				}
				if _, ok := body["value_template"]; ok {
					t.Error("value_template must be omitted when empty")
				}
				if _, ok := body["unit_of_measurement"]; ok {
					t.Error("unit_of_measurement must be omitted when empty")
				}
			},
		},
		{
			name:   "empty kind rejected",
			ev:     CombinedSensorEvent{DeviceAddress: "0002BEEF", Kind: ""},
			wantOK: false,
		},
		{
			name:   "empty device address rejected",
			ev:     CombinedSensorEvent{DeviceAddress: "", Kind: "hs_color"},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
			item := builder.BuildCombinedSensorDiscovery("ccu1", tc.ev)
			if item.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", item.OK, tc.wantOK)
			}
			if tc.wantOK && tc.checkFn != nil {
				tc.checkFn(t, item)
			}
		})
	}
}

func TestPublishCombinedSensorDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("publishes when enabled", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		err := b.PublishCombinedSensorDiscovery(context.Background(), "ccu1", CombinedSensorEvent{
			DeviceAddress: "0002BEEF",
			Kind:          "level_combined",
			Label:         "Position",
		})
		if err != nil {
			t.Fatalf("PublishCombinedSensorDiscovery: %v", err)
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
		err := b.PublishCombinedSensorDiscovery(context.Background(), "ccu1", CombinedSensorEvent{
			DeviceAddress: "0002BEEF",
			Kind:          "level_combined",
		})
		if err != nil {
			t.Fatalf("PublishCombinedSensorDiscovery: %v", err)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("expected no publish when HA discovery disabled, got %d", len(pub.sent))
		}
	})

	t.Run("propagates publisher error", func(t *testing.T) {
		t.Parallel()
		b, pub := newTestBridge(t)
		pub.err = errors.New("broker unavailable")
		err := b.PublishCombinedSensorDiscovery(context.Background(), "ccu1", CombinedSensorEvent{
			DeviceAddress: "0002BEEF",
			Kind:          "level_combined",
			Label:         "Position",
		})
		if err == nil {
			t.Fatal("expected error from failing publisher to propagate")
		}
	})
}

func TestPublishCombinedSensorState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		rawEnabled bool
		wantSent   bool
	}{
		{name: "publishes JSON state", rawEnabled: true, wantSent: true},
		{name: "no-op when raw disabled", rawEnabled: false, wantSent: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) {
				c.RawEnabled = tc.rawEnabled
			})
			jsonState := `{"level":0.5,"slats":0.25}`
			err := b.PublishCombinedSensorState(context.Background(), "ccu1", "HmIP-RF", "0002BEEF", 3, "level_combined", jsonState)
			if err != nil {
				t.Fatalf("PublishCombinedSensorState: %v", err)
			}
			if tc.wantSent {
				if len(pub.sent) == 0 {
					t.Fatal("expected a state publish")
				}
				last := pub.sent[len(pub.sent)-1]
				if last.payload != jsonState {
					t.Errorf("payload = %q, want %q", last.payload, jsonState)
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
