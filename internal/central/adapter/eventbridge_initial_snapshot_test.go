// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestPublishInitialSnapshotPushesEveryObservedDataPoint is the
// regression tripwire for the bug where the broker only saw retained
// state for parameters that changed after daemon start. The
// fetch_all_device_data seed populates DataPoints via OnWireValue
// (cache-only, no bus event) — without an explicit snapshot push the
// EventBridge never publishes the initial values, so HA Discovery
// configs never appear and consumers see no data until the device
// itself emits a change.
//
// Contract: PublishInitialSnapshot walks every central → device →
// channel → VALUES data point and publishes one MQTT topic per
// observed value.
func TestPublishInitialSnapshotPushesEveryObservedDataPoint(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	// Seed via OnWireValue — same code path the device pipeline uses
	// for fetch_all_device_data. Crucially, no bus event is emitted.
	if !dp.OnWireValue(true) {
		t.Fatalf("OnWireValue refused to seed")
	}

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	got := pub.Published()
	matched := 0
	availability := 0
	for _, p := range got {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			matched++
			// Slot-state topics carry the PerDPState JSON envelope; verify
			// value:true is present in the payload.
			if !strings.Contains(string(p.Payload), `"value":true`) {
				t.Fatalf("unexpected payload %q for %s (expected JSON with value:true)", p.Payload, p.Topic)
			}
		}
		if strings.HasSuffix(p.Topic, "/0001ABCD/availability") {
			availability++
			if string(p.Payload) != "online" {
				t.Fatalf("unexpected availability payload %q for %s", p.Payload, p.Topic)
			}
		}
	}
	if matched != 1 {
		t.Fatalf("expected 1 publish to .../0001ABCD/1/STATE, got %d (all=%v)", matched, got)
	}
	// HA Discovery declares the device-availability topic with
	// `availability_mode: all`; without an explicit publish HA marks
	// every entity as unavailable. Pin the contract that the initial
	// snapshot publishes one availability message per device.
	if availability != 1 {
		t.Fatalf("expected 1 publish to .../0001ABCD/availability, got %d (all=%v)", availability, got)
	}
}

// TestPublishInitialSnapshotPublishesUnavailableForUnobservedDP guards
// that a DataPoint without an observed value (and no ValueLoader
// installed, so no wire load happens) is published with an explicit
// `available:false` slot state carrying a `null` value — NOT evicted to
// an empty payload. An unobserved DP is not refreshed and therefore fails
// the IsValid() gate (not-refreshed → not valid → unavailable), mirroring
// the reference is_valid gate. The no-eviction contract is unchanged: the
// slot is always published with a JSON body, never with an empty payload.
func TestPublishInitialSnapshotPublishesUnavailableForUnobservedDP(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp) // intentionally NOT seeded; no ValueLoader installed → no wire load

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	// Expect exactly one available:false slot publish (null value) to the
	// state topic and zero empty-payload evictions.
	evictions := 0
	unavailablePublishes := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			switch {
			case len(p.Payload) == 0 && p.Retain:
				evictions++
			case strings.Contains(string(p.Payload), `"available":false`):
				unavailablePublishes++
				if !strings.Contains(string(p.Payload), `"value":null`) {
					t.Errorf("unobserved slot state should carry a null value, got %s", p.Payload)
				}
			}
		}
	}
	if evictions != 0 {
		t.Fatalf("expected 0 evictions for unobserved DP, got %d", evictions)
	}
	if unavailablePublishes != 1 {
		t.Fatalf("expected 1 available:false slot publish for unobserved DP, got %d", unavailablePublishes)
	}
}

// TestPublishInitialSnapshotPublishesOnlineForReachableDevice pins the
// availability contract: a device with no observed UNREACH parameter is
// reachable ([device.Device.Available] defaults to true) and is therefore
// published as availability=online at boot — even when none of its data
// points have reported a value yet. Availability tracks reachability,
// not value observation; the per-DP slot states
// carry `available:true` with `unknown` values until the CCU pushes.
func TestPublishInitialSnapshotPublishesOnlineForReachableDevice(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp) // intentionally NOT seeded — no observed value, but device stays reachable.

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	availabilityPayload := ""
	availabilityCount := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/availability") {
			availabilityCount++
			availabilityPayload = string(p.Payload)
		}
	}
	if availabilityCount != 1 {
		t.Fatalf("expected 1 availability publish, got %d", availabilityCount)
	}
	if availabilityPayload != "online" {
		t.Fatalf("availability payload = %q, want \"online\" (reachable device)", availabilityPayload)
	}
}

// TestEventBridgePublishesGenericDPConfig pins that every value
// change additionally produces a `<bucket>/<param>/config` companion
// publish carrying the descriptor (min/max/value_list/unit/usage).
// Diff-gated: a second identical event must not re-publish the
// config (cache hit).
func TestEventBridgePublishesGenericDPConfig(t *testing.T) {
	t.Parallel()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)
	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	emit := func(v bool) {
		events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
			Base: hmevent.NewBaseAt(time.Now()),
			Key: hmtypes.DataPointKey{
				ChannelAddress: "0001ABCD:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      "STATE",
			},
			NewValue: hmtypes.BoolValue(v),
		})
	}
	emit(true)
	emit(false) // identical descriptor → config must NOT republish.

	// New bucket-aware topology: config topic is "<addr>/<ch>/<bucket>/<param>/config".
	suffix := "/0001ABCD/1/values/STATE/config"
	configCount := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, suffix) {
			configCount++
		}
	}
	if configCount != 1 {
		t.Fatalf("expected exactly 1 /config publish (diff-gated), got %d", configCount)
	}
}

// TestEventBridgeRoutesCalculatedDPToCalculatedBucket pins ADR 0011
// phase 1c: a calculated/synthetic DP (DEW_POINT, DEW_POINT_SPREAD,
// ENTHALPY, …) lands at `channels/<ch>/calculated/<name>/state`,
// NOT at `…/values/<name>/state`. Detected by walking the channel's
// CalculatedDataPoints() list before classifying the slot.
func TestEventBridgeRoutesCalculatedDPToCalculatedBucket(t *testing.T) {
	t.Parallel()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	calcDP := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "DEW_POINT",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Unit:       "°C",
		},
	})
	ch.AttachCalculatedDataPoint(calcDP)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)
	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "DEW_POINT",
		},
		NewValue: hmtypes.FloatValue(9.4),
	})

	// New bucket-aware topology: slot topic is "<addr>/<ch>/<bucket>/<param>"
	// (no "/channels/" infix, no "/state" suffix).
	calcSeen := 0
	wrongBucket := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/1/calculated/DEW_POINT") {
			calcSeen++
		}
		if strings.HasSuffix(p.Topic, "/1/values/DEW_POINT") {
			wrongBucket++
		}
	}
	if calcSeen != 1 {
		t.Fatalf("expected 1 publish at calculated bucket, got %d (all=%v)",
			calcSeen, pub.Published())
	}
	if wrongBucket != 0 {
		t.Fatalf("calculated DP must NOT publish under values bucket; got %d", wrongBucket)
	}
}

// TestEventBridgePublishesADR0011SlotState pins the ADR 0011 phase 1b
// parallel publish path: every value-change event additionally lands
// at the per-DP slot topic (`channels/<n>/values/<param>/state`)
// carrying the canonical [payload.PerDPState] JSON wrapper. Both publish
// paths run in parallel until the legacy aggregate publish is retired.
//
// The model DP is seeded via OnWireValue before the bus event is published
// so that IsValid() returns true and the slot is published with available:true.
// In production a value-change event always corresponds to an observed DP.
func TestEventBridgePublishesADR0011SlotState(t *testing.T) {
	t.Parallel()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// Seed the model DP so IsValid() is true when the bus event arrives.
	if !dp.OnWireValue(true) {
		t.Fatalf("OnWireValue refused to seed")
	}
	ch.Put(dp)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)
	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})

	// New bucket-aware slot-state topic shape:
	// `<base>/<central>/<iface>/<address>/<ch>/<bucket>/<param>`
	// (no "/channels/" infix, no "/state" suffix).
	// inferInterface returns "" for these synthetic events so the
	// iface segment is empty; we match on the structural suffix only.
	wantSuffix := "/0001ABCD/1/values/STATE"
	matched := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, wantSuffix) {
			matched++
			if !strings.Contains(string(p.Payload), `"value":true`) {
				t.Errorf("slot-state payload missing `value:true`: %s", p.Payload)
			}
			if !strings.Contains(string(p.Payload), `"available":true`) {
				t.Errorf("slot-state payload missing `available:true`: %s", p.Payload)
			}
		}
	}
	if matched != 1 {
		t.Fatalf("expected 1 slot-state publish at suffix %q, got %d (all=%v)",
			wantSuffix, matched, pub.Published())
	}
}

// TestEventBridgePublishesOnlineAtBootAndDoesNotRepublish pins the
// availability contract for a reachable device: it is published online
// once at boot (reachability defaults to true, independent of value
// observation), and subsequent non-reachability value-change events do
// NOT republish availability — the cache gate suppresses the redundant
// "online" message.
func TestEventBridgePublishesOnlineAtBootAndDoesNotRepublish(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	// Step 1: boot snapshot publishes online (reachable device).
	eb.PublishInitialSnapshot(context.Background())

	// Step 2: a value-change event arrives — STATE is not a reachability
	// parameter, so availability must NOT be republished (cache-gate).
	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})

	// Step 3: a second value-change event must NOT republish availability
	// either (still online, still non-reachability parameter).
	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(false),
	})

	// Collect availability publishes in order.
	got := pub.Published()
	availability := []string{}
	for _, p := range got {
		if strings.HasSuffix(p.Topic, "/0001ABCD/availability") {
			availability = append(availability, string(p.Payload))
		}
	}
	if len(availability) != 1 {
		t.Fatalf("expected 1 availability publish (online at boot, no republish on value events), got %d: %v", len(availability), availability)
	}
	if availability[0] != "online" {
		t.Errorf("availability = %q, want online", availability[0])
	}
}
