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

// newTempHumChannel builds a channel with ACTUAL_TEMPERATURE + HUMIDITY
// sensors and returns handles to drive them.
//
//nolint:gocritic // test rig helper — positional returns are the test convention
func newTempHumChannel(t *testing.T, addr string) (*device.Channel, *generic.Sensor[float64], *generic.Sensor[float64]) {
	t.Helper()
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

//nolint:gocritic // test rig helper — positional returns are the test convention
func newClimateChannel(t *testing.T) (*device.Channel, *generic.Sensor[float64], *generic.Sensor[float64]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

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

func TestDewPointSensorAutoSubscribesToChannel(t *testing.T) {
	ch, temp, hum := newClimateChannel(t)

	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	// Push CCU events to the temp + humidity sub-DPs.
	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	v, ok := sensor.Value()
	if !ok {
		t.Fatalf("DewPoint should have emitted a value after temp+hum updates")
	}
	if v < 9.0 || v > 10.5 {
		t.Fatalf("DewPoint(20°C, 50%%) ~ 9.27°C; got %v", v)
	}
}

func TestApparentTemperatureSubscribesAllThreeInputs(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0002"})
	ch := d.AddChannel("ABC0002:1", 1, "WEATHER", hmenum.ParamsetKeyValues)

	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "ACTUAL_TEMPERATURE"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "HUMIDITY"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	wind := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "WIND_SPEED"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
	})
	ch.Put(temp)
	ch.Put(hum)
	ch.Put(wind)

	sensor := calculated.NewApparentTemperatureSensor()
	ch.AttachCalculatedDataPoint(sensor)

	// Without wind, the sensor should hold off (its recompute requires all three).
	temp.OnEvent(35.0)
	hum.OnEvent(60.0)
	if _, ok := sensor.Value(); ok {
		t.Fatalf("ApparentTemp should not emit before wind speed observed")
	}
	wind.OnEvent(10.0)
	if _, ok := sensor.Value(); !ok {
		t.Fatalf("ApparentTemp should now emit after wind speed observed")
	}
}

func TestReAttachReplacesPriorSubscription(t *testing.T) {
	ch, temp, _ := newClimateChannel(t)
	first := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(first)

	// Re-attach a new sensor with the *same* DataPointKey — the channel
	// should release the prior subscription and wire the new one.
	second := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(second)

	temp.OnEvent(15.0) // both still listen to ACTUAL_TEMPERATURE; first should ALSO have moved on, but the contract is "second is registered"

	calcDPs := ch.CalculatedDataPoints()
	if len(calcDPs) != 1 {
		t.Fatalf("expected one calculated DP after re-attach (same key), got %d", len(calcDPs))
	}
	if calcDPs[0] != second {
		t.Fatalf("expected the second sensor to be registered, got first")
	}
}

func TestDewPointSpreadSensorSubscribe(t *testing.T) {
	ch, temp, hum := newTempHumChannel(t, "DEF0001")
	sensor := calculated.NewDewPointSpreadSensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(22.0)
	hum.OnEvent(55.0)

	v, ok := sensor.Value()
	if !ok {
		t.Fatal("DewPointSpread should compute after temp+hum observed")
	}
	ref, _ := calculated.DewPointSpread(22.0, 55.0)
	if v != ref {
		t.Fatalf("got %v, want %v", v, ref)
	}
}

func TestFrostPointSensorSubscribe(t *testing.T) {
	ch, temp, hum := newTempHumChannel(t, "DEF0002")
	sensor := calculated.NewFrostPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(-5.0)
	hum.OnEvent(70.0)

	_, ok := sensor.Value()
	if !ok {
		t.Fatal("FrostPoint should compute after temp+hum observed")
	}
}

func TestVaporConcentrationSensorSubscribe(t *testing.T) {
	ch, temp, hum := newTempHumChannel(t, "DEF0003")
	sensor := calculated.NewVaporConcentrationSensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	v, ok := sensor.Value()
	if !ok {
		t.Fatal("VaporConcentration should compute after temp+hum observed")
	}
	ref, _ := calculated.VaporConcentration(20.0, 50.0)
	if v != ref {
		t.Fatalf("got %v, want %v", v, ref)
	}
}

func TestEnthalpySensorSubscribeWithPressure(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEF0004"})
	ch := d.AddChannel("DEF0004:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	pressure := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterAirPressure)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(temp)
	ch.Put(hum)
	ch.Put(pressure)

	sensor := calculated.NewEnthalpySensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)
	pressure.OnEvent(980.0)

	v, ok := sensor.Value()
	if !ok {
		t.Fatal("Enthalpy should compute when all three inputs are present")
	}
	ref, _ := calculated.Enthalpy(20.0, 50.0, 980.0)
	if v != ref {
		t.Fatalf("got %v, want %v", v, ref)
	}
}

func TestEnthalpySensorSubscribeWithoutPressure(t *testing.T) {
	ch, temp, hum := newTempHumChannel(t, "DEF0005")
	sensor := calculated.NewEnthalpySensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	v, ok := sensor.Value()
	if !ok {
		t.Fatal("Enthalpy should compute using default pressure when pressure param absent")
	}
	ref, _ := calculated.Enthalpy(20.0, 50.0, calculated.DefaultPressureHPa)
	if v != ref {
		t.Fatalf("got %v, want %v", v, ref)
	}
}
