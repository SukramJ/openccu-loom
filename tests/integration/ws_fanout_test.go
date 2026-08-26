// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// This file closes the model->WS fan-out gap called out against
// internal/central/adapter/eventbridge_test.go's
// TestEventBridgeValueChangedFansOut: that test drives a real
// DataPointValueChangedEvent through a central's event bus and asserts
// only the MQTT-side publish. The WebSocket side had no equivalent
// end-to-end check — every existing WS assertion either called an
// unexported EventBridge method directly (bypassing the
// events.Subscribe wiring set up by EventBridge.Start) or asserted a
// different broadcast family. The tests below publish through the
// same public events.Publish surface production code uses and assert
// the resulting *ws.Hub broadcast, covering the two highest-frequency
// broadcasts declared in assets/wsapi.json: "datapoint.value_changed"
// and "custom_data_point.state_changed".

// pollWSHubEvent waits for a broadcast whose topic satisfies match to
// appear in hub's replay buffer. The Hub has no blocking Go-native
// subscribe API (real clients read frames off a WebSocket connection),
// so polling the replay buffer — the same mechanism
// internal/north/rest/ws/hub_events_test.go's pollHub helper uses — is
// the least invasive way to observe a broadcast from a black-box test.
func pollWSHubEvent(t *testing.T, hub *ws.Hub, match func(topic string) bool) ws.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, match)
		if len(res.Events) > 0 {
			return res.Events[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected WS hub broadcast did not appear within deadline")
	return ws.Event{}
}

// TestDataPointValueChangedFansOutToWSHub publishes a
// DataPointValueChangedEvent on a central's real event bus and asserts
// it reaches the *ws.Hub as a "datapoint.value_changed" broadcast on
// the documented topic (assets/wsapi.json) with the expected payload
// fields. Mirrors the bus-publish shape of
// TestEventBridgeValueChangedFansOut (internal/central/adapter/
// eventbridge_test.go), which only asserted the MQTT side.
func TestDataPointValueChangedFansOutToWSHub(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001ABCD", Model: "HmIP-STH", Name: "Flur",
	})
	c.ModelRegistry.Put(d)

	hub := ws.NewHub()
	bridge := adapter.NewEventBridge(reg, hub, nil)
	bridge.Start(context.Background())
	defer bridge.Stop()

	events.Publish(c.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		NewValue: hmtypes.BoolValue(true),
		OldValue: hmtypes.BoolValue(false),
	})

	wantTopic := ws.DataPointTopic(d.Address, 1, "STATE")
	ev := pollWSHubEvent(t, hub, func(topic string) bool { return topic == wantTopic })

	if ev.Type != string(hmevent.EventTypeDataPointValueChanged) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeDataPointValueChanged))
	}
	p, ok := ev.Payload.(ws.DataPointValueChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.DataPointValueChangedPayload", ev.Payload)
	}
	if p.Central != "ccu-01" {
		t.Errorf("central = %q, want %q", p.Central, "ccu-01")
	}
	if p.DeviceAddress != d.Address {
		t.Errorf("device_address = %q, want %q", p.DeviceAddress, d.Address)
	}
	if p.Channel != 1 {
		t.Errorf("channel = %d, want 1", p.Channel)
	}
	if p.Parameter != "STATE" {
		t.Errorf("parameter = %q, want %q", p.Parameter, "STATE")
	}
	if got, ok := p.Value.(bool); !ok || !got {
		t.Errorf("value = %v (ok=%v), want true", p.Value, ok)
	}
	if got, ok := p.Previous.(bool); !ok || got {
		t.Errorf("previous = %v (ok=%v), want false", p.Previous, ok)
	}
}

// TestCustomDataPointStateChangedFansOutToWSHub publishes a
// DataPointValueChangedEvent for a wire DP bound to a switch Custom-DP
// and asserts the aggregated CDP snapshot reaches the *ws.Hub as a
// "custom_data_point.state_changed" broadcast — the second
// highest-frequency broadcast in assets/wsapi.json, and the one SPA
// tiles subscribe to instead of the raw per-parameter stream. Unlike
// the existing coverage in internal/central/adapter/
// eventbridge_cdp_channel_group_test.go (which calls the unexported
// EventBridge.onValueChangedKind directly), this drives the change
// through the real event bus so the events.Subscribe wiring set up by
// EventBridge.Start is itself under test.
func TestCustomDataPointStateChangedFansOutToWSHub(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}

	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "00021BE9957782", Model: "HMIP-PS", Name: "Bücherregal",
	})
	chAddr := d.Address + ":1"
	ch := d.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	stateDP := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(stateDP)
	cdp := switchdev.New(ch, custom.RebasedChannelGroupConfig{})
	if cdp == nil {
		t.Fatalf("switch.New returned nil for %s", chAddr)
	}
	ch.SetCustomDataPoint(cdp)
	c.ModelRegistry.Put(d)

	hub := ws.NewHub()
	bridge := adapter.NewEventBridge(reg, hub, nil)
	bridge.Start(context.Background())
	defer bridge.Stop()

	// The bus-carried event only signals "a value changed" — it is not
	// what feeds the CDP's aggregated State(). Mirror what the real
	// callback-decode path does: land the confirmed value on the wire
	// DP first, then publish the change so EventBridge's subscription
	// observes an is_on:true snapshot.
	stateDP.OnEvent(true)

	events.Publish(c.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
		OldValue: hmtypes.BoolValue(false),
	})

	wantTopic := ws.CustomDataPointTopic(d.Address, "STATE")
	ev := pollWSHubEvent(t, hub, func(topic string) bool { return topic == wantTopic })

	if ev.Type != string(hmevent.EventTypeCustomDataPointStateChanged) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeCustomDataPointStateChanged))
	}
	p, ok := ev.Payload.(ws.CustomDataPointStateChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ws.CustomDataPointStateChangedPayload", ev.Payload)
	}
	if p.Central != "ccu-01" {
		t.Errorf("central = %q, want %q", p.Central, "ccu-01")
	}
	if p.DeviceAddress != d.Address {
		t.Errorf("device_address = %q, want %q", p.DeviceAddress, d.Address)
	}
	if p.Channel != 1 {
		t.Errorf("channel = %d, want 1", p.Channel)
	}
	if p.Name != "STATE" {
		t.Errorf("name = %q, want %q (bare — no channel-group collision)", p.Name, "STATE")
	}
	if isOn, ok := p.State["is_on"].(bool); !ok || !isOn {
		t.Errorf("state[is_on] = %v (ok=%v), want true", p.State["is_on"], ok)
	}
}
