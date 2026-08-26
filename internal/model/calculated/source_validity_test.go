// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newBoundedTempHumChannel builds a channel whose temperature and humidity
// sensors carry declared MIN/MAX bounds, so a reading outside the descriptor
// range flips the source's own IsValid() to false while it stays observed.
//
//nolint:gocritic // test rig helper — positional returns are the test convention
func newBoundedTempHumChannel(t *testing.T, addr string) (*device.Channel, *generic.Sensor[float64], *generic.Sensor[float64]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: addr})
	ch := d.AddChannel(addr+":1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)

	temp := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage(`-30`),
			Max:        json.RawMessage(`60`),
		},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage(`0`),
			Max:        json.RawMessage(`100`),
		},
	})
	ch.Put(temp)
	ch.Put(hum)
	return ch, temp, hum
}

// stateAvailable extracts the `available` flag from a sensor's state payload.
func stateAvailable(t *testing.T, st payload.StatePayload) bool {
	t.Helper()
	gs, ok := st.(*payload.GenericDataPointState)
	if !ok {
		t.Fatalf("expected *payload.GenericDataPointState, got %T", st)
	}
	return gs.Available
}

// TestCalculatedAvailabilityFollowsSourceStatus locks in that a source whose
// paired `<param>_STATUS` reports a measurement fault takes the derived sensor
// down with it. Before the validity gate the calculated sensor kept reporting
// available=true (and kept recomputing) off a reading the CCU had already
// flagged as unusable.
func TestCalculatedAvailabilityFollowsSourceStatus(t *testing.T) {
	ch, temp, hum := newBoundedTempHumChannel(t, "SRCVAL01")
	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	if !stateAvailable(t, sensor.State()) {
		t.Fatal("dew point should be available while both sources are healthy")
	}

	// The CCU reports the temperature measurement as out of the sensor's
	// operating envelope. The value stays observed, but is no longer usable.
	temp.UpdateStatus(hmenum.ParameterStatusOverflow)

	if temp.IsValid() {
		t.Fatal("test rig: temperature source should be invalid with OVERFLOW status")
	}
	if stateAvailable(t, sensor.State()) {
		t.Fatal("dew point must not stay available while its temperature source is invalid")
	}
	if sensor.IsValid() {
		t.Fatal("dew point IsValid must follow its sources (north-bound dpValid gate)")
	}

	// Recovery: once the CCU reports a normal measurement again the derived
	// sensor comes back without needing a fresh calculated value.
	temp.UpdateStatus(hmenum.ParameterStatusNormal)
	if !stateAvailable(t, sensor.State()) {
		t.Fatal("dew point should recover once the source status returns to NORMAL")
	}
}

// TestCalculatedAvailabilityFollowsSourceRange covers the second way a source
// can be observed-but-unusable: a reading outside the descriptor's declared
// MIN/MAX. The derived value computed from it is physically meaningless and
// must not be published as a confirmed reading.
func TestCalculatedAvailabilityFollowsSourceRange(t *testing.T) {
	ch, temp, hum := newBoundedTempHumChannel(t, "SRCVAL02")
	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	hum.OnEvent(50.0)
	temp.OnEvent(999.0) // far outside the declared -30..60 envelope

	if temp.IsValid() {
		t.Fatal("test rig: out-of-range temperature source should be invalid")
	}
	if stateAvailable(t, sensor.State()) {
		t.Fatal("dew point must not report available off an out-of-range source")
	}
}

// TestCalculatedWithoutSourcesIsUnavailable pins the "nothing to derive from"
// case: a calculated sensor that never resolved a single state-carrying source
// has no basis for a value and must not claim availability.
func TestCalculatedWithoutSourcesIsUnavailable(t *testing.T) {
	sensor := calculated.NewDewPointSensor()
	// Drive the inner sensor directly, bypassing any source registration.
	sensor.OnEvent(9.3)

	if stateAvailable(t, sensor.State()) {
		t.Fatal("a calculated sensor without registered sources must not be available")
	}
}

// TestCalculatedAvailabilityIgnoresMasterSources guards the MASTER carve-out:
// LOW_BAT_LIMIT is a configuration input, not a state carrier. It is read into
// a reference field instead of being registered as a source, so its own
// validity must not decide whether the derived battery level is available —
// a sleeping battery device may never deliver a fresh MASTER read.
func TestCalculatedAvailabilityIgnoresMasterSources(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SRCVAL03"})
	d.Model = "HmIP-STHO"
	ch := d.AddChannel("SRCVAL03:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	voltage := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterOperatingVoltage)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage(`0`),
			Max:        json.RawMessage(`4`),
		},
	})
	ch.Put(voltage)

	// The MASTER reference is present but reads outside its own declared
	// bounds — invalid by the same rules that gate a VALUES source.
	lowBat := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterLowBatLimit)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead,
			Min:        json.RawMessage(`0`),
			Max:        json.RawMessage(`1`),
		},
	})
	lowBat.OnEvent(2.2)
	ch.PutMaster(lowBat)
	if lowBat.IsValid() {
		t.Fatal("test rig: the MASTER reference should read as invalid")
	}

	sensor := calculated.NewOperatingVoltageLevelSensor()
	ch.AttachCalculatedDataPoint(sensor)
	voltage.OnEvent(2.9)

	if _, ok := sensor.Value(); !ok {
		t.Fatal("operating voltage level should compute from the battery table + MASTER reference")
	}
	if !stateAvailable(t, sensor.State()) {
		t.Fatal("an invalid MASTER LOW_BAT_LIMIT must not gate the derived level")
	}
}
