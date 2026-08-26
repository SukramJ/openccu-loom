// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// metrics_observe_test.go covers the hub-package Metrics aggregate and
// MetricHubSensor view not covered by hub_aggregate_test.go.
//
// Covered:
//   - Snapshot returns all observed metrics as an independent copy
//   - Observe dedup suppresses per-kind and OnAny callbacks for equal values
//   - Observe with changed value fires both per-kind and OnAny subscribers
//   - MetricHubSensor Available gate (false before first Observe, true after)
//   - MetricHubSensor Value reflects latest observation
//   - MetricHubSensor Name and Unit for each standard metric kind
//   - MetricHubSensors factory returns all three standard sensors

package hub

import (
	"sync/atomic"
	"testing"
)

// TestMetricsSnapshotReturnsAllObserved verifies that Snapshot returns a
// deep copy of every metric that has been observed at least once.
func TestMetricsSnapshotReturnsAllObserved(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.Observe(MetricSystemHealth, 95.0)
	m.Observe(MetricConnectionLatMs, 12.5)

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len=%d, want 2", len(snap))
	}
	if snap[MetricSystemHealth].Value != 95.0 {
		t.Errorf("SystemHealth value=%v, want 95.0", snap[MetricSystemHealth].Value)
	}
	if snap[MetricConnectionLatMs].Value != 12.5 {
		t.Errorf("ConnectionLatMs value=%v, want 12.5", snap[MetricConnectionLatMs].Value)
	}
}

// TestMetricsSnapshotIsIndependent verifies that mutations to the returned
// snapshot map do not affect the internal state.
func TestMetricsSnapshotIsIndependent(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.Observe(MetricSystemHealth, 80.0)

	snap := m.Snapshot()
	snap[MetricSystemHealth] = MetricSample{Kind: MetricSystemHealth, Value: 0}

	// The internal state must still hold the original value.
	v, ok := m.Value(MetricSystemHealth)
	if !ok || v.Value != 80.0 {
		t.Errorf("Value after snapshot mutation: got (%v,%v), want (80.0,true)", v.Value, ok)
	}
}

// TestMetricsObserveDedupDoesNotFireCallback verifies that re-observing
// the same value does not fire per-kind subscribers.
func TestMetricsObserveDedupDoesNotFireCallback(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.Observe(MetricSystemHealth, 100.0)

	var calls atomic.Int32
	m.OnUpdate(MetricSystemHealth, func(_ MetricSample) { calls.Add(1) })

	changed := m.Observe(MetricSystemHealth, 100.0)
	if changed {
		t.Fatal("Observe returned true for identical value, want false")
	}
	if calls.Load() != 0 {
		t.Fatalf("callback fired %d times on dedup, want 0", calls.Load())
	}
}

// TestMetricsObserveChangedValueFiresCallback verifies that a new value
// fires both the per-kind subscriber and the OnAny subscriber.
func TestMetricsObserveChangedValueFiresCallback(t *testing.T) {
	t.Parallel()
	m := NewMetrics()

	var perKind, anyCount atomic.Int32
	m.OnUpdate(MetricSystemHealth, func(_ MetricSample) { perKind.Add(1) })
	m.OnAny(func(_ MetricSample) { anyCount.Add(1) })

	m.Observe(MetricSystemHealth, 50.0)
	if perKind.Load() != 1 {
		t.Errorf("per-kind callback count=%d, want 1", perKind.Load())
	}
	if anyCount.Load() != 1 {
		t.Errorf("OnAny callback count=%d, want 1", anyCount.Load())
	}
}

// TestMetricsOnAnyDedupSuppressesCallback verifies the dedup contract for
// the OnAny subscriber: same value must not fire the any-handler.
func TestMetricsOnAnyDedupSuppressesCallback(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.Observe(MetricConnectionLatMs, 5.0)

	var calls atomic.Int32
	m.OnAny(func(_ MetricSample) { calls.Add(1) })

	m.Observe(MetricConnectionLatMs, 5.0)
	if calls.Load() != 0 {
		t.Fatalf("OnAny fired %d times on dedup, want 0", calls.Load())
	}
}

// TestMetricHubSensorAvailableBeforeAndAfterObservation checks the
// Available gate: false before first Observe, true after.
func TestMetricHubSensorAvailableBeforeAndAfterObservation(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	s := NewMetricHubSensor("c1", MetricLastEventAgeSecs, m)

	if s.Available() {
		t.Fatal("Available must be false before first Observe")
	}
	m.Observe(MetricLastEventAgeSecs, 30.0)
	if !s.Available() {
		t.Fatal("Available must be true after first Observe")
	}
}

// TestMetricHubSensorValueReflectsLatestObservation verifies that
// Value() always returns the most recently observed sample.
func TestMetricHubSensorValueReflectsLatestObservation(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	s := NewMetricHubSensor("c1", MetricSystemHealth, m)

	_, ok := s.Value()
	if ok {
		t.Fatal("Value ok must be false before first Observe")
	}

	m.Observe(MetricSystemHealth, 75.0)
	v, ok := s.Value()
	if !ok || v != 75.0 {
		t.Errorf("Value()=(%v,%v), want (75.0,true)", v, ok)
	}

	m.Observe(MetricSystemHealth, 80.0)
	v, ok = s.Value()
	if !ok || v != 80.0 {
		t.Errorf("Value()=(%v,%v), want (80.0,true) after update", v, ok)
	}
}

// TestMetricHubSensorNameAndUnit verifies the human-readable name and
// unit string for each of the three standard metric kinds.
func TestMetricHubSensorNameAndUnit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind MetricKind
		name string
		unit string
	}{
		{MetricSystemHealth, "HM-System-Health", "%"},
		{MetricConnectionLatMs, "HM-Connection-Latency", "ms"},
		{MetricLastEventAgeSecs, "HM-Last-Event-Age", "s"},
	}
	m := NewMetrics()
	for _, tc := range cases {
		s := NewMetricHubSensor("c1", tc.kind, m)
		if s.Name != tc.name {
			t.Errorf("kind=%v Name=%q, want %q", tc.kind, s.Name, tc.name)
		}
		if s.Unit != tc.unit {
			t.Errorf("kind=%v Unit=%q, want %q", tc.kind, s.Unit, tc.unit)
		}
	}
}

// TestMetricHubSensorsFactoryReturnsThreeSensors verifies that
// MetricHubSensors returns exactly the three standard sensors in the
// documented order (systemHealth, latency, eventAge).
func TestMetricHubSensorsFactoryReturnsThreeSensors(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	sh, lat, age := MetricHubSensors("c2", m)

	if sh.Kind != MetricSystemHealth {
		t.Errorf("systemHealth Kind=%v, want %v", sh.Kind, MetricSystemHealth)
	}
	if lat.Kind != MetricConnectionLatMs {
		t.Errorf("latency Kind=%v, want %v", lat.Kind, MetricConnectionLatMs)
	}
	if age.Kind != MetricLastEventAgeSecs {
		t.Errorf("eventAge Kind=%v, want %v", age.Kind, MetricLastEventAgeSecs)
	}
}
