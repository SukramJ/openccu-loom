// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
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

// addSwitchChannel registers a STATE *generic.Switch on a fresh channel of d
// and binds a switch Custom-DP to it. Returns the channel and the underlying
// STATE wire DP so the test can drive value events.
func addSwitchChannel(t *testing.T, d *device.Device, channelNo int) (*device.Channel, *generic.Switch) {
	t.Helper()
	addr := d.Address + ":" + itoaSmall(channelNo)
	ch := d.AddChannel(addr, channelNo, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	cdp := switchdev.New(ch, custom.RebasedChannelGroupConfig{})
	if cdp == nil {
		t.Fatalf("switch.New returned nil for %s", addr)
	}
	ch.SetCustomDataPoint(cdp)
	return ch, dp
}

// itoaSmall renders a small non-negative int without pulling strconv.
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestOnValueChanged_ChannelGroupSwitch_PublishesWireNamedState reproduces the
// channel-group switch bug: a device with several switch CDPs (STATE on
// ch3/ch4/ch5) materialises disambiguated wire names (STATE@3, STATE@4, …) on
// the cdps REST/WS surface. A STATE value_changed on ch3 must publish a
// custom_data_point.state_changed whose name matches the wire identity
// (STATE@3) — not the bare parameter (STATE) — so the client's (addr, name)
// keyed CDP receives it. With the bare name the event was silently dropped and
// the HA switch snapped back to off.
func TestOnValueChanged_ChannelGroupSwitch_PublishesWireNamedState(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}

	// HMIP-PS-style device with switch CDPs on ch3, ch4, ch5 → WireName
	// disambiguates to STATE@3 / STATE@4 / STATE@5.
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "00021BE9957782", Model: "HMIP-PS", Name: "Bücherregal",
	})
	ch3, stateDP3 := addSwitchChannel(t, d, 3)
	addSwitchChannel(t, d, 4)
	addSwitchChannel(t, d, 5)
	c.ModelRegistry.Put(d)

	hub := ws.NewHub()
	bridge := NewEventBridge(reg, hub, nil)

	// Drive the relay on: the embedded STATE DP observes true so the CDP's
	// State() reports is_on=true.
	stateDP3.OnEvent(true)

	bridge.onValueChangedKind(context.Background(), "ccu-01", ws.KindChange, hmevent.DataPointValueChangedEvent{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch3.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		NewValue: hmtypes.BoolValue(true),
		OldValue: hmtypes.BoolValue(false),
	})

	res := hub.Replay(0, nil)
	var cdpEvents []ws.CustomDataPointStateChangedPayload
	for _, ev := range res.Events {
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
	if got.Name != "STATE@3" {
		t.Errorf("CDP state name = %q, want %q (disambiguated wire name)", got.Name, "STATE@3")
	}
	if got.DeviceAddress != "00021BE9957782" {
		t.Errorf("device = %q, want 00021BE9957782", got.DeviceAddress)
	}
	if got.Channel != 3 {
		t.Errorf("channel = %d, want 3", got.Channel)
	}
	if isOn, ok := got.State["is_on"].(bool); !ok || !isOn {
		t.Errorf("state[is_on] = %v (ok=%v), want true", got.State["is_on"], ok)
	}
}

// TestCustomDPStatePayload_SwitchSource verifies the Source-contract path:
// a real switch CDP exposes State() as a typed payload struct, which the
// helper must JSON-round-trip into {is_on: true}. Before the fix the helper
// only matched a State() map[string]any shape that no shipping CDP
// implements, so the CDP-state push never fired in production.
func TestCustomDPStatePayload_SwitchSource(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "00021BE9957782", Model: "HMIP-PS"})
	_, stateDP := addSwitchChannel(t, d, 3)
	stateDP.OnEvent(true)

	cdp := d.Channels()[0].CustomDataPoint()
	state, ok := customDPStatePayload(cdp)
	if !ok {
		t.Fatal("customDPStatePayload must extract state from a payload.Source switch CDP")
	}
	if isOn, ok := state["is_on"].(bool); !ok || !isOn {
		t.Errorf("state[is_on] = %v (ok=%v), want true", state["is_on"], ok)
	}
}
