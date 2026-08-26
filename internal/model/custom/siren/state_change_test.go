// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// attachStringSensor attaches a *generic.Sensor[string] for the given
// parameter to the channel and returns the DP for event injection.
func attachStringSensor(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[string] {
	dp := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// attachSmokeStatusSensor attaches SMOKE_DETECTOR_ALARM_STATUS as the
// read-only ENUM index sensor the resolver projects it onto (see
// custom.EnumSensorField), mirroring production's *generic.Sensor[int32]
// shape rather than a *generic.Sensor[string].
func attachSmokeStatusSensor(ch *device.Channel) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSmokeDetectorAlarmStatus),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"IDLE_OFF", "IDLE_ON", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"},
		},
	})
	ch.Put(dp)
	return dp
}

// fireSmokeStatus fires a CCU event on a SMOKE_DETECTOR_ALARM_STATUS index
// sensor for the given VALUE_LIST label, mirroring how the resolver delivers
// a raw 0-based index for this read-only ENUM parameter.
func fireSmokeStatus(t *testing.T, dp *generic.Sensor[int32], label string) {
	t.Helper()
	idx, ok := custom.EnumLabelIndex(dp, label)
	if !ok {
		t.Fatalf("label %q not in VALUE_LIST", label)
	}
	dp.OnEvent(idx)
}

// newSmokeSirenWithDPs builds a SmokeSiren backed by real
// SMOKE_DETECTOR_ALARM_STATUS and SMOKE_DETECTOR_COMMAND DPs so that
// IsActive / IsStateChange read real observed values.
func newSmokeSirenWithDPs(t *testing.T) (*SmokeSiren, *generic.Sensor[int32]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SWSD001"})
	ch := d.AddChannel("SWSD001:1", 1, "SIREN", hmenum.ParamsetKeyValues)
	statusDP := attachSmokeStatusSensor(ch)
	attachStringSensor(ch, hmenum.ParameterSmokeDetectorCommand)
	ss := NewSmokeSiren(SmokeSirenConfig{Channel: ch})
	return ss, statusDP
}

// attachEnumSensor attaches a *generic.Sensor[int32] for the given
// read-only ENUM parameter to the channel, mirroring how the resolver
// projects such parameters (see custom.EnumSensorField) onto a raw-index
// sensor rather than a *generic.Sensor[string].
func attachEnumSensor(ch *device.Channel, param hmenum.Parameter, valueList []string) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	})
	ch.Put(dp)
	return dp
}

// fireDirection resolves label against dp's own VALUE_LIST and fires the
// resulting raw index as a wire event — mirrors how the resolver projects
// the read-only ENUM parameter DIRECTION onto an index-valued Sensor[int32].
func fireDirection(t *testing.T, dp *generic.Sensor[int32], label string) {
	t.Helper()
	idx, ok := custom.EnumLabelIndex(dp, label)
	if !ok {
		t.Fatalf("label %q not in VALUE_LIST", label)
	}
	dp.OnEvent(idx)
}

// newSoundPlayerWithDPs builds a SoundPlayer backed by a real DIRECTION DP
// so that IsPlaying / IsStateChange read real observed values.
func newSoundPlayerWithDPs(t *testing.T) (*SoundPlayer, *generic.Sensor[int32]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P001"})
	ch := d.AddChannel("MP3P001:2", 2, "SIREN", hmenum.ParamsetKeyValues)
	directionDP := attachEnumSensor(ch, hmenum.ParameterDirection, []string{"", "UP", "DOWN"})
	attachEnumSensor(ch, hmenum.ParameterSoundfile, []string{"SOUNDFILE_001"})
	sp := NewSoundPlayer(SoundPlayerConfig{Channel: ch})
	return sp, directionDP
}

// --- Siren.IsStateChange ---

// TestSirenIsStateChangeReturnsTrueWhenUnobserved verifies that
// IsStateChange returns true when no acoustic or optical DP has been
// observed yet (first command always goes through).
func TestSirenIsStateChangeReturnsTrueWhenUnobserved(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", nil, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	if !r.siren.IsStateChange(true) {
		t.Error("IsStateChange(true) with unobserved state = false, want true")
	}
	if !r.siren.IsStateChange(false) {
		t.Error("IsStateChange(false) with unobserved state = false, want true")
	}
}

// TestSirenIsStateChangeReturnsFalseWhenAlreadyActive verifies that
// IsStateChange(true) returns false when the siren is already active
// (acoustic=true observed).
func TestSirenIsStateChangeReturnsFalseWhenAlreadyActive(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", nil, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticActiveDP.OnEvent(true)
	if r.siren.IsStateChange(true) {
		t.Error("IsStateChange(true) when already active = true, want false")
	}
}

// TestSirenIsStateChangeReturnsTrueWhenTransitioningToActive verifies
// that IsStateChange(true) returns true when the siren is currently
// inactive (acoustic=false observed).
func TestSirenIsStateChangeReturnsTrueWhenTransitioningToActive(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", nil, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticActiveDP.OnEvent(false)
	if !r.siren.IsStateChange(true) {
		t.Error("IsStateChange(true) when inactive = false, want true")
	}
}

// --- SmokeSiren.IsStateChange ---

// TestSmokeSirenIsStateChangeReturnsTrueWhenUnobserved verifies that
// IsStateChange returns true when no SMOKE_DETECTOR_ALARM_STATUS has
// been observed (first command always goes through).
func TestSmokeSirenIsStateChangeReturnsTrueWhenUnobserved(t *testing.T) {
	t.Parallel()

	ss, _ := newSmokeSirenWithDPs(t)
	if !ss.IsStateChange(true) {
		t.Error("SmokeSiren.IsStateChange(true) unobserved = false, want true")
	}
	if !ss.IsStateChange(false) {
		t.Error("SmokeSiren.IsStateChange(false) unobserved = false, want true")
	}
}

// TestSmokeSirenIsStateChangeReturnsFalseWhenAlreadyActive verifies
// that IsStateChange(true) returns false when the siren is already in
// an active alarm state (status != IDLE_OFF).
func TestSmokeSirenIsStateChangeReturnsFalseWhenAlreadyActive(t *testing.T) {
	t.Parallel()

	ss, statusDP := newSmokeSirenWithDPs(t)
	fireSmokeStatus(t, statusDP, string(SmokeStatusPrimaryAlarm))
	if ss.IsStateChange(true) {
		t.Error("SmokeSiren.IsStateChange(true) when active = true, want false")
	}
}

// TestSmokeSirenIsStateChangeReturnsTrueWhenTransitioningToActive
// verifies that IsStateChange(true) is true when status is IDLE_OFF.
func TestSmokeSirenIsStateChangeReturnsTrueWhenTransitioningToActive(t *testing.T) {
	t.Parallel()

	ss, statusDP := newSmokeSirenWithDPs(t)
	fireSmokeStatus(t, statusDP, string(SmokeStatusIdleOff))
	if !ss.IsStateChange(true) {
		t.Error("SmokeSiren.IsStateChange(true) when idle = false, want true")
	}
}

// --- SoundPlayer.IsStateChange ---

// TestSoundPlayerIsStateChangeReturnsTrueWhenUnobserved verifies that
// IsStateChange returns true when DIRECTION has not been observed yet.
func TestSoundPlayerIsStateChangeReturnsTrueWhenUnobserved(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerWithDPs(t)
	if !sp.IsStateChange(true) {
		t.Error("SoundPlayer.IsStateChange(true) unobserved = false, want true")
	}
	if !sp.IsStateChange(false) {
		t.Error("SoundPlayer.IsStateChange(false) unobserved = false, want true")
	}
}

// TestSoundPlayerIsStateChangeReturnsFalseWhenAlreadyPlaying verifies
// that IsStateChange(true) is false when DIRECTION is "UP" (playing).
func TestSoundPlayerIsStateChangeReturnsFalseWhenAlreadyPlaying(t *testing.T) {
	t.Parallel()

	sp, directionDP := newSoundPlayerWithDPs(t)
	fireDirection(t, directionDP, "UP")
	if sp.IsStateChange(true) {
		t.Error("SoundPlayer.IsStateChange(true) when playing = true, want false")
	}
}

// TestSoundPlayerIsStateChangeReturnsTrueWhenTransitioningToPlaying
// verifies that IsStateChange(true) is true when DIRECTION is empty
// (no playback direction — stopped).
func TestSoundPlayerIsStateChangeReturnsTrueWhenTransitioningToPlaying(t *testing.T) {
	t.Parallel()

	sp, directionDP := newSoundPlayerWithDPs(t)
	fireDirection(t, directionDP, "")
	if !sp.IsStateChange(true) {
		t.Error("SoundPlayer.IsStateChange(true) when stopped = false, want true")
	}
}
