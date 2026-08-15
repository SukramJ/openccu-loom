// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// helpers shared across subscribe and activity-source tests
// ---------------------------------------------------------------------------

func putBoolDP(ch *device.Channel, param hmenum.Parameter) *generic.DataPoint[bool] {
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	return dp
}

func putIntegerDP(ch *device.Channel, param hmenum.Parameter) *generic.Integer {
	dp := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	return dp
}

func putFloatDPValues(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	return dp
}

// ---------------------------------------------------------------------------
// Subscribe — nil channel
// ---------------------------------------------------------------------------

func TestSubscribeNilChannel(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	cancel := r.climate.Subscribe(nil)
	if cancel == nil {
		t.Fatal("Subscribe(nil) must return a non-nil cancel func")
	}
	cancel() // must not panic
}

// ---------------------------------------------------------------------------
// Subscribe — IP kind wires ACTIVE_PROFILE + SET_POINT_MODE + BOOST_MODE
// ---------------------------------------------------------------------------

func TestSubscribeIPActiveProfileAndSetPointMode(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "XYZ0001"})
	ch := d.AddChannel("XYZ0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	activeProfile := putIntegerDP(ch, hmenum.ParameterActiveProfile)
	setPointMode := putIntegerDP(ch, hmenum.ParameterSetPointMode)
	boostMode := putBoolDP(ch, hmenum.ParameterBoostMode)
	_ = boostMode

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{SupportsBoost: true}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	// Drive ACTIVE_PROFILE=3 (1-based).
	activeProfile.OnEvent(int32(3))
	// Drive SET_POINT_MODE=0 (AUTO).
	setPointMode.OnEvent(int32(0))

	m, mOK := c.Mode()
	p, pOK := c.Profile()
	if !mOK || m != ModeAuto {
		t.Errorf("Mode() = (%v, %v), want (auto, true)", m, mOK)
	}
	if !pOK || p != ProfileWeekProgram3 {
		t.Errorf("Profile() = (%v, %v), want (week_program_3, true)", p, pOK)
	}
}

func TestSubscribeIPBoostModeTrue(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "XYZ0002"})
	ch := d.AddChannel("XYZ0002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	boostMode := putBoolDP(ch, hmenum.ParameterBoostMode)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{SupportsBoost: true}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	boostMode.OnEvent(true)
	p, ok := c.Profile()
	if !ok || p != ProfileBoost {
		t.Errorf("Profile() = (%v, %v), want (boost, true) after BOOST_MODE=true", p, ok)
	}
}

func TestSubscribeIPBoostModeFlipToFalseInAuto(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "XYZ0003"})
	ch := d.AddChannel("XYZ0003:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	boostMode := putBoolDP(ch, hmenum.ParameterBoostMode)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{SupportsBoost: true}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	// Set mode to AUTO + profile to BOOST, then flip BOOST_MODE=false.
	c.OnMode(ModeAuto)
	c.OnActiveProfile(2, false) // week_program_2 cached
	boostMode.OnEvent(true)
	boostMode.OnEvent(false)

	// After BOOST_MODE=false in AUTO, profile should recover to week_program_2.
	p, ok := c.Profile()
	if !ok || p != ProfileWeekProgram2 {
		t.Errorf("Profile() = (%v, %v), want (week_program_2, true)", p, ok)
	}
}

func TestSubscribeIPBoostModeFlipToFalseInManu(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "XYZ0003B"})
	ch := d.AddChannel("XYZ0003B:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	boostMode := putBoolDP(ch, hmenum.ParameterBoostMode)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{SupportsBoost: true}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	c.OnMode(ModeHeat)
	boostMode.OnEvent(true)
	boostMode.OnEvent(false)

	p, ok := c.Profile()
	if !ok || p != ProfileNone {
		t.Errorf("Profile() = (%v, %v), want (none, true) after boost→off in MANU", p, ok)
	}
}

// ---------------------------------------------------------------------------
// Subscribe — RF kind wires WEEK_PROGRAM_POINTER + CONTROL_MODE
// ---------------------------------------------------------------------------

func TestSubscribeRFWeekProgramPointerAndControlMode(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmRF", Address: "RF0001"})
	ch := d.AddChannel("RF0001:2", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	weekPointer := putIntegerDP(ch, hmenum.ParameterWeekProgramPointer)
	controlMode := putIntegerDP(ch, hmenum.ParameterControlMode) // int index
	_ = controlMode

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindRF})
	cancel := c.Subscribe(ch)
	defer cancel()

	// WEEK_PROGRAM_POINTER=1 (0-based) → week_program_2.
	weekPointer.OnEvent(int32(1))
	// CONTROL_MODE=0 → AUTO-MODE.
	controlMode.OnEvent(int32(0))

	m, mOK := c.Mode()
	p, pOK := c.Profile()
	if !mOK || m != ModeAuto {
		t.Errorf("Mode() = (%v, %v), want (auto, true)", m, mOK)
	}
	if !pOK || p != ProfileWeekProgram2 {
		t.Errorf("Profile() = (%v, %v), want (week_program_2, true)", p, pOK)
	}
}

// ---------------------------------------------------------------------------
// Subscribe — VALVE_STATE / LEVEL / STATE activity sources
// ---------------------------------------------------------------------------

func TestSubscribeValveStateActivity(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmRF", Address: "VSTATE01"})
	ch := d.AddChannel("VSTATE01:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	valveState := putFloatDPValues(ch, hmenum.ParameterValveState)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindRF})
	cancel := c.Subscribe(ch)
	defer cancel()

	valveState.OnEvent(50.0)
	a, ok := c.Activity()
	if !ok || a != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true)", a, ok)
	}

	valveState.OnEvent(0.0)
	a, ok = c.Activity()
	if !ok || a != ActivityIdle {
		t.Errorf("Activity() = (%v, %v), want (idle, true)", a, ok)
	}
}

func TestSubscribeLevelActivity(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LVL0001"})
	ch := d.AddChannel("LVL0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	level := putFloatDPValues(ch, hmenum.ParameterLevel)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	level.OnEvent(0.8)
	a, ok := c.Activity()
	if !ok || a != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true)", a, ok)
	}
}

func TestSubscribeLevelIPCoolingActivity(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LVL0002"})
	ch := d.AddChannel("LVL0002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	level := putFloatDPValues(ch, hmenum.ParameterLevel)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindIP})
	c.OnHeatingCooling("COOLING")
	cancel := c.Subscribe(ch)
	defer cancel()

	level.OnEvent(0.5)
	a, ok := c.Activity()
	if !ok || a != ActivityCooling {
		t.Errorf("Activity() = (%v, %v), want (cooling, true)", a, ok)
	}
}

func TestSubscribeStateActivity(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ST0001"})
	ch := d.AddChannel("ST0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	state := putBoolDP(ch, hmenum.ParameterState)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	state.OnEvent(true)
	a, ok := c.Activity()
	if !ok || a != ActivityHeating {
		t.Errorf("Activity() = (%v, %v), want (heating, true)", a, ok)
	}

	state.OnEvent(false)
	a, ok = c.Activity()
	if !ok || a != ActivityIdle {
		t.Errorf("Activity() = (%v, %v), want (idle, true)", a, ok)
	}
}

// ---------------------------------------------------------------------------
// Subscribe — TEMPERATURE_OFFSET + HEATING_COOLING master params
// ---------------------------------------------------------------------------

func TestSubscribeTemperatureOffsetMaster(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "TOFF01"})
	ch := d.AddChannel("TOFF01:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	// TEMPERATURE_OFFSET is typically a MASTER paramset DP.
	tempOff := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterTemperatureOffset),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	ch.PutMaster(tempOff)

	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindIP})
	cancel := c.Subscribe(ch)
	defer cancel()

	tempOff.OnEvent(1.5)
	v, ok := c.TemperatureOffset()
	if !ok || v != "1.5" {
		t.Errorf("TemperatureOffset() = (%v, %v), want (\"1.5\", true)", v, ok)
	}
}

// TestSubscribeHeatingCooling drives HEATING_COOLING in both wire shapes.
//
// The select case is the shape production builds: HEATING_COOLING is a
// read+write ENUM, which resolves to a data point carrying the 0-based
// VALUE_LIST index. Asserting the label alone left heatingMode() pinned to
// its "HEATING" default, so a cooling installation reported hvac_action
// `heating` for the lifetime of the process.
func TestSubscribeHeatingCooling(t *testing.T) {
	cases := []struct {
		name string
		// build returns the HEATING_COOLING DP in the shape under test
		// plus the closure that pushes a COOLING event in that shape.
		build func(generic.Spec) (device.ParameterDataPoint, func())
	}{
		{
			name: "enum index",
			build: func(s generic.Spec) (device.ParameterDataPoint, func()) {
				s.Descriptor = hmproto.ParameterData{
					Type:       hmenum.ParameterTypeEnum,
					Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
					ValueList:  []string{"HEATING", "COOLING"},
				}
				dp := generic.NewSelect(s)
				return dp, func() { dp.OnEvent(int32(1)) }
			},
		},
		{
			name: "enum label",
			build: func(s generic.Spec) (device.ParameterDataPoint, func()) {
				s.Descriptor = hmproto.ParameterData{
					Type:       hmenum.ParameterTypeString,
					Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
				}
				dp := generic.NewSensor[string](s)
				return dp, func() { dp.OnEvent("COOLING") }
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &stubWriter{}
			d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "HC0001"})
			ch := d.AddChannel("HC0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

			hcDP, fireCooling := tc.build(generic.Spec{
				Key: hmtypes.DataPointKey{
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(hmenum.ParameterHeatingCooling),
				},
			})
			ch.Put(hcDP)

			c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{}, Kind: KindIP})
			cancel := c.Subscribe(ch)
			defer cancel()

			fireCooling()
			if c.IsHeating() {
				t.Error("IsHeating() should be false after HEATING_COOLING=COOLING via Subscribe")
			}
		})
	}
}
