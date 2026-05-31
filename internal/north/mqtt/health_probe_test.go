// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

type fakeConn struct {
	connected bool
	lastOK    time.Time
}

func (f *fakeConn) IsConnected() bool          { return f.connected }
func (f *fakeConn) LastConnectedAt() time.Time { return f.lastOK }

type fakeTracker struct {
	mu      sync.Mutex
	samples []health.Sample
}

func (t *fakeTracker) Record(_ string, s health.Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples = append(t.samples, s)
}

func (t *fakeTracker) snapshot() []health.Sample {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]health.Sample(nil), t.samples...)
}

func TestStartHealthProbe_NilClient_ReturnsNoOp(t *testing.T) {
	t.Parallel()
	stop := StartHealthProbe(context.Background(), nil, &fakeTracker{}, time.Second)
	if stop == nil {
		t.Fatal("stop must not be nil")
	}
	stop() // no panic
}

func TestStartHealthProbe_NilTracker_ReturnsNoOp(t *testing.T) {
	t.Parallel()
	stop := StartHealthProbe(context.Background(), &fakeConn{}, nil, time.Second)
	stop() // no panic
}

func TestStartHealthProbe_ZeroInterval_DefaultsApplied(t *testing.T) {
	t.Parallel()
	// Zero interval falls back to DefaultProbeInterval (30s). We can
	// only assert that the first sample lands immediately on the
	// initial probeOnce call — the interval timer never fires in the
	// test window.
	c := &fakeConn{connected: true, lastOK: time.Now().Add(-time.Minute)}
	tr := &fakeTracker{}
	stop := StartHealthProbe(context.Background(), c, tr, 0)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(tr.snapshot()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(tr.snapshot()) == 0 {
		t.Fatal("initial probe did not fire within 1 s")
	}
}

func TestProbeOnce_ConnectedRecordsHealthy(t *testing.T) {
	t.Parallel()
	c := &fakeConn{connected: true, lastOK: time.Now().Add(-5 * time.Minute)}
	tr := &fakeTracker{}
	probeOnce(c, tr)
	got := tr.snapshot()
	if len(got) != 1 || !got[0].Healthy {
		t.Fatalf("samples=%+v, want one healthy", got)
	}
	if !strings.Contains(got[0].Note, "uptime=") {
		t.Fatalf("Note missing uptime suffix: %q", got[0].Note)
	}
}

func TestProbeOnce_DisconnectedNeverConnected_RecordsEscalated(t *testing.T) {
	t.Parallel()
	c := &fakeConn{connected: false}
	tr := &fakeTracker{}
	probeOnce(c, tr)
	got := tr.snapshot()
	// Disconnected emits two samples per the double-sample flap-damp.
	if len(got) != 2 {
		t.Fatalf("samples=%d, want 2", len(got))
	}
	if got[0].Healthy || got[1].Healthy {
		t.Fatalf("both samples must be unhealthy: %+v", got)
	}
	if !strings.Contains(got[0].Note, "never connected") {
		t.Fatalf("first note=%q, want 'never connected'", got[0].Note)
	}
}

func TestProbeOnce_DisconnectedWithLastOK_RecordsLastOKTimestamp(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	c := &fakeConn{connected: false, lastOK: last}
	tr := &fakeTracker{}
	probeOnce(c, tr)
	got := tr.snapshot()
	if len(got) != 2 {
		t.Fatalf("samples=%d, want 2", len(got))
	}
	if !strings.Contains(got[0].Note, "2026-05-24T18:00:00") {
		t.Fatalf("first note missing last_ok timestamp: %q", got[0].Note)
	}
}

func TestStartHealthProbe_CancelStops(t *testing.T) {
	t.Parallel()
	c := &fakeConn{connected: true, lastOK: time.Now()}
	tr := &fakeTracker{}
	stop := StartHealthProbe(context.Background(), c, tr, 50*time.Millisecond)
	// Let a couple of probes land.
	time.Sleep(180 * time.Millisecond)
	stop()
	pre := len(tr.snapshot())
	time.Sleep(100 * time.Millisecond)
	post := len(tr.snapshot())
	if post != pre {
		t.Fatalf("samples after stop: pre=%d post=%d (probe didn't stop)", pre, post)
	}
}
