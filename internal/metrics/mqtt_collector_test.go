// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// promName is the Prometheus metric-name grammar: the exposition parser
// rejects any name outside it and drops the entire scrape.
var promName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func TestNewMqttCollector(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	mc := NewMqttCollector(reg)

	mc.MessagesSent("ccu1").Inc()
	mc.MessagesSent("ccu1").Inc()
	mc.DiscoverySent("ccu1").Inc()
	mc.PublishErrors("ccu1").Inc()

	for _, m := range reg.Metrics() {
		if m.Name == "mqtt_messages_sent" && m.value.Load() != 2 {
			t.Errorf("messages_sent count=%d, want 2", m.value.Load())
		}
		if !promName.MatchString(m.Name) {
			t.Errorf("collector produced invalid Prometheus metric name %q", m.Name)
		}
	}
}

// TestNewMqttCollector_SharesSeriesAcrossConstructions pins the
// idempotent-registration guarantee the daemon-wide counters relied on:
// two collectors built against the same Registry resolve the same
// central to the SAME underlying series, so wiring the collector twice
// (a defensive re-init, a test helper) never splits one CCU's traffic
// across two counters.
func TestNewMqttCollector_SharesSeriesAcrossConstructions(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	a := NewMqttCollector(reg)
	b := NewMqttCollector(reg)
	a.MessagesSent("ccu1").Inc()
	a.MessagesSent("ccu1").Inc()
	b.MessagesSent("ccu1").Inc()
	if got := b.MessagesSent("ccu1").Value(); got != 3 {
		t.Fatalf("same (registry, central) must share one series: b sees %d after 3 total increments, want 3", got)
	}
	// Exactly one messages_sent series is registered for "ccu1", not one per call.
	count := 0
	for _, m := range reg.Metrics() {
		if m.Name == "mqtt_messages_sent" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want a single mqtt_messages_sent series for one central, got %d", count)
	}
}

// TestMqttCollectorPerCentralLabel pins the per-CCU dimension:
// on a multi-CCU daemon the mqtt_* counters must carry a `central` label
// so two CCUs' traffic renders as two distinct series with independent
// counts, not one folded total. Before the fix MqttCollector exposed a
// single unlabeled series shared by every central — this test would have
// found b's three increments merged into a's series (or vice versa) with
// no way to tell them apart in the Prometheus render.
func TestMqttCollectorPerCentralLabel(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	mc := NewMqttCollector(reg)

	mc.MessagesSent("ccu-a").Inc()
	mc.MessagesSent("ccu-a").Inc()
	mc.MessagesSent("ccu-b").Inc()

	if got := mc.MessagesSent("ccu-a").Value(); got != 2 {
		t.Fatalf("ccu-a messages_sent = %d, want 2 (independent of ccu-b)", got)
	}
	if got := mc.MessagesSent("ccu-b").Value(); got != 1 {
		t.Fatalf("ccu-b messages_sent = %d, want 1 (independent of ccu-a)", got)
	}

	var buf bytes.Buffer
	if err := reg.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `mqtt_messages_sent{central="ccu-a"} 2`) {
		t.Fatalf("render missing ccu-a series:\n%s", out)
	}
	if !strings.Contains(out, `mqtt_messages_sent{central="ccu-b"} 1`) {
		t.Fatalf("render missing ccu-b series:\n%s", out)
	}
}
