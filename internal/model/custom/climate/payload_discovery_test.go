// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// discoveryCtx is a minimal stub for payload.HADiscoveryContext used in
// payload-builder smoke tests. It returns stable, testable topic strings.
type discoveryCtx struct{}

func (discoveryCtx) CustomDPStateTopic() string { return "test/custom/state" }
func (discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + parameter
}

func (discoveryCtx) WireParameterStateTopicOn(channelAddress, parameter string) string {
	return "test/" + channelAddress + "/" + parameter
}

// compile-time check: discoveryCtx satisfies payload.HADiscoveryContext.
var _ payload.HADiscoveryContext = discoveryCtx{}

func TestClimateHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var c *Climate
	comp, body := c.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestClimateHADiscoveryPayload_NilContextReturnsNil(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	comp, body := r.climate.HADiscoveryPayload(nil)
	if comp != "" || body != nil {
		t.Fatalf("nil ctx: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestClimateHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	comp, body := r.climate.HADiscoveryPayload(discoveryCtx{})
	if comp != "climate" {
		t.Fatalf("component = %q, want %q", comp, "climate")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestClimateHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	required := []string{
		"min_temp",
		"max_temp",
		"temp_step",
		"temperature_unit",
		"mode_state_topic",
		"mode_state_template",
		"mode_command_topic",
		"current_temperature_topic",
		"current_temperature_template",
	}
	for _, key := range required {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q in HA discovery body", key)
		}
	}
}

func TestClimateHADiscoveryPayload_TopicValues(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	ctx := discoveryCtx{}
	_, body := r.climate.HADiscoveryPayload(ctx)

	// ADR 0011: derived fields use the custom-DP aggregate state topic;
	// direct wire values reference per-DP topics.
	if v, _ := body["mode_state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("body[mode_state_topic] = %q, want aggregate %q", v, ctx.CustomDPStateTopic())
	}
	// Both wire values name the channel they resolved to — for an HmIP
	// thermostat that is the custom DP's own channel.
	wantCurrentTemp := ctx.WireParameterStateTopicOn("HmIP-BWTH:1", "ACTUAL_TEMPERATURE")
	if v, _ := body["current_temperature_topic"].(string); v != wantCurrentTemp {
		t.Errorf("body[current_temperature_topic] = %q, want per-DP topic %q", v, wantCurrentTemp)
	}
	wantSetpoint := ctx.WireParameterStateTopicOn("HmIP-BWTH:1", "SET_POINT_TEMPERATURE")
	if v, _ := body["temperature_state_topic"].(string); v != wantSetpoint {
		t.Errorf("body[temperature_state_topic] = %q, want per-DP topic %q", v, wantSetpoint)
	}

	wantModeCmd := ctx.ServiceMethodCommandTopic("set_mode")
	if v, _ := body["mode_command_topic"].(string); v != wantModeCmd {
		t.Errorf("body[mode_command_topic] = %q, want %q", v, wantModeCmd)
	}

	wantTempCmd := ctx.ServiceMethodCommandTopic("set_temperature")
	if v, _ := body["temperature_command_topic"].(string); v != wantTempCmd {
		t.Errorf("body[temperature_command_topic] = %q, want %q", v, wantTempCmd)
	}
}

// TestClimateStatePayloadAlwaysCarriesHvacModeActionPreset verifies that
// the StatePayload MUST always carry
// hvac_mode / preset_mode / action keys, even before the CCU has
// emitted a CONTROL_MODE / ACTIVITY value. HA's
// `value_json.<field>` templates render to empty strings when the
// key is absent, leaving the climate card stuck on "unknown" until
// the first push event. Stamping safe defaults (off / none / idle)
// gives HA a coherent initial state and keeps the card interactive.
func TestClimateStatePayloadAlwaysCarriesHvacModeActionPreset(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5, MaxTemperature: 30.5,
	})
	state, ok := r.climate.State().(*payload.ClimateState)
	if !ok || state == nil {
		t.Fatal("StatePayload did not return *payload.ClimateState")
	}

	for _, kv := range []struct{ key, val string }{
		{"hvac_mode", state.HVACMode},
		{"preset_mode", state.PresetMode},
		{"action", state.Action},
	} {
		if kv.val == "" {
			t.Errorf("StatePayload[%q] is empty string — HA template renders nothing", kv.key)
		}
	}

	if state.HVACMode != string(ModeOff) {
		t.Errorf("hvac_mode default = %q, want %q (safest pre-observation fallback)", state.HVACMode, ModeOff)
	}
	if state.PresetMode != string(ProfileNone) {
		t.Errorf("preset_mode default = %q, want %q", state.PresetMode, ProfileNone)
	}
	// When hvac_mode is "off" (either observed or defaulted), action must
	// Also be "off" — mirrors py:492. Before the
	// first push event the default mode is ModeOff, so the default action
	// must be ActivityOff, not ActivityIdle.
	if state.Action != string(ActivityOff) {
		t.Errorf("action default = %q, want %q (mode=off implies action=off)", state.Action, ActivityOff)
	}
}

// TestClimateStatePayloadActivityOff pins the
// (climate.py:492): when hvac_mode is "off", action MUST be "off".
// This covers three cases:
// 1. Mode explicitly set to ModeOff — action must be ActivityOff.
// 2. Mode set to non-off with activity heating — action must be ActivityHeating.
// 3. Mode set to non-off without observed activity — action must be ActivityIdle.
func TestClimateStatePayloadActivityOff(t *testing.T) {
	t.Parallel()

	caps := custom.ClimateCapabilities{
		SupportsOff:    true,
		SupportsHeat:   true,
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	}

	t.Run("mode=off → action=off", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "HmIP-BWTH:1", KindRF, &stubWriter{}, caps)
		r.climate.OnMode(ModeOff)
		state, _ := r.climate.State().(*payload.ClimateState)
		if state == nil || state.Action != string(ActivityOff) {
			t.Errorf("action = %q, want %q when mode=off", state.Action, ActivityOff)
		}
	})

	t.Run("mode=heat + activity=heating → action=heating", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "HmIP-BWTH:2", KindRF, &stubWriter{}, caps)
		r.climate.OnMode(ModeHeat)
		r.climate.OnActivity(ActivityHeating)
		state, _ := r.climate.State().(*payload.ClimateState)
		if state == nil || state.Action != string(ActivityHeating) {
			t.Errorf("action = %q, want %q when mode=heat and activity=heating", state.Action, ActivityHeating)
		}
	})

	t.Run("mode=heat + no activity observed → action=idle", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "HmIP-BWTH:3", KindRF, &stubWriter{}, caps)
		r.climate.OnMode(ModeHeat)
		// deliberately do NOT call OnActivity
		state, _ := r.climate.State().(*payload.ClimateState)
		if state == nil || state.Action != string(ActivityIdle) {
			t.Errorf("action = %q, want %q when mode=heat but no activity observed", state.Action, ActivityIdle)
		}
	})
}

// TestClimateHADiscoveryPayload_PresetModesExcludesNone pins the HA
// schema rule discovered via a real HA log: `preset_modes must not
// include preset mode 'none'`. The domain-side Profiles() list keeps
// ProfileNone for state reporting, but the HA discovery payload must
// strip it. Reproduces the 60-error storm from the
// `home-assistant_2026-04-30T11-54-57.157Z.log` capture.
//
// Per the openccu-loom Profiles() list mirrors
// 's — week-program slots are exposed
// unconditionally (no mode gating), and ProfileAway is **not**
// Surfaced as a preset (
// channels for HmIP-thermostats; HA-native preset_modes for these
// devices is `[boost, week_program_1..6]`).
func TestClimateHADiscoveryPayload_PresetModesExcludesNone(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		SupportsProfile: true,
		SupportsBoost:   true,
		SupportsAuto:    true,
	})
	r.climate.OnMode(ModeAuto)
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	raw, ok := body["preset_modes"]
	if !ok {
		t.Fatal("preset_modes missing — SupportsProfile=true should expose it")
	}
	list, ok := raw.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("preset_modes type/empty: %T %v", raw, raw)
	}
	for _, mode := range list {
		if mode == string(ProfileNone) {
			t.Fatalf("preset_modes must not include 'none' (HA reserves it as the implicit unset state); got %v", list)
		}
	}
	// Sanity: Boost + week-program slots are present (matches
	// the HA integration's HmIP-WTH preset_modes list).
	want := map[string]bool{
		string(ProfileBoost):        true,
		string(ProfileWeekProgram1): true,
		string(ProfileWeekProgram6): true,
	}
	for _, mode := range list {
		delete(want, mode)
	}
	if len(want) > 0 {
		t.Errorf("preset_modes missing expected modes %v (have %v)", want, list)
	}

	// The companion topics must accompany a non-empty preset_modes list.
	for _, key := range []string{
		"preset_mode_state_topic",
		"preset_mode_command_topic",
		"preset_mode_value_template",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing companion key %q for preset_modes", key)
		}
	}
}

// TestClimateConfigPayload_PresetModesExcludesNone covers the second
// emitter (ConfigPayload, used for non-HA UI consumers that mirror the
// HA shape). Same rule applies.
func TestClimateConfigPayload_PresetModesExcludesNone(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		SupportsProfile: true,
		SupportsBoost:   true,
	})
	cfg, _ := r.climate.Config().(*payload.ClimateConfig)
	if cfg == nil || len(cfg.PresetModes) == 0 {
		t.Fatal("preset_modes missing in ConfigPayload")
	}
	for _, mode := range cfg.PresetModes {
		if mode == string(ProfileNone) {
			t.Fatalf("ConfigPayload preset_modes must not include 'none'; got %v", cfg.PresetModes)
		}
	}
}

// TestClimatePrecisionAbsent pins the fix: openccu-loom
// No longer emits `precision` because
// it (the HA-native integration only configures
// `_attr_target_temperature_step`). Emitting both produced 26× drift
// in the discovery snapshot without any UX benefit — HA derives
// slider granularity from `temp_step` alone.
func TestClimatePrecisionAbsent(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		TemperatureStep: 0.5,
	})
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	if _, ok := body["precision"]; ok {
		t.Errorf("precision must be absent — HA-native integration does not set it; got %v", body["precision"])
	}
	tempStep, ok := body["temp_step"]
	if !ok {
		t.Fatal("temp_step key missing from HA discovery body")
	}
	if v, _ := tempStep.(float64); v != 0.5 {
		t.Errorf("temp_step = %v, want 0.5", v)
	}
}

// TestClimateOptimisticIsFalse pins the
// optimistic must be false so HA does not apply setpoint changes locally
// before the CCU echoes them back.
func TestClimateOptimisticIsFalse(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	raw, ok := body["optimistic"]
	if !ok {
		t.Fatal("optimistic key missing from HA discovery body")
	}
	v, ok := raw.(bool)
	if !ok {
		t.Fatalf("optimistic type = %T, want bool", raw)
	}
	if v {
		t.Error("optimistic = true, want false")
	}
}

// TestClimateTemperatureUnitMappedFromCapabilities verifies that the degree
// sign is stripped so HA receives "C" / "F" rather than "°C" / "°F".
func TestClimateTemperatureUnitMappedFromCapabilities(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		TemperatureUnit: "°F",
	})
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	v, _ := body["temperature_unit"].(string)
	if v != "F" {
		t.Errorf("temperature_unit = %q, want %q (degree sign must be stripped for HA)", v, "F")
	}
}

func TestClimateHADiscoveryPayload_TempBoundsAndUnit(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	_, body := r.climate.HADiscoveryPayload(discoveryCtx{})

	// Capabilities-fallback 4.5 == _OFF_TEMPERATURE → MinTemp adds the
	// default step (0.5) so HA's slider doesn't expose the off-state
	// as a normal setpoint.
	if v, _ := body["min_temp"].(float64); v != 5.0 {
		t.Errorf("min_temp = %v, want 5.0", v)
	}
	if v, _ := body["max_temp"].(float64); v != 30.5 {
		t.Errorf("max_temp = %v, want 30.5", v)
	}
	if v, _ := body["temperature_unit"].(string); v != "C" {
		t.Errorf("temperature_unit = %q, want %q", v, "C")
	}
}

// TestClimateStatePayloadCarriesMeasurements pins the aggregate-state
// measurement fields: once the channel field DPs report, the CDP state
// must carry current_temperature / set_temperature / current_humidity so
// REST/WS consumers can populate a climate card from the state alone —
// before observation the keys are omitted (nil), never zero-stamped.
func TestClimateStatePayloadCarriesMeasurements(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5, MaxTemperature: 30.5,
	})

	state, ok := r.climate.State().(*payload.ClimateState)
	if !ok {
		t.Fatal("StatePayload did not return *payload.ClimateState")
	}
	if state.CurrentTemperature != nil || state.SetTemperature != nil || state.CurrentHumidity != nil {
		t.Fatal("measurement fields must be omitted before observation")
	}

	r.actualTemperature.OnEvent(21.7)
	r.setpoint.OnEvent(15.0)

	state, _ = r.climate.State().(*payload.ClimateState)
	if state.CurrentTemperature == nil || *state.CurrentTemperature != 21.7 {
		t.Errorf("current_temperature = %v, want 21.7", state.CurrentTemperature)
	}
	if state.SetTemperature == nil || *state.SetTemperature != 15.0 {
		t.Errorf("set_temperature = %v, want 15.0", state.SetTemperature)
	}
}

// TestClimateHumidityIntegerTyped pins the HmIP humidity quirk: the
// HUMIDITY parameter is INTEGER-typed on HmIP thermostats, so the
// float-only slot never resolved and current_humidity stayed absent
// from the aggregate state (wall thermostats showed no humidity in HA).
func TestClimateHumidityIntegerTyped(t *testing.T) {
	t.Parallel()
	c := &Climate{humidityInt: generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BWTH:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHumidity),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})}
	if _, ok := c.Humidity(); ok {
		t.Fatal("Humidity() must be unobserved before any event")
	}
	c.humidityInt.OnEvent(int32(51))
	if v, ok := c.Humidity(); !ok || v != 51 {
		t.Fatalf("Humidity() = (%v,%v), want (51,true)", v, ok)
	}
}

// newIntegerHumidityClimate builds a bare Climate whose only humidity
// slot is the INTEGER one HmIP wall thermostats resolve into.
func newIntegerHumidityClimate() *Climate {
	return &Climate{humidityInt: generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BWTH:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHumidity),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})}
}

// TestClimateDiscoveryAdvertisesIntegerHumidity pins that the HA
// discovery humidity plane follows the same both-slots rule
// [Climate.Humidity] does: an HmIP thermostat types HUMIDITY INTEGER,
// so gating on the FLOAT slot alone drops current_humidity_topic and
// the humidity never reaches Home Assistant.
func TestClimateDiscoveryAdvertisesIntegerHumidity(t *testing.T) {
	t.Parallel()
	c := newIntegerHumidityClimate()
	_, body := c.HADiscoveryPayload(discoveryCtx{})
	if _, ok := body["current_humidity_topic"]; !ok {
		t.Fatal("current_humidity_topic missing for an INTEGER-typed HUMIDITY channel")
	}
	if v, _ := body["current_humidity_template"].(string); v != "{{ value_json.value }}" {
		t.Errorf("current_humidity_template = %q", v)
	}
}

// TestClimateDiscoveryOmitsHumidityWithoutSlot is the negative half:
// a channel without any HUMIDITY parameter must not advertise the plane.
func TestClimateDiscoveryOmitsHumidityWithoutSlot(t *testing.T) {
	t.Parallel()
	c := &Climate{}
	_, body := c.HADiscoveryPayload(discoveryCtx{})
	if _, ok := body["current_humidity_topic"]; ok {
		t.Fatal("current_humidity_topic advertised without a HUMIDITY parameter")
	}
}
