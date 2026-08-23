// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
				if err := b.PublishCombinedState(ctx, central, iface, addr, 1, "duration", "30"); err != nil {
					t.Fatalf("PublishCombinedState(duration): %v", err)
				}
				return b.topics.CombinedState(central, iface, addr, 1, "duration")
			},
		},
		{
			name: "combined_sensor",
			publish: func(t *testing.T, b *Bridge) string {
				t.Helper()
				if err := b.PublishCombinedState(ctx, central, iface, addr, 1, "hs_color", `{"h":1}`); err != nil {
					t.Fatalf("PublishCombinedState(hs_color): %v", err)
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

// TestRetractRawStateForDeviceMatchesMixedCaseAddresses pins the raw
// retraction against the one class of address a CCU spells in mixed case.
//
// Every raw topic is written with the address upper-cased, while the
// removal event carries the CCU's own spelling — `HmIP-RCV-1`,
// `BidCoS-RF`, `BidCoS-Wir`. A case-sensitive needle matched none of
// their retained topics, so the virtual remote's last button state and
// its `/config` companion stayed on the broker reading `available: true`
// while its availability topic said the device was gone.
func TestRetractRawStateForDeviceMatchesMixedCaseAddresses(t *testing.T) {
	t.Parallel()

	const (
		central = "ccu01"
		iface   = "HmIP-RF"
		addr    = "HmIP-RCV-1" // the CCU's spelling of the virtual remote
	)
	ctx := context.Background()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", RawEnabled: true, CentralName: central}, mp)

	slot := pload.TopicSlot{Address: addr, Channel: 1, Bucket: pload.BucketValues, Parameter: "PRESS_SHORT"}
	if err := b.PublishSlotState(ctx, central, iface, slot, pload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}
	stateTopic := b.topics.SlotState(central, iface, slot)
	before := len(mp.publications())

	b.RetractRawStateForDevice(ctx, central, iface, addr)

	var found bool
	for _, p := range mp.publications()[before:] {
		if p.topic == stateTopic {
			found = true
			if p.payload != "" || !p.retain {
				t.Fatalf("retract %q: payload=%q retain=%v, want an empty retained publish", stateTopic, p.payload, p.retain)
			}
		}
	}
	if !found {
		t.Fatalf("removal left the retained payload on %q", stateTopic)
	}
	b.mu.Lock()
	_, stillTracked := b.rawTopics[stateTopic]
	b.mu.Unlock()
	if stillTracked {
		t.Fatalf("%q is still in the raw-topic index after retraction", stateTopic)
	}
}

// TestHubDiscoveryRetractionLeavesTheDeclaredSet pins that retracting a hub
// entity — the empty-payload publish the hub publisher emits when the CCU
// operator deletes a program or a system variable — removes the topic from the
// `declared` set instead of parking an empty entry there.
//
// The set is what the retained-orphan sweeps treat as "the entities this build
// drives" and what the HA-birth replay re-publishes. A retracted entity left in
// it shields its own topic from the sweep forever and makes every replay push
// an empty payload to a topic the broker no longer retains.
func TestHubDiscoveryRetractionLeavesTheDeclaredSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", HADiscoveryEnabled: true}, mp)
	item := DiscoveryItem{
		Component: "button", NodeID: "ccu01_hub_program", ObjectID: "morning_press",
		Payload: []byte(`{"name":"Morning"}`), OK: true,
	}
	topic := b.topics.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID)

	if err := b.PublishHubDiscovery(ctx, item); err != nil {
		t.Fatalf("PublishHubDiscovery: %v", err)
	}
	b.mu.Lock()
	_, declared := b.declared[topic]
	b.mu.Unlock()
	if !declared {
		t.Fatalf("%q is not in the declared set after the config was published", topic)
	}

	retraction := item
	retraction.Payload = nil
	if err := b.PublishHubDiscovery(ctx, retraction); err != nil {
		t.Fatalf("PublishHubDiscovery (retraction): %v", err)
	}
	if got := len(mp.publications()); got != 2 {
		t.Fatalf("publications = %d, want 2 (the retraction was swallowed by the dedup gate)", got)
	}
	b.mu.Lock()
	_, stillDeclared := b.declared[topic]
	b.mu.Unlock()
	if stillDeclared {
		t.Fatalf("%q is still in the declared set after its config was retracted", topic)
	}

	before := len(mp.publications())
	if err := b.RepublishDiscovery(ctx); err != nil {
		t.Fatalf("RepublishDiscovery: %v", err)
	}
	if got := len(mp.publications()) - before; got != 0 {
		t.Fatalf("republished topics = %d, want 0 (the retracted entity was resurrected)", got)
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

// TestRetractionForOneCentralLeavesTheOtherCentralsTopics pins the scope of
// device retraction on a daemon serving two CCUs.
//
// Device addresses are unique per CCU but not across CCUs: the virtual
// remote and the BidCoS pseudo devices carry the identical address on every
// one of them. An address-only match therefore reached the second CCU's
// live entities whenever the first CCU was deleted or had its cache reset —
// the entities vanished from Home Assistant, and their raw-plane state was
// blanked, until the next daemon restart republished them.
func TestRetractionForOneCentralLeavesTheOtherCentralsTopics(t *testing.T) {
	t.Parallel()

	const (
		centralA = "ccu-a"
		centralB = "ccu-b"
		iface    = "BidCos-RF"
		addr     = "BidCoS-RF" // repeats verbatim on every CCU
	)
	ctx := context.Background()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base: "loom", CentralName: centralA, CentralNames: []string{centralA, centralB},
		RawEnabled: true, HADiscoveryEnabled: true,
	}, mp)

	slot := pload.TopicSlot{Address: addr, Channel: 1, Bucket: pload.BucketValues, Parameter: "PRESS_SHORT"}
	discoveryTopics := map[string]string{}
	stateTopics := map[string]string{}
	for _, central := range []string{centralA, centralB} {
		if err := b.PublishSlotState(ctx, central, iface, slot,
			pload.PerDPState{Value: true, Available: true}); err != nil {
			t.Fatalf("%s: PublishSlotState: %v", central, err)
		}
		stateTopics[central] = b.topics.SlotState(central, iface, slot)

		nodeID := naming.NewDevicePathData(hmtypes.ParseWireInterfaceID(iface), addr).DiscoveryNodeID(central)
		if err := b.publishDiscovery(ctx, "event", nodeID, "1_press_short",
			[]byte(`{"name":"`+central+`"}`)); err != nil {
			t.Fatalf("%s: publishDiscovery: %v", central, err)
		}
		discoveryTopics[central] = b.topics.DiscoveryConfig("event", nodeID, "1_press_short")
	}
	if discoveryTopics[centralA] == discoveryTopics[centralB] {
		t.Fatalf("both centrals produced the same discovery topic %q — the fixture cannot show the scoping",
			discoveryTopics[centralA])
	}

	b.RetractDiscoveryForCentralDevice(ctx, centralA, addr)
	b.RetractRawStateForDevice(ctx, centralA, iface, addr)

	b.mu.Lock()
	_, declaredA := b.declared[discoveryTopics[centralA]]
	_, declaredB := b.declared[discoveryTopics[centralB]]
	_, rawA := b.rawTopics[stateTopics[centralA]]
	_, rawB := b.rawTopics[stateTopics[centralB]]
	b.mu.Unlock()

	if declaredA {
		t.Errorf("the removed central's discovery config %q survived", discoveryTopics[centralA])
	}
	if rawA {
		t.Errorf("the removed central's raw state topic %q survived", stateTopics[centralA])
	}
	if !declaredB {
		t.Errorf("removing %s retracted %s's discovery config %q", centralA, centralB, discoveryTopics[centralB])
	}
	if !rawB {
		t.Errorf("removing %s retracted %s's raw state topic %q", centralA, centralB, stateTopics[centralB])
	}

	cleared := map[string]bool{}
	for _, p := range mp.publications() {
		if p.retain && p.payload == "" {
			cleared[p.topic] = true
		}
	}
	if !cleared[discoveryTopics[centralA]] || !cleared[stateTopics[centralA]] {
		t.Errorf("the removed central's topics were not cleared on the broker: %v", cleared)
	}
	if cleared[discoveryTopics[centralB]] || cleared[stateTopics[centralB]] {
		t.Errorf("the surviving central's topics were cleared on the broker: %v", cleared)
	}
}
