// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package calculated tests Translation() promotion from generic.Sensor[float64]
// and OperatingVoltageLevelSensor.AdditionalInformation().
package calculated

import (
	"fmt"
	"testing"
)

// ─── Translation() promoted from generic.Sensor ──────────────────────────────

// TestCalcSensorTranslationPromoted verifies that Translation() is
// accessible on every calculated sensor struct via promotion from the
// Embedded *generic.Sensor[float64]. This mirrors
// CalculatedDataPoint.translation DelegatedProperty
// (calculated/data_point.py:131) which delegates to
// ccu_translations.get_parameter_translation.
//
// In Go, Translation() is supplied by generic.DataPoint[float64].Spec.
// Calculated sensors are constructed with no CCU translation catalogue
// entry, so the value is always "" — the important invariant is that the
// method exists and does not panic.
func TestCalcSensorTranslationPromoted(t *testing.T) {
	t.Parallel()

	sensors := []struct {
		name  string
		trans func() string
	}{
		{"DewPointSensor", NewDewPointSensor().Translation},
		{"DewPointSpreadSensor", NewDewPointSpreadSensor().Translation},
		{"FrostPointSensor", NewFrostPointSensor().Translation},
		{"VaporConcentrationSensor", NewVaporConcentrationSensor().Translation},
		{"EnthalpySensor", NewEnthalpySensor().Translation},
		{"ApparentTemperatureSensor", NewApparentTemperatureSensor().Translation},
		{"OperatingVoltageLevelSensor", NewOperatingVoltageLevelSensor().Translation},
	}

	for _, tc := range sensors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.trans()
			// Calculated sensors have no CCU translation catalogue entry;
			// the method must return "" (not panic).
			if got != "" {
				t.Errorf("%s: Translation()=%q, want empty (no catalogue entry)", tc.name, got)
			}
		})
	}
}

// TestCalcSensorTranslationIndependentOfTranslationKey verifies that
// Translation() and TranslationKey() are independent. TranslationKey
// returns the HA-compatible lowercase slug; Translation returns the
// CCU-catalogue human label (empty for calculated sensors).
func TestCalcSensorTranslationIndependentOfTranslationKey(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensor()
	key := s.TranslationKey()
	trans := s.Translation()
	if key == "" {
		t.Error("TranslationKey() must not be empty for DewPointSensor")
	}
	if trans != "" {
		t.Errorf("Translation() must be empty for a calculated sensor, got %q", trans)
	}
	if key == trans {
		t.Error("TranslationKey() and Translation() must not coincide for calculated sensors")
	}
}

// ─── OperatingVoltageLevelSensor.AdditionalInformation ───────────────────────

// TestAdditionalInformationNilWithNoBattery verifies that
// AdditionalInformation returns nil when no battery config has been
// resolved (i.e. the sensor has not been subscribed against a channel
// with a known model string). Mirrors Python's `if self._battery_data
// is not None` guard (operating_voltage_level.py:106).
func TestAdditionalInformationNilWithNoBattery(t *testing.T) {
	t.Parallel()
	s := NewOperatingVoltageLevelSensor()
	if got := s.AdditionalInformation(); got != nil {
		t.Fatalf("AdditionalInformation() must return nil when no battery config is resolved, got %v", got)
	}
}

// TestAdditionalInformationMapKeysAndTypes verifies that
// AdditionalInformation returns the expected map keys with the correct
// types once a battery config is wired. Key names exactly match
// Py:22–26)
//
//	"Battery Qty" → int
//	"Battery Type" → string
//	"Low Battery Limit" → string "<V>V"
//	"Low Battery Limit Default" → string "<V>V"
//	"Voltage max" → string "<V>V"
func TestAdditionalInformationMapKeysAndTypes(t *testing.T) {
	t.Parallel()
	s := NewOperatingVoltageLevelSensor()

	// Inject a battery config as Subscribe would.
	cfg := BatteryConfig{Battery: BatteryTypeAA, Quantity: 2}
	s.battery = &cfg
	s.lowBatLimit = 2.2
	s.lowBatLimitDefault = 2.1
	s.voltageMax = 3.0

	ai := s.AdditionalInformation()
	if ai == nil {
		t.Fatal("AdditionalInformation() must not be nil with battery config set")
	}

	// Verify expected keys exist and have correct types.
	if qty, ok := ai["Battery Qty"]; !ok {
		t.Error("missing key 'Battery Qty'")
	} else if _, ok := qty.(int); !ok {
		t.Errorf("'Battery Qty' must be int, got %T", qty)
	} else if qty.(int) != 2 {
		t.Errorf("'Battery Qty'=%v, want 2", qty)
	}

	if bt, ok := ai["Battery Type"]; !ok {
		t.Error("missing key 'Battery Type'")
	} else if _, ok := bt.(string); !ok {
		t.Errorf("'Battery Type' must be string, got %T", bt)
	} else if bt.(string) != "AA" {
		t.Errorf("'Battery Type'=%v, want AA", bt)
	}

	if lbl, ok := ai["Low Battery Limit"]; !ok {
		t.Error("missing key 'Low Battery Limit'")
	} else if _, ok := lbl.(string); !ok {
		t.Errorf("'Low Battery Limit' must be string, got %T", lbl)
	} else if want := fmt.Sprintf("%gV", 2.2); lbl.(string) != want {
		t.Errorf("'Low Battery Limit'=%v, want %s", lbl, want)
	}

	if lbld, ok := ai["Low Battery Limit Default"]; !ok {
		t.Error("missing key 'Low Battery Limit Default'")
	} else if _, ok := lbld.(string); !ok {
		t.Errorf("'Low Battery Limit Default' must be string, got %T", lbld)
	} else if want := fmt.Sprintf("%gV", 2.1); lbld.(string) != want {
		t.Errorf("'Low Battery Limit Default'=%v, want %s", lbld, want)
	}

	if vmax, ok := ai["Voltage max"]; !ok {
		t.Error("missing key 'Voltage max'")
	} else if _, ok := vmax.(string); !ok {
		t.Errorf("'Voltage max' must be string, got %T", vmax)
	} else if want := fmt.Sprintf("%gV", 3.0); vmax.(string) != want {
		t.Errorf("'Voltage max'=%v, want %s", vmax, want)
	}
}

// TestAdditionalInformationMapHasExactlyFiveKeys verifies that the map
// contains exactly the five expected keys — no extras, no fewer.
func TestAdditionalInformationMapHasExactlyFiveKeys(t *testing.T) {
	t.Parallel()
	s := NewOperatingVoltageLevelSensor()
	cfg := BatteryConfig{Battery: BatteryTypeCR2032, Quantity: 1}
	s.battery = &cfg
	s.lowBatLimit = 2.0
	s.lowBatLimitDefault = 2.0
	s.voltageMax = 3.0

	ai := s.AdditionalInformation()
	if len(ai) != 5 {
		t.Errorf("AdditionalInformation() len=%d, want 5; got %v", len(ai), ai)
	}
}
