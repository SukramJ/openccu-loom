// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"

	contract "thingfabric.dev/go-fabric/contract"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestApparentTemperatureMatterClass — feels-like temperature →
// TemperatureMeasurement (0x0402).
func TestApparentTemperatureMatterClass(t *testing.T) {
	s := NewApparentTemperatureSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementTemperature {
		t.Fatalf("got %v, want MatterMeasurementTemperature", got)
	}
}

// TestDewPointMatterClass — dew-point → TemperatureMeasurement.
func TestDewPointMatterClass(t *testing.T) {
	s := NewDewPointSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementTemperature {
		t.Fatalf("got %v, want MatterMeasurementTemperature", got)
	}
}

// TestFrostPointMatterClass — frost-point → TemperatureMeasurement.
func TestFrostPointMatterClass(t *testing.T) {
	s := NewFrostPointSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementTemperature {
		t.Fatalf("got %v, want MatterMeasurementTemperature", got)
	}
}

// TestDewPointSpreadMatterClassNone — delta has no Matter cluster.
func TestDewPointSpreadMatterClassNone(t *testing.T) {
	s := NewDewPointSpreadSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("got %v, want MatterMeasurementNone (MQTT-only)", got)
	}
}

// TestEnthalpyMatterClassNone — J/kg has no Matter unit.
func TestEnthalpyMatterClassNone(t *testing.T) {
	s := NewEnthalpySensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("got %v, want MatterMeasurementNone", got)
	}
}

// TestVaporConcentrationMatterClassNone — absolute humidity has no
// Matter cluster.
func TestVaporConcentrationMatterClassNone(t *testing.T) {
	s := NewVaporConcentrationSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("got %v, want MatterMeasurementNone", got)
	}
}

// TestOperatingVoltageLevelMatterClassBattery — derived battery
// percentage → PowerSource (0x002F).
func TestOperatingVoltageLevelMatterClassBattery(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementBattery {
		t.Fatalf("got %v, want MatterMeasurementBattery", got)
	}
}

// TestDerivedBinaryIntrusionAlarmMapsToContact — IntrusionAlarm →
// BooleanState (0x0045).
func TestDerivedBinaryIntrusionAlarmMapsToContact(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterIntrusionAlarm, []string{"INTRUSION"}, []string{"OFF"})
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementContact {
		t.Fatalf("IntrusionAlarm: got %v, want MatterMeasurementContact", got)
	}
}

// TestDerivedBinaryWindowOpenMapsToContact — WindowOpen → BooleanState.
func TestDerivedBinaryWindowOpenMapsToContact(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, []string{"CLOSED"})
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementContact {
		t.Fatalf("WindowOpen: got %v, want MatterMeasurementContact", got)
	}
}

// TestDerivedBinarySmokeAlarmMatterClassNone — smoke is surfaced by
// siren.SmokeSiren via SmokeCOAlarm; the calculated derivation is
// redundant for Matter and stays MQTT-only.
func TestDerivedBinarySmokeAlarmMatterClassNone(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterSmokeAlarm, []string{"PRIMARY_ALARM"}, []string{"IDLE_OFF"})
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("SmokeAlarm: got %v, want MatterMeasurementNone (siren.SmokeSiren handles SmokeCOAlarm)", got)
	}
}

// TestOperatingVoltageLevelSensorStatesUnspecifiedBatteryReplaceability pins
// what the derived battery percentage tells the Matter node about its
// battery: the CCU does not report how it is replaced, so the source says
// it does not know. Without the statement the node serves the selection's
// zero value, a battery the user replaces.
func TestOperatingVoltageLevelSensorStatesUnspecifiedBatteryReplaceability(t *testing.T) {
	t.Parallel()
	var src any = NewOperatingVoltageLevelSensor()
	sel, ok := src.(contract.PowerSourceSelectionSource)
	if !ok {
		t.Fatal("OperatingVoltageLevelSensor states no PowerSourceSelection; the node serves a user-replaceable battery")
	}
	if got := sel.MatterPowerSourceSelection(); got.Wired || got.BatteryReplaceability != contract.BatteryReplaceabilityUnspecified {
		t.Errorf("selection = %+v, want a battery with BatteryReplaceabilityUnspecified", got)
	}
}
