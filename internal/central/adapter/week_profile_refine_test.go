// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeFloatDPWithBounds builds a *generic.Float DP with the given
// MIN/MAX descriptor bounds. Used to feed deriveWeekProfileMetadata
// realistic descriptor data without standing up the full pipeline.
func makeFloatDPWithBounds(channelAddr string, p hmenum.Parameter, minVal, maxVal float64) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "Test-HmIP-RF",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage([]byte(toJSONNumber(minVal))),
			Max:        json.RawMessage([]byte(toJSONNumber(maxVal))),
		},
	})
}

// makeIntDPWithMax builds a *generic.Integer with the given MAX bound
// for ACTIVE_PROFILE / WEEK_PROGRAM_POINTER fixtures.
func makeIntDPWithMax(channelAddr string, p hmenum.Parameter, maxVal int) *generic.Integer {
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "Test-HmIP-RF",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Max:        json.RawMessage([]byte(toJSONNumber(float64(maxVal)))),
		},
	})
}

func toJSONNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func TestDeriveWeekProfileMetadataReadsSetpointBounds(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	ch := d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	ch.Put(makeFloatDPWithBounds("0001ABCD:1", hmenum.ParameterSetPointTemperature, 4.5, 30.5))

	meta := deriveWeekProfileMetadata(d, ch)
	if meta.MinTemp != 4.5 {
		t.Errorf("MinTemp = %v, want 4.5", meta.MinTemp)
	}
	if meta.MaxTemp != 30.5 {
		t.Errorf("MaxTemp = %v, want 30.5", meta.MaxTemp)
	}
}

func TestDeriveWeekProfileMetadataPrefersActiveProfileOverPointer(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	ch := d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	// HmIP-style: ACTIVE_PROFILE 1-based, MAX=6 → ProfileCount=6.
	ch.Put(makeIntDPWithMax("0001ABCD:1", hmenum.ParameterActiveProfile, 6))
	// Classic-HM-style: WEEK_PROGRAM_POINTER 0-based, MAX=2 → ProfileCount=3.
	// On a device that ships both we must trust ACTIVE_PROFILE; the
	// pointer is then the legacy mirror for compat readers.
	ch.Put(makeIntDPWithMax("0001ABCD:1", hmenum.ParameterWeekProgramPointer, 2))

	meta := deriveWeekProfileMetadata(d, ch)
	if meta.ProfileCount != 6 {
		t.Errorf("ProfileCount = %d, want 6 (from ACTIVE_PROFILE.MAX, not pointer)", meta.ProfileCount)
	}
}

func TestDeriveWeekProfileMetadataRFPointerOnly(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "Test-BidCos-RF", Address: "JEQ0123"})
	ch := d.AddChannel("JEQ0123:4", 4, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	// Classic-HM RF: WEEK_PROGRAM_POINTER 0-based, MAX=2 → ProfileCount=3.
	ch.Put(makeIntDPWithMax("JEQ0123:4", hmenum.ParameterWeekProgramPointer, 2))

	meta := deriveWeekProfileMetadata(d, ch)
	if meta.ProfileCount != 3 {
		t.Errorf("ProfileCount = %d, want 3 (RF: pointer.MAX+1)", meta.ProfileCount)
	}
}

func TestDeriveWeekProfileMetadataNoSourcesYieldsZero(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	meta := deriveWeekProfileMetadata(d, d.Channel("0001ABCD:1"))
	if meta.ProfileCount != 0 {
		t.Errorf("ProfileCount = %d, want 0 (no source DPs → ApplyDeviceMetadata leaves count untouched)", meta.ProfileCount)
	}
	if meta.MinTemp != 0 || meta.MaxTemp != 0 {
		t.Errorf("Temp bounds = (%v, %v), want both 0 (no setpoint DP)", meta.MinTemp, meta.MaxTemp)
	}
}

func TestSubscribeProfilePointerSeedsCurrentProfile(t *testing.T) {
	t.Parallel()
	// HmIP-style: ACTIVE_PROFILE has an observed value of 3 → DP
	// CurrentProfile must read "P3" after subscribe.
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	ch := d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	dp := makeIntDPWithMax("0001ABCD:1", hmenum.ParameterActiveProfile, 6)
	if !dp.OnWireValue(int32(3)) {
		t.Fatalf("OnWireValue(3) returned false — fixture cannot seed initial value")
	}
	ch.Put(dp)
	attachWeekProfileToChannel(ch, "Test")
	wp := ch.WeekProfile()
	wp.ApplyDeviceMetadata(weekprofile.DeviceMetadata{ProfileCount: 6})

	subscribeProfilePointer(d, wp)
	if got := wp.CurrentProfile(); got != "P3" {
		t.Errorf("after subscribe, CurrentProfile = %q, want %q", got, "P3")
	}
}

func TestSubscribeProfilePointerUpdatesOnPush(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	ch := d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	dp := makeIntDPWithMax("0001ABCD:1", hmenum.ParameterActiveProfile, 6)
	ch.Put(dp)
	attachWeekProfileToChannel(ch, "Test")
	wp := ch.WeekProfile()
	wp.ApplyDeviceMetadata(weekprofile.DeviceMetadata{ProfileCount: 6})
	subscribeProfilePointer(d, wp)

	// CCU push event: ACTIVE_PROFILE moved to slot 4.
	dp.OnWireValue(int32(4))
	if got := wp.CurrentProfile(); got != "P4" {
		t.Errorf("after push update to 4, CurrentProfile = %q, want %q", got, "P4")
	}
}

func TestApplyDeviceMetadataKeepsProfileCountWhenZero(t *testing.T) {
	t.Parallel()
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "Test",
		ChannelAddress: "ABC:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   6,
	})
	wp.ApplyDeviceMetadata(weekprofile.DeviceMetadata{
		MinTemp:      4.5,
		MaxTemp:      30.5,
		ProfileCount: 0, // unchanged source — must not zero out
	})
	if wp.ProfileCount() != 6 {
		t.Errorf("ProfileCount = %d, want 6 (zero source must not overwrite)", wp.ProfileCount())
	}
	if wp.MinTemp() != 4.5 || wp.MaxTemp() != 30.5 {
		t.Errorf("temp bounds = (%v, %v), want (4.5, 30.5)", wp.MinTemp(), wp.MaxTemp())
	}
}

func TestDeriveWeekProfileMetadataLooksAcrossChannels(t *testing.T) {
	t.Parallel()
	// SET_POINT_TEMPERATURE on channel 1, ACTIVE_PROFILE on channel 0.
	// The helper must walk every channel of the device, not just the
	// channel that owns the schedule.
	d := device.New(device.Config{InterfaceID: "Test-HmIP-RF", Address: "0001ABCD"})
	ch0 := d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch1 := d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	ch0.Put(makeIntDPWithMax("0001ABCD:0", hmenum.ParameterActiveProfile, 6))
	ch1.Put(makeFloatDPWithBounds("0001ABCD:1", hmenum.ParameterSetPointTemperature, 5.0, 30.0))

	meta := deriveWeekProfileMetadata(d, ch1) // pass climate channel; helper still scans whole device
	if meta.ProfileCount != 6 || meta.MinTemp != 5.0 || meta.MaxTemp != 30.0 {
		t.Errorf("metadata = %+v, want {6, 5.0, 30.0}", meta)
	}
}
