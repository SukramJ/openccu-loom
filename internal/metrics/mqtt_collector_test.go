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
	mc := NewMqttCollector(reg)
	if mc.MessagesSent == nil || mc.DiscoverySent == nil || mc.PublishErrors == nil {
		t.Fatal("MqttCollector fields must not be nil after construction")
	}
	mc.MessagesSent.Inc()
	mc.MessagesSent.Inc()
	mc.DiscoverySent.Inc()
	mc.PublishErrors.Inc()

	for _, m := range reg.Metrics() {
		if m.Name == "mqtt_messages_sent" && m.value.Load() != 2 {
			t.Errorf("messages_sent count=%d, want 2", m.value.Load())
		}
		if !promName.MatchString(m.Name) {
			t.Errorf("collector produced invalid Prometheus metric name %q", m.Name)
		}
	}
}

// TestNewMqttCollector_IsDaemonWideSingleSeries pins the fix for the
// multi-CCU miscounting: the counters are a single daemon-wide series, so
// two calls with the same Registry resolve to the SAME underlying counter
// (the shared bridge increments one series for every central's traffic).
// A per-central name — the previous behaviour — would have kept them
// separate and hidden every CCU but the first.
func TestNewMqttCollector_IsDaemonWideSingleSeries(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	a := NewMqttCollector(reg)
	b := NewMqttCollector(reg)
	a.MessagesSent.Inc()
	a.MessagesSent.Inc()
	b.MessagesSent.Inc()
	if got := b.MessagesSent.Value(); got != 3 {
		t.Fatalf("counters must be one daemon-wide series: b sees %d after 3 total increments, want 3", got)
	}
	// Exactly one messages_sent series is registered, not one per call.
	count := 0
	for _, m := range reg.Metrics() {
		if m.Name == "mqtt_messages_sent" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want a single mqtt_messages_sent series, got %d", count)
	}
}
