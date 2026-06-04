// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newMinimalRig builds a rig with only the setpoint and ACTUAL_TEMPERATURE
// data points wired in. Humidity is absent so tests can exercise the
// graceful-degradation path.
func newMinimalRig(t *testing.T, address string, kind Kind, w Writer, caps custom.ClimateCapabilities) *rig {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "CLIMATE", hmenum.ParamsetKeyValues)

	setpointParam := hmenum.ParameterSetTemperature
	if kind == KindIP {
		setpointParam = hmenum.ParameterSetPointTemperature
	}
	setpoint := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(setpointParam),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(setpoint)

	actual := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(actual)

	// Humidity is intentionally absent for these minimal rigs.
	c := New(Config{Channel: ch, Writer: w, Capabilities: caps, Kind: kind})
	return &rig{
		climate:           c,
		channel:           ch,
		setpoint:          setpoint,
		actualTemperature: actual,
		humidity:          nil, // not wired
	}
}

// TestIPThermostatSetTemperatureForwardsToSetPoint verifies that
// SetTemperature on a KindIP climate writes to SET_POINT_TEMPERATURE.
func TestIPThermostatSetTemperatureForwardsToSetPoint(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})

	if err := r.climate.SetTemperature(context.Background(), 21.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetPointTemperature {
		t.Errorf("KindIP SetTemperature wrote to %s, want SET_POINT_TEMPERATURE", got.param)
	}
	if got.value.(float64) != 21.5 {
		t.Errorf("KindIP SetTemperature value = %v, want 21.5", got.value)
	}
}

// TestRFThermostatSetTemperatureForwardsToSetTemperature verifies that
// SetTemperature on a KindRF climate writes to SET_TEMPERATURE.
func TestRFThermostatSetTemperatureForwardsToSetTemperature(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000341:2", KindRF, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})

	if err := r.climate.SetTemperature(context.Background(), 18.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetTemperature {
		t.Errorf("KindRF SetTemperature wrote to %s, want SET_TEMPERATURE", got.param)
	}
	if got.value.(float64) != 18.0 {
		t.Errorf("KindRF SetTemperature value = %v, want 18.0", got.value)
	}
}

// TestThermostatCurrentTemperatureReflectsWireDP verifies that the actual
// temperature accessor reflects updates pushed on the channel-side DP.
func TestThermostatCurrentTemperatureReflectsWireDP(t *testing.T) {
	t.Parallel()

	r := newRig(t, "VCU0000050:4", KindIP, &stubWriter{}, custom.ClimateCapabilities{})

	// Before any event: not observed.
	if _, ok := r.climate.CurrentTemperature(); ok {
		t.Error("CurrentTemperature() should not be observed before any event")
	}

	r.actualTemperature.OnEvent(22.5)
	if v, ok := r.climate.CurrentTemperature(); !ok || v != 22.5 {
		t.Errorf("CurrentTemperature() = (%v, %v), want (22.5, true)", v, ok)
	}

	// Update again — new value must be reflected.
	r.actualTemperature.OnEvent(19.0)
	if v, ok := r.climate.CurrentTemperature(); !ok || v != 19.0 {
		t.Errorf("CurrentTemperature() after second event = (%v, %v), want (19.0, true)", v, ok)
	}
}

// TestThermostatHumidityOptionalGracefulDegradation verifies that a climate
// without a HUMIDITY DP returns (0, false) from Humidity() and does not
// panic — all other operations remain functional.
func TestThermostatHumidityOptionalGracefulDegradation(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	// newMinimalRig does not wire the humidity DP.
	r := newMinimalRig(t, "VCU0000341:2", KindRF, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})

	hum, ok := r.climate.Humidity()
	if ok || hum != 0 {
		t.Errorf("Humidity() = (%v, %v), want (0, false) when DP absent", hum, ok)
	}

	// SetTemperature must still work even without humidity.
	if err := r.climate.SetTemperature(context.Background(), 20.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature with absent humidity DP: %v", err)
	}
}

// TestSimpleRFThermostatDoesNotSupportAutoMode verifies that SetMode(AUTO)
// returns ErrModeNotSupported for KindSimpleRF.
func TestSimpleRFThermostatDoesNotSupportAutoMode(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "HM-CC-SCD:2", KindSimpleRF, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})

	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
		t.Errorf("SimpleRF SetMode(AUTO) = %v, want ErrModeNotSupported", err)
	}
	if err := r.climate.SetMode(context.Background(), ModeCool, hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
		t.Errorf("SimpleRF SetMode(COOL) = %v, want ErrModeNotSupported", err)
	}
}

// TestSimpleRFThermostatHeatModeForwardsTemperature verifies that
// SetMode(HEAT) on KindSimpleRF calls SetTemperature(MaxTemperature).
func TestSimpleRFThermostatHeatModeForwardsTemperature(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "HM-CC-SCD:2", KindSimpleRF, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})

	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.value.(float64) != 30.5 {
		t.Errorf("SimpleRF HEAT mode wrote %v, want MaxTemperature=30.5", got.value)
	}
}

// TestThermostatBoostActivationSetsProfileOptimistically verifies that
// calling EnableBoost not only writes BOOST_MODE=true but also updates the
// in-memory Profile to ProfileBoost before the CCU echoes back.
func TestThermostatBoostActivationSetsProfileOptimistically(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})

	if err := r.climate.EnableBoost(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// BOOST_MODE=true must have been written.
	got := w.last()
	if got.param != hmenum.ParameterBoostMode || got.value != true {
		t.Errorf("EnableBoost: last write = %+v, want BOOST_MODE=true", got)
	}
	// Profile must be set optimistically.
	if p, ok := r.climate.Profile(); !ok || p != ProfileBoost {
		t.Errorf("Profile after EnableBoost = (%v, %v), want (ProfileBoost, true)", p, ok)
	}
}

// TestThermostatModesReflectsCapabilities verifies that the Modes() list
// is derived from ClimateCapabilities flags and that the order matches
func TestThermostatModesReflectsCapabilities(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		SupportsAuto: true,
		SupportsHeat: true,
		SupportsCool: false,
		SupportsOff:  true,
	})
	modes := r.climate.Modes()
	expected := []Mode{ModeAuto, ModeHeat, ModeOff}
	if len(modes) != len(expected) {
		t.Fatalf("Modes() = %v, want %v", modes, expected)
	}
	for i, m := range modes {
		if m != expected[i] {
			t.Errorf("Modes()[%d] = %v, want %v", i, m, expected[i])
		}
	}
}

// TestThermostatProfilesListIncludesBoostAndWeekPrograms pins
// : Profiles() in AUTO mode includes BOOST + the six
// week-program slots when SupportsProfile + SupportsBoost +
// SupportsAuto are set
// list for HmIP-thermostats. ProfileAway is **not** surfaced as a
// Preset
// HmIP-thermostats. Returns nil when SupportsProfile is false.
func TestThermostatProfilesListIncludesBoostAndWeekPrograms(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		SupportsProfile: true,
		SupportsBoost:   true,
		SupportsAuto:    true,
	})
	r.climate.OnMode(ModeAuto)
	profiles := r.climate.Profiles()
	if len(profiles) == 0 {
		t.Fatal("Profiles() returned empty with SupportsProfile=true")
	}
	hasBoost := false
	weeks := map[Profile]bool{}
	for _, p := range profiles {
		if p == ProfileBoost {
			hasBoost = true
		}
		switch p { //nolint:exhaustive // only week-program profiles are tallied; static profiles are checked via hasBoost
		case ProfileWeekProgram1, ProfileWeekProgram2, ProfileWeekProgram3,
			ProfileWeekProgram4, ProfileWeekProgram5, ProfileWeekProgram6:
			weeks[p] = true
		}
	}
	if !hasBoost {
		t.Error("Profiles() missing ProfileBoost when SupportsBoost=true")
	}
	if len(weeks) != 6 {
		t.Errorf("Profiles() (AUTO mode, KindIP) must include all six week-programs (got %d)", len(weeks))
	}

	// Without SupportsProfile the list must be nil.
	r2 := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if r2.climate.Profiles() != nil {
		t.Error("Profiles() must return nil when SupportsProfile=false")
	}
}

// TestThermostatActivitySubscribe verifies that the Subscribe wiring
// causes OnActivity to fire when the VALVE_STATE channel DP is updated.
func TestThermostatActivitySubscribe(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("VCU0000341:2", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	valveState := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0000341:2",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterValveState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(valveState)

	c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: KindRF, Capabilities: custom.ClimateCapabilities{}})
	unsubscribe := c.Subscribe(ch)
	defer unsubscribe()

	// Before any event: Activity not observed.
	if _, ok := c.Activity(); ok {
		t.Error("Activity should not be observed before any event")
	}

	// VALVE_STATE > 0 → heating.
	valveState.OnEvent(float64(45))
	if a, ok := c.Activity(); !ok || a != ActivityHeating {
		t.Errorf("VALVE_STATE=45 → Activity = (%v, %v), want (heating, true)", a, ok)
	}

	// VALVE_STATE = 0 → idle.
	valveState.OnEvent(float64(0))
	if a, ok := c.Activity(); !ok || a != ActivityIdle {
		t.Errorf("VALVE_STATE=0 → Activity = (%v, %v), want (idle, true)", a, ok)
	}
}

// TestThermostatTemperatureStepDefault verifies that TemperatureStep returns
// 0.5 when the capability is unset, and the configured value otherwise.
func TestThermostatTemperatureStepDefault(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if step := r.climate.TemperatureStep(); step != 0.5 {
		t.Errorf("TemperatureStep() = %v, want 0.5", step)
	}

	r2 := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{TemperatureStep: 1.0})
	if step := r2.climate.TemperatureStep(); step != 1.0 {
		t.Errorf("TemperatureStep() with cap = %v, want 1.0", step)
	}
}

// --- temperatureForHeatMode edge cases ---
//
// The helper is package-internal; tests call it through the exported
// SetMode path (KindIP HEAT) and inspect the SET_POINT_TEMPERATURE
// parameter the stubWriter captures.

// TestTemperatureForHeatModeNoSetpointObserved verifies that when no
// setpoint has been observed yet, temperatureForHeatMode falls back to
// min_temp (when min_temp > 4.5) or 5.0 (otherwise).
func TestTemperatureForHeatModeNoSetpointObserved(t *testing.T) {
	t.Parallel()

	// min_temp > 4.5 → fall back to min_temp (5.0)
	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.5,
		SupportsHeat:   true,
	})
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetPointTemperature {
		t.Fatalf("expected SET_POINT_TEMPERATURE, got %s", got.param)
	}
	if got.value != 5.0 {
		t.Errorf("HEAT setpoint (min>4.5, no observed) = %v, want 5.0", got.value)
	}
}

// TestTemperatureForHeatModeSetpointAtOffSentinel verifies that when the
// setpoint is at the OFF sentinel (4.5), temperatureForHeatMode returns 5.0
// (min_temp=4.5 → 4.5+0.5=5.0).
func TestTemperatureForHeatModeSetpointAtOffSentinel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
		SupportsHeat:   true,
	})
	// Push the OFF sentinel as the current setpoint.
	r.setpoint.OnEvent(4.5)

	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetPointTemperature {
		t.Fatalf("expected SET_POINT_TEMPERATURE, got %s", got.param)
	}
	if got.value != 5.0 {
		t.Errorf("HEAT setpoint (min=4.5, temp=4.5) = %v, want 5.0 (= 4.5+0.5)", got.value)
	}
}

// TestTemperatureForHeatModeSetpointAboveMinAndMaxClamps verifies the
// clamp-to-max branch: when the observed setpoint exceeds max_temp, the
// helper clamps it to max_temp.
func TestTemperatureForHeatModeSetpointAboveMaxClamps(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.5,
		SupportsHeat:   true,
	})
	// Push a value above max_temp.
	r.setpoint.OnEvent(35.0)

	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.value != 30.5 {
		t.Errorf("HEAT setpoint (temp>max) = %v, want 30.5 (clamped)", got.value)
	}
}

// TestTemperatureForHeatModeSetpointInRangePassesThrough verifies that a
// valid setpoint in [min, max] is passed through unchanged.
func TestTemperatureForHeatModeSetpointInRangePassesThrough(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.5,
		SupportsHeat:   true,
	})
	r.setpoint.OnEvent(22.0)

	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.value != 22.0 {
		t.Errorf("HEAT setpoint (valid range) = %v, want 22.0", got.value)
	}
}

// TestTemperatureForHeatModeSetpointBelowMinFallsBack verifies that when the
// observed setpoint is below min_temp (but above the OFF sentinel), the
// helper falls back to min_temp.
func TestTemperatureForHeatModeSetpointBelowMinFallsBack(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newMinimalRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 10.0,
		MaxTemperature: 30.5,
		SupportsHeat:   true,
	})
	// Push a value that is below min_temp but above the OFF sentinel.
	r.setpoint.OnEvent(7.0)

	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.value != 10.0 {
		t.Errorf("HEAT setpoint (temp<min) = %v, want 10.0 (fallback to min)", got.value)
	}
}

// TestThermostatActivityHonoursCoolingMode is a regression test for the fix
// It verifies that the cooling-mode-aware activity
// inference in Subscribe (and the replayCurrentValue path) produces
// ActivityCooling for KindIP thermostats when HEATING_COOLING == "COOLING",
// while KindRF thermostats remain heat-only regardless of the HEATING_COOLING
// parameter (the `c.Kind == KindIP` guard in activeMode).
//
// Cases covered (table-driven):
//  1. KindIP, COOLING, LEVEL > 0         → ActivityCooling
//  2. KindIP, COOLING, LEVEL = 0         → ActivityIdle
//  3. KindIP, COOLING, STATE = true      → ActivityCooling
//  4. KindIP, COOLING, STATE = false     → ActivityIdle
//  5. KindIP, HEATING (default), LEVEL > 0 → ActivityHeating (regression)
//  6. KindRF, COOLING, LEVEL > 0         → ActivityHeating (RF heat-only)
//  7. KindRF, HEATING, VALVE_STATE > 0   → ActivityHeating (regression)
func TestThermostatActivityHonoursCoolingMode(t *testing.T) {
	t.Parallel()

	const addr = "VCU0000999:4"

	type dpKind int
	const (
		dpLevel      dpKind = iota // float LEVEL
		dpValveState               // float VALVE_STATE
		dpState                    // bool STATE
	)

	cases := []struct {
		name     string
		kind     Kind
		hcMode   string // "" means "do not call OnHeatingCooling"
		dp       dpKind
		value    any // float64 or bool
		wantAct  Activity
		wantObsv bool
	}{
		// 1: KindIP + COOLING + LEVEL > 0 → ActivityCooling
		{
			name: "KindIP_COOLING_LEVEL_gt0",
			kind: KindIP, hcMode: "COOLING",
			dp: dpLevel, value: float64(55),
			wantAct: ActivityCooling, wantObsv: true,
		},
		// 2: KindIP + COOLING + LEVEL = 0 → ActivityIdle
		{
			name: "KindIP_COOLING_LEVEL_zero",
			kind: KindIP, hcMode: "COOLING",
			dp: dpLevel, value: float64(0),
			wantAct: ActivityIdle, wantObsv: true,
		},
		// 3: KindIP + COOLING + STATE = true → ActivityCooling
		{
			name: "KindIP_COOLING_STATE_true",
			kind: KindIP, hcMode: "COOLING",
			dp: dpState, value: true,
			wantAct: ActivityCooling, wantObsv: true,
		},
		// 4: KindIP + COOLING + STATE = false → ActivityIdle
		{
			name: "KindIP_COOLING_STATE_false",
			kind: KindIP, hcMode: "COOLING",
			dp: dpState, value: false,
			wantAct: ActivityIdle, wantObsv: true,
		},
		// 5: KindIP + HEATING (default) + LEVEL > 0 → ActivityHeating (regression)
		{
			name: "KindIP_HEATING_default_LEVEL_gt0",
			kind: KindIP, hcMode: "",
			dp: dpLevel, value: float64(30),
			wantAct: ActivityHeating, wantObsv: true,
		},
		// 6: KindRF + COOLING + LEVEL > 0 → ActivityHeating (RF ignores COOLING)
		{
			name: "KindRF_COOLING_LEVEL_gt0_heats_only",
			kind: KindRF, hcMode: "COOLING",
			dp: dpLevel, value: float64(75),
			wantAct: ActivityHeating, wantObsv: true,
		},
		// 7: KindRF + HEATING + VALVE_STATE > 0 → ActivityHeating (regression)
		{
			name: "KindRF_HEATING_VALVE_STATE_gt0",
			kind: KindRF, hcMode: "HEATING",
			dp: dpValveState, value: float64(45),
			wantAct: ActivityHeating, wantObsv: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
			ch := d.AddChannel(addr, 1, "CLIMATE", hmenum.ParamsetKeyValues)

			// Wire only the DP relevant to this test case.
			var (
				levelDP      *generic.Sensor[float64]
				valveStateDP *generic.Sensor[float64]
				stateDP      *generic.BinarySensor
			)
			switch tc.dp {
			case dpLevel:
				levelDP = generic.NewFloatSensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: addr,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterLevel),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					},
				})
				ch.Put(levelDP)
			case dpValveState:
				valveStateDP = generic.NewFloatSensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: addr,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterValveState),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					},
				})
				ch.Put(valveStateDP)
			case dpState:
				stateDP = generic.NewBinarySensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: addr,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterState),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeBool,
						Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					},
				})
				ch.Put(stateDP)
			default:
				t.Fatalf("unhandled dpKind %d", tc.dp)
			}

			c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: tc.kind})

			// Set heating/cooling mode BEFORE Subscribe so that the
			// replayCurrentValue path inside Subscribe sees the correct mode.
			if tc.hcMode != "" {
				c.OnHeatingCooling(tc.hcMode)
			}

			unsubscribe := c.Subscribe(ch)
			defer unsubscribe()

			// Before any event: Activity should not be observed.
			if _, ok := c.Activity(); ok {
				t.Error("Activity should not be observed before any event")
			}

			// Fire the DP event to drive activity inference.
			switch tc.dp {
			case dpLevel:
				levelDP.OnEvent(tc.value.(float64))
			case dpValveState:
				valveStateDP.OnEvent(tc.value.(float64))
			case dpState:
				stateDP.OnEvent(tc.value.(bool))
			default:
				t.Fatalf("unhandled dpKind %d in event dispatch", tc.dp)
			}

			got, ok := c.Activity()
			if ok != tc.wantObsv {
				t.Errorf("Activity() observed = %v, want %v", ok, tc.wantObsv)
			}
			if ok && got != tc.wantAct {
				t.Errorf("Activity() = %v, want %v (case: %s)", got, tc.wantAct, fmt.Sprintf("kind=%v hcMode=%q dp=%d value=%v", tc.kind, tc.hcMode, tc.dp, tc.value))
			}
		})
	}
}
