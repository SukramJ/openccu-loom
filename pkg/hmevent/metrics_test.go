// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import "testing"

func TestMetricEventTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		event Event
		want  EventType
	}{
		{
			event: LatencyMetricEvent{
				MetricEvent: MetricEvent{Base: NewBase(), MetricKey: "ping_pong.rtt.hmip_rf"},
				DurationMs:  42.0,
			},
			want: EventTypeLatencyMetric,
		},
		{
			event: CounterMetricEvent{
				MetricEvent: MetricEvent{Base: NewBase(), MetricKey: "circuit.failure.hmip_rf"},
				Delta:       1,
			},
			want: EventTypeCounterMetric,
		},
		{
			event: GaugeMetricEvent{
				MetricEvent: MetricEvent{Base: NewBase(), MetricKey: "rpc_server.active_tasks"},
				Value:       7.0,
			},
			want: EventTypeGaugeMetric,
		},
		{
			event: HealthMetricEvent{
				MetricEvent: MetricEvent{Base: NewBase(), MetricKey: "client.health.hmip_rf"},
				Healthy:     false,
				Reason:      "circuit open",
			},
			want: EventTypeHealthMetric,
		},
	}

	for _, tc := range cases {
		if got := tc.event.Type(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestMetricEventKeyForwarding(t *testing.T) {
	t.Parallel()

	ev := LatencyMetricEvent{
		MetricEvent: MetricEvent{
			Base:      NewBase(),
			MetricKey: "ping_pong.rtt.bidcos",
		},
		DurationMs: 10,
	}

	if got := ev.EventKey(); got != "ping_pong.rtt.bidcos" {
		t.Errorf("EventKey()=%q, want ping_pong.rtt.bidcos", got)
	}
}

func TestMetricEventTimestampNotZero(t *testing.T) {
	t.Parallel()

	ev := CounterMetricEvent{
		MetricEvent: MetricEvent{Base: NewBase(), MetricKey: "x.y"},
		Delta:       1,
	}
	if ev.Timestamp().IsZero() {
		t.Error("timestamp should not be zero after NewBase()")
	}
}

func TestMetricEventTypeTags(t *testing.T) {
	t.Parallel()

	// Verify the stable string values — these are used as metrics labels.
	if EventTypeLatencyMetric != "metric.latency" {
		t.Errorf("unexpected tag: %q", EventTypeLatencyMetric)
	}
	if EventTypeCounterMetric != "metric.counter" {
		t.Errorf("unexpected tag: %q", EventTypeCounterMetric)
	}
	if EventTypeGaugeMetric != "metric.gauge" {
		t.Errorf("unexpected tag: %q", EventTypeGaugeMetric)
	}
	if EventTypeHealthMetric != "metric.health" {
		t.Errorf("unexpected tag: %q", EventTypeHealthMetric)
	}
}

// --------------------------------------------------------------------------
// MetricType enum (A7v4-05)
// --------------------------------------------------------------------------

func TestMetricType_StringValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mt   MetricType
		want string
	}{
		{MetricTypeLatency, "LATENCY"},
		{MetricTypeCounter, "COUNTER"},
		{MetricTypeGauge, "GAUGE"},
		{MetricTypeHealth, "HEALTH"},
	}
	for _, tc := range cases {
		if got := tc.mt.String(); got != tc.want {
			t.Errorf("MetricType(%q).String() = %q, want %q", tc.mt, got, tc.want)
		}
	}
}

func TestMetricType_AllValuesDistinct(t *testing.T) {
	t.Parallel()
	seen := make(map[MetricType]bool)
	for _, mt := range []MetricType{MetricTypeLatency, MetricTypeCounter, MetricTypeGauge, MetricTypeHealth} {
		if seen[mt] {
			t.Errorf("MetricType %q is duplicated", mt)
		}
		seen[mt] = true
	}
}
