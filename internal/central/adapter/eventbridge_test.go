// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// nonAvailabilityPublishes filters out the orthogonal side-effect
// publishes (per-device availability + ADR-0011 per-DP slot configs)
// from a recorded slice. Most assertions only care about the
// per-DP slot state publish (the canonical value topic). Centralising
// the filter avoids fragile "expected 1 publish, got 2" assertions
// when the bridge gains additional dispatch arms.
func nonAvailabilityPublishes(got []mqtt.Publication) []mqtt.Publication {
	out := make([]mqtt.Publication, 0, len(got))
	for _, p := range got {
		// Trailing segment "/availability" identifies device-availability
		// publishes regardless of central / interface / address shape.
		if strings.HasSuffix(p.Topic, "/availability") {
			continue
		}
		// Old ADR 0011 phase 1b slot topics (now retired but guard kept
		// for tests that still exercise the old "/channels/<n>/..." shape).
		if strings.Contains(p.Topic, "/channels/") &&
			(strings.HasSuffix(p.Topic, "/state") || strings.HasSuffix(p.Topic, "/config")) {
			continue
		}
		// New bucket-aware topology: the slot-config companion
		// "<addr>/<ch>/<bucket>/<param>/config" carries static descriptor
		// metadata (min/max/value_list/unit/usage). Filter it so
		// assertions only see the slot-state publish.
		if strings.HasSuffix(p.Topic, "/config") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// stubVisSet is a test-double for filter.VisibilitySet that hides the
// parameters listed in its `hidden` map. The WS path is never consulted.
type stubVisSet struct {
	hidden map[hmenum.Parameter]bool
}

func newStubVis(hidden ...hmenum.Parameter) *stubVisSet {
	m := make(map[hmenum.Parameter]bool, len(hidden))
	for _, p := range hidden {
		m[p] = true
	}
	return &stubVisSet{hidden: m}
}

func (s *stubVisSet) Visible(_, _ string, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.hidden[p]
}

func (s *stubVisSet) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.hidden[p]
}

func TestEventBridgeValueChangedFansOut(t *testing.T) {
	reg, d := registryWithDevice(t)

	wsHub := ws.NewHub()
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	ebridge := NewEventBridge(reg, wsHub, mw)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	// Emit a change via the central's bus.
	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("registry size %d", len(list))
	}
	events.Publish(list[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	// MQTT assertion. Filter the orthogonal availability + config side-publishes.
	// The new bucket-aware topology produces "openccu-loom/<central>/<iface>/<addr>/<ch>/values/<param>".
	// inferInterface returns "" for these synthetic events so the iface segment is empty.
	if got := nonAvailabilityPublishes(pub.Published()); len(got) != 1 || got[0].Topic != "openccu-loom/ccu-01//0001ABCD/1/values/STATE" {
		t.Fatalf("mqtt published=%+v", got)
	}
}

// TestEventBridgeDeviceRemovedRetractsRawState reproduces the B4 bug: the
// raw-plane per-DP state topic a device published while alive survived a
// DeviceRemovedEvent forever because onDeviceRemoved only retracted
// HA-Discovery configs. It publishes a real value, fires DeviceRemovedEvent
// on the owning central's bus, and asserts the bridge clears both the
// per-DP state topic and the device-scoped availability/info/diagnostics
// topics with an empty retained publish.
func TestEventBridgeDeviceRemovedRetractsRawState(t *testing.T) {
	reg, d := registryWithDevice(t)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	ebridge := NewEventBridge(reg, ws.NewHub(), mw)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("registry size %d", len(list))
	}
	unit := list[0]

	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	before := nonAvailabilityPublishes(pub.Published())
	if len(before) != 1 {
		t.Fatalf("setup: expected 1 state publish before removal, got %+v", before)
	}
	stateTopic := before[0].Topic

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: d.InterfaceID,
		Address:     d.Address,
	})
	ebridge.Flush()

	seen := map[string]mqtt.Publication{}
	for _, p := range pub.Published() {
		seen[p.Topic] = p
	}
	wantTopics := []string{
		stateTopic,
		bridge.Topics().DeviceAvailability("ccu-01", d.InterfaceID, d.Address),
		bridge.Topics().DeviceInfo("ccu-01", d.InterfaceID, d.Address),
		bridge.Topics().DeviceDiagnostics("ccu-01", d.InterfaceID, d.Address),
	}
	for _, topic := range wantTopics {
		p, ok := seen[topic]
		if !ok {
			t.Fatalf("expected a retracting publish to %q after DeviceRemovedEvent, none seen (got %+v)", topic, pub.Published())
		}
		if len(p.Payload) != 0 {
			t.Fatalf("retract %q: payload not empty (%q)", topic, p.Payload)
		}
	}
}

func TestEventBridgeCentralStateFansOutWS(t *testing.T) {
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	wsHub := ws.NewHub()
	bridge := NewEventBridge(reg, wsHub, nil)
	bridge.Start(context.Background())
	defer bridge.Stop()

	events.Publish(c.EventBus, hmevent.CentralStateChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		From:        hmenum.CentralStateStarting,
		To:          hmenum.CentralStateRunning,
	})
	// wsHub exposes no inspection surface; smoke test verifies Start
	// path runs without panic. Real end-to-end test lives in the ws
	// package.
}

// ---------------------------------------------------------------------------
// Cluster — MQTT outbound visibility filter (ADR 0007)
// ---------------------------------------------------------------------------

// TestVisibilityFilterAppliedAtMQTTOutbound verifies that a hidden parameter
// is NOT forwarded to MQTT when a VisibilitySet is wired into EventBridge.
//
// This is the ADR-0007 contract test for the MQTT outbound filter.
func TestVisibilityFilterAppliedAtMQTTOutbound(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	// Hide "STATE" — every other parameter is visible.
	vis := newStubVis(hmenum.ParameterState)

	ebridge := NewEventBridge(reg, nil, mw).WithVisibility(vis)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	list := reg.List()
	events.Publish(list[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	// Hidden parameter must not be published to MQTT.
	if got := pub.Published(); len(got) != 0 {
		t.Fatalf("hidden parameter was published to MQTT: %+v", got)
	}
}

// TestVisibilityFilterDoesNotBlockVisibleMQTTPublish verifies that a visible
// parameter IS forwarded to MQTT when a VisibilitySet is wired.
func TestVisibilityFilterDoesNotBlockVisibleMQTTPublish(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	// Hide LEVEL but not STATE.
	vis := newStubVis(hmenum.ParameterLevel)

	ebridge := NewEventBridge(reg, nil, mw).WithVisibility(vis)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	list := reg.List()
	events.Publish(list[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(false),
	})
	ebridge.Flush()

	// Visible parameter must reach MQTT (filter availability side-publish).
	if got := nonAvailabilityPublishes(pub.Published()); len(got) != 1 {
		t.Fatalf("expected 1 MQTT publish for visible param, got %d: %+v", len(got), got)
	}
}

// TestPerDataPointVisibilityGateBlocksHiddenDP pins the
// HmIP-BWTH ch10/11/12 STATE-leak fix. The static
// [filter.VisibilitySet] only consults the global rules
// (ignoredParameters / hiddenParameters) — it does not see the
// per-DP forced-usage marks the materialiser +
// SuppressUndefinedGenericDataPoints pass apply at runtime. Without
// the per-DP visibility gate, every channel of the BWTH
// that exposes STATE (ch9 visible by profile, ch10/11/12
// suppressed) would still publish discovery + state to MQTT.
//
// This test sets up a single device + channel + STATE DP, marks the
// DP as forced [hmenum.DataPointUsageNoCreate] (the same mark the
// suppression pass applies), and verifies the value-changed event
// is NOT forwarded to MQTT.
func TestPerDataPointVisibilityGateBlocksHiddenDP(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)
	ch := d.AddChannel(d.Address+":10", 10, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: d.Address + ":10",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// Suppression-equivalent: undefined generic DP on a custom-DP
	// device gets force-marked NoCreate.
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	ch.Put(dp)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	// No global visibility filter — proves the per-DP gate carries the
	// suppression on its own.
	ebridge := NewEventBridge(reg, nil, mw)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":10",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	if got := pub.Published(); len(got) != 0 {
		t.Fatalf("DP forced-marked NoCreate must not reach MQTT; got %+v", got)
	}
}

// TestPerDataPointVisibilityGateLetsVisibleDPsPass is the positive
// counterpart: a DP with a user-facing forced usage (CDPVisible / DataPoint)
// — the same marks `applyFieldVisibility` and `markAdditionalDataPoints`
// install — must continue to publish.
func TestPerDataPointVisibilityGateLetsVisibleDPsPass(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)
	ch := d.AddChannel(d.Address+":9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: d.Address + ":9",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// Profile-visible mark: ch9 STATE on HmIP-BWTH is the switch
	// output the IPThermostat profile makes visible.
	dp.SetForcedUsage(hmenum.DataPointUsageCDPVisible)
	ch.Put(dp)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	ebridge := NewEventBridge(reg, nil, mw)
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":9",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	if got := nonAvailabilityPublishes(pub.Published()); len(got) != 1 {
		t.Fatalf("CDPVisible DP must reach MQTT; got %d publishes: %+v", len(got), got)
	}
}

// TestVisibilityFilterNilAllowsAllMQTTPublish verifies that a nil VisibilitySet
// does not gate any MQTT publish (backward-compat no-op behavior).
func TestVisibilityFilterNilAllowsAllMQTTPublish(t *testing.T) {
	t.Parallel()
	reg, d := registryWithDevice(t)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	// No visibility filter wired.
	ebridge := NewEventBridge(reg, nil, mw) // vis defaults to nil
	ebridge.Start(context.Background())
	defer ebridge.Stop()

	list := reg.List()
	events.Publish(list[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ON_TIME_LIST_1",
		},
		NewValue: hmtypes.BoolValue(true),
	})
	ebridge.Flush()

	// Nil vis → everything is published (filter availability side-publish).
	if got := nonAvailabilityPublishes(pub.Published()); len(got) != 1 {
		t.Fatalf("nil vis: expected 1 MQTT publish, got %d: %+v", len(got), got)
	}
}

// ============================================================
// publishCustomDPState — nil channel path
// ============================================================

func TestPublishCustomDPStateNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{} // mqtt is nil
	b.publishCustomDPState(context.Background(), "ccu-01", "HmIP-RF", "DEV001", 1, nil)
}

func TestPublishCustomDPStateNilChannel(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	b.publishCustomDPState(context.Background(), "ccu-01", "HmIP-RF", "DEV001", 1, nil)
}

func TestPublishCustomDPStateNoCustomDP(t *testing.T) {
	t.Parallel()
	b := &EventBridge{} // mqtt nil → returns before customDP check
	dev := device.New(device.Config{Address: "EPDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("EPDEV001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// No custom DP attached → short-circuit
	b.publishCustomDPState(context.Background(), "ccu-01", "HmIP-RF", "EPDEV001", 1, ch)
}

// ============================================================
// publishCustomDPConfig — nil channel / nil mqtt paths
// ============================================================

func TestPublishCustomDPConfigNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{} // mqtt is nil
	dev := device.New(device.Config{Address: "EPDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("EPDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	b.publishCustomDPConfig(context.Background(), "ccu-01", "HmIP-RF", "EPDEV002", 1, ch)
}

func TestPublishCustomDPConfigNilChannel(t *testing.T) {
	t.Parallel()
	b := &EventBridge{}
	b.publishCustomDPConfig(context.Background(), "ccu-01", "HmIP-RF", "DEV001", 1, nil)
}

// ============================================================
// publishWeekProfileSnapshot — nil mqtt path
// ============================================================

func TestPublishWeekProfileSnapshotNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{} // mqtt nil → early return
	dev := device.New(device.Config{Address: "WPDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("WPDEV001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	b.publishWeekProfileSnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)
}

// ============================================================
// publishDeviceDiagnostics — nil mqtt path
// ============================================================

func TestPublishDeviceDiagnosticsNilMQTT(t *testing.T) {
	t.Parallel()
	b := &EventBridge{} // mqtt nil → early return
	dev := device.New(device.Config{Address: "DIAGDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	b.publishDeviceDiagnostics(context.Background(), "ccu-01", "HmIP-RF", dev)
}

// ============================================================
// EventBridge WS unique_id propagation
// ============================================================

// TestEventBridgeDataPointValueChangedUniqueID verifies that the EventBridge
// fans a DataPointValueChangedEvent to the WebSocket hub with the correct
// canonical unique_id on the DataPointValueChangedPayload. Normal device
// addresses carry no serial prefix; the hub receives the correct loom-namespaced
// key.
func TestEventBridgeDataPointValueChangedUniqueID(t *testing.T) {
	t.Parallel()

	reg, _ := registryWithDevice(t)
	// Fetch the central to publish on its bus.
	c, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("central ccu-01 not found in registry")
	}

	wsHub := ws.NewHub()
	bridge := NewEventBridge(reg, wsHub, nil)
	bridge.Start(context.Background())
	defer bridge.Stop()

	events.Publish(c.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})

	wantTopic := ws.DataPointTopic("0001ABCD", 1, "STATE")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := wsHub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) > 0 {
			p, castOK := res.Events[0].Payload.(ws.DataPointValueChangedPayload)
			if !castOK {
				t.Fatalf("payload type %T, want DataPointValueChangedPayload", res.Events[0].Payload)
			}
			if want := "loom_0001abcd_1_state"; p.UniqueID != want {
				t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DataPointValueChangedEvent did not reach the WS hub within deadline")
}

// ============================================================
// Combined-DP / schedule discovery publish errors are observable
// ============================================================

// failingPublisher is a [mqtt.Publisher] that always fails. Used to prove
// that a broker-level publish failure from the discardable
// `_ = bridge.PublishXxxDiscovery(...)` call sites in this file is not
// silently swallowed end-to-end — it still lands on the bridge's
// publish_errors counter.
type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, []byte, mqtt.QoS, bool, ...mqtt.PublishOption) error {
	return errors.New("broker unavailable")
}

// TestPublishScheduleEntitySnapshotDiscardedErrorIsCounted verifies that
// publishScheduleEntitySnapshot's discarded
// `_ = bridge.PublishScheduleEntityDiscovery(...)` error still increments
// the bridge's publish_errors metric, so a broker outage during schedule
// discovery is observable even though the call site ignores the error
// value.
func TestPublishScheduleEntitySnapshotDiscardedErrorIsCounted(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	collector := metrics.NewMqttCollector(reg)
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: collector,
	}, failingPublisher{})
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(nil, nil, mw)
	dev := device.New(device.Config{Address: "SCHEDDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("SCHEDDEV001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	ch.AttachWeekProfile(weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		CentralName:    "ccu-01",
		ChannelAddress: ch.Address,
	}))

	if before := collector.PublishErrors("ccu-01").Value(); before != 0 {
		t.Fatalf("publish_errors before call = %d, want 0", before)
	}

	eb.publishScheduleEntitySnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)

	if after := collector.PublishErrors("ccu-01").Value(); after == 0 {
		t.Fatal("publish_errors counter did not increment after a failing PublishScheduleEntityDiscovery call — the discarded error is unobservable")
	}
}

// TestPublishCombinedDPSnapshotDiscardedErrorIsCounted mirrors
// TestPublishScheduleEntitySnapshotDiscardedErrorIsCounted for the
// combined-DP (Timer) discovery path.
func TestPublishCombinedDPSnapshotDiscardedErrorIsCounted(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	collector := metrics.NewMqttCollector(reg)
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: collector,
	}, failingPublisher{})
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(nil, nil, mw)
	dev := device.New(device.Config{Address: "TIMERDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-ASIR"})
	ch := dev.AddChannel("TIMERDEV001:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	timer := combined.NewTimer(ch.Address, &noopCombinedWriter{}, "DURATION_VALUE", "DURATION_UNIT")
	ch.AttachCalculatedDataPoint(timer)

	if before := collector.PublishErrors("ccu-01").Value(); before != 0 {
		t.Fatalf("publish_errors before call = %d, want 0", before)
	}

	eb.publishCombinedDPSnapshot(context.Background(), "ccu-01", "HmIP-RF", dev, ch)

	if after := collector.PublishErrors("ccu-01").Value(); after == 0 {
		t.Fatal("publish_errors counter did not increment after a failing PublishCombinedTimerDiscovery call — the discarded error is unobservable")
	}
}

// noopCombinedWriter is a no-op [combined.Writer] — the discovery-publish
// paths under test never invoke SetValue.
type noopCombinedWriter struct{}

func (noopCombinedWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}
