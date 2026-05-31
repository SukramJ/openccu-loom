// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// CircuitEventPublisher is the minimal contract every event sink must
// satisfy to receive [hmevent.CircuitBreakerStateChangedEvent] notices
// from a [CircuitBreaker]. The internal/central/events.Bus satisfies
// this interface via its top-level [events.Publish] function — the
// caller wraps it with a tiny adapter so this package stays free of
// dependencies on the central event bus.
type CircuitEventPublisher interface {
	PublishCircuitBreakerStateChange(e hmevent.CircuitBreakerStateChangedEvent)
}

// CircuitEventPublisherFunc adapts a plain function into a
// [CircuitEventPublisher].
type CircuitEventPublisherFunc func(e hmevent.CircuitBreakerStateChangedEvent)

// PublishCircuitBreakerStateChange implements [CircuitEventPublisher].
func (f CircuitEventPublisherFunc) PublishCircuitBreakerStateChange(e hmevent.CircuitBreakerStateChangedEvent) {
	f(e)
}

// CoalesceEventPublisher is the minimal contract event sinks must
// satisfy to receive [hmevent.RequestCoalescedEvent] notices.
type CoalesceEventPublisher interface {
	PublishRequestCoalesced(e hmevent.RequestCoalescedEvent)
}

// CoalesceEventPublisherFunc adapts a plain function into a
// [CoalesceEventPublisher].
type CoalesceEventPublisherFunc func(e hmevent.RequestCoalescedEvent)

// PublishRequestCoalesced implements [CoalesceEventPublisher].
func (f CoalesceEventPublisherFunc) PublishRequestCoalesced(e hmevent.RequestCoalescedEvent) {
	f(e)
}

// WireCoalesceBus installs a [Coalescer.SetHook] callback that
// publishes a [hmevent.RequestCoalescedEvent] for every coalesced
// follower. Pass nil publisher to disable. Replaces an existing hook
// — a coalescer has at most one hook at a time.
//
// `central` and `iface` are baked into every event so subscribers
// can route by interface.
func WireCoalesceBus(co *Coalescer, pub CoalesceEventPublisher, central, iface string) {
	if co == nil {
		return
	}
	if pub == nil {
		co.SetHook(nil)
		return
	}
	co.SetHook(func(key string, waiters int) {
		pub.PublishRequestCoalesced(hmevent.RequestCoalescedEvent{
			Base:        hmevent.NewBase(),
			CentralName: central,
			InterfaceID: iface,
			Key:         key,
			Waiters:     waiters,
		})
	})
}

// WireCircuitBus installs an [CircuitBreaker.AddOnStateChange]
// callback that publishes a [hmevent.CircuitBreakerStateChangedEvent]
// for every transition. Pass nil publisher to disable. Coexists with
// [WireCircuitIncidents] and the retry-recovery hook because all bus
// subscribers go through `AddOnStateChange`.
//
// `central` and `iface` are baked into every event so subscribers
// (e.g. `WireHealth`) can route by interface.
func WireCircuitBus(cb *CircuitBreaker, pub CircuitEventPublisher, central, iface string) {
	if cb == nil || pub == nil {
		return
	}
	cb.AddOnStateChange(func(from, to hmenum.CircuitState) {
		pub.PublishCircuitBreakerStateChange(hmevent.CircuitBreakerStateChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: central,
			InterfaceID: iface,
			From:        from,
			To:          to,
		})
	})
}

// IncidentRecorder is the contract every reliability primitive uses to log an
// incident into a persistent backend. The store/sqlite IncidentStore
// satisfies the [Recorder] shape via the small [IncidentRecord] adapter
// (`reliability.NewSQLiteRecorder`).
//
// In Go the contract is synchronous but the recorder may dispatch
// asynchronously internally.
type IncidentRecorder interface {
	// RecordIncident persists one incident. The implementation is
	// expected to handle deduplication (e.g. SQLite BumpIfRecent).
	// Errors are logged but not propagated — incidents are best-effort.
	RecordIncident(ctx context.Context, inc IncidentRecord) error
}

// IncidentRecord is the wire-format-neutral payload an
// [IncidentRecorder] consumes. Source-specific helpers (e.g.
// `WireCircuitIncidents`) build these and pass them on.
type IncidentRecord struct {
	CentralName string
	InterfaceID string
	Type        hmenum.IncidentType
	Severity    hmenum.IncidentSeverity
	Message     string
	Details     string
}

// WirePingPongIncidents installs a [PingPongTracker.SetMismatchHook]
// that records every mismatch as an incident. Pass nil to disable.
//
// Severity defaults to Warning for pending-mismatches (PING sent but
// no PONG within TTL) and Error for unknown-mismatches (PONG arrived
// without a matching PING — possible CCU restart).
func WirePingPongIncidents(t *PingPongTracker, rec IncidentRecorder, central, iface string) {
	if t == nil || rec == nil {
		return
	}
	t.SetMismatchHook(func(m Mismatch) {
		sev := hmenum.IncidentSeverityWarning
		msg := "ping/pong: pending TTL exceeded"
		if m.Kind == hmenum.PingPongMismatchUnknown {
			// PingPongMismatchPending is handled by the default severity/message above.
			sev = hmenum.IncidentSeverityError
			msg = "ping/pong: unknown PONG (CCU restart?)"
		}
		_ = rec.RecordIncident(context.Background(), IncidentRecord{
			CentralName: central,
			InterfaceID: iface,
			Type:        hmenum.IncidentTypePingPongMismatch,
			Severity:    sev,
			Message:     msg,
			Details:     m.ID,
		})
	})
}

// IncidentSink is the narrow write contract the [Retrier] uses to report
// exhausted retry chains. It is satisfied by any [IncidentRecorder] wrapper
// that supplies the central/interface routing metadata — see [WireRetryIncidents].
type IncidentSink interface {
	// ReportRetryExhausted is called once per exhausted chain, after all
	// MaxAttempts have been consumed without success. err is the last error
	// returned by the user-supplied function. The implementation is expected
	// to be non-blocking (best-effort; errors are silently discarded).
	ReportRetryExhausted(err error)
}

// IncidentSinkFunc adapts a plain function into an [IncidentSink].
type IncidentSinkFunc func(err error)

// ReportRetryExhausted implements [IncidentSink].
func (f IncidentSinkFunc) ReportRetryExhausted(err error) { f(err) }

// WireRetryIncidents returns an [IncidentSink] that records exhausted retry
// chains as [hmenum.IncidentTypeRetryExhausted] incidents via rec.
// Pass nil rec to get a no-op sink.
func WireRetryIncidents(rec IncidentRecorder, central, iface string) IncidentSink {
	if rec == nil {
		return nil
	}
	return IncidentSinkFunc(func(err error) {
		msg := "retry: all attempts exhausted"
		if err != nil {
			msg = fmt.Sprintf("retry: all attempts exhausted: %s", err.Error())
		}
		_ = rec.RecordIncident(context.Background(), IncidentRecord{
			CentralName: central,
			InterfaceID: iface,
			Type:        hmenum.IncidentTypeRetryExhausted,
			Severity:    hmenum.IncidentSeverityWarning,
			Message:     msg,
		})
	})
}

// WireCircuitIncidents installs an [CircuitBreaker.OnStateChange]
// callback that turns every transition into an incident on `rec`. The
// `central` / `iface` strings are baked into every record so the
// caller does not have to thread them through. Pass a nil recorder
// to disable.
//
// The hook is fire-and-forget: incident-recording errors are
// swallowed (the circuit breaker must not be coupled to the DB
// availability).
func WireCircuitIncidents(cb *CircuitBreaker, rec IncidentRecorder, central, iface string) {
	if cb == nil || rec == nil {
		return
	}
	cb.AddOnStateChange(func(from, to hmenum.CircuitState) {
		var sev hmenum.IncidentSeverity
		var msg string
		var incType hmenum.IncidentType
		switch to {
		case hmenum.CircuitStateOpen:
			sev = hmenum.IncidentSeverityError
			msg = fmt.Sprintf("circuit-breaker tripped: %s → %s", from, to)
			// use the specific CIRCUIT_BREAKER_TRIPPED incident type when the breaker
			// trips open.
			incType = hmenum.IncidentTypeCircuitBreakerTripped
		case hmenum.CircuitStateHalfOpen:
			sev = hmenum.IncidentSeverityWarning
			msg = fmt.Sprintf("circuit-breaker probing: %s → %s", from, to)
			incType = hmenum.IncidentTypeCircuitBreakerOpen
		case hmenum.CircuitStateClosed:
			sev = hmenum.IncidentSeverityInfo
			msg = fmt.Sprintf("circuit-breaker recovered: %s → %s", from, to)
			// use the specific CIRCUIT_BREAKER_RECOVERED incident type when the
			// breaker closes.
			incType = hmenum.IncidentTypeCircuitBreakerRecovered
		default:
			incType = hmenum.IncidentTypeCircuitBreakerOpen
		}
		_ = rec.RecordIncident(context.Background(), IncidentRecord{
			CentralName: central,
			InterfaceID: iface,
			Type:        incType,
			Severity:    sev,
			Message:     msg,
		})
	})
}
