// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Bus is the minimal interface required by the emitter functions.
// Keeping it package-local avoids a direct import of internal/central/events
// and prevents circular dependencies.
//
// The real EventBus in internal/central/events satisfies this interface
// automatically because it has a Publish(hmevent.Event) method.
type Bus interface {
	Publish(hmevent.Event)
}

// EmitLatency publishes a LatencyMetricEvent on bus.
//
// loom:reachable:reason="called by client transport layer to report per-interface round-trip latency"
func EmitLatency(bus Bus, key MetricKey, durationMs float64) {
	if bus == nil {
		return
	}
	bus.Publish(hmevent.LatencyMetricEvent{
		MetricEvent: hmevent.MetricEvent{
			Base:      hmevent.NewBaseAt(time.Now()),
			MetricKey: key.String(),
		},
		DurationMs: durationMs,
	})
}

// EmitCounter publishes a CounterMetricEvent on bus with the given delta.
//
// loom:reachable:reason="called by client transport layer to count requests and errors per interface"
func EmitCounter(bus Bus, key MetricKey, delta int64) {
	if bus == nil {
		return
	}
	bus.Publish(hmevent.CounterMetricEvent{
		MetricEvent: hmevent.MetricEvent{
			Base:      hmevent.NewBaseAt(time.Now()),
			MetricKey: key.String(),
		},
		Delta: delta,
	})
}

// EmitGauge publishes a GaugeMetricEvent on bus.
//
// loom:reachable:reason="called by coordinators to publish queue-depth and connection-count gauges"
func EmitGauge(bus Bus, key MetricKey, value float64) {
	if bus == nil {
		return
	}
	bus.Publish(hmevent.GaugeMetricEvent{
		MetricEvent: hmevent.MetricEvent{
			Base:      hmevent.NewBaseAt(time.Now()),
			MetricKey: key.String(),
		},
		Value: value,
	})
}

// EmitHealth publishes a HealthMetricEvent on bus.
//
// loom:reachable:reason="called by health tracker to surface connection-health transitions as metric events"
func EmitHealth(bus Bus, key MetricKey, healthy bool, reason string) {
	if bus == nil {
		return
	}
	bus.Publish(hmevent.HealthMetricEvent{
		MetricEvent: hmevent.MetricEvent{
			Base:      hmevent.NewBaseAt(time.Now()),
			MetricKey: key.String(),
		},
		Healthy: healthy,
		Reason:  reason,
	})
}
