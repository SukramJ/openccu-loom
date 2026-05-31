// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// metricsTestEvent is a minimal Event implementation used only by the
// EventStats / TotalSubscriptionCount tests. Defined here (rather than
// in pkg/hmevent) because the metrics tests don't need a real domain
// event — any type implementing Event suffices.
type metricsTestEvent struct {
	hmevent.Base
}

func (metricsTestEvent) Type() hmevent.EventType { return "metrics.test.alpha" }

type metricsTestBetaEvent struct {
	hmevent.Base
}

func (metricsTestBetaEvent) Type() hmevent.EventType { return "metrics.test.beta" }

func TestBusEventStatsCountsPerType(t *testing.T) {
	t.Parallel()

	bus := NewBus()
	if got := bus.EventStats(); len(got) != 0 {
		t.Errorf("baseline stats len=%d", len(got))
	}

	for range 3 {
		Publish(bus, metricsTestEvent{Base: hmevent.NewBase()})
	}
	for range 5 {
		Publish(bus, metricsTestBetaEvent{Base: hmevent.NewBase()})
	}

	stats := bus.EventStats()
	if got := stats["metrics.test.alpha"]; got != 3 {
		t.Errorf("alpha=%d, want 3", got)
	}
	if got := stats["metrics.test.beta"]; got != 5 {
		t.Errorf("beta=%d, want 5", got)
	}

	// Mutating the returned map must not affect the bus.
	stats["metrics.test.alpha"] = 999
	if again := bus.EventStats()["metrics.test.alpha"]; again != 3 {
		t.Errorf("returned map shared state: alpha=%d", again)
	}
}

func TestBusEventStatsCountsWithoutSubscribers(t *testing.T) {
	t.Parallel()

	bus := NewBus()
	for range 4 {
		Publish(bus, metricsTestEvent{Base: hmevent.NewBase()})
	}
	if got := bus.EventStats()["metrics.test.alpha"]; got != 4 {
		t.Errorf("counted=%d, want 4 even with no subscribers", got)
	}
}

func TestBusTotalSubscriptionCountAggregates(t *testing.T) {
	t.Parallel()

	bus := NewBus()
	if got := bus.TotalSubscriptionCount(); got != 0 {
		t.Errorf("baseline=%d", got)
	}

	unsubA1 := Subscribe(bus, func(metricsTestEvent) {})
	_ = Subscribe(bus, func(metricsTestEvent) {})
	unsubB := Subscribe(bus, func(metricsTestBetaEvent) {})
	if got := bus.TotalSubscriptionCount(); got != 3 {
		t.Errorf("after subscribe got=%d, want 3", got)
	}

	unsubA1()
	if got := bus.TotalSubscriptionCount(); got != 2 {
		t.Errorf("after one unsubscribe got=%d, want 2", got)
	}
	unsubB()
	if got := bus.TotalSubscriptionCount(); got != 1 {
		t.Errorf("after two unsubscribes got=%d, want 1", got)
	}
}

func TestBusEventStatsRaceSafe(t *testing.T) {
	t.Parallel()

	bus := NewBus()
	const goroutines = 8
	const publishes = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range publishes {
				Publish(bus, metricsTestEvent{Base: hmevent.NewBase()})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range publishes * goroutines {
			_ = bus.EventStats()
			_ = bus.TotalSubscriptionCount()
		}
	}()
	wg.Wait()

	if got := bus.EventStats()["metrics.test.alpha"]; got != goroutines*publishes {
		t.Errorf("total=%d, want %d", got, goroutines*publishes)
	}
}
