// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestHotPlugRebuildCoalescesAReconnectBurst pins that a CCU
// re-announcing its whole inventory costs one index rebuild, not one per
// device.
//
// Our listDevices reply is deliberately empty, so every reconnect
// re-announces the full fleet and the device pipeline publishes one
// DeviceCreatedEvent per device — synchronously, on the bus dispatch
// goroutine. Rebuilding per event ran a whole-fleet pass (three store
// round trips plus every device × channel × data point) N times back to
// back, each ending in a state publish the MQTT plane reconciles
// against, so the event pipeline and the retained plane stalled for the
// length of every reconnect.
func TestHotPlugRebuildCoalescesAReconnectBurst(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	svc, _, _ := newTestService(t, func(d *Deps) { d.Registry = reg })

	unit, err := central.New(central.Config{Name: "ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}
	unit.MarkSouthboundReady()

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx) })

	// Count rebuilds by their published result — the rebuild is the only
	// producer of a state event in this test.
	rebuilds := make(chan struct{}, 64)
	unsub := events.Subscribe(svc.Bus(), func(hmevent.SecurityStateChangedEvent) {
		select {
		case rebuilds <- struct{}{}:
		default:
		}
	})
	t.Cleanup(unsub)

	const announced = 40
	for range announced {
		events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	}

	select {
	case <-rebuilds:
	case <-time.After(5 * time.Second):
		t.Fatal("the announcement burst produced no index rebuild at all")
	}
	// Give any further rebuild time to arrive before counting.
	time.Sleep(4 * indexRebuildDebounce)
	extra := len(rebuilds)
	if extra != 0 {
		t.Fatalf("%d announcements produced %d index rebuilds, want 1", announced, extra+1)
	}
}
