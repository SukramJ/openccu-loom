// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// climateChannelWithModel builds a temperature/humidity channel on a
// device with the given model. Used to drive the factory through the
// model-gated relevance branches.
func climateChannelWithModel(t *testing.T, model string) (ch *device.Channel, tempDP, humDP *generic.Sensor[float64]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "FAC0001", Model: model})
	ch = d.AddChannel("FAC0001:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

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

// TestCreateCalculatedDataPointsAttachesClimateFamily pins the BWTH symptom:
// a temperature+humidity channel must yield DewPoint, DewPointSpread,
// VaporConcentration, Enthalpy as attached, subscribed calculated sensors.
func TestCreateCalculatedDataPointsAttachesClimateFamily(t *testing.T) {
	ch, temp, hum := climateChannelWithModel(t, "HmIP-BWTH")

	sensors := calculated.CreateCalculatedDataPoints(ch, "HmIP-BWTH")
	if len(sensors) == 0 {
		t.Fatal("factory returned no sensors for a temperature+humidity channel")
	}

	// All four temp+humidity sensors must be present.
	want := map[hmenum.CalculatedParameter]bool{
		hmenum.CalculatedParameterDewPoint:           false,
		hmenum.CalculatedParameterDewPointSpread:     false,
		hmenum.CalculatedParameterVaporConcentration: false,
		hmenum.CalculatedParameterEnthalpy:           false,
	}
	for _, s := range sensors {
		want[s.CalculatedParameter()] = true
	}
	for cp, ok := range want {
		if !ok {
			t.Errorf("missing calculated sensor %q in factory output", cp)
		}
	}

	// Channel must mirror the attached sensors so REST / WS / MQTT
	// can enumerate them through Channel.CalculatedDataPoints().
	if got := len(ch.CalculatedDataPoints()); got < 4 {
		t.Errorf("Channel.CalculatedDataPoints() = %d; want ≥ 4", got)
	}

	// Drive the source DPs and verify the wiring is live.
	temp.OnEvent(20.0)
	hum.OnEvent(50.0)
	for _, s := range sensors {
		if !s.IsRefreshed() {
			t.Errorf("sensor %q did not refresh after temp+humidity updates — Subscribe wiring missing",
				s.CalculatedParameter())
		}
	}
}

// TestCreateCalculatedDataPointsModelGatedSensorsSkipNonMatching
// guards the FrostPoint / ApparentTemperature whitelist: an
// HmIP-BWTH (not in either whitelist) must not get those sensors.
func TestCreateCalculatedDataPointsModelGatedSensorsSkipNonMatching(t *testing.T) {
	ch, _, _ := climateChannelWithModel(t, "HmIP-BWTH")

	sensors := calculated.CreateCalculatedDataPoints(ch, "HmIP-BWTH")
	for _, s := range sensors {
		switch s.CalculatedParameter() { //nolint:exhaustive // only whitelist-gated parameters need assertion; others are always allowed
		case hmenum.CalculatedParameterFrostPoint:
			t.Errorf("FrostPoint must not be created for HmIP-BWTH (not on the whitelist)")
		case hmenum.CalculatedParameterApparentTemperature:
			t.Errorf("ApparentTemperature must not be created for HmIP-BWTH (not on the whitelist)")
		}
	}
}

// TestCreateCalculatedDataPointsModelGatedSensorsAdmitWhitelist
// verifies that the whitelisted models (HmIP-SWO for ApparentTemperature
// + FrostPoint, HmIP-STHO for FrostPoint) actually get those sensors
// when the channel exposes the required source parameters.
func TestCreateCalculatedDataPointsModelGatedSensorsAdmitWhitelist(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "WX0001", Model: "HmIP-SWO"})
	ch := d.AddChannel("WX0001:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterActualTemperature, hmenum.ParameterHumidity, hmenum.ParameterWindSpeed,
	} {
		dp := generic.NewFloatSensor(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(p)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
		})
		ch.Put(dp)
	}

	sensors := calculated.CreateCalculatedDataPoints(ch, "HmIP-SWO")
	hits := map[hmenum.CalculatedParameter]bool{}
	for _, s := range sensors {
		hits[s.CalculatedParameter()] = true
	}
	for _, want := range []hmenum.CalculatedParameter{
		hmenum.CalculatedParameterFrostPoint,
		hmenum.CalculatedParameterApparentTemperature,
	} {
		if !hits[want] {
			t.Errorf("sensor %q expected on HmIP-SWO; not in factory output", want)
		}
	}
}

// TestCreateCalculatedDataPointsOperatingVoltageLevelWiring verifies that
// OperatingVoltageLevel comes online on a battery-powered model (HmIP-eTRV →
// 2× AA → voltage_max=3.0V) once OPERATING_VOLTAGE + LOW_BAT_LIMIT have been
// observed.
func TestCreateCalculatedDataPointsOperatingVoltageLevelWiring(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BATT001", Model: "HmIP-eTRV"})
	ch := d.AddChannel("BATT001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyMaster)

	opVoltage := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterOperatingVoltage), ParamsetKey: hmenum.ParamsetKeyValues},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(opVoltage)
	lowBatLimit := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterLowBatLimit), ParamsetKey: hmenum.ParamsetKeyMaster},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	lowBatLimit.OnEvent(2.2)
	ch.PutMaster(lowBatLimit)

	sensors := calculated.CreateCalculatedDataPoints(ch, "HmIP-eTRV")
	var voltage calculated.Sensor
	for _, s := range sensors {
		if s.CalculatedParameter() == hmenum.CalculatedParameterOperatingVoltageLevel {
			voltage = s
			break
		}
	}
	if voltage == nil {
		t.Fatal("OperatingVoltageLevel sensor not created for HmIP-eTRV")
	}

	// Drive a full-battery reading.
	opVoltage.OnEvent(3.0)
	if !voltage.IsRefreshed() {
		t.Fatal("OperatingVoltageLevel did not emit after OPERATING_VOLTAGE update — Subscribe wiring missing")
	}
}
