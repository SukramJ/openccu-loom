// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file pins internal/model/safety.Classify against a curated
// cross-section of the real Homematic/HmIP device fleet.
//
// No embedded or generated data source in the daemon enumerates full
// (model, channel type, parameter) triples for the security-relevant
// devices: the ccudata easymode archive is keyed by channel type but
// only carries MASTER-paramset configuration schema (option groups,
// cross-validation), not the VALUES-paramset runtime parameters the
// classifier operates on, and its sender-type dimension is the
// generic "_MASTER" placeholder rather than a real model. The
// generated HA entity-description rules key on (category, model,
// parameter) with no channel-type dimension at all, and the table is
// an unexported, not-yet-wired reference table. The custom-profile
// registry never assigns a channel type to a security sensor —
// smoke/water/tamper channels have no custom data-point wrapper. The
// only artefact that does carry the full triple is the model
// snapshot (tests/integration/testdata/model_snapshot_openccu-loom.json),
// which is gitignored and produced on demand, so a contract test
// cannot depend on its presence. The fleet table below is therefore
// hand-curated from the real device models and channel types already
// documented on the classifier itself and in
// notes/parity/by_design.md.
var safetyFleet = []struct {
	model          string
	channelType    string
	parameter      hmenum.Parameter
	wantClassified bool
}{
	// HmIP-SWD — water sensor, three independent boolean sources on
	// WATER_DETECTION_TRANSMITTER.
	{"HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterAlarmState, true},
	{"HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterMoistureDetected, true},
	{"HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterWaterLevelDetected, true},

	// HM-Sec-WDS — three-step ENUM water sensor.
	{"HM-Sec-WDS", "WATERDETECTIONSENSOR", hmenum.ParameterState, true},

	// HM-Sec-SD-2 — battery smoke detector, raw STATE.
	{"HM-Sec-SD-2", "SMOKE_DETECTOR", hmenum.ParameterState, true},

	// HmIP-SWSD — smoke/intrusion combi detector: the calculated
	// alarm, the raw status ENUM it derives from, and the write-only
	// command that must stay excluded even though it shares the
	// classified channel type.
	{"HmIP-SWSD", "SMOKE_DETECTOR", hmenum.ParameterSmokeAlarm, true},
	{"HmIP-SWSD", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorAlarmStatus, true},
	{"HmIP-SWSD", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorCommand, false},

	// HmIP-ASIR — siren: acoustic/optical activity and the engine's
	// own calculated intrusion verdict are outputs, never detections.
	{"HmIP-ASIR", "", hmenum.ParameterAcousticAlarmActive, false},
	{"HmIP-ASIR", "", hmenum.ParameterAcousticAlarmSelection, false},
	{"HmIP-ASIR", "", "OPTICAL_ALARM_ACTIVE", false},
	{"HmIP-ASIR", "", "OPTICAL_ALARM_SELECTION", false},
	{"", "", "EMERGENCY_OPERATION", false},
	{"", "", "INTRUSION_ALARM", false},

	// Device-independent maintenance/fault signals — real across the
	// whole fleet, not tied to a single model.
	{"HmIP-BROLL", "", hmenum.ParameterLowBat, true},
	{"HmIP-BROLL", "", hmenum.ParameterUnreach, true},
	{"HmIP-BROLL", "", hmenum.ParameterStickyUnreach, true},
	{"HmIP-BROLL", "", hmenum.ParameterBlockedTemporary, true},
	{"HmIP-BROLL", "", hmenum.ParameterBlockedPermanent, true},
	{"HM-Sec-Key", "", hmenum.ParameterSabotage, true},
	{"HM-Sec-Key", "", hmenum.ParameterSabotageBattery, true},
	{"HM-Sec-SD-2", "", hmenum.ParameterErrorSmokeChamber, true},
	{"HM-Sec-SD-2", "", hmenum.ParameterErrorAlarmTest, true},
	{"HmIP-RCV-50", "", "DUTY_CYCLE", true},
	{"HmIP-RCV-50", "", "DUTYCYCLE", true},

	// Ordinary, non-security parameters must fall through unclassified.
	{"HmIP-BROLL", "", "LEVEL", false},
	{"HmIP-STH", "", "ACTUAL_TEMPERATURE", false},

	// Rain is weather, not a leak (notes/concepts/security-safety-concept.md
	// §6.1) — both real rain-sensor channel types must stay unclassified.
	{"HM-Sen-RD-O", "RAINDETECTOR", hmenum.ParameterState, false},
	{"HM-Sen-RD-O", "RAIN_DETECTION_TRANSMITTER", hmenum.ParameterRaining, false},
}

// TestSafetyExclusionListIsNonEmpty guards against an accidentally
// emptied exclusion table: every one of these parameters is a known
// piece of actuator feedback, so if the table were ever emptied all
// of these would flip to false and this test would catch it — the
// other tests in this file would otherwise pass vacuously.
func TestSafetyExclusionListIsNonEmpty(t *testing.T) {
	t.Parallel()

	knownExcluded := []hmenum.Parameter{
		hmenum.ParameterAcousticAlarmActive,
		hmenum.ParameterAcousticAlarmSelection,
		"OPTICAL_ALARM_ACTIVE",
		"OPTICAL_ALARM_SELECTION",
		hmenum.ParameterSmokeDetectorCommand,
		"EMERGENCY_OPERATION",
		"INTRUSION_ALARM",
	}
	for _, p := range knownExcluded {
		if !safety.Excluded(p) {
			t.Errorf("Excluded(%q) = false, want true", p)
		}
	}
	if safety.Excluded("LEVEL") {
		t.Error("Excluded(LEVEL) = true, want false — an ordinary parameter must not be on the exclusion list")
	}
}

// TestSafetyFleetExcludedParametersNeverClassify walks the fleet
// table and verifies that every parameter safety.Excluded reports as
// actuator feedback refuses to classify, on every channel type it is
// paired with — including a channel type that would otherwise resolve
// through the classified-channel table.
func TestSafetyFleetExcludedParametersNeverClassify(t *testing.T) {
	t.Parallel()

	excludedSeen := 0
	for _, tc := range safetyFleet {
		if !safety.Excluded(tc.parameter) {
			continue
		}
		excludedSeen++
		if _, ok := safety.Classify(tc.model, tc.channelType, tc.parameter); ok {
			t.Errorf("Classify(%q, %q, %q) ok=true, want false (actuator feedback)", tc.model, tc.channelType, tc.parameter)
		}
	}
	if excludedSeen == 0 {
		t.Fatal("fleet table carries no excluded parameter — the exclusion guard above is untested")
	}
}

// TestSafetyFleetClassifiedTriplesAreValidTaxonomy walks the fleet
// table and verifies every triple the classifier accepts yields a
// value from the defined taxonomy: a valid SecurityClass, a valid
// SecurityFaultReason whenever one is set, and a valid severity
// projection — so a future taxonomy addition can never ship without
// updating Valid()/SeverityForClass() to match.
func TestSafetyFleetClassifiedTriplesAreValidTaxonomy(t *testing.T) {
	t.Parallel()

	classified := 0
	for _, tc := range safetyFleet {
		got, ok := safety.Classify(tc.model, tc.channelType, tc.parameter)
		if !ok {
			continue
		}
		classified++
		if !got.Class.Valid() {
			t.Errorf("Classify(%q, %q, %q).Class = %q, not a valid SecurityClass", tc.model, tc.channelType, tc.parameter, got.Class)
		}
		if got.Reason != "" && !got.Reason.Valid() {
			t.Errorf("Classify(%q, %q, %q).Reason = %q, not a valid SecurityFaultReason", tc.model, tc.channelType, tc.parameter, got.Reason)
		}
		if sev := hmenum.SeverityForClass(got.Class); !sev.Valid() {
			t.Errorf("SeverityForClass(%q) = %q, not a valid SecuritySeverity", got.Class, sev)
		}
	}
	if classified == 0 {
		t.Fatal("fleet table carries no classified triple — the validity guard above is untested")
	}
}

// TestSafetyFleetMatchesExpectedClassification cross-checks the fleet
// table's own expectation against the classifier, catching a
// regression where a known real data point silently stops (or
// starts) carrying security meaning.
func TestSafetyFleetMatchesExpectedClassification(t *testing.T) {
	t.Parallel()

	for _, tc := range safetyFleet {
		_, ok := safety.Classify(tc.model, tc.channelType, tc.parameter)
		if ok != tc.wantClassified {
			t.Errorf("Classify(%q, %q, %q) ok=%v, want %v", tc.model, tc.channelType, tc.parameter, ok, tc.wantClassified)
		}
	}
}

// TestSafetyActiveValuesArePinnedValueLists pins the two ENUM value
// lists no embedded data source in the daemon exposes:
// SMOKE_DETECTOR_ALARM_STATUS on the SMOKE_DETECTOR channel type, and
// STATE on WATERDETECTIONSENSOR. The classifier's ActiveValues must
// be a proper, non-empty subset of the real value list that excludes
// the label meaning "this device was driven as an output", not a
// detection.
func TestSafetyActiveValuesArePinnedValueLists(t *testing.T) {
	t.Parallel()

	t.Run("smoke detector alarm status", func(t *testing.T) {
		t.Parallel()

		valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}
		got, ok := safety.Classify("", "SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorAlarmStatus)
		if !ok {
			t.Fatal("Classify(SMOKE_DETECTOR, SMOKE_DETECTOR_ALARM_STATUS) ok=false, want true")
		}
		for _, v := range got.ActiveValues {
			if !slices.Contains(valueList, v) {
				t.Errorf("ActiveValues contains %q, not a member of the value list %v", v, valueList)
			}
		}
		if slices.Contains(got.ActiveValues, "INTRUSION_ALARM") {
			t.Error("ActiveValues must exclude INTRUSION_ALARM — it is the siren command, not a smoke detection")
		}
		if len(got.ActiveValues) == 0 || len(got.ActiveValues) >= len(valueList) {
			t.Errorf("ActiveValues = %v, want a proper non-empty subset of %v", got.ActiveValues, valueList)
		}
	})

	t.Run("water detection sensor state", func(t *testing.T) {
		t.Parallel()

		valueList := []string{"DRY", "WET", "WATER"}
		got, ok := safety.Classify("", "WATERDETECTIONSENSOR", hmenum.ParameterState)
		if !ok {
			t.Fatal("Classify(WATERDETECTIONSENSOR, STATE) ok=false, want true")
		}
		for _, v := range got.ActiveValues {
			if !slices.Contains(valueList, v) {
				t.Errorf("ActiveValues contains %q, not a member of the value list %v", v, valueList)
			}
		}
		if slices.Contains(got.ActiveValues, "DRY") {
			t.Error("ActiveValues must exclude DRY — dry is the inactive state, not a leak")
		}
		if len(got.ActiveValues) == 0 || len(got.ActiveValues) >= len(valueList) {
			t.Errorf("ActiveValues = %v, want a proper non-empty subset of %v", got.ActiveValues, valueList)
		}
	})
}

// TestSafetyCoverageFloorKnownDevices pins the classification of the
// specific real devices the Security & Safety domain must recognize.
// A table regression must fail here even if the fleet cross-section
// above shifted for an unrelated reason.
func TestSafetyCoverageFloorKnownDevices(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		model       string
		channelType string
		parameter   hmenum.Parameter
		wantClass   hmenum.SecurityClass
	}{
		{"HmIP-SWD alarm state", "HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterAlarmState, hmenum.SecurityClassWater},
		{"HmIP-SWD moisture detected", "HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterMoistureDetected, hmenum.SecurityClassWater},
		{"HmIP-SWD water level detected", "HmIP-SWD", "WATER_DETECTION_TRANSMITTER", hmenum.ParameterWaterLevelDetected, hmenum.SecurityClassWater},
		{"HM-Sec-WDS state", "HM-Sec-WDS", "WATERDETECTIONSENSOR", hmenum.ParameterState, hmenum.SecurityClassWater},
		{"HM-Sec-SD-2 state", "HM-Sec-SD-2", "SMOKE_DETECTOR", hmenum.ParameterState, hmenum.SecurityClassSmoke},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := safety.Classify(tc.model, tc.channelType, tc.parameter)
			if !ok {
				t.Fatalf("Classify(%q, %q, %q) ok=false, want true", tc.model, tc.channelType, tc.parameter)
			}
			if got.Class != tc.wantClass {
				t.Errorf("Class = %q, want %q", got.Class, tc.wantClass)
			}
		})
	}
}
