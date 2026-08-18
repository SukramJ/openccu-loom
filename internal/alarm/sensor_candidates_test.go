// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSensorCandidateFor exercises sensorCandidateFor as a pure table
// test: the function classifies plain strings, so no central, device or
// channel model is needed.
func TestSensorCandidateFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		model       string
		channelType string
		parameter   hmenum.Parameter

		wantOK bool

		wantSensorType     hmenum.AlarmSensorType
		wantSecurityClass  hmenum.SecurityClass
		wantActiveValues   []string
		wantRecommended    bool
		wantDeprioritised  bool
		wantReasonNonEmpty bool
	}{
		{
			name:              "smoke detector calculated alarm",
			model:             "HmIP-SWSD",
			channelType:       "SMOKE_DETECTOR",
			parameter:         hmenum.ParameterSmokeAlarm,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeHazard,
			wantSecurityClass: hmenum.SecurityClassSmoke,
			wantRecommended:   true,
		},
		{
			// The raw enumeration status: its value list carries the
			// alarm system's own intrusion-siren command (INTRUSION_ALARM)
			// alongside the two real detections, so it is offered but
			// flagged as the deprioritised sibling of the calculated
			// SMOKE_ALARM above.
			name:               "smoke detector raw alarm status",
			model:              "HmIP-SWSD",
			channelType:        "SMOKE_DETECTOR",
			parameter:          hmenum.ParameterSmokeDetectorAlarmStatus,
			wantOK:             true,
			wantSensorType:     hmenum.AlarmSensorTypeHazard,
			wantSecurityClass:  hmenum.SecurityClassSmoke,
			wantActiveValues:   []string{"PRIMARY_ALARM", "SECONDARY_ALARM"},
			wantDeprioritised:  true,
			wantReasonNonEmpty: true,
		},
		{
			name:              "water detection transmitter alarm state",
			model:             "HmIP-SWD",
			channelType:       "WATER_DETECTION_TRANSMITTER",
			parameter:         hmenum.ParameterAlarmState,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeHazard,
			wantSecurityClass: hmenum.SecurityClassWater,
			wantRecommended:   true,
		},
		{
			name:              "water detection transmitter moisture detected",
			model:             "HmIP-SWD",
			channelType:       "WATER_DETECTION_TRANSMITTER",
			parameter:         hmenum.ParameterMoistureDetected,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeHazard,
			wantSecurityClass: hmenum.SecurityClassWater,
		},
		{
			name:              "water detection transmitter water level detected",
			model:             "HmIP-SWD",
			channelType:       "WATER_DETECTION_TRANSMITTER",
			parameter:         hmenum.ParameterWaterLevelDetected,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeHazard,
			wantSecurityClass: hmenum.SecurityClassWater,
		},
		{
			name:              "water detection sensor state enumeration",
			model:             "HM-Sec-WDS",
			channelType:       "WATERDETECTIONSENSOR",
			parameter:         hmenum.ParameterState,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeHazard,
			wantSecurityClass: hmenum.SecurityClassWater,
			wantActiveValues:  []string{"WET", "WATER"},
			wantRecommended:   true,
		},
		{
			name:              "shutter contact state is an intrusion candidate by channel type",
			channelType:       "SHUTTER_CONTACT",
			parameter:         hmenum.ParameterState,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeWindow,
			wantSecurityClass: hmenum.SecurityClassIntrusion,
			wantRecommended:   true,
		},
		{
			name:              "motion detector motion is an intrusion candidate by channel type",
			channelType:       "MOTIONDETECTOR_TRANSCEIVER",
			parameter:         hmenum.ParameterMotion,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeMotion,
			wantSecurityClass: hmenum.SecurityClassIntrusion,
			wantRecommended:   true,
		},
		{
			name:              "virtual motion detector motion is an intrusion candidate by channel type",
			channelType:       "MOTIONDETECTOR_VIRTUAL_TRANSCEIVER",
			parameter:         hmenum.ParameterMotion,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeMotion,
			wantSecurityClass: hmenum.SecurityClassIntrusion,
			wantRecommended:   true,
		},
		{
			// HmIP-SRH, the current-generation window-handle sensor: its
			// tri-state STATE (CLOSED/TILTED/OPEN) reaches the same
			// intrusion branch as HmIP-Wired's ROTARY_HANDLE_SENSOR.
			name:              "rotary handle transceiver state is an intrusion candidate by channel type",
			channelType:       "ROTARY_HANDLE_TRANSCEIVER",
			parameter:         hmenum.ParameterState,
			wantOK:            true,
			wantSensorType:    hmenum.AlarmSensorTypeWindow,
			wantSecurityClass: hmenum.SecurityClassIntrusion,
			wantRecommended:   true,
		},
		{
			// LOW_BAT classifies as a battery fault, but battery is not a
			// hazard class, and LOW_BAT is not one of the state/motion
			// parameters the intrusion-by-channel-type branch admits —
			// it must fall through to "not a candidate".
			name:        "non-state parameter on an intrusion channel type is not a candidate",
			channelType: "SHUTTER_CONTACT",
			parameter:   hmenum.ParameterLowBat,
			wantOK:      false,
		},
		{
			// Offering the parameters below would let the enrolment
			// picker read the alarm system's own output back as a
			// detection — the exclusion must win before any
			// classification table is even consulted.
			name:        "acoustic alarm active is excluded",
			model:       "HmIP-ASIR",
			channelType: "",
			parameter:   hmenum.ParameterAcousticAlarmActive,
			wantOK:      false,
		},
		{
			name:        "smoke detector command is excluded even under the smoke channel type",
			channelType: "SMOKE_DETECTOR",
			parameter:   hmenum.ParameterSmokeDetectorCommand,
			wantOK:      false,
		},
		{
			name:        "the calculated intrusion alarm is excluded",
			channelType: "",
			parameter:   "INTRUSION_ALARM",
			wantOK:      false,
		},
		{
			name:      "ordinary level parameter is not a candidate",
			parameter: "LEVEL",
			wantOK:    false,
		},
		{
			name:      "ordinary actual temperature parameter is not a candidate",
			parameter: "ACTUAL_TEMPERATURE",
			wantOK:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := sensorCandidateFor(tc.model, tc.channelType, tc.parameter)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (candidate=%+v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.SensorType != tc.wantSensorType {
				t.Errorf("SensorType = %q, want %q", got.SensorType, tc.wantSensorType)
			}
			if got.SecurityClass != tc.wantSecurityClass {
				t.Errorf("SecurityClass = %q, want %q", got.SecurityClass, tc.wantSecurityClass)
			}
			if !slices.Equal(got.ActiveValues, tc.wantActiveValues) {
				t.Errorf("ActiveValues = %v, want %v", got.ActiveValues, tc.wantActiveValues)
			}
			if got.Recommended != tc.wantRecommended {
				t.Errorf("Recommended = %v, want %v", got.Recommended, tc.wantRecommended)
			}
			if got.Deprioritised != tc.wantDeprioritised {
				t.Errorf("Deprioritised = %v, want %v", got.Deprioritised, tc.wantDeprioritised)
			}
			if tc.wantReasonNonEmpty && got.Reason == "" {
				t.Error("Reason is empty, want a non-empty explanation")
			}
			if !tc.wantReasonNonEmpty && got.Reason != "" {
				t.Errorf("Reason = %q, want empty", got.Reason)
			}
		})
	}
}
