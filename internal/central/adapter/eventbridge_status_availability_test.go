// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// eventbridge_status_availability_test.go covers the republishBaseForStatusPair
// helper and the end-to-end event-bus path that calls it when a _STATUS
// parameter changes. The invariant under test: OVERFLOW/UNDERFLOW mark the
// base slot unavailable; NORMAL and UNKNOWN leave it available for all
// parameter types (no measured-vs-control distinction).

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// latestSlotPayload returns the most recent MQTT payload published for the
// bucket/parameter topic suffix, or "" when nothing matched.
func latestSlotPayload(pub *mqtt.NoopClient, param string) string {
	suffix := "/values/" + param
	latest := ""
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, suffix) {
			latest = string(p.Payload)
		}
	}
	return latest
}

// TestRepublishBaseForStatusPair_OverflowIsUnavailable verifies that a base
// data point whose status is OVERFLOW is republished with available=false.
func TestRepublishBaseForStatusPair_OverflowIsUnavailable(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(hmenum.ParameterActualTemperature, "0001ABCD:1")
	ch.Put(dp)
	dp.OnWireValue(21.5)
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))

	eb.republishBaseForStatusPair(
		context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1,
		"ACTUAL_TEMPERATURE_STATUS", ch,
	)

	body := latestSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state republished for ACTUAL_TEMPERATURE")
	}
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("OVERFLOW must mark the slot unavailable; got: %s", body)
	}
}

// TestRepublishBaseForStatusPair_NormalStaysAvailable verifies that a base
// data point with STATUS=NORMAL is republished with available=true.
func TestRepublishBaseForStatusPair_NormalStaysAvailable(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(hmenum.ParameterActualTemperature, "0001ABCD:1")
	ch.Put(dp)
	dp.OnWireValue(21.5)
	dp.UpdateStatus(hmenum.ParameterStatusNormal)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))

	eb.republishBaseForStatusPair(
		context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1,
		"ACTUAL_TEMPERATURE_STATUS", ch,
	)

	body := latestSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state republished for ACTUAL_TEMPERATURE")
	}
	if !strings.Contains(body, `"available":true`) {
		t.Fatalf("NORMAL must keep the slot available; got: %s", body)
	}
}

// TestRepublishBaseForStatusPair_ControlUnknownStaysAvailable verifies that a
// control parameter (LEVEL) with STATUS=UNKNOWN is republished with
// available=true. UNKNOWN is valid for all parameters — there is no
// measured-vs-control distinction in the current design.
func TestRepublishBaseForStatusPair_ControlUnknownStaysAvailable(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	ch.Put(dp)
	dp.OnWireValue(0.0)
	dp.UpdateStatus(hmenum.ParameterStatusUnknown)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))

	eb.republishBaseForStatusPair(
		context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1,
		"LEVEL_STATUS", ch,
	)

	body := latestSlotPayload(pub, "LEVEL")
	if body == "" {
		t.Fatal("no base slot state republished for LEVEL")
	}
	if !strings.Contains(body, `"available":true`) {
		t.Fatalf("UNKNOWN must keep the slot available for all parameters; got: %s", body)
	}
}

// TestEventBus_StatusPairEvent_OverflowRepublishesUnavailable drives the real
// event bus end-to-end: publishing a DataPointValueChangedEvent for
// ACTUAL_TEMPERATURE_STATUS triggers republishBaseForStatusPair which must
// emit a base-slot publish with available=false when the base DP carries
// STATUS=OVERFLOW.
func TestEventBus_StatusPairEvent_OverflowRepublishesUnavailable(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(hmenum.ParameterActualTemperature, "0001ABCD:1")
	ch.Put(dp)
	dp.OnWireValue(21.5)
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: dev.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ACTUAL_TEMPERATURE_STATUS",
		},
		NewValue: hmtypes.FloatValue(2.0), // numeric status index for OVERFLOW
	})
	eb.Flush()

	body := latestSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state published for ACTUAL_TEMPERATURE after STATUS event")
	}
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("OVERFLOW STATUS event must republish base slot as unavailable; got: %s", body)
	}
}
