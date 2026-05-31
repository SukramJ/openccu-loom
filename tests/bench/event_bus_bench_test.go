// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build bench

package bench

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// BenchmarkEventBusPublish measures per-publish overhead for the typed
// generic event bus.
func BenchmarkEventBusPublish(b *testing.B) {
	bus := events.NewBus()
	events.Subscribe(bus, func(hmevent.DataPointValueChangedEvent) {})
	ev := hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBaseAt(time.Now()),
		Key:      hmtypes.DataPointKey{ChannelAddress: "0001:1", Parameter: "STATE"},
		NewValue: hmtypes.BoolValue(true),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events.Publish(bus, ev)
	}
}

// BenchmarkEventBusSubscribeUnsubscribe measures the cost of the
// subscription lifecycle — a hot path during daemon startup.
func BenchmarkEventBusSubscribeUnsubscribe(b *testing.B) {
	bus := events.NewBus()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		unsub := events.Subscribe(bus, func(hmevent.DataPointValueChangedEvent) {})
		unsub()
	}
}
