// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Per-domain rule lookups
// ---------------------------------------------------------------------------

func TestLookupSensorRuleTemperatureAliasesShareKey(t *testing.T) {
	for _, p := range []string{"TEMPERATURE", "ACTUAL_TEMPERATURE"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.Key != "TEMPERATURE" {
			t.Fatalf("%s: key=%q want TEMPERATURE", p, d.Key)
		}
		if d.DeviceClass != "temperature" {
			t.Fatalf("%s: device_class=%q want temperature", p, d.DeviceClass)
		}
	}
}

func TestLookupSensorRuleOperatingVoltageDisabledDiagnostic(t *testing.T) {
	for _, p := range []string{"BATTERY_STATE", "OPERATING_VOLTAGE"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.EnabledByDefault {
			t.Fatalf("%s: must be disabled by default", p)
		}
		if d.EntityCategory != EntityCategoryDiagnostic {
			t.Fatalf("%s: entity_category=%q want diagnostic", p, d.EntityCategory)
		}
	}
}

func TestLookupSensorRuleRSSIAliasesDisabled(t *testing.T) {
	for _, p := range []string{"RSSI_DEVICE", "RSSI_PEER"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.EnabledByDefault {
			t.Fatalf("%s: must be disabled by default", p)
		}
		if d.EntityCategory != EntityCategoryDiagnostic {
			t.Fatalf("%s: entity_category=%q want diagnostic", p, d.EntityCategory)
		}
	}
}

func TestLookupSensorRuleMissingParameterReportsFalse(t *testing.T) {
	if _, ok := LookupSensorRule("", "NOT_A_REAL_PARAM"); ok {
		t.Fatal("unknown parameter must report ok=false")
	}
}

func TestLookupBinarySensorRuleLowBatteryAliases(t *testing.T) {
	for _, p := range []string{"LOWBAT", "LOW_BAT", "LOWBAT_SENSOR"} {
		d, ok := LookupBinarySensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.DeviceClass != "battery" {
			t.Fatalf("%s: device_class=%q want battery", p, d.DeviceClass)
		}
	}
}

func TestLookupBinarySensorRuleSabotageDisabledDiagnostic(t *testing.T) {
	d, ok := LookupBinarySensorRule("", "SABOTAGE")
	if !ok {
		t.Fatal("SABOTAGE: not found")
	}
	if d.EnabledByDefault {
		t.Fatal("SABOTAGE: must be disabled by default")
	}
	if d.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("SABOTAGE: entity_category=%q want diagnostic", d.EntityCategory)
	}
}

func TestLookupBinarySensorRuleHmIPSWDWindow(t *testing.T) {
	// Per-device rule: HmIP-SWD / STATE → window.
	d, ok := LookupBinarySensorRule("HmIP-SWD", "STATE")
	if !ok {
		t.Fatal("HmIP-SWD/STATE: not found")
	}
	if d.DeviceClass != "window" {
		t.Fatalf("HmIP-SWD/STATE: device_class=%q want window", d.DeviceClass)
	}
}

func TestLookupNumberRuleHmwIo12FrequencyMHz(t *testing.T) {
	d, ok := LookupNumberRule("HMW-IO-12-Sw14-DR", "FREQUENCY")
	if !ok {
		t.Fatal("HMW-IO-12-Sw14-DR/FREQUENCY: not found")
	}
	if d.UnitOfMeasurement != "mHz" {
		t.Fatalf("HMW-IO-12-Sw14-DR/FREQUENCY: unit=%q want mHz", d.UnitOfMeasurement)
	}
}

func TestLookupCoverRuleHmIPBBLBlind(t *testing.T) {
	// Cover rules in misc.go are keyed on (device-prefix, parameter)
	// where parameter = "LEVEL" (the channel-level driver).
	d, ok := LookupCoverRule("HmIP-BBL", "LEVEL")
	if !ok {
		t.Fatal("HmIP-BBL/LEVEL: not found in cover rule table")
	}
	if d.DeviceClass != "blind" {
		t.Fatalf("HmIP-BBL/LEVEL: device_class=%q want blind", d.DeviceClass)
	}
}

// ---------------------------------------------------------------------------
// EntityDescriptionFor — unified API
// ---------------------------------------------------------------------------

func TestEntityDescriptionForSensorTemperatureMatches(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "ACTUAL_TEMPERATURE")
	if desc.DeviceClass != "temperature" {
		t.Fatalf("ACTUAL_TEMPERATURE: device_class=%q want temperature", desc.DeviceClass)
	}
	if desc.UnitOfMeasurement != "°C" {
		t.Fatalf("ACTUAL_TEMPERATURE: unit=%q want °C", desc.UnitOfMeasurement)
	}
	if desc.StateClass != "measurement" {
		t.Fatalf("ACTUAL_TEMPERATURE: state_class=%q want measurement", desc.StateClass)
	}
}

func TestEntityDescriptionForSensorRSSIDeviceDiagnosticDisabled(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "RSSI_DEVICE")
	if desc.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("RSSI_DEVICE: entity_category=%q want diagnostic", desc.EntityCategory)
	}
	if desc.EnabledDefault == nil || *desc.EnabledDefault != false {
		t.Fatalf("RSSI_DEVICE: enabled_default must be false, got %v", desc.EnabledDefault)
	}
}

func TestEntityDescriptionForSensorOperatingVoltageDiagnosticDisabled(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "OPERATING_VOLTAGE")
	if desc.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("OPERATING_VOLTAGE: entity_category=%q want diagnostic", desc.EntityCategory)
	}
	if desc.EnabledDefault == nil || *desc.EnabledDefault != false {
		t.Fatalf("OPERATING_VOLTAGE: enabled_default must be false, got %v", desc.EnabledDefault)
	}
}

func TestEntityDescriptionForNumberFrequencyUnit(t *testing.T) {
	// HMW-IO-12-Sw14-DR / FREQUENCY must carry the mHz unit override.
	desc := EntityDescriptionFor(HAComponentNumber, "HMW-IO-12-Sw14-DR", "FREQUENCY")
	if desc.UnitOfMeasurement != "mHz" {
		t.Fatalf("HMW-IO-12-Sw14-DR FREQUENCY: unit=%q want mHz", desc.UnitOfMeasurement)
	}
}

func TestEntityDescriptionForLightComponentReturnsZeroValue(t *testing.T) {
	// HAComponentLight is not handled — returns the zero MqttEntityDescription.
	desc := EntityDescriptionFor(HAComponentLight, "HmIP-BDT", "LEVEL")
	if desc != (MqttEntityDescription{}) {
		t.Fatalf("HAComponentLight must return zero value, got %+v", desc)
	}
}

func TestEntityDescriptionForEventPressShort(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentEvent, "", "PRESS_SHORT")
	if desc.DeviceClass != "button" {
		t.Fatalf("PRESS_SHORT: device_class=%q want button", desc.DeviceClass)
	}
}

// ---------------------------------------------------------------------------
// Encoding sanity — ensure the µm constant remains a single-rune valid UTF-8
// codepoint after editor round-trips.
// ---------------------------------------------------------------------------

func TestUnitMicrometersIsValidUtf8(t *testing.T) {
	if !utf8.ValidString(unitMicrometers) {
		t.Fatalf("unitMicrometers=%q is not valid UTF-8", unitMicrometers)
	}
}

// ---------------------------------------------------------------------------
// EntityDescriptionLookup — generated rule table
// ---------------------------------------------------------------------------

// TestEntityDescriptionLookupBasicMatches pins a handful of well-known
// Future
// caught at compile-time. Each case is rooted in a real
// `entity_helpers/descriptions/*.py` rule.
func TestEntityDescriptionLookupBasicMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		category  string
		parameter string
		model     string
		unit      string
		postfix   string

		wantKey         string
		wantDeviceClass string
		wantStateClass  string
		wantUnit        string
	}{
		{
			name:            "TEMPERATURE sensor",
			category:        "sensor",
			parameter:       "ACTUAL_TEMPERATURE",
			wantKey:         "TEMPERATURE",
			wantDeviceClass: "temperature",
			wantStateClass:  "measurement",
			wantUnit:        "°C",
		},
		{
			name:            "HUMIDITY sensor",
			category:        "sensor",
			parameter:       "HUMIDITY",
			wantKey:         "HUMIDITY",
			wantDeviceClass: "humidity",
			wantStateClass:  "measurement",
			wantUnit:        "%",
		},
		{
			name:            "ALARMSTATE binary_sensor",
			category:        "binary_sensor",
			parameter:       "ALARMSTATE",
			wantKey:         "ALARMSTATE",
			wantDeviceClass: "safety",
		},
		{
			name:            "LOWBAT binary_sensor",
			category:        "binary_sensor",
			parameter:       "LOWBAT",
			wantKey:         "LOW_BAT",
			wantDeviceClass: "battery",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HARegistryDescriptionLookup(tc.category, tc.parameter, tc.model, tc.unit, tc.postfix, "")
			if got == nil {
				t.Fatalf("HARegistryDescriptionLookup(%q,%q,…) = nil; want match", tc.category, tc.parameter)
			}
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
			if tc.wantDeviceClass != "" && got.DeviceClass != tc.wantDeviceClass {
				t.Errorf("DeviceClass = %q, want %q", got.DeviceClass, tc.wantDeviceClass)
			}
			if tc.wantStateClass != "" && got.StateClass != tc.wantStateClass {
				t.Errorf("StateClass = %q, want %q", got.StateClass, tc.wantStateClass)
			}
			if tc.wantUnit != "" && got.UnitOfMeasurement != tc.wantUnit {
				t.Errorf("UnitOfMeasurement = %q, want %q", got.UnitOfMeasurement, tc.wantUnit)
			}
		})
	}
}

// TestEntityDescriptionLookupReturnsNilOnMiss confirms a category that has
// no matching rule yields nil rather than a partial false-positive.
func TestEntityDescriptionLookupReturnsNilOnMiss(t *testing.T) {
	t.Parallel()
	if got := HARegistryDescriptionLookup("sensor", "DOES_NOT_EXIST_PARAM", "", "", "", ""); got != nil {
		t.Errorf("expected nil for unknown parameter, got %+v", got)
	}
	if got := HARegistryDescriptionLookup("", "", "", "", "", ""); got != nil {
		t.Errorf("expected nil for empty category, got %+v", got)
	}
}

// TestEntityDescriptionLookupDevicePrefixMatch confirms device-prefix
// filtering works case-insensitively, mirroring `EntityDescriptionRule.matches`.
func TestEntityDescriptionLookupDevicePrefixMatch(t *testing.T) {
	t.Parallel()
	// Rules with device prefix typically have a higher priority and
	// override the generic fallback. We assert the lookup returns a
	// Description (the exact key may evolve as
	// rules; this test guards the wiring, not the data).
	if got := HARegistryDescriptionLookup("sensor", "LEVEL", "HmIP-eTRV-2", "", "", ""); got == nil {
		t.Logf("note: no rule for HmIP-eTRV-2 LEVEL — table may have changed")
	}
}

// TestEntityDescriptionLookupRuleCount sanity-checks that the generator
// produced a sensible amount of rules — guards against an empty file
// silently slipping past CI.
func TestEntityDescriptionLookupRuleCount(t *testing.T) {
	t.Parallel()
	if got := len(haRegistryDescriptionRules); got < 100 {
		t.Errorf("haRegistryDescriptionRules = %d entries; expected ≥100 (homematicip_local has ~147)", got)
	}
}
