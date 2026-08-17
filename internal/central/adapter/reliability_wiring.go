// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// cacheIncidentRecorder resolves the central's incident recorder at
// record time instead of capture time. The persistent recorder is
// installed on the CacheCoordinator during daemon boot and may be
// (re)wired after southbound clients are built, so reliability hooks
// must not bind the concrete recorder when they are installed. A nil
// cache or an uninstalled recorder degrades to a silent no-op —
// incidents are best-effort by contract.
type cacheIncidentRecorder struct {
	cache *coordinators.CacheCoordinator
}

// RecordIncident implements [reliability.IncidentRecorder].
func (r cacheIncidentRecorder) RecordIncident(ctx context.Context, inc reliability.IncidentRecord) error {
	if r.cache == nil {
		return nil
	}
	rec := r.cache.GetIncidentRecorder()
	if rec == nil {
		return nil
	}
	return rec.RecordIncident(ctx, inc)
}

// wireClientReliability installs the observability hooks on one
// interface client's reliability primitives:
//
//   - circuit-breaker transitions are published as
//     [hmevent.CircuitBreakerStateChangedEvent] on the central's bus
//     (consumed by connection recovery, health wiring and the
//     diagnostics event tap) and recorded as incidents,
//   - coalesced request followers are published as
//     [hmevent.RequestCoalescedEvent] for the diagnostics event tap,
//   - ping/pong mismatches are recorded as incidents,
//   - transitions and coalesced calls are counted on the central's
//     metrics observer, which is what the diagnostics `rpc` section
//     renders. The per-call half of that section comes from
//     [newRPCOutcomeHook], installed on the client itself.
//
// Call it once per client, right after construction; the hooks live
// as long as the client.
func wireClientReliability(unit *central.Unit, ic *client.InterfaceClient, interfaceID string) {
	if unit == nil || ic == nil {
		return
	}
	bus := unit.EventBus
	name := unit.Name()
	rec := cacheIncidentRecorder{cache: unit.Cache}

	reliability.WireCircuitBus(ic.Circuit(), reliability.CircuitEventPublisherFunc(
		func(e hmevent.CircuitBreakerStateChangedEvent) {
			events.Publish(bus, e)
			if obs := unitObserver(unit); obs != nil {
				obs.ObserveCounter(metrics.MetricKeys.CircuitStateTransition(interfaceID).String(), 1)
			}
		},
	), name, interfaceID)
	reliability.WireCircuitIncidents(ic.Circuit(), rec, name, interfaceID)
	reliability.WireCoalesceBus(ic.Coalescer(), reliability.CoalesceEventPublisherFunc(
		func(e hmevent.RequestCoalescedEvent) {
			events.Publish(bus, e)
			if obs := unitObserver(unit); obs != nil {
				// The event names how many followers the leader absorbed;
				// the counter tracks calls saved, not coalesce groups.
				obs.ObserveCounter(metrics.MetricKeys.CoalescerCoalesced(interfaceID).String(), int64(max(e.Waiters, 1)))
			}
		},
	), name, interfaceID)
	reliability.WirePingPongIncidents(ic.PingPong(), rec, name, interfaceID)
}

// newRPCOutcomeHook returns the [client.Config.RPCOutcomeHook] that feeds
// one interface's outcomes into its central's metrics observer.
//
// The observer is resolved per call rather than captured: the aggregator
// is attached to the central during boot wiring, which can run after the
// interface clients are built, so a hook that bound it here would stay
// nil for the life of the daemon.
func newRPCOutcomeHook(unit *central.Unit, interfaceID string) func(string, time.Duration, client.RPCOutcome) {
	if unit == nil {
		return nil
	}
	return func(method string, duration time.Duration, outcome client.RPCOutcome) {
		obs := unitObserver(unit)
		if obs == nil {
			return
		}
		ms := float64(duration.Nanoseconds()) / float64(time.Millisecond)
		// A rejected call never reached the CCU, and an ignored one carries
		// an error that is not evidence about the CCU either (a caller
		// cancellation, a permanent semantic fault, or the daemon shedding
		// its own load) — neither belongs in the per-method service
		// durations, which is what the diagnostics `rpc` section renders as
		// service health.
		if outcome != client.RPCOutcomeRejected && outcome != client.RPCOutcomeIgnored {
			obs.ObserveService(method, ms, outcome == client.RPCOutcomeFailed)
		}
		switch outcome {
		case client.RPCOutcomeFailed:
			obs.ObserveCounter(metrics.MetricKeys.CircuitFailure(interfaceID).String(), 1)
		case client.RPCOutcomeRejected:
			obs.ObserveCounter(metrics.MetricKeys.CircuitRejection(interfaceID).String(), 1)
		case client.RPCOutcomeIgnored, client.RPCOutcomeSuccess:
			// Neither is failure evidence about the wire; no counter to bump.
		}
	}
}

// unitObserver resolves the central's metrics observation sink, or nil
// when no aggregator has been attached yet.
func unitObserver(unit *central.Unit) *metrics.Observer {
	if unit == nil || unit.Aggregator == nil {
		return nil
	}
	return unit.Aggregator.Observer()
}

// newClientRetrier builds the retrier for one interface client with
// the exhausted-chain incident sink installed. initial <= 0 keeps the
// package default backoff.
func newClientRetrier(unit *central.Unit, interfaceID string, initial time.Duration) *reliability.Retrier {
	cfg := reliability.RetryConfig{}
	if initial > 0 {
		cfg.Initial = initial
	}
	if unit != nil {
		cfg.IncidentSink = reliability.WireRetryIncidents(
			cacheIncidentRecorder{cache: unit.Cache}, unit.Name(), interfaceID,
		)
	}
	return reliability.NewRetrier(cfg)
}
