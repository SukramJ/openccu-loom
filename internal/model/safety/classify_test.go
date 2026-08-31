// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package safety

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestClassifyByChannelAndParameter verifies every entry of the
// channel+parameter table resolves to the documented class, active-value
// list and preferred flag.
func TestClassifyByChannelAndParameter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		channelType   string
		parameter     hmenum.Parameter
		wantClass     hmenum.SecurityClass
		wantActive    []string
		wantPreferred bool
	}{
		{
			name:          "smoke detector calculated alarm",
			channelType:   "SMOKE_DETECTOR",
			parameter:     hmenum.ParameterSmokeAlarm,
			wantClass:     hmenum.SecurityClassSmoke,
			wantPreferred: true,
		},
		{
			name:        "smoke detector alarm status",
			channelType: "SMOKE_DETECTOR",
			parameter:   hmenum.ParameterSmokeDetectorAlarmStatus,
			wantClass:   hmenum.SecurityClassSmoke,
			wantActive:  []string{"PRIMARY_ALARM", "SECONDARY_ALARM"},
		},
		{
			name:          "smoke detector state",
			channelType:   "SMOKE_DETECTOR",
			parameter:     hmenum.ParameterState,
			wantClass:     hmenum.SecurityClassSmoke,
			wantPreferred: true,
		},
		{
			name:          "water detection transmitter alarm state",
			channelType:   "WATER_DETECTION_TRANSMITTER",
			parameter:     hmenum.ParameterAlarmState,
			wantClass:     hmenum.SecurityClassWater,
			wantPreferred: true,
		},
		{
			name:        "water detection transmitter moisture detected",
			channelType: "WATER_DETECTION_TRANSMITTER",
			parameter:   hmenum.ParameterMoistureDetected,
			wantClass:   hmenum.SecurityClassWater,
		},
		{
			name:        "water detection transmitter water level detected",
			channelType: "WATER_DETECTION_TRANSMITTER",
			parameter:   hmenum.ParameterWaterLevelDetected,
			wantClass:   hmenum.SecurityClassWater,
		},
		{
			name:          "water detection sensor state",
			channelType:   "WATERDETECTIONSENSOR",
			parameter:     hmenum.ParameterState,
			wantClass:     hmenum.SecurityClassWater,
			wantActive:    []string{"WET", "WATER"},
			wantPreferred: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Classify("", tc.channelType, tc.parameter)
			if !ok {
				t.Fatalf("Classify(%q, %q) ok=false, want true", tc.channelType, tc.parameter)
			}
			if got.Class != tc.wantClass {
				t.Errorf("Class = %q, want %q", got.Class, tc.wantClass)
			}
			if got.Preferred != tc.wantPreferred {
				t.Errorf("Preferred = %v, want %v", got.Preferred, tc.wantPreferred)
			}
			if !slices.Equal(got.ActiveValues, tc.wantActive) {
				t.Errorf("ActiveValues = %v, want %v", got.ActiveValues, tc.wantActive)
			}
		})
	}
}

// TestClassifySmokeAlarmStatusExcludesIntrusion is the actuator-feedback
// loop guard: SMOKE_DETECTOR_ALARM_STATUS's INTRUSION_ALARM value means the
// installation drove the detector as an intrusion siren, not that it sensed
// smoke. If INTRUSION_ALARM ever leaked into the smoke ActiveValues list (or
// were separately classified as smoke), the domain would report its own
// siren command as the cause of a fire.
func TestClassifySmokeAlarmStatusExcludesIntrusion(t *testing.T) {
	t.Parallel()

	got, ok := Classify("", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorAlarmStatus)
	if !ok {
		t.Fatalf("Classify(SMOKE_DETECTOR, SMOKE_DETECTOR_ALARM_STATUS) ok=false, want true")
	}
	if slices.Contains(got.ActiveValues, "INTRUSION_ALARM") {
		t.Errorf("ActiveValues = %v must not contain INTRUSION_ALARM (actuator-feedback loop guard)", got.ActiveValues)
	}

	// INTRUSION_ALARM itself is the alarm engine's own calculated verdict
	// (see excludedParameters); classifying it as a security signal would
	// let the engine's output feed back in as an input.
	if _, ok := Classify("", "SMOKE_DETECTOR", "INTRUSION_ALARM"); ok {
		t.Errorf("Classify(SMOKE_DETECTOR, INTRUSION_ALARM) ok=true, want false (actuator-feedback loop guard)")
	}
}

// TestClassifyByParameter verifies the device-independent parameter-only
// table: maintenance and fault signals that classify the same way
// regardless of channel type.
func TestClassifyByParameter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		parameter  hmenum.Parameter
		wantClass  hmenum.SecurityClass
		wantReason hmenum.SecurityFaultReason
	}{
		{"sabotage", hmenum.ParameterSabotage, hmenum.SecurityClassTamper, hmenum.SecurityFaultReasonTamper},
		{"sabotage acceleration", hmenum.ParameterSabotageAcceleration, hmenum.SecurityClassTamper, hmenum.SecurityFaultReasonTamper},
		{"sabotage battery", hmenum.ParameterSabotageBattery, hmenum.SecurityClassTamper, hmenum.SecurityFaultReasonTamper},
		{"sabotage magnetic field", hmenum.ParameterSabotageMagneticField, hmenum.SecurityClassTamper, hmenum.SecurityFaultReasonTamper},
		{"sabotage vertical", hmenum.ParameterSabotageVertical, hmenum.SecurityClassTamper, hmenum.SecurityFaultReasonTamper},

		{"low bat", hmenum.ParameterLowBat, hmenum.SecurityClassBattery, hmenum.SecurityFaultReasonLowBattery},
		{"lowbat alias", "LOWBAT", hmenum.SecurityClassBattery, hmenum.SecurityFaultReasonLowBattery},
		{"lowbat sensor alias", "LOWBAT_SENSOR", hmenum.SecurityClassBattery, hmenum.SecurityFaultReasonLowBattery},

		{"unreach", hmenum.ParameterUnreach, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonUnreachable},
		{"sticky unreach", hmenum.ParameterStickyUnreach, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonUnreachable},

		{"blocked temporary", hmenum.ParameterBlockedTemporary, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonBlocked},
		{"blocked permanent", hmenum.ParameterBlockedPermanent, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonBlocked},

		{"error smoke chamber", hmenum.ParameterErrorSmokeChamber, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonDeviceError},
		{"error alarm test", hmenum.ParameterErrorAlarmTest, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonDeviceError},
		{"error jammed", hmenum.ParameterErrorJammed, hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonDeviceError},

		{"dutycycle", "DUTYCYCLE", hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonDutyCycle},
		{"duty cycle", "DUTY_CYCLE", hmenum.SecurityClassTechnical, hmenum.SecurityFaultReasonDutyCycle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Classify("", "", tc.parameter)
			if !ok {
				t.Fatalf("Classify(%q) ok=false, want true", tc.parameter)
			}
			if got.Class != tc.wantClass {
				t.Errorf("Class = %q, want %q", got.Class, tc.wantClass)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestExcludedParametersNeverClassify verifies that every parameter on the
// actuator-feedback exclusion list is reported by Excluded and is refused by
// Classify, even for a channel+parameter pair that would otherwise resolve
// through the channel table — the exclusion check runs first.
func TestExcludedParametersNeverClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		channelType string
		parameter   hmenum.Parameter
	}{
		{"acoustic alarm active", "", hmenum.ParameterAcousticAlarmActive},
		{"acoustic alarm selection", "", hmenum.ParameterAcousticAlarmSelection},
		{"optical alarm active", "", "OPTICAL_ALARM_ACTIVE"},
		{"optical alarm selection", "", "OPTICAL_ALARM_SELECTION"},
		{"emergency operation", "", "EMERGENCY_OPERATION"},
		{"intrusion alarm", "", "INTRUSION_ALARM"},
		// Smoke-detector command on the very channel type that owns the
		// smoke ActiveValues table: the exclusion must win even though a
		// channel+parameter row would otherwise be consulted.
		{"smoke detector command under smoke channel", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorCommand},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !Excluded(tc.parameter) {
				t.Errorf("Excluded(%q) = false, want true", tc.parameter)
			}
			if _, ok := Classify("", tc.channelType, tc.parameter); ok {
				t.Errorf("Classify(%q, %q) ok=true, want false", tc.channelType, tc.parameter)
			}
		})
	}
}

// TestClassifyUnclassifiedParameters verifies that ordinary, non-security
// parameters resolve to ok=false and a zero Classification.
func TestClassifyUnclassifiedParameters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		channelType string
		parameter   hmenum.Parameter
	}{
		{"level", "", "LEVEL"},
		{"actual temperature", "", "ACTUAL_TEMPERATURE"},
		{"empty parameter", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Classify("", tc.channelType, tc.parameter)
			if ok {
				t.Errorf("Classify(%q, %q) ok=true, want false", tc.channelType, tc.parameter)
			}
			if got.Class != "" || got.Reason != "" || got.ActiveValues != nil || got.Preferred {
				t.Errorf("Classify(%q, %q) = %+v, want zero Classification", tc.channelType, tc.parameter, got)
			}
		})
	}
}

// TestClassifyRainIsNotWater verifies that rain sensors do not classify as
// the water security class: precipitation is weather, not a leak
// (notes/concepts/security-safety-concept.md §6.1), so RAINDETECTOR / RAIN_DETECTION_
// TRANSMITTER data points must fall through unclassified rather than
// triggering the water fault plane.
func TestClassifyRainIsNotWater(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		channelType string
		parameter   hmenum.Parameter
	}{
		{"raindetector state", "RAINDETECTOR", hmenum.ParameterState},
		{"rain detection transmitter raining", "RAIN_DETECTION_TRANSMITTER", hmenum.ParameterRaining},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := Classify("", tc.channelType, tc.parameter); ok {
				t.Errorf("Classify(%q, %q) ok=true, want false (rain is weather, not a leak)", tc.channelType, tc.parameter)
			}
		})
	}
}
