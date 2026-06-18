// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// statusAvailabilityEnv builds an EventBridge wired to a capturing NoopClient
// plus a channel carrying the given base data point, seeded with the CCU-restart
// shape: a placeholder value plus STATUS=UNKNOWN.
func statusAvailabilityEnv(t *testing.T, baseParam hmenum.Parameter) (*EventBridge, *mqtt.NoopClient, *device.Channel) {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(baseParam, "0001ABCD:1")
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
	return eb, pub, ch
}

// baseSlotPayload returns the most recent slot-state payload published for the
// `values/<param>` topic suffix, or "" when none was captured.
func baseSlotPayload(pub *mqtt.NoopClient, param string) string {
	suffix := "/values/" + param
	latest := ""
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, suffix) {
			latest = string(p.Payload)
		}
	}
	return latest
}

// TestRepublishBaseForStatusPair_MeasuredUnknownIsUnavailable verifies that a
// measured value (ACTUAL_TEMPERATURE) reporting STATUS=UNKNOWN after a CCU
// restart republishes its base slot as unavailable, so HA does not record the
// DEFAULT placeholder 0.0.
func TestRepublishBaseForStatusPair_MeasuredUnknownIsUnavailable(t *testing.T) {
	t.Parallel()

	eb, pub, ch := statusAvailabilityEnv(t, hmenum.ParameterActualTemperature)
	eb.republishBaseForStatusPair(
		context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1, "ACTUAL_TEMPERATURE_STATUS", ch,
	)

	body := baseSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state republished for ACTUAL_TEMPERATURE")
	}
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("measured value with STATUS=UNKNOWN must be unavailable, got: %s", body)
	}
}

// TestRepublishBaseForStatusPair_ControlUnknownStaysAvailable verifies that a
// control parameter (LEVEL carries no physical quantity) stays available while
// STATUS=UNKNOWN — the regression guard for control values after a restart.
func TestRepublishBaseForStatusPair_ControlUnknownStaysAvailable(t *testing.T) {
	t.Parallel()

	eb, pub, ch := statusAvailabilityEnv(t, hmenum.ParameterLevel)
	eb.republishBaseForStatusPair(
		context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1, "LEVEL_STATUS", ch,
	)

	body := baseSlotPayload(pub, "LEVEL")
	if body == "" {
		t.Fatal("no base slot state republished for LEVEL")
	}
	if !strings.Contains(body, `"available":true`) {
		t.Fatalf("control parameter with STATUS=UNKNOWN must stay available, got: %s", body)
	}
}

// statusAvailabilityBusEnv is like statusAvailabilityEnv but also returns the
// *central.Registry so callers can publish events onto the bus (needed for
// end-to-end tests that drive the real onValueChangedKind wiring path).
// The EventBridge is started so it subscribes to the bus before returning.
func statusAvailabilityBusEnv(
	t *testing.T,
	baseParam hmenum.Parameter,
) (*EventBridge, *mqtt.NoopClient, *device.Channel, *central.Registry) {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := newFloatDP(baseParam, "0001ABCD:1")
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
	eb.Start(context.Background())
	t.Cleanup(eb.Stop)
	return eb, pub, ch, reg
}

// TestEventBus_StatusPairEvent_MeasuredUnknownRepublishesUnavailable drives the
// real event bus to verify that a <X>_STATUS value-changed event triggers a
// republish of the base parameter slot marked unavailable for a measured value
// (ACTUAL_TEMPERATURE) whose STATUS is UNKNOWN.
func TestEventBus_StatusPairEvent_MeasuredUnknownRepublishesUnavailable(t *testing.T) {
	t.Parallel()

	_, pub, _, reg := statusAvailabilityBusEnv(t, hmenum.ParameterActualTemperature)

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ACTUAL_TEMPERATURE_STATUS",
		},
		NewValue: hmtypes.FloatValue(0.0),
	})

	body := baseSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state published for ACTUAL_TEMPERATURE after STATUS event")
	}
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("measured value with STATUS=UNKNOWN must be published unavailable, got: %s", body)
	}
}

// TestEventBus_StatusPairEvent_ControlUnknownStaysAvailable drives the real
// event bus to verify that a LEVEL_STATUS value-changed event leaves the base
// LEVEL slot published as available — control values carry no physical quantity
// and must not be suppressed when STATUS is UNKNOWN.
func TestEventBus_StatusPairEvent_ControlUnknownStaysAvailable(t *testing.T) {
	t.Parallel()

	_, pub, _, reg := statusAvailabilityBusEnv(t, hmenum.ParameterLevel)

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL_STATUS",
		},
		NewValue: hmtypes.FloatValue(0.0),
	})

	body := baseSlotPayload(pub, "LEVEL")
	if body == "" {
		t.Fatal("no base slot state published for LEVEL after STATUS event")
	}
	if !strings.Contains(body, `"available":true`) {
		t.Fatalf("control parameter with STATUS=UNKNOWN must stay available, got: %s", body)
	}
}

// TestEventBus_BaseValueEvent_MeasuredUnknownIsUnavailable drives the real
// event bus with an event for the BASE parameter itself (ACTUAL_TEMPERATURE)
// while the DP already carries STATUS=UNKNOWN. Exercises the publishSlotState
// gating line in onValueChangedKind: the slot must be published unavailable
// even when the event arrives on the base parameter (not the STATUS parameter).
func TestEventBus_BaseValueEvent_MeasuredUnknownIsUnavailable(t *testing.T) {
	t.Parallel()

	_, pub, _, reg := statusAvailabilityBusEnv(t, hmenum.ParameterActualTemperature)

	events.Publish(reg.List()[0].EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "ACTUAL_TEMPERATURE",
		},
		NewValue: hmtypes.FloatValue(0.0),
	})

	body := baseSlotPayload(pub, "ACTUAL_TEMPERATURE")
	if body == "" {
		t.Fatal("no base slot state published for ACTUAL_TEMPERATURE after base value event")
	}
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("measured value with STATUS=UNKNOWN must be published unavailable on base event, got: %s", body)
	}
}
