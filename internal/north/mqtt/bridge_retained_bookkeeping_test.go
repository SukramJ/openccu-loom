// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// stateSource is the narrow read side [Bridge.PublishSourceState] takes.
type stateSource struct{ state pload.StatePayload }

func (s stateSource) State() pload.StatePayload { return s.state }

// TestRetractRawStateForDeviceClearsEveryDeviceScopedPublisher pins that
// every retained raw-plane topic a device can own is cleared when the device
// disappears.
//
// Retraction finds a topic only if the publisher recorded it in the
// address-scoped index, and the boot-time orphan sweep only recognises the
// `<iface>/<addr>/<ch>/<bucket>/<PARAM>` shape — so a publisher that skips
// the bookkeeping leaves its retained payload on the broker forever after an
// unpair, and raw-plane consumers keep reading the removed device's last
// known firmware state, week profile or schedule.
func TestRetractRawStateForDeviceClearsEveryDeviceScopedPublisher(t *testing.T) {
	t.Parallel()

	const (
		central = "ccu01"
		iface   = "HmIP-RF"
		addr    = "AABBCCDD1122"
	)
	ctx := context.Background()

	cases := []struct {
		name    string
		publish func(t *testing.T, b *Bridge) string
	}{
		{
			name: "update",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishUpdateState(ctx, central, iface, addr, pload.StatePayload(map[string]any{"firmware": "1.0.0"})); err != nil {
					t.Fatalf("PublishUpdateState: %v", err)
				}
				return b.topics.DeviceUpdateState(central, iface, addr)
			},
		},
		{
			name: "week_profile",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishWeekProfileState(ctx, central, iface, addr, 1, "P2"); err != nil {
					t.Fatalf("PublishWeekProfileState: %v", err)
				}
				return b.topics.WeekProfileState(central, iface, addr, 1)
			},
		},
		{
			name: "schedule_state",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishScheduleEntityState(ctx, central, iface, addr, 1, 3); err != nil {
					t.Fatalf("PublishScheduleEntityState: %v", err)
				}
				return b.topics.ScheduleEntityState(central, iface, addr, 1)
			},
		},
		{
			name: "schedule_attrs",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishScheduleEntityAttrs(ctx, central, iface, addr, 1, map[string]any{"slots": 3}); err != nil {
					t.Fatalf("PublishScheduleEntityAttrs: %v", err)
				}
				return b.topics.ScheduleEntityAttrs(central, iface, addr, 1)
			},
		},
		{
			name: "schedule_switch",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishScheduleSwitchState(ctx, central, iface, addr, 1, "p1", true); err != nil {
					t.Fatalf("PublishScheduleSwitchState: %v", err)
				}
				return b.topics.ScheduleSwitchState(central, iface, addr, 1, "p1")
			},
		},
		{
			name: "aggregated_source",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				src := stateSource{state: pload.StatePayload(map[string]any{"hvac_mode": "heat"})}
				if err := b.PublishSourceState(ctx, central, iface, addr, 1, src); err != nil {
					t.Fatalf("PublishSourceState: %v", err)
				}
				return b.topics.AggregatedState(central, iface, addr, 1)
			},
		},
		{
			name: "combined_timer",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishCombinedTimerState(ctx, central, iface, addr, 1, "duration", 30); err != nil {
					t.Fatalf("PublishCombinedTimerState: %v", err)
				}
				return b.topics.CombinedState(central, iface, addr, 1, "duration")
			},
		},
		{
			name: "combined_sensor",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishCombinedSensorState(ctx, central, iface, addr, 1, "hs_color", `{"h":1}`); err != nil {
					t.Fatalf("PublishCombinedSensorState: %v", err)
				}
				return b.topics.CombinedState(central, iface, addr, 1, "hs_color")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mp := &mockPublisher{}
			b := NewBridge(BridgeConfig{Base: "loom", RawEnabled: true, CentralName: central}, mp)

			topic := tc.publish(t, b)
			before := len(mp.publications())

			b.RetractRawStateForDevice(ctx, central, iface, addr)

			var retracted *publishRecord
			for _, p := range mp.publications()[before:] {
				if p.topic == topic {
					retracted = &p
					break
				}
			}
			if retracted == nil {
				t.Fatalf("removal left the retained payload on %q", topic)
			}
			if retracted.payload != "" {
				t.Fatalf("retract %q: payload = %q, want empty", topic, retracted.payload)
			}
			if !retracted.retain {
				t.Fatalf("retract %q: must be a retained publish", topic)
			}
		})
	}
}

// failingPublisher fails every publish whose topic contains failFor, so a
// test can model a broker outage or an open circuit breaker that affects
// part of the traffic only.
type failingPublisher struct {
	mockPublisher
	failFor string
}

var errPublishRefused = errors.New("broker refused the publish")

func (f *failingPublisher) Publish(ctx context.Context, topic string, payload []byte, qos QoS, retain bool, opts ...PublishOption) error {
	if f.failFor != "" && strings.Contains(topic, f.failFor) {
		return errPublishRefused
	}
	return f.mockPublisher.Publish(ctx, topic, payload, qos, retain, opts...)
}

// TestPublishDiscoveryRepublishesAfterAFailedPublish pins that the dedup
// cache reflects what the broker accepted, not what the daemon attempted.
//
// The production publisher sits behind a publish-only circuit breaker: a
// broker outage during the boot snapshot fails every discovery publish. If
// the payload were cached anyway, the identical payload rebuilt from the next
// value event would hit the dedup gate and publish nothing — the entity would
// stay absent from Home Assistant until an operator restarted HA.
func TestPublishDiscoveryRepublishesAfterAFailedPublish(t *testing.T) {
	t.Parallel()

	mp := &failingPublisher{failFor: "homeassistant/"}
	b := NewBridge(BridgeConfig{Base: "loom", HADiscoveryEnabled: true}, mp)
	ctx := context.Background()
	payload := []byte(`{"name":"Bücherregal"}`)

	if err := b.publishDiscovery(ctx, "switch", "ccu01_aabb", "ch1_state", payload); err == nil {
		t.Fatal("publishDiscovery: want the broker error, got nil")
	}

	mp.failFor = ""
	if err := b.publishDiscovery(ctx, "switch", "ccu01_aabb", "ch1_state", payload); err != nil {
		t.Fatalf("publishDiscovery (broker back): %v", err)
	}
	if got := len(mp.publications()); got != 1 {
		t.Fatalf("publishes after the broker returned = %d, want 1 (the failed payload was cached as declared)", got)
	}
}

// TestPublishSlotConfigRepublishesAfterAFailedPublish is the raw-plane twin
// of the discovery dedup cache: a `/config` companion whose publish failed
// must be retried, not suppressed as already-published.
func TestPublishSlotConfigRepublishesAfterAFailedPublish(t *testing.T) {
	t.Parallel()

	mp := &failingPublisher{failFor: "/config"}
	b := NewBridge(BridgeConfig{Base: "loom", RawEnabled: true, CentralName: "ccu01"}, mp)
	ctx := context.Background()
	slot := pload.TopicSlot{Address: "AABBCCDD1122", Channel: 1, Bucket: pload.BucketValues, Parameter: "LEVEL"}
	cfg := pload.ConfigPayload(map[string]any{"min": 0, "max": 100})

	if err := b.PublishSlotConfig(ctx, "ccu01", "HmIP-RF", slot, cfg); err == nil {
		t.Fatal("PublishSlotConfig: want the broker error, got nil")
	}

	mp.failFor = ""
	if err := b.PublishSlotConfig(ctx, "ccu01", "HmIP-RF", slot, cfg); err != nil {
		t.Fatalf("PublishSlotConfig (broker back): %v", err)
	}
	if got := len(mp.publications()); got != 1 {
		t.Fatalf("publishes after the broker returned = %d, want 1 (the failed payload was cached)", got)
	}
}

// TestRepublishDiscoveryContinuesPastAFailedTopic pins that the HA-birth
// replay is best-effort per topic. A breaker that is open for one topic must
// not abort the replay for every entity behind it — that would leave most of
// the fleet unavailable after a broker restart.
func TestRepublishDiscoveryContinuesPastAFailedTopic(t *testing.T) {
	t.Parallel()

	mp := &failingPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", HADiscoveryEnabled: true}, mp)
	ctx := context.Background()
	for _, object := range []string{"ch1_state", "ch2_state", "ch3_state"} {
		if err := b.publishDiscovery(ctx, "switch", "ccu01_aabb", object, []byte(`{"o":"`+object+`"}`)); err != nil {
			t.Fatalf("publishDiscovery(%s): %v", object, err)
		}
	}

	mp.failFor = "ch2_state"
	before := len(mp.publications())
	err := b.RepublishDiscovery(ctx)
	if err == nil {
		t.Fatal("RepublishDiscovery: want the failing topic reported, got nil")
	}
	if got := len(mp.publications()) - before; got != 2 {
		t.Fatalf("republished topics = %d, want 2 (the replay aborted on the first failure)", got)
	}
}
