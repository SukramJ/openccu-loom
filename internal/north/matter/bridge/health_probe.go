// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"fmt"
	"time"
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

// HealthSample is everything this probe has to say about the bridge: a
// verdict and the stable, un-localized machine string that explains it.
// It is deliberately narrower than the daemon's tracker sample — the
// probe never sets a catalogue key, a timestamp or the staleness
// exemption — so the Matter subtree needs no dependency on the tracker
// package. The host converts on the way in.
type HealthSample struct {
	Healthy bool
	// Note is the stable, English machine string. The tracker's scoring
	// treats it as a sentinel, so it must stay stable and un-localized.
	Note string
}

// HealthRecorder is the slim tracker contract the probe touches. The
// daemon's health tracker satisfies it through a host-side adapter that
// translates [HealthSample] into the tracker's own sample type.
type HealthRecorder interface {
	Record(name string, sample HealthSample)
	// RecordUnhealthy states an unhealthy condition rather than sampling one,
	// so it takes effect without the flap-damping Record applies.
	RecordUnhealthy(name string, sample HealthSample)
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
		tracker.Record(HealthComponentName, HealthSample{
			Healthy: true,
			Note:    fmt.Sprintf("listening (addr=%s)", addr),
		})
		return
	}
	// Listener gone. Escalate immediately via the double-sample
	// flap-damp rule — matches the MQTT-disconnected handling.
	// A listener that is not bound is a condition, not a sample, so it is
	// reported rather than probed — this used to record twice with an
	// invented "(escalated)" note to get past the tracker's flap-damping.
	tracker.RecordUnhealthy(HealthComponentName, HealthSample{
		Note: "bridge listener not bound",
	})
}
