// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// InterfaceReachability is one (interface_id, reachable) pair returned
// by the [ConnectivityProbe].
type InterfaceReachability struct {
	InterfaceID string
	Reachable   bool
	// LatencyMs is the round-trip duration measured during the probe, in
	// milliseconds. Zero when the probe implementation does not track
	// latency.
	LatencyMs float64
}

// ConnectivityProbe is the south-bound contract the [Reconciler] calls
// to obtain the CCU's authoritative interface-reachability snapshot.
// Implementations typically wrap a JSON-RPC `Interface.listInterfaces`
// or BidCos `listBidcosInterfaces` call.
type ConnectivityProbe interface {
	Probe(ctx context.Context) ([]InterfaceReachability, error)
}

// SystemHealthProbe queries the CCU's `systemHealth` metric (0..100).
// Returning -1 means the metric is unavailable for this poll cycle
// the Reconciler then leaves the cached value untouched.
type SystemHealthProbe interface {
	Probe(ctx context.Context) (int, error)
}

// UnobservedSweepRunner is the contract the Reconciler calls to drive a
// periodic LoadValue sweep over data points that are still unobserved after
// the daemon's bootstrap pass. Implementations live in the adapter layer
// (where the Device + LoadValue surface is accessible) and apply a whitelist
// — typically RELEVANT_INIT parameters + readable events — so the sweep does
// not hammer the CCU with calls for parameters that may genuinely never have
// a value (event-only DPs that have not fired).
type UnobservedSweepRunner interface {
	SweepUnobserved(ctx context.Context) (loaded, errored int)
}

// UnobservedSweepFunc adapts a free function to [UnobservedSweepRunner].
type UnobservedSweepFunc func(ctx context.Context) (loaded, errored int)

// SweepUnobserved implements [UnobservedSweepRunner].
func (f UnobservedSweepFunc) SweepUnobserved(ctx context.Context) (loaded, errored int) {
	return f(ctx)
}

// ProbeFunc adapts a free function to [ConnectivityProbe].
type ProbeFunc func(ctx context.Context) ([]InterfaceReachability, error)

// Probe implements [ConnectivityProbe].
func (f ProbeFunc) Probe(ctx context.Context) ([]InterfaceReachability, error) { return f(ctx) }

// HealthProbeFunc adapts a free function to [SystemHealthProbe].
type HealthProbeFunc func(ctx context.Context) (int, error)

// Probe implements [SystemHealthProbe].
func (f HealthProbeFunc) Probe(ctx context.Context) (int, error) { return f(ctx) }

// Reconciler bridges the gap between push-driven Connectivity
// SystemHealth tracking and the CCU's authoritative state. The
// scheduled Reconcile call probes the CCU on a slow cadence (default
// 5 minutes), compares the result against the cached state, applies
// corrections, and emits [ConnectivityChangedEvent]
// [DriftCorrectedEvent] for any divergence.
//
// Callers wire the Reconciler as a standard job by passing
// [Reconciler.Reconcile] into [central.StandardJobs].
type Reconciler struct {
	CentralName  string
	Connectivity *hub.Connectivity
	Metrics      *hub.Metrics
	Connect      ConnectivityProbe
	Health       SystemHealthProbe

	// Unobserved is the optional runner that walks devices for DPs
	// that are still unobserved and re-attempts a LoadValue. Nil
	// disables the sweep stage entirely; a non-nil runner is invoked
	// on every Reconcile tick after the connectivity / health probes.
	Unobserved UnobservedSweepRunner

	Bus      *events.Bus
	Recorder observability.Recorder
}

// Reconcile fetches the CCU's authoritative state and reconciles the
// in-memory caches. Returns the first error encountered; subsequent
// probes still execute so a partial drift can be corrected.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("reconciler: nil receiver")
	}
	rec := r.Recorder
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	return observability.Instrument(ctx, rec, "reconciler.reconcile", observability.ScopeCoordinator,
		func(ctx context.Context) error {
			var firstErr error
			if err := r.reconcileConnectivity(ctx); err != nil {
				firstErr = err
			}
			if err := r.reconcileSystemHealth(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
			r.reconcileUnobservedDataPoints(ctx)
			return firstErr
		})
}

func (r *Reconciler) reconcileConnectivity(ctx context.Context) error {
	if r.Connect == nil || r.Connectivity == nil {
		return nil
	}
	probed, err := r.Connect.Probe(ctx)
	if err != nil {
		return fmt.Errorf("reconciler: connectivity probe: %w", err)
	}
	for _, p := range probed {
		cached, observed := r.Connectivity.Reachable(p.InterfaceID)
		drifted := !observed || cached != p.Reachable
		// Propagate the typed Interface enum so downstream consumers
		// (REST, MQTT, Discovery) can render `interface_id` AND
		// `interface` (e.g. "BidCos-RF" enum).
		r.Connectivity.OnStateWithInterface(p.InterfaceID, hmenum.Interface(p.InterfaceID), p.Reachable)
		if !drifted {
			continue
		}
		if r.Bus != nil {
			events.Publish(r.Bus, hmevent.ConnectivityChangedEvent{
				Base:        hmevent.NewBase(),
				CentralName: r.CentralName,
				InterfaceID: p.InterfaceID,
				Reachable:   p.Reachable,
				LatencyMs:   p.LatencyMs,
			})
			events.Publish(r.Bus, hmevent.DriftCorrectedEvent{
				Base:        hmevent.NewBase(),
				CentralName: r.CentralName,
				Component:   "connectivity",
				Detail:      fmt.Sprintf("%s observed=%v cached=%v -> %v", p.InterfaceID, observed, cached, p.Reachable),
			})
		}
	}
	return nil
}

// reconcileUnobservedDataPoints invokes the registered
// [UnobservedSweepRunner] (when configured). The runner returns the
// (loaded, errored) tuple so the Reconciler can emit a
// [DriftCorrectedEvent] when at least one DP transitioned from
// unobserved to observed — mirrors the connectivity / health stages.
//
// Errors during individual LoadValue calls inside the runner are not
// propagated up: an event-only DP that has never fired is a normal
// state, not an actionable failure.
func (r *Reconciler) reconcileUnobservedDataPoints(ctx context.Context) {
	if r.Unobserved == nil {
		return
	}
	loaded, errored := r.Unobserved.SweepUnobserved(ctx)
	if loaded == 0 {
		return
	}
	if r.Bus != nil {
		events.Publish(r.Bus, hmevent.DriftCorrectedEvent{
			Base:        hmevent.NewBase(),
			CentralName: r.CentralName,
			Component:   "unobserved_data_points",
			Detail:      fmt.Sprintf("loaded=%d errored=%d", loaded, errored),
		})
	}
}

func (r *Reconciler) reconcileSystemHealth(ctx context.Context) error {
	if r.Health == nil || r.Metrics == nil {
		return nil
	}
	score, err := r.Health.Probe(ctx)
	if err != nil {
		return fmt.Errorf("reconciler: system health probe: %w", err)
	}
	if score < 0 {
		return nil
	}
	cached, observed := r.Metrics.Value(hub.MetricSystemHealth)
	if observed && cached.Value == float64(score) {
		return nil
	}
	r.Metrics.Observe(hub.MetricSystemHealth, float64(score))
	if r.Bus != nil {
		events.Publish(r.Bus, hmevent.DriftCorrectedEvent{
			Base:        hmevent.NewBase(),
			CentralName: r.CentralName,
			Component:   "system_health",
			Detail:      fmt.Sprintf("observed=%v cached=%v -> %d", observed, cached.Value, score),
		})
	}
	return nil
}
