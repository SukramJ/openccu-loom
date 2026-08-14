// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestReconcilerInvokesUnobservedSweepWhenConfigured pins that the new
// reconcileUnobservedDataPoints stage runs on every Reconcile tick when an
// UnobservedSweepRunner is wired.
func TestReconcilerInvokesUnobservedSweepWhenConfigured(t *testing.T) {
	bus := events.NewBus()
	var drifts []hmevent.DriftCorrectedEvent
	defer events.Subscribe(bus, func(e hmevent.DriftCorrectedEvent) { drifts = append(drifts, e) })()

	called := 0
	r := &coordinators.Reconciler{
		CentralName: "ccu1",
		Bus:         bus,
		Unobserved: coordinators.UnobservedSweepFunc(func(_ context.Context) (int, int) {
			called++
			return 3, 1 // 3 loaded, 1 errored
		}),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	if called != 1 {
		t.Errorf("sweep called %d times, want 1", called)
	}
	// Drift event must be emitted because loaded > 0.
	if len(drifts) != 1 {
		t.Fatalf("drift events = %d, want 1", len(drifts))
	}
	if drifts[0].Component != "unobserved_data_points" {
		t.Errorf("drift component = %q, want %q", drifts[0].Component, "unobserved_data_points")
	}
}

// TestReconcilerOmitsDriftWhenSweepLoadedZero verifies the bus stays
// quiet when no DP transitioned from unobserved to observed — a
// reconcile tick with nothing to do should not flood the event bus.
func TestReconcilerOmitsDriftWhenSweepLoadedZero(t *testing.T) {
	bus := events.NewBus()
	var drifts []hmevent.DriftCorrectedEvent
	defer events.Subscribe(bus, func(e hmevent.DriftCorrectedEvent) { drifts = append(drifts, e) })()

	r := &coordinators.Reconciler{
		CentralName: "ccu1",
		Bus:         bus,
		Unobserved: coordinators.UnobservedSweepFunc(func(_ context.Context) (int, int) {
			return 0, 0
		}),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("drift events = %d, want 0 (sweep was a no-op)", len(drifts))
	}
}

// TestReconcilerSweepIsOptional verifies the Reconciler is a no-op for
// the new stage when UnobservedSweepRunner is nil — backwards compat
// for callers that have not wired the sweep.
func TestReconcilerSweepIsOptional(t *testing.T) {
	r := &coordinators.Reconciler{CentralName: "ccu1"}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile with nil sweep should not err: %v", err)
	}
}

func TestReconcilerEmitsDriftOnConnectivityFlip(t *testing.T) {
	bus := events.NewBus()
	connectivity := hub.NewConnectivity()
	// Initial state: HmIP-RF reachable.
	connectivity.OnState("HmIP-RF", true)

	var connChanged []hmevent.ConnectivityChangedEvent
	var drifts []hmevent.DriftCorrectedEvent
	defer events.Subscribe(bus, func(e hmevent.ConnectivityChangedEvent) { connChanged = append(connChanged, e) })()
	defer events.Subscribe(bus, func(e hmevent.DriftCorrectedEvent) { drifts = append(drifts, e) })()

	r := &coordinators.Reconciler{
		CentralName:  "ccu1",
		Connectivity: connectivity,
		Bus:          bus,
		Connect: coordinators.ProbeFunc(func(_ context.Context) ([]coordinators.InterfaceReachability, error) {
			return []coordinators.InterfaceReachability{
				{InterfaceID: "HmIP-RF", Reachable: false},
			}, nil
		}),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	if len(connChanged) != 1 || connChanged[0].InterfaceID != "HmIP-RF" || connChanged[0].Reachable {
		t.Fatalf("expected ConnectivityChanged HmIP-RF=false, got %+v", connChanged)
	}
	if len(drifts) != 1 || drifts[0].Component != "connectivity" {
		t.Fatalf("expected one connectivity drift, got %+v", drifts)
	}
	if r, _ := connectivity.Reachable("HmIP-RF"); r {
		t.Fatalf("connectivity cache should now be false")
	}
}

func TestReconcilerNoEventWhenStateMatches(t *testing.T) {
	bus := events.NewBus()
	connectivity := hub.NewConnectivity()
	connectivity.OnState("HmIP-RF", true)

	var drifts []hmevent.DriftCorrectedEvent
	defer events.Subscribe(bus, func(e hmevent.DriftCorrectedEvent) { drifts = append(drifts, e) })()

	r := &coordinators.Reconciler{
		Connectivity: connectivity,
		Bus:          bus,
		Connect: coordinators.ProbeFunc(func(_ context.Context) ([]coordinators.InterfaceReachability, error) {
			return []coordinators.InterfaceReachability{{InterfaceID: "HmIP-RF", Reachable: true}}, nil
		}),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Fatalf("no drift expected when cached state matches probe, got %+v", drifts)
	}
}

func TestReconcilerSystemHealthDrift(t *testing.T) {
	bus := events.NewBus()
	metrics := hub.NewMetrics()
	metrics.Observe(hub.MetricSystemHealth, 95)

	var drifts []hmevent.DriftCorrectedEvent
	defer events.Subscribe(bus, func(e hmevent.DriftCorrectedEvent) { drifts = append(drifts, e) })()

	r := &coordinators.Reconciler{
		Metrics: metrics,
		Bus:     bus,
		Health: coordinators.HealthProbeFunc(func(_ context.Context) (int, error) {
			return 80, nil
		}),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 || drifts[0].Component != "system_health" {
		t.Fatalf("expected system_health drift, got %+v", drifts)
	}
	if got, _ := metrics.Value(hub.MetricSystemHealth); got.Value != 80 {
		t.Fatalf("metrics not updated, got %v", got.Value)
	}
}

func TestReconcilerSurfacesProbeError(t *testing.T) {
	r := &coordinators.Reconciler{
		Connectivity: hub.NewConnectivity(),
		Connect: coordinators.ProbeFunc(func(_ context.Context) ([]coordinators.InterfaceReachability, error) {
			return nil, errors.New("boom")
		}),
	}
	if err := r.Reconcile(context.Background()); err == nil {
		t.Fatalf("expected error from failing probe")
	}
}

func TestReconcilerSkipsOnNegativeHealth(t *testing.T) {
	metrics := hub.NewMetrics()
	r := &coordinators.Reconciler{
		Metrics: metrics,
		Health:  coordinators.HealthProbeFunc(func(_ context.Context) (int, error) { return -1, nil }),
	}
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, observed := metrics.Value(hub.MetricSystemHealth); observed {
		t.Fatalf("metric should remain unobserved")
	}
}

// TestReconcilerConnectivityFollowsTheProbeMembership walks a full probe
// sequence and pins which ConnectivityChangedEvents each answer produces.
//
// The CCU lists the interfaces it currently serves, so an interface that
// stops appearing is gone — not unchanged. Reconciling only the entries
// the probe returned left such an interface reporting its last known
// state forever, which every consumer (MQTT sensor, WS topic, the alarm
// domain's sensor availability) then believed.
//
// The two answers that carry no membership information are pinned in the
// same walk: a probe that errors out and a probe that comes back empty
// are what an unreachable or rebooting CCU produces, and turning either
// into a down event per interface would run the alarm domain's
// central-loss escalation on every CCU restart.
func TestReconcilerConnectivityFollowsTheProbeMembership(t *testing.T) {
	type reach struct {
		iface     string
		reachable bool
	}
	up := func(ids ...string) []coordinators.InterfaceReachability {
		out := make([]coordinators.InterfaceReachability, 0, len(ids))
		for _, id := range ids {
			out = append(out, coordinators.InterfaceReachability{InterfaceID: id, Reachable: true})
		}
		return out
	}

	steps := []struct {
		name    string
		probed  []coordinators.InterfaceReachability
		probeEr error
		wantErr bool
		want    []reach
	}{
		{
			name:   "first probe reports every interface it sees",
			probed: up("BidCos-RF", "HmIP-RF"),
			want:   []reach{{"BidCos-RF", true}, {"HmIP-RF", true}},
		},
		{
			name:   "an interface that disappears goes down",
			probed: up("HmIP-RF"),
			want:   []reach{{"BidCos-RF", false}},
		},
		{
			name:   "staying absent does not repeat the down event",
			probed: up("HmIP-RF"),
			want:   nil,
		},
		{
			name:    "a failed probe is no information",
			probeEr: errors.New("ccu unreachable"),
			wantErr: true,
			want:    nil,
		},
		{
			name:   "an empty answer is no information",
			probed: nil,
			want:   nil,
		},
		{
			name:   "the interface comes back up",
			probed: up("BidCos-RF", "HmIP-RF"),
			want:   []reach{{"BidCos-RF", true}},
		},
	}

	bus := events.NewBus()
	connectivity := hub.NewConnectivity()
	var seen []reach
	defer events.Subscribe(bus, func(e hmevent.ConnectivityChangedEvent) {
		seen = append(seen, reach{e.InterfaceID, e.Reachable})
	})()

	var (
		probed  []coordinators.InterfaceReachability
		probeEr error
	)
	r := &coordinators.Reconciler{
		CentralName:  "ccu1",
		Connectivity: connectivity,
		Bus:          bus,
		Connect: coordinators.ProbeFunc(func(_ context.Context) ([]coordinators.InterfaceReachability, error) {
			return probed, probeEr
		}),
	}

	for _, step := range steps {
		probed, probeEr = step.probed, step.probeEr
		seen = nil
		err := r.Reconcile(context.Background())
		if (err != nil) != step.wantErr {
			t.Fatalf("%s: Reconcile err = %v, wantErr = %v", step.name, err, step.wantErr)
		}
		if len(seen) != len(step.want) {
			t.Fatalf("%s: connectivity events = %+v, want %+v", step.name, seen, step.want)
		}
		for i, w := range step.want {
			if seen[i] != w {
				t.Fatalf("%s: connectivity event %d = %+v, want %+v", step.name, i, seen[i], w)
			}
		}
	}

	// The vanished interface stays in the tracker as unreachable rather
	// than dropping out of it, so the north-bound surfaces keep showing
	// it with an honest state.
	if reachable, observed := connectivity.Reachable("BidCos-RF"); !observed || !reachable {
		t.Fatalf("BidCos-RF reachable=%v observed=%v, want true/true after it returned", reachable, observed)
	}
}
