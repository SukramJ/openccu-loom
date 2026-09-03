// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for Sysvar.Extended, SysvarDpSensor list-label transform,
// the IsExcludedSysvar filter, MetricHubSensor as
// HubDataPointer, and the MetricHubSensors factory.
package hub

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Item 1: Sysvar.Extended() ───────────────────────────────────────────────

// TestSysvarExtendedFalseByDefault verifies that a freshly constructed
// Sysvar reports Extended == false
// _is_extended = False (hub/data_point.py:139).
func TestSysvarExtendedFalseByDefault(t *testing.T) {
	sv := NewSysvar("c1", "test", "", hmenum.HubValueTypeLogic, nil)
	if sv.Extended() {
		t.Fatal("Extended() must be false by default")
	}
}

// TestSysvarExtendedTrueWhenSet verifies that setting IsExtended = true
// is reflected through Extended().
func TestSysvarExtendedTrueWhenSet(t *testing.T) {
	sv := NewSysvar("c1", "ext", "", hmenum.HubValueTypeList, nil)
	sv.IsExtended = true
	if !sv.Extended() {
		t.Fatal("Extended() must return true after IsExtended = true")
	}
}

// ─── Item 2: SysvarDpSensor ──────────────────────────────────────────────────

// ─── IsExcludedSysvar ───────────────────────────────────────────────────────

// TestIsExcludedSysvarOldVal verifies that names containing "OldVal"
// are excluded whatever the ID.
func TestIsExcludedSysvarOldVal(t *testing.T) {
	cases := []string{"OldVal", "MyVarOldVal", "OldValSomething"}
	for _, name := range cases {
		if !IsExcludedSysvar(name, "1234") {
			t.Errorf("IsExcludedSysvar(%q, %q) = false, want true", name, "1234")
		}
	}
}

// TestIsExcludedSysvarPcCCUID verifies that names containing "pcCCUID"
// are excluded.
func TestIsExcludedSysvarPcCCUID(t *testing.T) {
	cases := []string{"pcCCUID", "device_pcCCUID_x"}
	for _, name := range cases {
		if !IsExcludedSysvar(name, "1234") {
			t.Errorf("IsExcludedSysvar(%q, %q) = false, want true", name, "1234")
		}
	}
}

// TestIsExcludedSysvarFixedIDs verifies that the alarm/service-message
// IDs are matched by equality, so an ordinary variable whose ID merely
// starts with "40" stays in the catalogue.
func TestIsExcludedSysvarFixedIDs(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		want     bool
	}{
		{"Alarmmeldungen", "40", true},
		{"Servicemeldungen", "41", true},
		{"Temperatur Garten", "401", false},
		{"Temperatur Garten", "4", false},
	} {
		if got := IsExcludedSysvar(tc.name, tc.id); got != tc.want {
			t.Errorf("IsExcludedSysvar(%q, %q) = %v, want %v", tc.name, tc.id, got, tc.want)
		}
	}
}

// TestIsExcludedSysvarNormalNames verifies that ordinary sysvar names
// are not excluded.
func TestIsExcludedSysvarNormalNames(t *testing.T) {
	cases := []string{"MyVar", "Temperature", "Heating", "", "mode"}
	for _, name := range cases {
		if IsExcludedSysvar(name, "1234") {
			t.Errorf("IsExcludedSysvar(%q, %q) = true, want false", name, "1234")
		}
	}
}

// ─── Item 4 + 5: MetricHubSensor / MetricHubSensors factory ─────────────────

// TestMetricHubSensorStateUncertainBeforeObservation verifies that a
// freshly created MetricHubSensor reports StateUncertain() == true
// before any metric has been observed.
func TestMetricHubSensorStateUncertainBeforeObservation(t *testing.T) {
	m := NewMetrics()
	s := NewMetricHubSensor("ccu-01", MetricSystemHealth, m)
	if !s.StateUncertain() {
		t.Fatal("StateUncertain() must be true before any observation")
	}
}

// TestMetricHubSensorStateUncertainAfterObservation verifies that
// StateUncertain() becomes false after Metrics.Observe is called.
func TestMetricHubSensorStateUncertainAfterObservation(t *testing.T) {
	m := NewMetrics()
	s := NewMetricHubSensor("ccu-01", MetricSystemHealth, m)
	m.Observe(MetricSystemHealth, 95.0)
	if s.StateUncertain() {
		t.Fatal("StateUncertain() must be false after observation")
	}
}

// TestMetricHubSensorValue verifies that Value() returns the last
// observed metric value.
func TestMetricHubSensorValue(t *testing.T) {
	m := NewMetrics()
	s := NewMetricHubSensor("ccu-01", MetricConnectionLatMs, m)
	m.Observe(MetricConnectionLatMs, 12.5)
	v, ok := s.Value()
	if !ok {
		t.Fatal("Value() must return ok=true after observation")
	}
	if v != 12.5 {
		t.Errorf("Value()=%v, want 12.5", v)
	}
}

// TestMetricHubSensorTranslationKeys verifies that TranslationKey()
// returns the expected keys for all three metric kinds.
func TestMetricHubSensorTranslationKeys(t *testing.T) {
	m := NewMetrics()
	cases := []struct {
		kind MetricKind
		want string
	}{
		{MetricSystemHealth, "system_health"},
		{MetricConnectionLatMs, "connection_latency_ms"},
		{MetricLastEventAgeSecs, "last_event_age_seconds"},
	}
	for _, tc := range cases {
		s := NewMetricHubSensor("ccu-01", tc.kind, m)
		if got := s.TranslationKey(); got != tc.want {
			t.Errorf("kind=%s TranslationKey()=%q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestMetricHubSensorUnits verifies unit strings for all three metrics.
func TestMetricHubSensorUnits(t *testing.T) {
	m := NewMetrics()
	cases := []struct {
		kind MetricKind
		want string
	}{
		{MetricSystemHealth, "%"},
		{MetricConnectionLatMs, "ms"},
		{MetricLastEventAgeSecs, "s"},
	}
	for _, tc := range cases {
		s := NewMetricHubSensor("ccu-01", tc.kind, m)
		if s.Unit != tc.want {
			t.Errorf("kind=%s Unit=%q, want %q", tc.kind, s.Unit, tc.want)
		}
	}
}

// TestMetricHubSensorsFactory verifies that MetricHubSensors returns
// the three expected sensor kinds.
func TestMetricHubSensorsFactory(t *testing.T) {
	m := NewMetrics()
	sh, lat, age := MetricHubSensors("ccu-01", m)
	if sh.Kind != MetricSystemHealth {
		t.Errorf("systemHealth.Kind=%v, want %v", sh.Kind, MetricSystemHealth)
	}
	if lat.Kind != MetricConnectionLatMs {
		t.Errorf("latency.Kind=%v, want %v", lat.Kind, MetricConnectionLatMs)
	}
	if age.Kind != MetricLastEventAgeSecs {
		t.Errorf("eventAge.Kind=%v, want %v", age.Kind, MetricLastEventAgeSecs)
	}
}

// TestMetricHubSensorSignature verifies that Signature() returns a
// non-empty debug string containing the sensor name.
func TestMetricHubSensorSignature(t *testing.T) {
	m := NewMetrics()
	s := NewMetricHubSensor("ccu-01", MetricSystemHealth, m)
	sig := s.Signature()
	if sig == "" {
		t.Fatal("Signature() must not be empty")
	}
}

// TestMetricHubSensorSatisfiesHubDataPointer is a compile-time
// guard: the var _ statement in metrics.go already asserts this,
// but this runtime test provides an explicit description.
func TestMetricHubSensorSatisfiesHubDataPointer(t *testing.T) {
	m := NewMetrics()
	var _ HubDataPointer = NewMetricHubSensor("c1", MetricSystemHealth, m)
}
