// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSeamEffect_HubReadyRestart_FiresWhenACentralBecomesReady asserts
// what the mqtt.hub_ready_restart seam's Why claims: that the hub
// publisher is re-started once a central's southbound bring-up completes,
// and that the post-ready slot riding the same trigger runs with it.
//
// This is the seam that was attached with an empty closure until round 7 —
// the manifest reported it as wired while the subscription sat beside it —
// so an assertion on the declaration alone would have been green
// throughout. The observable is the trigger firing, which is the whole
// reason the seam exists: without it the publisher keeps the empty serial
// it started with and hub discovery is skipped.
func TestSeamEffect_HubReadyRestart_FiresWhenACentralBecomesReady(t *testing.T) {
	// The trigger debounces a burst of staggered multi-CCU bring-ups, so the
	// restart is a timer away from the event. The bubble's fake clock spends
	// that window instantly and lets the test pin both sides of it.
	synctest.Test(t, func(t *testing.T) {
		var fired atomic.Int64

		reg := central.NewRegistry()
		unit := registerSeamEffectCentral(t, reg, "ready-central")

		// A real publisher, not nil. Its Start no-ops without an MQTT wiring,
		// which is what this test wants — but a nil one panics, and
		// runHubDiscoveryRestart recovers and logs, so the post-ready slot
		// would silently never run and the failure would look like the seam.
		deps := southboundWiringDeps{
			reg:          reg,
			logger:       discardTestLogger(),
			hubMQTT:      adapter.NewHubMQTTPublisher(reg, nil, discardTestLogger()),
			postHubReady: func() { fired.Add(1) },
		}
		// The debounce goroutine lives until its context ends.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closers, _ := wireHubReadyRestart(ctx, deps, reg, discardTestLogger())
		t.Cleanup(func() {
			for _, c := range closers {
				c()
			}
		})

		unit.EventBus.Publish(hmevent.CentralSouthboundReadyEvent{CentralName: "ready-central"})

		// Nothing restarts inside the debounce window...
		synctest.Sleep(hubDiscoveryReadyDebounce - time.Nanosecond)
		if n := fired.Load(); n != 0 {
			t.Fatalf("the hub publisher was re-started %d times before the debounce window elapsed, want 0", n)
		}
		// ...and the restart, with the post-ready slot riding it, fires once
		// it has.
		synctest.Sleep(time.Nanosecond)
		synctest.Wait()
		if n := fired.Load(); n != 1 {
			t.Errorf("a central reported southbound-ready and the hub publisher was re-started %d times, want 1: "+
				"with none it keeps the empty serial it started with, so hub discovery is skipped and no "+
				"sysvar, program or service-message entity appears in Home Assistant", n)
		}
	})
}
