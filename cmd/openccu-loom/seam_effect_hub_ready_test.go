// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
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
	closers, _ := wireHubReadyRestart(context.Background(), deps, reg, discardTestLogger())
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})

	events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{CentralName: "ready-central"})

	// The trigger debounces a burst of staggered multi-CCU bring-ups, so
	// the wait has to outlast that window; polling rather than sleeping
	// keeps the test measuring the seam and not the scheduler.
	deadline := time.Now().Add(hubDiscoveryReadyDebounce + 2*time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("a central reported southbound-ready and the hub publisher was never re-started: " +
		"it keeps the empty serial it started with, so hub discovery is skipped and no " +
		"sysvar, program or service-message entity appears in Home Assistant")
}
