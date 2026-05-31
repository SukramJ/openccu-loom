// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

// MetricType discriminates the four categories of metric observations.
//
// The values are stable strings; they appear in serialised snapshots and
// metric-key prefixes so changing them is a breaking change.
type MetricType string

// MetricType values.
const (
	// MetricTypeLatency represents a latency observation in milliseconds.
	MetricTypeLatency MetricType = "LATENCY"
	// MetricTypeCounter represents an additive counter delta.
	MetricTypeCounter MetricType = "COUNTER"
	// MetricTypeGauge represents a point-in-time gauge snapshot.
	MetricTypeGauge MetricType = "GAUGE"
	// MetricTypeHealth represents a binary health-state observation.
	MetricTypeHealth MetricType = "HEALTH"
)

// String implements fmt.Stringer.
func (t MetricType) String() string { return string(t) }

// Metric event type tags.
const (
	EventTypeLatencyMetric EventType = "metric.latency"
	EventTypeCounterMetric EventType = "metric.counter"
	EventTypeGaugeMetric   EventType = "metric.gauge"
	EventTypeHealthMetric  EventType = "metric.health"
)

// MetricEvent is the base for all metric events. Concrete sub-types embed it.
//
// MetricKey holds the full key string (component.metric[.identifier]) exactly
// as returned by MetricKey.String(). EventKey() forwards it so that EventBus
// subscribers can filter by prefix.
type MetricEvent struct {
	Base
	MetricKey string
}

// EventKey forwards the metric key so bus subscribers can filter by prefix.
func (e MetricEvent) EventKey() string { return e.MetricKey }

// LatencyMetricEvent carries a latency sample in milliseconds.
type LatencyMetricEvent struct {
	MetricEvent
	DurationMs float64
}

// Type implements Event.
func (LatencyMetricEvent) Type() EventType { return EventTypeLatencyMetric }

// CounterMetricEvent carries a counter delta.
type CounterMetricEvent struct {
	MetricEvent
	Delta int64
}

// Type implements Event.
func (CounterMetricEvent) Type() EventType { return EventTypeCounterMetric }

// GaugeMetricEvent carries a gauge snapshot value.
type GaugeMetricEvent struct {
	MetricEvent
	Value float64
}

// Type implements Event.
func (GaugeMetricEvent) Type() EventType { return EventTypeGaugeMetric }

// HealthMetricEvent reports a component's health state.
type HealthMetricEvent struct {
	MetricEvent
	Healthy bool
	Reason  string // optional; empty means no reason given
}

// Type implements Event.
func (HealthMetricEvent) Type() EventType { return EventTypeHealthMetric }
