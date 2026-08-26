// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putCrossChannelDP registers one wire data point on ch using the generic
// shape the device pipeline resolves for the descriptor.
func putCrossChannelDP(
	t *testing.T,
	ch *device.Channel,
	parameter string,
	typ hmenum.ParameterType,
	ops hmenum.Operations,
) {
	t.Helper()
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       typ,
			Operations: ops,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("100"),
		},
	}
	switch kind := generic.ResolveDataPointKind(generic.ResolveInput{Parameter: parameter, Descriptor: spec.Descriptor}); kind {
	case generic.KindSensor:
		if typ == hmenum.ParameterTypeFloat {
			ch.Put(generic.NewFloatSensor(spec))
			return
		}
		ch.Put(generic.NewIntegerSensor(spec))
	case generic.KindNumberFloat:
		ch.Put(generic.NewFloat(spec))
	default:
		t.Fatalf("unexpected resolved kind %q for %s", kind, parameter)
	}
}

// TestOnValueChanged_SiblingChannelSlot_PublishesHostingCDPState pins the
// fan-out for a wire value that belongs to a custom DP on *another*
// channel.
//
// The HM-CC-TC thermostat is materialised on the weather channel while
// its setpoint lives on the regulator channel — the profile schema says
// so, and the channel carrying the setpoint hosts no custom DP of its
// own. Every custom-DP push on this path keys on the event's channel, so
// a setpoint change used to update the model and reach no aggregate
// surface at all: the SPA tile kept the previous target temperature until
// an unrelated parameter on the weather channel happened to change.
func TestOnValueChanged_SiblingChannelSlot_PublishesHostingCDPState(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}

	d := device.New(device.Config{
		InterfaceID: "BidCos-RF", Interface: hmenum.InterfaceBidCosRF,
		Address: "LEQ0123456", Model: "HM-CC-TC", Name: "Wohnzimmer",
	})
	weather := d.AddChannel("LEQ0123456:1", 1, "WEATHER", hmenum.ParamsetKeyValues)
	regulator := d.AddChannel("LEQ0123456:2", 2, "CLIMATECONTROL_REGULATOR", hmenum.ParamsetKeyValues)
	putCrossChannelDP(t, weather, "TEMPERATURE", hmenum.ParameterTypeFloat,
		hmenum.OperationsRead|hmenum.OperationsEvent)
	putCrossChannelDP(t, regulator, "SETPOINT", hmenum.ParameterTypeFloat,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent)
	if err := custom.CreateCustomDataPoints(d, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	if weather.CustomDataPoint() == nil {
		t.Fatal("weather channel must host the thermostat custom DP")
	}
	if regulator.CustomDataPoint() != nil {
		t.Fatal("regulator channel must not host a custom DP — the premise of this test")
	}
	c.ModelRegistry.Put(d)

	hub := ws.NewHub()
	bridge := NewEventBridge(reg, hub, nil)

	setpoint, ok := regulator.Parameter(hmenum.ParameterSetpoint).(*generic.Float)
	if !ok {
		t.Fatal("SETPOINT must resolve to a writable float data point")
	}
	setpoint.OnEvent(21.5)

	bridge.onValueChangedKind(context.Background(), "ccu-01", ws.KindChange, hmevent.DataPointValueChangedEvent{
		Key: hmtypes.DataPointKey{
			ChannelAddress: regulator.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetpoint),
		},
		NewValue: hmtypes.FloatValue(21.5),
		OldValue: hmtypes.FloatValue(20.0),
	})

	var cdpEvents []ws.CustomDataPointStateChangedPayload
	for _, ev := range hub.Replay(0, nil).Events {
		if ev.Type != string(hmevent.EventTypeCustomDataPointStateChanged) {
			continue
		}
		p, ok := ev.Payload.(ws.CustomDataPointStateChangedPayload)
		if !ok {
			t.Fatalf("unexpected CDP payload type %T", ev.Payload)
		}
		cdpEvents = append(cdpEvents, p)
	}

	if len(cdpEvents) != 1 {
		t.Fatalf("expected exactly one custom_data_point.state_changed, got %d: %+v", len(cdpEvents), cdpEvents)
	}
	got := cdpEvents[0]
	if got.Channel != 1 {
		t.Errorf("channel = %d, want 1 — the channel the thermostat is attached to", got.Channel)
	}
	if got.DeviceAddress != "LEQ0123456" {
		t.Errorf("device = %q, want LEQ0123456", got.DeviceAddress)
	}
	if temp, ok := got.State["set_temperature"].(float64); !ok || temp != 21.5 {
		t.Errorf("state[set_temperature] = %v (ok=%v), want 21.5", got.State["set_temperature"], ok)
	}
}
