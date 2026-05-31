// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// HealthComponentName is the tracker component the Matter bridge
// reports under. Multi-CCU deployments share one bridge instance so
// the name is intentionally not scoped per central.
const HealthComponentName = "matter"

// DefaultProbeInterval matches the MQTT and SQLite probe cadence so an
// operator scanning the Diagnostics surface sees every probe update on
// the same rhythm. Two missed probes downgrade the component to
// UNKNOWN via the tracker's StaleAfter rule.
const DefaultProbeInterval = 30 * time.Second

// Status is the minimal contract the probe needs from the
// running bridge. The concrete [*Bridge] satisfies it via LocalAddr;
// tests can drop in a fake.
type Status interface {
	// LocalAddr returns the bound UDP `host:port` of the operational
	// listener, or the empty string when the bridge has not started
	// (or has been torn down). Empty string is the unhealthy signal.
	LocalAddr() string
}

// HealthRecorder is the slim tracker contract the probe touches.
// [health.Tracker] satisfies it.
type HealthRecorder interface {
	Record(name string, sample health.Sample)
}

// StartHealthProbe spawns a goroutine that polls the Matter bridge on
// a fixed cadence and reports the listener verdict to tracker. The
// returned stop function cancels the goroutine and waits for it to
// exit.
//
// Silent when status or tracker is nil — same shape as the MQTT and
// SQLite probes so callers can wire it unconditionally during boot
// even when the Matter feature flag is off.
func StartHealthProbe(
	ctx context.Context,
	status Status,
	tracker HealthRecorder,
	interval time.Duration,
) func() {
	if status == nil || tracker == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		probeOnce(status, tracker)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				probeOnce(status, tracker)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func probeOnce(status Status, tracker HealthRecorder) {
	addr := status.LocalAddr()
	if addr != "" {
		tracker.Record(HealthComponentName, health.Sample{
			Healthy: true,
			Note:    fmt.Sprintf("listening (addr=%s)", addr),
		})
		return
	}
	// Listener gone. Escalate immediately via the double-sample
	// flap-damp rule — matches the MQTT-disconnected handling.
	tracker.Record(HealthComponentName, health.Sample{
		Healthy: false,
		Note:    "bridge listener not bound",
	})
	tracker.Record(HealthComponentName, health.Sample{
		Healthy: false,
		Note:    "bridge listener not bound (escalated)",
	})
}
