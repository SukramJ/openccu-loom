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
//   - ping/pong mismatches are recorded as incidents.
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
		func(e hmevent.CircuitBreakerStateChangedEvent) { events.Publish(bus, e) },
	), name, interfaceID)
	reliability.WireCircuitIncidents(ic.Circuit(), rec, name, interfaceID)
	reliability.WireCoalesceBus(ic.Coalescer(), reliability.CoalesceEventPublisherFunc(
		func(e hmevent.RequestCoalescedEvent) { events.Publish(bus, e) },
	), name, interfaceID)
	reliability.WirePingPongIncidents(ic.PingPong(), rec, name, interfaceID)
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
