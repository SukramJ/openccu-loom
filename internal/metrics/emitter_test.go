// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeBus captures published events for assertions.
type fakeBus struct {
	mu     sync.Mutex
	events []hmevent.Event
}

func (b *fakeBus) Publish(e hmevent.Event) {
	b.mu.Lock()
	b.events = append(b.events, e)
	b.mu.Unlock()
}

func (b *fakeBus) pop() hmevent.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return nil
	}
	e := b.events[0]
	b.events = b.events[1:]
	return e
}

func TestEmitLatencyPublishesEvent(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	key := MetricKeys.PingPongRTT("hmip_rf")
	EmitLatency(bus, key, 42.0)

	e := bus.pop()
	if e == nil {
		t.Fatal("no event published")
	}
	ev, ok := e.(hmevent.LatencyMetricEvent)
	if !ok {
		t.Fatalf("got %T, want LatencyMetricEvent", e)
	}
	if ev.MetricKey != "ping_pong.rtt.hmip_rf" {
		t.Errorf("metric key=%q", ev.MetricKey)
	}
	if ev.DurationMs != 42.0 {
		t.Errorf("duration=%f", ev.DurationMs)
	}
}

func TestEmitCounterPublishesEvent(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	key := MetricKeys.CircuitFailure("hmip_rf")
	EmitCounter(bus, key, 3)

	e := bus.pop()
	if e == nil {
		t.Fatal("no event published")
	}
	ev, ok := e.(hmevent.CounterMetricEvent)
	if !ok {
		t.Fatalf("got %T, want CounterMetricEvent", e)
	}
	if ev.Delta != 3 {
		t.Errorf("delta=%d", ev.Delta)
	}
}

func TestEmitGaugePublishesEvent(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	key := MetricKeys.RPCServerActiveTasks()
	EmitGauge(bus, key, 7.0)

	e := bus.pop()
	if e == nil {
		t.Fatal("no event published")
	}
	ev, ok := e.(hmevent.GaugeMetricEvent)
	if !ok {
		t.Fatalf("got %T, want GaugeMetricEvent", e)
	}
	if ev.Value != 7.0 {
		t.Errorf("value=%f", ev.Value)
	}
}

func TestEmitHealthPublishesEvent(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	key := MetricKeys.ClientHealth("hmip_rf")
	EmitHealth(bus, key, false, "circuit open")

	e := bus.pop()
	if e == nil {
		t.Fatal("no event published")
	}
	ev, ok := e.(hmevent.HealthMetricEvent)
	if !ok {
		t.Fatalf("got %T, want HealthMetricEvent", e)
	}
	if ev.Healthy {
		t.Error("expected healthy=false")
	}
	if ev.Reason != "circuit open" {
		t.Errorf("reason=%q", ev.Reason)
	}
}

// TestEmitNilBusIsNoop verifies that passing a nil bus never panics.
func TestEmitNilBusIsNoop(t *testing.T) {
	t.Parallel()

	key := MetricKeys.PingPongRTT("hmip_rf")
	EmitLatency(nil, key, 1)
	EmitCounter(nil, key, 1)
	EmitGauge(nil, key, 1)
	EmitHealth(nil, key, true, "")
}

// TestEmitEventKeyForwarding verifies EventKey() returns the metric key.
func TestEmitEventKeyForwarding(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{}
	key := MetricKeys.PingPongRTT("bidcos")
	EmitLatency(bus, key, 5)
	e := bus.pop()
	if e.EventKey() != "ping_pong.rtt.bidcos" {
		t.Errorf("event key=%q", e.EventKey())
	}
}
