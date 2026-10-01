// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
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
	synctest.Test(t, func(t *testing.T) {
		reg := central.NewRegistry()
		svc, _ := newTestService(t, func(d *Deps) { d.Registry = reg })

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
		var rebuilds atomic.Int32
		unsub := svc.Bus().Subscribe(func(hmevent.SecurityStateChangedEvent) {
			rebuilds.Add(1)
		})
		t.Cleanup(unsub)

		const announced = 40
		for range announced {
			unit.EventBus.Publish(hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
		}

		// Nothing rebuilds inside the debounce window...
		synctest.Sleep(indexRebuildDebounce - time.Nanosecond)
		if n := rebuilds.Load(); n != 0 {
			t.Fatalf("%d index rebuilds before the debounce window elapsed, want 0", n)
		}
		// ...the burst coalesces into exactly one rebuild once it has, and
		// no further rebuild follows.
		synctest.Sleep(4 * indexRebuildDebounce)
		if n := rebuilds.Load(); n != 1 {
			t.Fatalf("%d announcements produced %d index rebuilds, want 1", announced, n)
		}
	})
}
