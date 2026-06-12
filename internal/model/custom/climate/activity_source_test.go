// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putFloatSensor installs a FLOAT VALUES sensor DP on ch.
func putFloatSensor(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[float64] {
	dp := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// putIntSensor installs an INTEGER/ENUM-shaped VALUES sensor DP on ch.
func putIntSensor(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[int32] {
	dp := generic.NewSensor[int32](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// putBoolSensor installs a BOOL VALUES sensor DP on ch.
func putBoolSensor(ch *device.Channel, param hmenum.Parameter) *generic.BinarySensor {
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// TestIPThermostatIgnoresValveStateEnum reproduces the eTRV parity bug:
// HmIP VALVE_STATE is an adaption-state ENUM (4 == ADAPTION_DONE), not
// a valve-openness percentage. With LEVEL == 0 (valve closed) the
// activity must stay idle even when VALVE_STATE pushes a value > 0.
func TestIPThermostatIgnoresValveStateEnum(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "000A1709AF4FE0"})
	ch := d.AddChannel("000A1709AF4FE0:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	levelDP := putFloatSensor(ch, hmenum.ParameterLevel)
	valveStateDP := putIntSensor(ch, hmenum.ParameterValveState)

	c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: KindIP})
	defer c.Subscribe(ch)()

	levelDP.OnEvent(0.0)
	valveStateDP.OnEvent(4) // ADAPTION_DONE — must NOT flip activity to heating

	got, observed := c.Activity()
	if !observed {
		t.Fatal("Activity must be observed after the LEVEL push")
	}
	if got != ActivityIdle {
		t.Errorf("Activity = %v, want %v (VALVE_STATE ENUM must not be an IP activity source)", got, ActivityIdle)
	}
}

// TestIPThermostatActivityFromProfileMappedStateChannel covers the
// HmIP-BWTH shape: the heating relay STATE lives on channel 9 (profile
// channel-field offset 8), not on the climate channel. Subscribe must
// wire it through Config.ActivityStateChannels.
func TestIPThermostatActivityFromProfileMappedStateChannel(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "000C9709AEF298"})
	climateCh := d.AddChannel("000C9709AEF298:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	relayCh := d.AddChannel("000C9709AEF298:9", 9, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	stateDP := putBoolSensor(relayCh, hmenum.ParameterState)

	c := New(Config{
		Channel:               climateCh,
		Writer:                &stubWriter{},
		Kind:                  KindIP,
		ActivityStateChannels: []int{9},
	})
	if !c.HasActivitySource() {
		t.Fatal("HasActivitySource must be true with a profile-mapped STATE channel")
	}
	defer c.Subscribe(climateCh)()

	stateDP.OnEvent(true)
	if got, observed := c.Activity(); !observed || got != ActivityHeating {
		t.Errorf("Activity = (%v, %v), want (heating, true) after relay STATE=true", got, observed)
	}
	stateDP.OnEvent(false)
	if got, observed := c.Activity(); !observed || got != ActivityIdle {
		t.Errorf("Activity = (%v, %v), want (idle, true) after relay STATE=false", got, observed)
	}
}

// TestDisplayOnlyThermostatOmitsAction covers the HmIP-STHD shape: no
// LEVEL / STATE / VALVE_STATE anywhere — the aggregate state must omit
// the action key and the HA discovery must not advertise an
// action_topic, mirroring the reference stack's `hvac_action = None`.
func TestDisplayOnlyThermostatOmitsAction(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "000E97099D2ABE"})
	ch := d.AddChannel("000E97099D2ABE:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	putFloatSensor(ch, hmenum.ParameterSetPointTemperature)
	putFloatSensor(ch, hmenum.ParameterActualTemperature)

	c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: KindIP})
	defer c.Subscribe(ch)()

	if c.HasActivitySource() {
		t.Fatal("HasActivitySource must be false without LEVEL/STATE/peer sources")
	}
	state, ok := c.State().(*payload.ClimateState)
	if !ok || state == nil {
		t.Fatal("State() did not return *payload.ClimateState")
	}
	if state.Action != "" {
		t.Errorf("State().Action = %q, want empty (omitted) for display-only thermostats", state.Action)
	}
	// Even in OFF mode the action stays omitted — the reference stack
	// checks for a source before the mode override.
	c.OnMode(ModeOff)
	state, _ = c.State().(*payload.ClimateState)
	if state.Action != "" {
		t.Errorf("State().Action = %q in OFF mode, want empty for display-only thermostats", state.Action)
	}

	component, body := c.HADiscoveryPayload(discoveryCtx{})
	if component != "climate" {
		t.Fatalf("HADiscoveryPayload component = %q, want climate", component)
	}
	if _, has := body["action_topic"]; has {
		t.Error("discovery must not advertise action_topic without an activity source")
	}
	if _, has := body["action_template"]; has {
		t.Error("discovery must not advertise action_template without an activity source")
	}
}

// TestDiscoveryGainsActionTopicAfterPeerActivity pins the convergence
// path for peer-only-source climates: a thermostat without own
// LEVEL/STATE sources omits the action surface at first, but once a
// linked peer push feeds OnActivity (via the subscriptions installed
// by RefreshLinkPeerActivitySources) the rebuilt discovery payload
// carries action_topic — the bridge's diff-gated cache then
// re-publishes the changed bytes.
func TestDiscoveryGainsActionTopicAfterPeerActivity(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "PEERWTH01"})
	climateCh := d.AddChannel("PEERWTH01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	peerDev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "PEERTRV01"})
	peerCh := peerDev.AddChannel("PEERTRV01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	peerLevel := putFloatSensor(peerCh, hmenum.ParameterLevel)

	c := New(Config{Channel: climateCh, Writer: &stubWriter{}, Kind: KindIP})
	c.OnMode(ModeHeat) // a non-off mode so the OFF override does not mask the activity
	if _, body := c.HADiscoveryPayload(discoveryCtx{}); body["action_topic"] != nil {
		t.Fatal("discovery must omit action_topic before any source exists")
	}

	closer := c.RefreshLinkPeerActivitySources([]*device.Channel{peerCh})
	defer closer()
	peerLevel.OnEvent(0.4)

	if got, observed := c.Activity(); !observed || got != ActivityHeating {
		t.Fatalf("Activity = (%v, %v), want (heating, true) after peer LEVEL push", got, observed)
	}
	_, body := c.HADiscoveryPayload(discoveryCtx{})
	if body["action_topic"] == nil {
		t.Error("rebuilt discovery must carry action_topic once peer activity is fed")
	}
	state, _ := c.State().(*payload.ClimateState)
	if state == nil || state.Action != string(ActivityHeating) {
		t.Errorf("State().Action = %v, want heating", state)
	}
}

// TestSimpleRFThermostatHasNoActivitySource pins that SimpleRF
// thermostats never derive activity from wire DPs — the reference
// stack's basic climate class has no activity property.
func TestSimpleRFThermostatHasNoActivitySource(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "SIMPLE01"})
	ch := d.AddChannel("SIMPLE01:1", 1, "CLIMATECONTROL_REGULATOR", hmenum.ParamsetKeyValues)
	valveDP := putFloatSensor(ch, hmenum.ParameterValveState)

	c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: KindSimpleRF})
	defer c.Subscribe(ch)()

	valveDP.OnEvent(55.0)
	if _, observed := c.Activity(); observed {
		t.Error("SimpleRF must not derive activity from VALVE_STATE")
	}
	if c.HasActivitySource() {
		t.Error("HasActivitySource must be false for SimpleRF")
	}
}

// TestActivityStateChannelsFromProfileSchema pins the constructor-side
// extraction of the STATE-field channels from the generated
// IPThermostat profile schema, rebased to group 1 (HmIP-BWTH shape):
// offsets -5 and 8 become channels -4 and 9.
func TestActivityStateChannelsFromProfileSchema(t *testing.T) {
	t.Parallel()
	cfg := custom.ProfileConfigs[hmenum.DeviceProfile("IPThermostat")]
	if cfg == nil {
		t.Fatal("generated IPThermostat profile config missing")
	}
	rebased := custom.RebaseChannelGroup(*cfg, 1)
	got := activityStateChannels(rebased)
	want := []int{-4, 9}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activityStateChannels = %v, want %v", got, want)
	}
}
