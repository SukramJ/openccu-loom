// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// matterHealthComponentName is the tracker component name reported
// by [startMatterHealthProbe]. Matches the SPA's component-list
// naming convention (subsystem dotted with the surface, here `matter`
// alone because the bridge is daemon-global rather than per-CCU).
const matterHealthComponentName = "matter.bridge"

// matterHealthProbeInterval matches the SQLite + MQTT probes so the
// SPA's Diagnostics view shows all coverage producers refreshing on
// the same cadence.
const matterHealthProbeInterval = 30 * time.Second

// matterStatusSnapshot is the subset of [handlers.MatterStatusReader]
// the probe needs. Defined locally so we keep the daemon-side
// adapter free of cross-package interface dependencies.
type matterStatusSnapshot interface {
	MatterStatus(ctx context.Context) handlers.MatterStatusResponse
}

// startMatterHealthProbe polls the [handlers.MatterStatusReader] on a
// fixed cadence and records the bridge's liveness verdict. Returns a
// stop function that cancels the goroutine and waits for it to exit.
// A nil reader or tracker yields a no-op stopper.
func startMatterHealthProbe(ctx context.Context, reader matterStatusSnapshot, tracker *health.Tracker, interval time.Duration) func() {
	if reader == nil || tracker == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = matterHealthProbeInterval
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		probeMatterOnce(pctx, reader, tracker)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				probeMatterOnce(pctx, reader, tracker)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func probeMatterOnce(ctx context.Context, reader matterStatusSnapshot, tracker *health.Tracker) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status := reader.MatterStatus(queryCtx)
	if !status.Enabled {
		// Bridge is intentionally off — record one "disabled" sample
		// so the SPA shows the explicit state rather than the
		// flap-damped UNKNOWN that staleness would produce.
		tracker.Record(matterHealthComponentName, health.Sample{
			Healthy: true,
			Note:    "disabled",
		})
		return
	}
	if !status.Listening {
		tracker.Record(matterHealthComponentName, health.Sample{
			Healthy: false,
			Note:    "bridge not listening",
		})
		tracker.Record(matterHealthComponentName, health.Sample{
			Healthy: false,
			Note:    "bridge not listening (escalated)",
		})
		return
	}
	note := fmt.Sprintf(
		"listening on %s (fabrics=%d, endpoints=%d, enabled=%d, window=%t)",
		status.ListenAddr, status.FabricCount, status.EndpointCount,
		status.EnabledCount, status.WindowOpen,
	)
	tracker.Record(matterHealthComponentName, health.Sample{Healthy: true, Note: note})
}
