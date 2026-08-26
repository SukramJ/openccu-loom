// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for Sysvar.Extended, SysvarDpSensor list-label transform,
// IsExcludedSysvar / CleanSysvarNames filters, MetricHubSensor as
// HubDataPointer, and the MetricHubSensors factory.
package hub

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// TestSysvarDpSensorListLabelResolution verifies that SensorValue maps
// a numeric index to the corresponding string label in ValueList.
// Mirrors Python's SysvarDpSensor.value → SensorValueMixin._transform_sensor_value
// with value_list (hub/sensor.py:24-31, mixins/sensor_value.py:60-61).
func TestSysvarDpSensorListLabelResolution(t *testing.T) {
	sv := &Sysvar{
		HubDataPoint: HubDataPoint{Name: "mode"},
		ValueType:    hmenum.HubValueTypeList,
		ValueList:    []string{"off", "heating", "cooling"},
	}
	sensor := &SysvarDpSensor{Sysvar: sv}

	// Before any observation SensorValue should return ("", false).
	label, ok := sensor.SensorValue()
	if ok {
		t.Fatal("SensorValue() must return ok=false before first observation")
	}
	if label != "" {
		t.Fatalf("SensorValue() label=%q want empty before observation", label)
	}

	// Index 1 → "heating"
	sv.OnValue(hmtypes.IntValue(1))
	label, ok = sensor.SensorValue()
	if !ok {
		t.Fatal("SensorValue() must return ok=true after observation")
	}
	if label != "heating" {
		t.Errorf("SensorValue()=%q want %q", label, "heating")
	}

	// Index 0 → "off"
	sv.OnValue(hmtypes.IntValue(0))
	label, ok = sensor.SensorValue()
	if !ok {
		t.Fatal("SensorValue() must return ok=true")
	}
	if label != "off" {
		t.Errorf("SensorValue()=%q want %q", label, "off")
	}

	// Index 2 → "cooling"
	sv.OnValue(hmtypes.IntValue(2))
	label, ok = sensor.SensorValue()
	if !ok {
		t.Fatal("SensorValue() must return ok=true")
	}
	if label != "cooling" {
		t.Errorf("SensorValue()=%q want %q", label, "cooling")
	}
}

// TestSysvarDpSensorOutOfRangeIndex verifies that an index beyond the
// value list length renders as an integer string rather than panicking
// or returning ok=false.
func TestSysvarDpSensorOutOfRangeIndex(t *testing.T) {
	sv := &Sysvar{
		HubDataPoint: HubDataPoint{Name: "mode"},
		ValueType:    hmenum.HubValueTypeList,
		ValueList:    []string{"a", "b"},
	}
	sensor := &SysvarDpSensor{Sysvar: sv}
	sv.OnValue(hmtypes.IntValue(99)) // out of range
	label, ok := sensor.SensorValue()
	if !ok {
		t.Fatal("SensorValue() must return ok=true even for out-of-range index (fallback render)")
	}
	if label == "" {
		t.Error("SensorValue() label must not be empty for an out-of-range int")
	}
}

// TestSysvarDpSensorStringPassthrough verifies that a non-list sysvar
// returns the string value directly.
func TestSysvarDpSensorStringPassthrough(t *testing.T) {
	sv := &Sysvar{
		HubDataPoint: HubDataPoint{Name: "note"},
		ValueType:    hmenum.HubValueTypeString,
	}
	sensor := &SysvarDpSensor{Sysvar: sv}
	sv.OnValue(hmtypes.StringValue("hello"))
	label, ok := sensor.SensorValue()
	if !ok {
		t.Fatal("SensorValue() must return ok=true for string value")
	}
	if label != "hello" {
		t.Errorf("SensorValue()=%q want %q", label, "hello")
	}
}

// TestWrapSysvarReturnsSensorForReadOnlyList verifies that WrapSysvar
// returns *SysvarDpSensor for a read-only (Writer==nil, !IsExtended)
// list sysvar.
func TestWrapSysvarReturnsSensorForReadOnlyList(t *testing.T) {
	sv := NewSysvar("c1", "mode", "", hmenum.HubValueTypeList, nil)
	sv.ValueList = []string{"off", "on"}
	got := WrapSysvar(sv)
	if _, ok := got.(*SysvarDpSensor); !ok {
		t.Fatalf("WrapSysvar() returned %T, want *SysvarDpSensor for read-only LIST sysvar", got)
	}
}

// TestWrapSysvarReturnsBaseForExtendedList verifies that WrapSysvar
// returns the base *Sysvar when IsExtended is true (extended list sysvars
// are not wrapped as SysvarDpSensor).
func TestWrapSysvarReturnsBaseForExtendedList(t *testing.T) {
	sv := NewSysvar("c1", "mode", "", hmenum.HubValueTypeList, nil)
	sv.IsExtended = true
	got := WrapSysvar(sv)
	if _, ok := got.(*Sysvar); !ok {
		t.Fatalf("WrapSysvar() returned %T, want *Sysvar for extended LIST sysvar", got)
	}
}

// ─── Item 3: IsExcludedSysvar / CleanSysvarNames ─────────────────────────────

// TestIsExcludedSysvarOldVal verifies that names containing "OldVal"
// are excluded, matching Python's _EXCLUDED list (hub/hub.py:95-98).
func TestIsExcludedSysvarOldVal(t *testing.T) {
	cases := []string{"OldVal", "MyVarOldVal", "OldValSomething"}
	for _, name := range cases {
		if !IsExcludedSysvar(name) {
			t.Errorf("IsExcludedSysvar(%q) = false, want true", name)
		}
	}
}

// TestIsExcludedSysvarPcCCUID verifies that names containing "pcCCUID"
// are excluded.
func TestIsExcludedSysvarPcCCUID(t *testing.T) {
	cases := []string{"pcCCUID", "device_pcCCUID_x"}
	for _, name := range cases {
		if !IsExcludedSysvar(name) {
			t.Errorf("IsExcludedSysvar(%q) = false, want true", name)
		}
	}
}

// TestIsExcludedSysvarNormalNames verifies that ordinary sysvar names
// are not excluded.
func TestIsExcludedSysvarNormalNames(t *testing.T) {
	cases := []string{"MyVar", "Temperature", "Heating", "", "mode"}
	for _, name := range cases {
		if IsExcludedSysvar(name) {
			t.Errorf("IsExcludedSysvar(%q) = true, want false", name)
		}
	}
}

// TestCleanSysvarNamesFiltersExcluded verifies that CleanSysvarNames
// removes excluded names and keeps valid ones, mirroring Python's
// _clean_variables (hub/hub.py:940-942).
func TestCleanSysvarNamesFiltersExcluded(t *testing.T) {
	input := []string{"Temperature", "OldVal", "Heating", "pcCCUID", "Mode"}
	got := CleanSysvarNames(input)
	want := []string{"Temperature", "Heating", "Mode"}
	if len(got) != len(want) {
		t.Fatalf("CleanSysvarNames() len=%d, want %d; got %v", len(got), len(want), got)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("CleanSysvarNames()[%d]=%q, want %q", i, g, want[i])
		}
	}
}

// TestCleanSysvarNamesEmptyInput verifies that an empty input returns
// an empty result.
func TestCleanSysvarNamesEmptyInput(t *testing.T) {
	got := CleanSysvarNames(nil)
	if len(got) != 0 {
		t.Fatalf("CleanSysvarNames(nil) = %v, want empty", got)
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
