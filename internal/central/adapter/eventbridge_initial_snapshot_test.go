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

// TestPublishInitialSnapshotEvictsUnobservedWhenNoLoader guards that a
// DataPoint without an observed value and no ValueLoader installed
// causes an eviction publish (empty retained payload) instead of a
// value publish. This prevents HA from keeping a stale retained value
// from a previous daemon run while the device hasn't reported yet.
func TestPublishInitialSnapshotEvictsUnobservedWhenNoLoader(t *testing.T) {
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
	ch.Put(dp) // intentionally NOT seeded; no ValueLoader installed → ErrNoValueLoader

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

	// Expect exactly one publish to the state topic with empty payload
	// and retain=true — the stale-eviction message.
	evictions := 0
	valuePublishes := 0
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			if len(p.Payload) == 0 && p.Retain {
				evictions++
			} else {
				valuePublishes++
			}
		}
	}
	if evictions != 1 {
		t.Fatalf("expected 1 eviction publish (empty+retain) for unobserved DP, got %d", evictions)
	}
	if valuePublishes != 0 {
		t.Fatalf("expected no value publishes for unobserved DP, got %d", valuePublishes)
	}
}

// TestPublishInitialSnapshotPublishesOfflineForUnobservedDevice pins
// the offline-availability contract: a device whose data points have
// never been observed is published as availability=offline
// (not online). Without this, HA marks the entity as available, then
// renders empty Jinja templates against the missing state JSON, which
// produces "Invalid modes mode:" / template-variable warnings on every
// MQTT-MQTT-Climate entity.
func TestPublishInitialSnapshotPublishesOfflineForUnobservedDevice(t *testing.T) {
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
	ch.Put(dp) // intentionally NOT seeded — no observed value.

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
	if availabilityPayload != "offline" {
		t.Fatalf("availability payload = %q, want \"offline\" (no observed DP)", availabilityPayload)
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

// TestEventBridgeFlipsAvailabilityToOnlineOnFirstObservedValue pins
// the transition: a device that was unavailable at boot (no observed
// DP → availability=offline) flips to online on its first real
// value-change event. The cache gate ensures subsequent value-change
// events on the same device do NOT republish availability.
func TestEventBridgeFlipsAvailabilityToOnlineOnFirstObservedValue(t *testing.T) {
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

	// Step 1: boot snapshot publishes offline (no observed DP yet).
	eb.PublishInitialSnapshot(context.Background())

	// Step 2: a value-change event arrives — flip to online.
	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
	})

	// Step 3: a second value-change event must NOT republish availability
	// (cache-gate prevents it — the device is already online).
	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
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
	if len(availability) != 2 {
		t.Fatalf("expected 2 availability publishes (offline → online, no third on second event), got %d: %v", len(availability), availability)
	}
	if availability[0] != "offline" {
		t.Errorf("first availability = %q, want offline", availability[0])
	}
	if availability[1] != "online" {
		t.Errorf("second availability = %q, want online", availability[1])
	}
}
