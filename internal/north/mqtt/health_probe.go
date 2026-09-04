// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// HealthComponentName is the tracker component name reported by
// [StartHealthProbe]. Multi-CCU deployments share one MQTT broker so
// the name is intentionally not scoped per central.
const HealthComponentName = "mqtt"

// DefaultProbeInterval matches the SQLite store probe so an operator
// scanning the SPA's Diagnostics view sees both subsystems update on
// the same rhythm. Two missed probes already downgrade the component
// to UNKNOWN via the tracker's StaleAfter rule.
const DefaultProbeInterval = 30 * time.Second

// ConnectionStatus is the minimal contract the probe needs from the
// MQTT client implementation. [*TCPClient] satisfies it; tests can
// drop in a fake without spinning up a real broker.
type ConnectionStatus interface {
	IsConnected() bool
	LastConnectedAt() time.Time
}

// HealthRecorder is the slim tracker contract the probe touches.
type HealthRecorder interface {
	Record(name string, sample health.Sample)
	// RecordUnhealthy states an unhealthy condition rather than sampling one,
	// so it takes effect without the flap-damping Record applies. See
	// [health.Tracker.RecordUnhealthy].
	RecordUnhealthy(name string, sample health.Sample)
}

// StartHealthProbe spawns a goroutine that polls the MQTT client on a
// fixed cadence and reports the connect verdict to tracker. The
// returned stop function cancels the goroutine and waits for it to
// exit.
//
// The probe is silent when tracker or client is nil — callers in
// tests that wire only one of them get a no-op stopper, matching the
// pattern in `internal/store/sqlite/health_probe.go`.
func StartHealthProbe(ctx context.Context, client ConnectionStatus, tracker HealthRecorder, interval time.Duration) func() {
	if client == nil || tracker == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		probeOnce(client, tracker)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				probeOnce(client, tracker)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func probeOnce(client ConnectionStatus, tracker HealthRecorder) {
	if client.IsConnected() {
		uptime := time.Since(client.LastConnectedAt())
		tracker.Record(HealthComponentName, health.Sample{
			Healthy: true,
			Note:    fmt.Sprintf("connected (uptime=%s)", uptime.Truncate(time.Second)),
		})
		return
	}
	// Disconnected — escalate to UNHEALTHY immediately via the
	// double-sample flap-damp rule. The MQTT bridge is either fully
	// online or off; there is no useful middle ground for the
	// diagnostic surface.
	last := client.LastConnectedAt()
	note := "disconnected (never connected)"
	if !last.IsZero() {
		note = fmt.Sprintf("disconnected (last_ok=%s)", last.UTC().Format(time.RFC3339))
	}
	// Reported, not sampled: the client tells us it is disconnected. This used
	// to record twice with an invented "(escalated)" note to clear the
	// tracker's flap-damping.
	tracker.RecordUnhealthy(HealthComponentName, health.Sample{Note: note})
}
