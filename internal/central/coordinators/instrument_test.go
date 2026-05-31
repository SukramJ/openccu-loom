// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// P0-1: every public coordinator entry point routes through
// observability.Instrument so the operator gets per-method latency +
// outcome counters. The recording fake below proves the wiring without
// touching the metrics registry.

type recordingRecorder struct {
	mu       sync.Mutex
	latency  map[string]int
	counters map[string]uint64
}

func newRecorder() *recordingRecorder {
	return &recordingRecorder{
		latency:  map[string]int{},
		counters: map[string]uint64{},
	}
}

func (r *recordingRecorder) ObserveLatency(name string, _ observability.Scope, _ time.Duration, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latency[name]++
}

func (r *recordingRecorder) IncCounter(name string, _ observability.Scope, delta uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name] += delta
}

func (r *recordingRecorder) latencyFor(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latency[name]
}

func (r *recordingRecorder) counterFor(name string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters[name]
}

func TestHubCoordinatorRefreshIsInstrumented(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	hc := NewHubCoordinator("c1", bus)
	rec := newRecorder()
	hc.SetRecorder(rec)

	// No-op pathways (no hook configured) intentionally skip the
	// recorder so dashboards only report real refresh latencies.
	if err := hc.RefreshPrograms(context.Background()); err != nil {
		t.Fatalf("RefreshPrograms err=%v", err)
	}
	if got := rec.latencyFor("hub_coordinator.refresh_programs"); got != 0 {
		t.Fatalf("no-op refresh must not record latency, got %d", got)
	}

	// Wire a successful hook → ok counter increments.
	hc.SetRefreshHooks(RefreshHooks{
		Programs: func(_ context.Context) error { return nil },
		Sysvars:  func(_ context.Context) error { return errors.New("boom") },
	})
	if err := hc.RefreshPrograms(context.Background()); err != nil {
		t.Fatalf("RefreshPrograms err=%v", err)
	}
	if got := rec.latencyFor("hub_coordinator.refresh_programs"); got != 1 {
		t.Fatalf("expected 1 latency obs, got %d", got)
	}
	if got := rec.counterFor("hub_coordinator.refresh_programs.ok"); got != 1 {
		t.Fatalf("ok counter=%d", got)
	}

	// Failing hook → error counter increments.
	if err := hc.RefreshSysvars(context.Background()); err == nil {
		t.Fatal("expected error from failing hook")
	}
	if got := rec.counterFor("hub_coordinator.refresh_sysvars.error"); got != 1 {
		t.Fatalf("error counter=%d", got)
	}
}

func TestLinkCoordinatorAddLinkIsInstrumented(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	lc := NewLinkCoordinator(nil)
	lc.SetRecorder(rec)

	// resolver is nil → AddLink fails before talking to a client.
	if err := lc.AddLink(context.Background(), "S:1", "R:2", "", ""); err == nil {
		t.Fatal("expected error for nil resolver")
	}
	if got := rec.latencyFor("link_coordinator.add_link"); got != 1 {
		t.Fatalf("latency obs=%d", got)
	}
	if got := rec.counterFor("link_coordinator.add_link.error"); got != 1 {
		t.Fatalf("error counter=%d", got)
	}
}

func TestConnectionRecoveryRunRecordsOutcome(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("c1", bus)
	rec := newRecorder()
	c.SetRecorder(rec)

	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(ctx context.Context) error { return nil }},
	}
	res := c.Run(context.Background(), "iface-1", pipeline)
	if res != hmenum.RecoveryResultSuccess {
		t.Fatalf("res=%v", res)
	}
	if got := rec.latencyFor("connection_recovery.run"); got != 1 {
		t.Fatalf("latency=%d", got)
	}
	if got := rec.counterFor("connection_recovery.run.success"); got != 1 {
		t.Fatalf("success counter=%d", got)
	}

	// Failing pipeline → failed counter increments.
	failing := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(ctx context.Context) error { return errors.New("nope") }},
	}
	c.Run(context.Background(), "iface-2", failing)
	if got := rec.counterFor("connection_recovery.run.failed"); got != 1 {
		t.Fatalf("failed counter=%d", got)
	}
}

func TestSetRecorderNilFallsBackToNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	hc := NewHubCoordinator("c1", bus)
	hc.SetRecorder(nil)
	// Smoke: must not panic / nil-deref.
	if err := hc.RefreshPrograms(context.Background()); err != nil {
		t.Fatalf("err=%v", err)
	}
}
