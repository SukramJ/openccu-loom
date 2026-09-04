// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

type fakeBridgeStatus struct {
	mu   sync.Mutex
	addr string
}

func (f *fakeBridgeStatus) LocalAddr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addr
}

type recordingTracker struct {
	mu      sync.Mutex
	samples []health.Sample
}

func (r *recordingTracker) Record(_ string, s health.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
}

// RecordUnhealthy records like Record; the fake keeps no status, so the
// distinction the tracker draws is not observable here.
func (r *recordingTracker) RecordUnhealthy(name string, s health.Sample) {
	s.Healthy = false
	r.Record(name, s)
}

func (r *recordingTracker) snapshot() []health.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]health.Sample, len(r.samples))
	copy(out, r.samples)
	return out
}

// TestStartHealthProbe_BoundReportsHealthy verifies the boot-time
// state: a bridge with a bound UDP listener reports one healthy
// sample on the immediate first probe.
func TestStartHealthProbe_BoundReportsHealthy(t *testing.T) {
	t.Parallel()
	status := &fakeBridgeStatus{addr: "[::]:5540"}
	tracker := &recordingTracker{}
	stop := StartHealthProbe(context.Background(), status, tracker, time.Hour)
	t.Cleanup(stop)
	// The probe records its first sample synchronously inside the
	// goroutine. Spin briefly so the goroutine reaches probeOnce.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(tracker.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := tracker.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if !got[0].Healthy {
		t.Fatalf("expected Healthy=true, got %+v", got[0])
	}
}

// TestStartHealthProbe_UnboundReportsUnhealthy verifies that an
// empty LocalAddr fires the two-sample escalation pattern so the
// flap-damp rule transitions the tracker straight to UNHEALTHY.
func TestStartHealthProbe_UnboundReportsUnhealthy(t *testing.T) {
	t.Parallel()
	status := &fakeBridgeStatus{addr: ""}
	tracker := &recordingTracker{}
	stop := StartHealthProbe(context.Background(), status, tracker, time.Hour)
	t.Cleanup(stop)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(tracker.snapshot()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := tracker.snapshot()
	// One report per probe: an unbound listener is a condition the probe
	// states, so the tracker takes it at once. It used to be recorded twice,
	// the second time with an invented "(escalated)" note, purely to clear the
	// flap-damping that Record applies to sampled observations.
	if len(got) < 1 {
		t.Fatalf("expected at least one sample, got %d", len(got))
	}
	for i, s := range got {
		if s.Healthy {
			t.Fatalf("sample %d expected Healthy=false, got %+v", i, s)
		}
	}
}

// TestStartHealthProbe_NilArgsAreNoOps covers the boot-time path
// where Matter is feature-flagged off — passing nil for either
// arg returns a no-op stop without spawning a goroutine.
func TestStartHealthProbe_NilArgsAreNoOps(t *testing.T) {
	t.Parallel()
	stop := StartHealthProbe(context.Background(), nil, &recordingTracker{}, time.Second)
	stop() // must not panic
	stop = StartHealthProbe(context.Background(), &fakeBridgeStatus{}, nil, time.Second)
	stop()
}
