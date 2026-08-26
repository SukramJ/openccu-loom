// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"testing"
)

// TestMetricSensorDescriptionNonEmpty verifies that every MetricKind has a
// non-empty description string.
func TestMetricSensorDescriptionNonEmpty(t *testing.T) {
	t.Parallel()
	cases := []MetricKind{MetricSystemHealth, MetricConnectionLatMs, MetricLastEventAgeSecs}
	for _, kind := range cases {
		d := MetricSensorDescription(kind)
		if d == "" {
			t.Errorf("MetricSensorDescription(%q) returned empty string", kind)
		}
	}
}

// TestMetricHubSensorDescriptionPopulated verifies that NewMetricHubSensor
// populates the Description field via MetricSensorDescription.
func TestMetricHubSensorDescriptionPopulated(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	sensor := NewMetricHubSensor("ccu1", MetricSystemHealth, m)
	if sensor.Description == "" {
		t.Fatal("Description must be non-empty for MetricSystemHealth")
	}
	if sensor.Description != MetricSensorDescription(MetricSystemHealth) {
		t.Errorf("Description mismatch: got %q, want %q",
			sensor.Description, MetricSensorDescription(MetricSystemHealth))
	}
}

// TestMetricSensorDescriptionUnknownKind verifies that an unknown MetricKind
// returns "" (graceful fallback for future kinds).
func TestMetricSensorDescriptionUnknownKind(t *testing.T) {
	t.Parallel()
	if d := MetricSensorDescription("unknown_metric"); d != "" {
		t.Errorf("expected empty string for unknown kind, got %q", d)
	}
}

// TestConnectivityStateUncertainTrueWhenEmpty verifies that a freshly
// created Connectivity tracker reports StateUncertain == true (no
// observations yet).
func TestConnectivityStateUncertainTrueWhenEmpty(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	if !c.StateUncertain() {
		t.Fatal("StateUncertain must be true before any observation")
	}
}

// TestConnectivityStateUncertainFalseAfterObservation verifies that after
// at least one OnState call StateUncertain returns false.
func TestConnectivityStateUncertainFalseAfterObservation(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	c.OnState("HmIP-RF", true)
	if c.StateUncertain() {
		t.Fatal("StateUncertain must be false after first observation")
	}
}

// TestConnectivityStateUncertainConsistentWithAvailable verifies that
// StateUncertain() == !Available() always holds.
func TestConnectivityStateUncertainConsistentWithAvailable(t *testing.T) {
	t.Parallel()
	c := NewConnectivity()
	if c.StateUncertain() != !c.Available() {
		t.Fatal("StateUncertain must equal !Available before observation")
	}
	c.OnState("HmIP-RF", false)
	if c.StateUncertain() != !c.Available() {
		t.Fatal("StateUncertain must equal !Available after observation")
	}
}
