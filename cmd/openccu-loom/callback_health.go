// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

const (
	// callbackHealthComponent is the /health component reporting whether the
	// callback listeners came up. Both transports share one component: what
	// an operator needs to know is "can this daemon receive pushes at all",
	// and the note names which listener is down.
	callbackHealthComponent = "callback.listeners"
	// callbackListenerDownMetric is 1 when at least one callback listener
	// failed to bind, else 0.
	callbackListenerDownMetric = "callback_listener_down"
)

// recordCallbackHealth surfaces whether the daemon's push path exists.
//
// A callback listener that cannot bind is deliberately non-fatal: the daemon
// still serves REST, the UI and the config surface, which is what an operator
// needs in order to fix the conflict. What was missing is that the condition
// was invisible — one WARN line at boot, after which the daemon reported
// `/health` 200 and every central `readiness.ready`, while no CCU event could
// ever arrive. A dead push path that looks identical to a live one is the
// failure this records.
//
// The component is not critical and not an interface, so it collapses to
// `degraded` in [health.ServiceAvailability]: the body says so and the SPA
// shows it, while the status code stays 200. A daemon that can still be
// reconfigured is not one a load balancer should drain.
//
// Recorded once at boot with Sticky set, for the same reason as
// [recordSecretHealth]: it is a one-shot fact with no heartbeat behind it, and
// without Sticky the tracker's staleness decay would turn it into
// StatusUnknown 90 s later.
func recordCallbackHealth(tracker *health.Tracker, reg *metrics.Registry, xmlrpcErr, binrpcErr error) {
	down := xmlrpcErr != nil || binrpcErr != nil
	if tracker != nil {
		sample := health.Sample{Healthy: !down, Sticky: true}
		switch {
		case xmlrpcErr != nil && binrpcErr != nil:
			sample.Note = fmt.Sprintf("no callback listener bound — XML-RPC: %v; BIN-RPC: %v", xmlrpcErr, binrpcErr)
		case xmlrpcErr != nil:
			sample.Note = fmt.Sprintf("XML-RPC callback listener down — no CCU value-change event can arrive: %v", xmlrpcErr)
		case binrpcErr != nil:
			sample.Note = fmt.Sprintf("BIN-RPC callback listener down — no CUxD event can arrive: %v", binrpcErr)
		default:
			sample.Note = callbackListenersUpNote
		}
		tracker.Record(callbackHealthComponent, sample)
	}
	if reg != nil {
		g := reg.Gauge(callbackListenerDownMetric,
			"1 when at least one callback listener failed to bind (the daemon cannot receive pushes), else 0")
		if down {
			g.Set(1)
		} else {
			g.Set(0)
		}
	}
}

// callbackListenersUpNote is the healthy note, kept as a constant because
// health.NoteKeys maps static notes to their i18n key by exact string.
const callbackListenersUpNote = "callback listeners bound"
