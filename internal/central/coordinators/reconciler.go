// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// InterfaceReachability is one (interface_id, reachable) pair returned
// by the [ConnectivityProbe].
type InterfaceReachability struct {
	// InterfaceID is the identifier every consuming surface keys on. It must
	// be the same id GET /interfaces reports (the wire form
	// `<central>-<interface>`), because the client cross-references the two:
	// it builds the connectivity sensors from /interfaces.id and looks their
	// value up by this id. A probe that only knows the bare interface name
	// sets Interface to the enum and lets the adapter stamp the wire id here.
	InterfaceID string
	// Interface is the typed enum for the interface, when the producer knows
	// it independently of InterfaceID. Empty means "derive it from
	// InterfaceID" — correct only while InterfaceID is a bare interface name.
	Interface hmenum.Interface
	Reachable bool
	// LatencyMs is the round-trip duration measured during the probe, in
	// milliseconds. Zero when the probe implementation does not track
	// latency.
	LatencyMs float64
}

// ResolvedInterface returns the typed interface enum: the explicit Interface
// field when set, otherwise InterfaceID parsed as an interface token (valid
// only when InterfaceID is still a bare name).
func (r InterfaceReachability) ResolvedInterface() hmenum.Interface {
	if r.Interface != "" {
		return r.Interface
	}
	return hmenum.Interface(r.InterfaceID)
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
//
// [Reconciler.Health] is wired by the daemon's job registration
// (cmd/openccu-loom/daemon_jobs.go) for every central: the probe reports the
// central's own health-tracker aggregate score, or -1 before the first
// heartbeat lands, which reconcileSystemHealth skips rather than observing
// a spurious 0.
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
	CentralName string

	// HubModel is the live hub. The connectivity aggregate is resolved from
	// it on every tick rather than captured once, because the aggregate does
	// not exist yet when the reconcile job is registered: WireHub creates it
	// during the readiness-gated south-bound bring-up, well after
	// registerStandardJobsFor builds this struct. A captured pointer is nil
	// forever, and reconcileConnectivity then returns early on every tick —
	// silently, since a dead reconcile pass looks exactly like a clean one.
	HubModel *hub.Hub

	// Connectivity is a direct override for callers that have an aggregate
	// but no Hub. HubModel wins when both are set and the Hub has one.
	Connectivity *hub.Connectivity

	Metrics *hub.Metrics
	Connect ConnectivityProbe
	Health  SystemHealthProbe

	// Unobserved is the optional runner that walks devices for DPs
	// that are still unobserved and re-attempts a LoadValue. Nil
	// disables the sweep stage entirely; a non-nil runner is invoked
	// on every Reconcile tick after the connectivity / health probes.
	Unobserved UnobservedSweepRunner

	Bus      *events.Bus
	Recorder observability.Recorder

	// connectMu guards Connect against the background WireHub recovery, which
	// re-assigns the probe after a transient boot-time hub failure while the
	// reconcile job may concurrently read it. Use SetConnect / connectProbe.
	connectMu sync.RWMutex

	// probedMu guards lastProbed, the interface-id set of the most recent
	// informative probe answer. reconcileConnectivity diffs the current
	// answer against it to detect interfaces that vanished.
	probedMu   sync.Mutex
	lastProbed map[string]bool
}

// SetConnect wires (or re-wires) the connectivity probe under connectMu. Use
// this instead of assigning the exported Connect field directly when the
// wiring can run concurrently with a reconcile tick.
func (r *Reconciler) SetConnect(p ConnectivityProbe) {
	r.connectMu.Lock()
	defer r.connectMu.Unlock()
	r.Connect = p
}

func (r *Reconciler) connectProbe() ConnectivityProbe {
	r.connectMu.RLock()
	defer r.connectMu.RUnlock()
	return r.Connect
}

// connectivityAggregate resolves the aggregate for this tick, preferring the
// live Hub over any directly-supplied value. Resolving per tick is what keeps
// the reconcile pass alive when the aggregate is wired after job registration.
func (r *Reconciler) connectivityAggregate() *hub.Connectivity {
	if r.HubModel != nil {
		if c := r.HubModel.ConnectivityDataPoints(); c != nil {
			return c
		}
	}
	return r.Connectivity
}

// Reconcile fetches the CCU's authoritative state and reconciles the
// in-memory caches. Returns the first error encountered; subsequent
// probes still execute so a partial drift can be corrected.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if r == nil {
		return errors.New("reconciler: nil receiver")
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
	connect := r.connectProbe()
	conn := r.connectivityAggregate()
	if connect == nil || conn == nil {
		return nil
	}
	probed, err := connect.Probe(ctx)
	if err != nil {
		return fmt.Errorf("reconciler: connectivity probe: %w", err)
	}
	for _, p := range probed {
		cached, observed := conn.Reachable(p.InterfaceID)
		drifted := !observed || cached != p.Reachable
		// Propagate the typed Interface enum so downstream consumers
		// (REST, MQTT, Discovery) can render `interface_id` AND
		// `interface` (e.g. "BidCos-RF" enum). The enum comes from the
		// probe entry, not from parsing InterfaceID, because InterfaceID
		// now carries the wire form `<central>-<interface>`.
		conn.OnStateWithInterface(p.InterfaceID, p.ResolvedInterface(), p.Reachable)
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
	r.reconcileVanishedInterfaces(conn, probed)
	return nil
}

// reconcileVanishedInterfaces marks every interface the previous probe
// listed and the current one no longer does as unreachable.
//
// The CCU answers with the interfaces it currently serves, so an entry
// that stops appearing is gone rather than unchanged. Reconciling the
// returned entries alone left it reporting its last known state until it
// came back — a radio that dies mid-flight kept every sensor behind it
// looking alive on MQTT, on the WebSocket topic and, worst, in the alarm
// domain's sensor-availability view.
//
// Two answers carry no membership information and must not produce down
// events: the first answer of the process (nothing to diff against) and
// an empty answer, which is what an unreachable or rebooting CCU
// produces. Reading either as "every interface went down" would run the
// alarm domain's central-loss escalation on every CCU restart, so an
// empty answer leaves the previous membership in place instead.
func (r *Reconciler) reconcileVanishedInterfaces(conn *hub.Connectivity, probed []InterfaceReachability) {
	current := make(map[string]bool, len(probed))
	for _, p := range probed {
		current[p.InterfaceID] = true
	}
	r.probedMu.Lock()
	previous := r.lastProbed
	if len(current) > 0 {
		r.lastProbed = current
	}
	r.probedMu.Unlock()
	if len(current) == 0 || previous == nil {
		return
	}

	vanished := make([]string, 0, len(previous))
	for id := range previous {
		if !current[id] {
			vanished = append(vanished, id)
		}
	}
	// Map iteration is unordered; a stable event order keeps the
	// north-bound sequence reproducible when several interfaces go at
	// once.
	slices.Sort(vanished)

	for _, id := range vanished {
		cached, observed := conn.Reachable(id)
		// The interface keeps its tracker entry with Reachable=false
		// instead of dropping out of it, so the north-bound surfaces
		// show it as down rather than as never-seen.
		conn.OnStateWithInterface(id, hmenum.Interface(id), false)
		if observed && !cached {
			continue
		}
		if r.Bus == nil {
			continue
		}
		events.Publish(r.Bus, hmevent.ConnectivityChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: r.CentralName,
			InterfaceID: id,
			Reachable:   false,
		})
		events.Publish(r.Bus, hmevent.DriftCorrectedEvent{
			Base:        hmevent.NewBase(),
			CentralName: r.CentralName,
			Component:   "connectivity",
			Detail:      fmt.Sprintf("%s absent from probe observed=%v cached=%v -> false", id, observed, cached),
		})
	}
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

// EmitNotReady demotes the north-bound state of a central whose south-bound
// bring-up is down (FAILED / not operational), so its surfaces stop
// advertising the last live readings as if current.
//
// The scheduled Reconcile pass is the only producer of the system_health
// metric and the per-interface connectivity aggregate, and it is gated on the
// central being operational. During a total CCU outage the central is FAILED,
// the pass never runs, and the HA "Systemzustand" sensor plus every
// connectivity binary_sensor freeze at their last reading. This runs in the
// pass's place while the central is not operational. It never probes the
// (unreachable) CCU — it only demotes what the probes can no longer confirm:
//
//   - system_health: a previously observed score is replaced with the negative
//     not-ready sentinel [hub.MetricSystemHealthUnknown], which the consumers
//     render as "unknown". A metric that was never observed stays unobserved —
//     no spurious value (a 0 would read as "0% healthy") is manufactured.
//   - connectivity: every interface still marked reachable is set unreachable,
//     the honest state while the CCU is down. Those events carry
//     [hmevent.ConnectivityChangedEvent.Unconfirmed], because nothing
//     confirmed them: they are derived from the central's own state, not
//     from the CCU's interface list. The same reason
//     reconcileVanishedInterfaces refuses to read an empty probe answer as a
//     down burst applies here — a consumer that escalates physically must
//     not act on a CCU that is merely away, or a maintenance reboot sounds
//     the siren.
func (r *Reconciler) EmitNotReady(_ context.Context) error {
	if r == nil {
		return errors.New("reconciler: nil receiver")
	}
	r.emitNotReadyHealth()
	r.emitNotReadyConnectivity()
	return nil
}

func (r *Reconciler) emitNotReadyHealth() {
	if r.Metrics == nil {
		return
	}
	cached, observed := r.Metrics.Value(hub.MetricSystemHealth)
	// Never observed → stay unknown (unobserved); already at the sentinel →
	// nothing to demote.
	if !observed || cached.Value < 0 {
		return
	}
	if r.Metrics.Observe(hub.MetricSystemHealth, hub.MetricSystemHealthUnknown) && r.Bus != nil {
		events.Publish(r.Bus, hmevent.DriftCorrectedEvent{
			Base:        hmevent.NewBase(),
			CentralName: r.CentralName,
			Component:   "system_health",
			Detail:      fmt.Sprintf("central not ready: %v -> unknown", cached.Value),
		})
	}
}

func (r *Reconciler) emitNotReadyConnectivity() {
	conn := r.connectivityAggregate()
	if conn == nil {
		return
	}
	for _, entry := range conn.List() {
		if !entry.Reachable {
			continue
		}
		conn.OnStateWithInterface(entry.InterfaceID, entry.ResolvedInterface(), false)
		if r.Bus != nil {
			events.Publish(r.Bus, hmevent.ConnectivityChangedEvent{
				Base:        hmevent.NewBase(),
				CentralName: r.CentralName,
				InterfaceID: entry.InterfaceID,
				Reachable:   false,
				Unconfirmed: true,
			})
		}
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
