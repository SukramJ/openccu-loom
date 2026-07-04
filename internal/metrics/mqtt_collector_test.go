// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"regexp"
	"testing"
)

// promName is the Prometheus metric-name grammar: the exposition parser
// rejects any name outside it and drops the entire scrape.
var promName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func TestNewMqttCollector(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	mc := NewMqttCollector(reg, "ccu1")
	if mc.MessagesSent == nil || mc.DiscoverySent == nil || mc.PublishErrors == nil {
		t.Fatal("MqttCollector fields must not be nil after construction")
	}
	mc.MessagesSent.Inc()
	mc.MessagesSent.Inc()
	mc.DiscoverySent.Inc()
	mc.PublishErrors.Inc()

	metrics := reg.Metrics()
	for _, m := range metrics {
		if m.Name == "mqtt_ccu1_messages_sent" && m.value.Load() != 2 {
			t.Errorf("messages_sent count=%d, want 2", m.value.Load())
		}
	}
}

// TestNewMqttCollector_UnsafeCentralNameProducesValidNames ensures an
// unusual central name (space / dash / unicode / empty) still yields
// valid Prometheus metric names, so the exposition parser never drops the
// whole scrape.
func TestNewMqttCollector_UnsafeCentralNameProducesValidNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ccu1", "a b", "a-b", "Küche/2", "", "1st"} {
		reg := NewRegistry()
		mc := NewMqttCollector(reg, name)
		if mc.MessagesSent == nil {
			t.Fatalf("collector must not be nil for central %q", name)
		}
		for _, m := range reg.Metrics() {
			if !promName.MatchString(m.Name) {
				t.Errorf("central %q produced invalid Prometheus metric name %q", name, m.Name)
			}
		}
	}
}

// TestMetricSegment_DistinctNamesDoNotCollide verifies that two distinct
// central names which naively sanitize to the same token ("a b" and "a-b"
// both → "a_b") stay distinct via the deterministic hash suffix, and that
// already-safe names pass through unchanged.
func TestMetricSegment_DistinctNamesDoNotCollide(t *testing.T) {
	t.Parallel()
	segA := metricSegment("a b")
	segB := metricSegment("a-b")
	if segA == segB {
		t.Fatalf("distinct unsafe names must not collide: both → %q", segA)
	}
	if metricSegment("a b") != segA {
		t.Error("metricSegment must be deterministic for the same input")
	}
	if got := metricSegment("ccu1"); got != "ccu1" {
		t.Errorf("safe name must pass through unchanged, got %q", got)
	}
}

// TestNewMqttCollector_DistinctNamesKeepSeparateCounters proves the
// collision fix at the registry level: two collectors built from names
// that sanitize alike keep independent counters instead of silently
// merging into one metric.
func TestNewMqttCollector_DistinctNamesKeepSeparateCounters(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	a := NewMqttCollector(reg, "a b")
	b := NewMqttCollector(reg, "a-b")
	a.MessagesSent.Inc()
	if b.MessagesSent.Value() != 0 {
		t.Fatalf("counters for distinct centrals merged: b=%d after incrementing a", b.MessagesSent.Value())
	}
}
