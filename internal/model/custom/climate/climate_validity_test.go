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

// buildValidityRig constructs a Climate channel pre-populated with value DPs
// (setpoint + actual temperature) and optionally one actuator/activity DP.
// It returns the Climate and typed references to the individual DPs so callers
// can selectively drive observations.
type validityRig struct {
	climate    *Climate
	setpoint   *generic.Float
	actualTemp *generic.Sensor[float64]
	valveState *generic.Sensor[float64] // non-nil for RF rigs
	level      *generic.Sensor[float64] // non-nil for IP rigs
	state      *generic.BinarySensor    // non-nil for IP rigs
}

func buildRFValidityRig(t *testing.T) *validityRig {
	t.Helper()
	const addr = "RFC0001:1"
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "RFC0001"})
	ch := d.AddChannel(addr, 1, "CLIMATE", hmenum.ParamsetKeyValues)

	// Setpoint for classic-RF thermostats uses SET_TEMPERATURE.
	sp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(sp)

	at := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(at)

	// Actuator/activity DP for classic-RF thermostats: VALVE_STATE.
	vs := generic.NewFloatSensor(generic.Spec{
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
	ch.Put(vs)

	c := New(Config{Channel: ch, Writer: &stubWriter{}, Kind: KindRF})
	return &validityRig{
		climate:    c,
		setpoint:   sp,
		actualTemp: at,
		valveState: vs,
	}
}

func buildIPValidityRig(t *testing.T) *validityRig {
	t.Helper()
	const addr = "IPC0001:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "IPC0001"})
	ch := d.AddChannel(addr, 1, "CLIMATE", hmenum.ParamsetKeyValues)

	// Setpoint for IP thermostats uses SET_POINT_TEMPERATURE.
	sp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetPointTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(sp)

	at := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(at)

	// Actuator/activity DPs for IP thermostats: LEVEL and STATE.
	lv := generic.NewFloatSensor(generic.Spec{
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
	ch.Put(lv)

	st := generic.NewBinarySensor(generic.Spec{
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
	ch.Put(st)

	c := New(Config{
		Channel:      ch,
		Writer:       &stubWriter{},
		Kind:         KindIP,
		Capabilities: custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5},
	})
	return &validityRig{
		climate:    c,
		setpoint:   sp,
		actualTemp: at,
		level:      lv,
		state:      st,
	}
}

// TestClimateValidityIgnoresUnobservedActuatorRF pins that a classic-RF
// thermostat reports refreshed once its temperature value slot is observed,
// regardless of whether the VALVE_STATE actuator DP has been observed.
func TestClimateValidityIgnoresUnobservedActuatorRF(t *testing.T) {
	r := buildRFValidityRig(t)

	// Sanity: nothing observed yet — climate must not report refreshed.
	if r.climate.IsRefreshed() {
		t.Fatal("climate must not be refreshed before any observation")
	}

	// Observe only the actual temperature value slot. The VALVE_STATE
	// actuator DP is deliberately left unobserved.
	r.actualTemp.OnEvent(21.5)

	// Confirm that VALVE_STATE is still unobserved.
	if _, vsObserved := r.valveState.RawValue(); vsObserved {
		t.Fatal("VALVE_STATE must not be observed — test precondition violated")
	}

	// Climate must report refreshed: the aggregate covers only value slots,
	// not actuator/activity DPs.
	if !r.climate.IsRefreshed() {
		t.Error("climate must be refreshed once a temperature value slot is observed, " +
			"even when the VALVE_STATE actuator DP is unobserved")
	}
}

// TestClimateValidityIgnoresUnobservedActuatorIP pins that an IP thermostat
// reports refreshed once its temperature value slot is observed, regardless of
// whether the LEVEL or STATE actuator DPs have been observed.
func TestClimateValidityIgnoresUnobservedActuatorIP(t *testing.T) {
	r := buildIPValidityRig(t)

	// Sanity: nothing observed yet — climate must not report refreshed.
	if r.climate.IsRefreshed() {
		t.Fatal("climate must not be refreshed before any observation")
	}

	// Observe only the actual temperature value slot. LEVEL and STATE
	// actuator DPs are deliberately left unobserved.
	r.actualTemp.OnEvent(20.0)

	// Confirm that LEVEL and STATE are still unobserved.
	if _, lvObserved := r.level.RawValue(); lvObserved {
		t.Fatal("LEVEL must not be observed — test precondition violated")
	}
	if _, stObserved := r.state.RawValue(); stObserved {
		t.Fatal("STATE must not be observed — test precondition violated")
	}

	// Climate must report refreshed: actuator/activity DPs are excluded
	// from the aggregate validity computation.
	if !r.climate.IsRefreshed() {
		t.Error("climate must be refreshed once a temperature value slot is observed, " +
			"even when LEVEL and STATE actuator DPs are unobserved")
	}
}

// TestClimateValidityRequiresAtLeastOneValueSlotObserved is the negative-sanity
// case: a climate where no value slot has been observed must not report refreshed,
// ensuring the positive assertions above are not vacuously true.
func TestClimateValidityRequiresAtLeastOneValueSlotObserved(t *testing.T) {
	// Use the RF rig: the channel has setpoint + actual temperature + VALVE_STATE,
	// none of which are observed.
	r := buildRFValidityRig(t)

	if r.climate.IsRefreshed() {
		t.Error("climate must not be refreshed when no value slot has been observed")
	}
}
