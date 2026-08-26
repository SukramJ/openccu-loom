// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/climate" // trigger init()
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putFloatDP attaches a *generic.Float for param on ch and returns it.
func putFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Float {
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

// makeChannel builds a bare device + channel for constructor testing.
func makeChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	return d.AddChannel(address, 1, "CLIMATE", hmenum.ParamsetKeyValues)
}

// --- Registration tests ---

// TestIPThermostatConstructorIsRegistered verifies that the init() block
// registers a non-nil constructor for DeviceProfileIPThermostat.
func TestIPThermostatConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostat)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPThermostat")
	}
}

// TestIPThermostatGroupConstructorIsRegistered verifies registration for
// DeviceProfileIPThermostatGroup.
func TestIPThermostatGroupConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostatGroup)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPThermostatGroup")
	}
}

// TestRfThermostatConstructorIsRegistered verifies registration for
// DeviceProfileRfThermostat.
func TestRfThermostatConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfThermostat)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileRfThermostat")
	}
}

// TestRfThermostatGroupConstructorIsRegistered verifies registration for
// DeviceProfileRfThermostatGroup.
func TestRfThermostatGroupConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfThermostatGroup)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileRfThermostatGroup")
	}
}

// TestSimpleRfThermostatConstructorIsRegistered verifies registration for
// DeviceProfileSimpleRfThermostat.
func TestSimpleRfThermostatConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileSimpleRfThermostat)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileSimpleRfThermostat")
	}
}

// --- Constructor returns valid DP ---

// TestIPThermostatConstructorReturnsValidDP verifies that the IP
// thermostat constructor returns a non-nil AttachableDataPoint without
// error when given a channel with the expected generic DPs wired in.
func TestIPThermostatConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU001:1")
	putFloatDP(ch, hmenum.ParameterSetPointTemperature)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostat)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// TestRfThermostatConstructorReturnsValidDP verifies that the RF
// thermostat constructor returns a non-nil AttachableDataPoint.
func TestRfThermostatConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU002:2")
	putFloatDP(ch, hmenum.ParameterSetTemperature)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfThermostat)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// TestSimpleRfThermostatConstructorReturnsValidDP verifies that the
// SimpleRF thermostat constructor returns a non-nil AttachableDataPoint.
func TestSimpleRfThermostatConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU003:1")
	putFloatDP(ch, hmenum.ParameterSetTemperature)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileSimpleRfThermostat)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// --- Field wiring tests ---

// TestIPThermostatConstructorWiresSetpointField verifies that the IP
// thermostat constructor captures the SET_POINT_TEMPERATURE generic DP
// as the setpoint so Setpoint() returns the observed value.
func TestIPThermostatConstructorWiresSetpointField(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU004:1")
	setpointDP := putFloatDP(ch, hmenum.ParameterSetPointTemperature)
	// Drive a value via the generic DP.
	setpointDP.OnEvent(21.5)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostat)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	// The returned DP must be a *Climate so we can call Setpoint().
	// We import the package as a blank import above; use the interface
	// check here via type assertion to the concrete package type.
	type setpointer interface {
		Setpoint() (float64, bool)
	}
	sp, ok := dp.(setpointer)
	if !ok {
		t.Fatalf("returned DP does not implement Setpoint(); got %T", dp)
	}
	v, observed := sp.Setpoint()
	if !observed {
		t.Fatal("Setpoint() not observed after OnEvent on the generic DP")
	}
	if v != 21.5 {
		t.Errorf("Setpoint() = %v, want 21.5", v)
	}
}

// TestRfThermostatConstructorWiresSetpointField verifies that the RF
// thermostat constructor captures SET_TEMPERATURE (not SET_POINT_TEMPERATURE).
func TestRfThermostatConstructorWiresSetpointField(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU005:2")
	setpointDP := putFloatDP(ch, hmenum.ParameterSetTemperature)
	setpointDP.OnEvent(18.0)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfThermostat)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	type setpointer interface {
		Setpoint() (float64, bool)
	}
	sp, ok := dp.(setpointer)
	if !ok {
		t.Fatalf("returned DP does not implement Setpoint(); got %T", dp)
	}
	v, observed := sp.Setpoint()
	if !observed {
		t.Fatal("Setpoint() not observed after OnEvent on the generic DP")
	}
	if v != 18.0 {
		t.Errorf("Setpoint() = %v, want 18.0", v)
	}
}

// TestIPThermostatConstructorDataPointKeyIsSet verifies that the IP
// thermostat constructor produces a DataPointKey with the channel address
// so the materializer can attach it.
func TestIPThermostatConstructorDataPointKeyIsSet(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU006:1")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostat)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	if dp.DataPointKey().ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q",
			dp.DataPointKey().ChannelAddress, ch.Address)
	}
	// IP thermostat key is on SET_POINT_TEMPERATURE (fallback: bare
	// channel key when no generic DP is present).
	if dp.DataPointKey().Parameter == "" {
		t.Error("DataPointKey().Parameter is empty")
	}
}

// TestSimpleRfThermostatConstructorDataPointKeyIsSet verifies that the
// SimpleRF thermostat constructor sets a non-zero DataPointKey.
func TestSimpleRfThermostatConstructorDataPointKeyIsSet(t *testing.T) {
	t.Parallel()

	ch := makeChannel(t, "VCU007:1")
	putFloatDP(ch, hmenum.ParameterSetTemperature)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileSimpleRfThermostat)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp.DataPointKey().ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q",
			dp.DataPointKey().ChannelAddress, ch.Address)
	}
}
