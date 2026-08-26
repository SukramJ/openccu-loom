// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// calcSlotAvailability drives an initial snapshot over a channel carrying a
// dew-point sensor plus its two sources and returns the `available` flag of
// the published calculated slot state. tempStatus, when non-empty, is applied
// to the temperature source so the caller can fault it.
func calcSlotAvailability(t *testing.T, tempStatus hmenum.ParameterStatus) bool {
	t.Helper()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

	temp := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHumidity),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(temp)
	ch.Put(hum)

	sensor := calculated.NewDewPointSensorWithIdentity("ccu-01", ch.Address)
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)
	if tempStatus != "" {
		temp.UpdateStatus(tempStatus)
	}

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/calculated/DEW_POINT") {
			continue
		}
		var state struct {
			Available bool `json:"available"`
		}
		if err := json.Unmarshal(p.Payload, &state); err != nil {
			t.Fatalf("decode slot state %q: %v", p.Topic, err)
		}
		return state.Available
	}
	t.Fatal("no calculated DEW_POINT slot state was published")
	return false
}

// TestCalculatedSlotStateGatedOnSourceValidity pins the MQTT half of the
// source-validity gate. The calculated bucket used to publish available=true
// unconditionally, so a Home Assistant entity kept showing a dew point derived
// from a temperature the CCU had already flagged OVERFLOW.
func TestCalculatedSlotStateGatedOnSourceValidity(t *testing.T) {
	t.Parallel()

	if !calcSlotAvailability(t, "") {
		t.Fatal("calculated slot must publish available=true while its sources are healthy")
	}
	if calcSlotAvailability(t, hmenum.ParameterStatusOverflow) {
		t.Fatal("calculated slot must publish available=false when a source reports OVERFLOW")
	}
}

// wsAvailability drives a live value change and returns the `available` flag
// of the WebSocket value-changed push for the named parameter.
//
// Live, not the boot snapshot: the snapshot signals a resync to WebSocket
// subscribers rather than replaying the model frame by frame (see
// [EventBridge.publishSnapshotValue]), so it emits no per-data-point push to
// read a flag off. The invariant under test belongs to the live push anyway.
func wsAvailability(t *testing.T, param string, tempStatus hmenum.ParameterStatus) bool {
	t.Helper()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

	temp := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHumidity),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(temp)
	ch.Put(hum)

	sensor := calculated.NewDewPointSensorWithIdentity("ccu-01", ch.Address)
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)
	if tempStatus != "" {
		temp.UpdateStatus(tempStatus)
	}

	wsHub := ws.NewHub()
	eb := NewEventBridge(reg, wsHub, nil)
	eb.Start(context.Background())
	defer eb.Stop()

	// A live CCU push over the bus the central owns — the path a real
	// value change takes.
	unit, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("setup: central not registered")
	}
	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		NewValue: hmtypes.FloatValue(20.0),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range wsHub.Replay(0, nil).Events {
			p, ok := e.Payload.(ws.DataPointValueChangedPayload)
			if !ok || p.Parameter != param {
				continue
			}
			return p.Available
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no value-changed push for %s", param)
	return false
}

// TestValueChangedPushCarriesAvailability pins that the WebSocket push says
// whether the value it carries is a confirmed reading. A consumer cannot
// derive this: `observed` stays true through a fault, and the transition into
// a fault usually arrives as a value change — so availability read only at
// catalogue-refresh time renders the faulted value as confirmed.
func TestValueChangedPushCarriesAvailability(t *testing.T) {
	t.Parallel()

	if !wsAvailability(t, "ACTUAL_TEMPERATURE", "") {
		t.Fatal("a healthy reading must push available=true")
	}
	if wsAvailability(t, "ACTUAL_TEMPERATURE", hmenum.ParameterStatusOverflow) {
		t.Fatal("an OVERFLOW reading must push available=false")
	}
	// The derived sensor follows its source over the same plane.
	if wsAvailability(t, "DEW_POINT", hmenum.ParameterStatusOverflow) {
		t.Fatal("a calculated value off a faulted source must push available=false")
	}
}
