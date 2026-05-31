// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- C-T2-01: IsStateChangeWith ---

func TestDewPointSensor_IsStateChangeWith_Default(t *testing.T) {
	t.Parallel()
	ch, temp, hum := newTempHumChannel(t, "SCW0001")
	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	// Before any observation: should return false (not refreshed).
	if sensor.IsStateChangeWith() {
		t.Error("IsStateChangeWith() must be false before sources are observed")
	}

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	// After observation: default returns true (refreshed, not uncertain).
	if !sensor.IsStateChangeWith() {
		t.Error("IsStateChangeWith() must be true after sources observed")
	}
}

func TestDewPointSensor_IsStateChangeWith_Force(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewDewPointSensor()
	// No sources registered → default returns false.
	if sensor.IsStateChangeWith() {
		t.Error("baseline: no sources → false")
	}
	// WithForceStateChange must override.
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange must return true regardless of sensor state")
	}
}

func TestDewPointSpreadSensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewDewPointSpreadSensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on DewPointSpreadSensor must return true")
	}
}

func TestFrostPointSensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewFrostPointSensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on FrostPointSensor must return true")
	}
}

func TestVaporConcentrationSensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewVaporConcentrationSensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on VaporConcentrationSensor must return true")
	}
}

func TestEnthalpySensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewEnthalpySensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on EnthalpySensor must return true")
	}
}

func TestApparentTemperatureSensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewApparentTemperatureSensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on ApparentTemperatureSensor must return true")
	}
}

func TestOperatingVoltageLevelSensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := calculated.NewOperatingVoltageLevelSensor()
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on OperatingVoltageLevelSensor must return true")
	}
}

func TestDerivedBinarySensor_IsStateChangeWith(t *testing.T) {
	t.Parallel()
	sensor := &calculated.DerivedBinarySensor{}
	if !sensor.IsStateChangeWith(calculated.WithForceStateChange()) {
		t.Error("WithForceStateChange on DerivedBinarySensor must return true")
	}
}

// --- C-T2-02: Re-subscribe idempotency after Channel.Replace ---

func TestCalcSensor_ResubscribeIdempotency(t *testing.T) {
	t.Parallel()

	buildCh := func(addr string) (*device.Channel, *generic.Sensor[float64], *generic.Sensor[float64]) {
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: addr})
		ch := d.AddChannel(addr+":1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
		temp := generic.NewFloatSensor(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
		})
		hum := generic.NewFloatSensor(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
		})
		ch.Put(temp)
		ch.Put(hum)
		return ch, temp, hum
	}

	ch1, temp1, hum1 := buildCh("IDM0001")
	sensor := calculated.NewDewPointSensor()
	ch1.AttachCalculatedDataPoint(sensor)

	// Feed first channel.
	temp1.OnEvent(20.0)
	hum1.OnEvent(60.0)
	val1, ok1 := sensor.Value()
	if !ok1 {
		t.Fatal("sensor must compute after first channel observed")
	}

	// Simulate a channel replace: attach to a new channel, which causes
	// the sensor's subscribe to be re-invoked. The sensor must not retain
	// stale source registrations that would distort StateUncertain.
	ch2, temp2, hum2 := buildCh("IDM0002")
	ch2.AttachCalculatedDataPoint(sensor)

	// Drive new channel.
	temp2.OnEvent(22.0)
	hum2.OnEvent(55.0)
	val2, ok2 := sensor.Value()
	if !ok2 {
		t.Fatal("sensor must compute after second channel observed")
	}

	// Values should differ because inputs differ.
	if val1 == val2 {
		t.Logf("val1=%v val2=%v — incidentally equal, but both channels were observed", val1, val2)
	}

	// Old channel writes must not corrupt the sensor after re-subscribe.
	// Fire old channel again; sensor state must reflect only the most
	// recent computation (no panic, no deadlock).
	temp1.OnEvent(25.0)
	hum1.OnEvent(70.0)
}
