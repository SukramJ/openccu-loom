// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// WireHealth subscribes the central's [health.Tracker] to the event bus so
// the per-interface component status updates automatically as the southbound
// layer reports incidents.
//
// Mapping (component name = interface ID):
//
// ClientStateChangedEvent → healthy / degraded / unhealthy by To-state
// ConnectionLostEvent → unhealthy CircuitBreakerStateChangedEvent → CLOSED
// healthy / HALF_OPEN degraded / OPEN unhealthy RecoveryStartedEvent →
// degraded (recovery in progress) RecoveryCompletedEvent → healthy on
// success, unhealthy on failure result RecoveryFailedEvent → unhealthy
// PingPongMismatchEvent → degraded on a SEPARATE `ping_pong/<interfaceID>`
// component (kept off the interface's liveness entry so correlation noise
// cannot drive the service-availability verdict to 503)
//
// After every Record call a [hmevent.ConnectionHealthChangedEvent] is
// published on the bus as recovery telemetry. It has no consumer today, and
// that is deliberate: every north-bound health surface reads the tracker this
// function feeds, which is the authoritative verdict — the event only repeats
// what the tracker already knows. The silence is declared in the contract
// suite's consumerless-event ratchet; a future subscriber that needs
// FailureReason / ConsecutiveFailures has to fill them here first, because the
// publish below leaves both at their zero value.
//
// Returns a closer that drops every subscription. Safe to call multiple times
// — the registered closures are idempotent.
func WireHealth(unit *central.Unit) func() { //nolint:funlen // composition/wiring: long sequential setup
	if unit == nil || unit.EventBus == nil || unit.Health == nil {
		return func() {}
	}
	tr := unit.Health
	bus := unit.EventBus
	centralName := unit.Name()

	component := func(interfaceID string) string {
		if interfaceID == "" {
			return "unknown"
		}
		return interfaceID
	}
	// reportUnhealthy states an unhealthy condition rather than sampling one:
	// a client that failed or stopped, a breaker that opened, a recovery that
	// gave up. The tracker's flap-damping is right for a probe and wrong here,
	// and these sites used to force it by recording twice with an invented
	// "(escalated)" note — re-encoding the tracker's threshold at the call
	// site and leaving a history sample for something that never happened.
	reportUnhealthy := func(interfaceID, note string) {
		tr.RecordUnhealthy(component(interfaceID), health.Sample{Note: note, NoteKey: health.NoteKeyFor(note)})
		events.Publish(bus, hmevent.ConnectionHealthChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: centralName,
			InterfaceID: interfaceID,
			IsHealthy:   false,
		})
	}

	record := func(interfaceID string, healthy bool, note string) {
		tr.Record(component(interfaceID), health.Sample{Healthy: healthy, Note: note, NoteKey: health.NoteKeyFor(note)})
		// Recovery telemetry for the bus; the tracker above is what every
		// health surface reads. See the doc comment on WireHealth.
		events.Publish(bus, hmevent.ConnectionHealthChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: centralName,
			InterfaceID: interfaceID,
			IsHealthy:   healthy,
		})
	}

	// Initial sync: seed the tracker with each client's current state so the
	// health view is accurate immediately after wiring, without waiting for the
	// first event. this one-shot pass closes the startup gap where all
	// components would appear StatusUnknown until the first callback arrives.
	if unit.Clients != nil {
		for _, entry := range unit.Clients.List() {
			healthy := entry.Connected()
			note := "initial-sync: connected"
			if !healthy {
				note = "initial-sync: not connected"
			}
			record(entry.InterfaceID, healthy, note)
		}
	}

	unsubs := []func(){
		events.Subscribe(bus, func(e hmevent.ClientStateChangedEvent) {
			switch e.To { //nolint:exhaustive // Created/Initializing/Initialized/Stopping are transient states that don't affect health records
			case hmenum.ClientStateConnected:
				record(e.InterfaceID, true, "client connected")
				// A reconnect chain that ends in Connected clears the
				// attempt counter — reset on every successful client
				// state transition so the reconnect history reflects
				// only the current failure window.
				tr.ResetReconnects(e.InterfaceID)
			case hmenum.ClientStateReconnecting,
				hmenum.ClientStateConnecting,
				hmenum.ClientStateDisconnected:
				record(e.InterfaceID, false, fmt.Sprintf("client %s", e.To))
			case hmenum.ClientStateFailed,
				hmenum.ClientStateStopped:
				reportUnhealthy(e.InterfaceID, fmt.Sprintf("client %s", e.To))
			}
		}),

		events.Subscribe(bus, func(e hmevent.ConnectionLostEvent) {
			record(e.InterfaceID, false, fmt.Sprintf("connection lost: %s", e.Reason))
		}),

		events.Subscribe(bus, func(e hmevent.CircuitBreakerStateChangedEvent) {
			switch e.To {
			case hmenum.CircuitStateClosed:
				record(e.InterfaceID, true, "breaker closed")
			case hmenum.CircuitStateHalfOpen:
				record(e.InterfaceID, false, "breaker half-open")
			case hmenum.CircuitStateOpen:
				reportUnhealthy(e.InterfaceID, "breaker open")
			}
		}),

		events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) {
			record(e.InterfaceID, false, "recovery started")
			tr.SetRecoveryFlag(e.InterfaceID, true)
		}),

		events.Subscribe(bus, func(e hmevent.RecoveryCompletedEvent) {
			if e.Result == hmenum.RecoveryResultSuccess {
				record(e.InterfaceID, true, "recovery completed")
			} else {
				record(e.InterfaceID, false, fmt.Sprintf("recovery completed: %s", e.Result))
			}
			tr.SetRecoveryFlag(e.InterfaceID, false)
		}),

		events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) {
			reportUnhealthy(e.InterfaceID, fmt.Sprintf("recovery failed: %s (attempts=%d)", e.Reason, e.Attempts))
			tr.SetRecoveryFlag(e.InterfaceID, false)
		}),

		events.Subscribe(bus, func(e hmevent.PingPongMismatchEvent) {
			// Record on a SEPARATE quality component, not the interface's
			// liveness entry, and via RecordQuality so it caps at DEGRADED: a
			// ping/pong mismatch (often orphan PONGs broadcast by a co-located
			// daemon on the same CCU) must neither escalate the interface to
			// UNHEALTHY and trip the "every interface down → 503" rule in
			// health.ServiceAvailability, nor cascade the central state to
			// failed. See [health.PingPongComponent] / [health.Tracker.RecordQuality].
			tr.RecordQuality(health.PingPongComponent(component(e.InterfaceID)),
				fmt.Sprintf("ping/pong mismatch: %s pending=%d unknown=%d",
					e.MismatchType, e.PendingCount, e.UnknownCount))
		}),

		// record last-event timestamp on every push-event so the
		// health tracker can expose "last event received N s ago" to the UI.
		// DataPointValueReceivedEvent carries CentralName + InterfaceID so we
		// can do a central-scoped filter and record per-interface activity.
		events.Subscribe(bus, func(e hmevent.DataPointValueReceivedEvent) {
			if e.CentralName != centralName {
				return
			}
			id := e.InterfaceID
			if id == "" {
				id = e.ChannelAddress
			}
			if id != "" {
				tr.RecordEventReceived(id)
			}
		}),

		// record reconnect attempts so the tracker surfaces
		// reconnect-attempt counts in MetricsHealthSummary.
		events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) {
			if e.CentralName != centralName {
				return
			}
			tr.RecordReconnectAttempt(e.InterfaceID)
		}),
	}

	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}
