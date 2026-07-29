// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

func TestJitteredInterval(t *testing.T) {
	t.Parallel()

	const base = 24 * time.Hour
	const jitterMax = time.Hour

	tests := []struct {
		name            string
		base, jitterMax time.Duration
		rnd             func() float64
		want            time.Duration
	}{
		{"rnd -1 subtracts jitterMax", base, jitterMax, func() float64 { return -1 }, base - jitterMax},
		{"rnd 0 returns base unchanged", base, jitterMax, func() float64 { return 0 }, base},
		{"rnd 1 adds jitterMax", base, jitterMax, func() float64 { return 1 }, base + jitterMax},
		{"result clamps to zero instead of going negative", time.Minute, time.Hour, func() float64 { return -1 }, 0},
		{"jitterMax zero returns base unchanged", base, 0, func() float64 { return 1 }, base},
		{"jitterMax negative returns base unchanged", base, -time.Hour, func() float64 { return 1 }, base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := jitteredInterval(tt.base, tt.jitterMax, tt.rnd); got != tt.want {
				t.Errorf("jitteredInterval(%v, %v, ...) = %v, want %v", tt.base, tt.jitterMax, got, tt.want)
			}
		})
	}
}

// newCountingCheckServer serves a minimal, always-valid "latest release"
// response and counts every hit, so PeriodicChecker tests can observe
// Updater.Check having actually run without inspecting Updater internals.
func newCountingCheckServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tag_name":"v1.0.0","html_url":"https://example.invalid","assets":[`+
			`{"name":"openccu-loom-ccu-1.0.0.tar.gz","browser_download_url":"https://example.invalid/a"},`+
			`{"name":"checksums.txt","browser_download_url":"https://example.invalid/c"}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &count
}

// newPeriodicTestUpdater builds a real *Updater whose Checker points at
// srv. PeriodicChecker only ever calls Updater.Check, so the
// Downloader/Installer defaults (pointed at real paths) are never
// exercised here.
func newPeriodicTestUpdater(t *testing.T, srv *httptest.Server) *Updater {
	t.Helper()
	return NewUpdater(Deps{
		Capability: CapabilityProbe{
			IsAddonBuild: func() bool { return true },
			StatInstaller: func(string) (os.FileInfo, error) {
				return fakeFileInfo{mode: 0o755}, nil
			},
		},
		Checker:        &Checker{HTTPClient: &http.Client{}, BaseURL: srv.URL},
		CurrentVersion: "0.1.0",
		Logger:         discardLogger(),
	})
}

// waitForPendingCount polls fake.PendingCount() until it reaches want,
// bounded by a short real-time timeout. This mirrors the synchronization
// pattern in internal/scheduler/scheduler_clock_test.go: it proves the
// background goroutine has registered (or dropped) its timer before the
// test advances virtual time again, so no fire can be missed or
// double-counted by a race between the goroutines.
func waitForPendingCount(t *testing.T, fake *clock.Fake, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := fake.PendingCount(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PendingCount() == %d, last = %d", want, fake.PendingCount())
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForCheckCount polls count until it reaches want. Needed because
// firing a fake timer only unblocks the background goroutine's select;
// the HTTP round trip inside Updater.Check still completes asynchronously
// relative to the test goroutine's Advance call.
func waitForCheckCount(t *testing.T, count *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := count.Load(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for check count == %d, last = %d", want, count.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// stopWithTimeout calls p.Stop() on a goroutine and fails the test if it
// does not return within timeout, catching a hung run() goroutine fast
// instead of relying on the whole test binary's -timeout.
func stopWithTimeout(t *testing.T, p *PeriodicChecker, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("Stop() did not return within timeout")
	}
}

func TestPeriodicCheckerBootDelay(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  0,
		BootDelay: 5 * time.Minute,
		Clock:     fake,
		Jitter:    func() float64 { return 0 },
		Logger:    discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	waitForPendingCount(t, fake, 1)
	if got := count.Load(); got != 0 {
		t.Fatalf("check count before boot delay elapses = %d, want 0", got)
	}

	fake.Advance(5 * time.Minute)
	waitForCheckCount(t, count, 1)
}

func TestPeriodicCheckerRecurringInterval(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  time.Hour,
		BootDelay: time.Minute,
		Clock:     fake,
		Jitter:    func() float64 { return 0 }, // pin the interval exact
		Logger:    discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	waitForPendingCount(t, fake, 1)
	fake.Advance(time.Minute)
	waitForCheckCount(t, count, 1)

	const n = 3
	for i := range n {
		waitForPendingCount(t, fake, 1)
		fake.Advance(time.Hour)
		waitForCheckCount(t, count, int64(2+i))
	}
}

func TestPeriodicCheckerNoRecurringWhenIntervalDisabled(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  0,
		BootDelay: time.Minute,
		Clock:     fake,
		Jitter:    func() float64 { return 0 },
		Logger:    discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	waitForPendingCount(t, fake, 1)
	fake.Advance(time.Minute)
	waitForCheckCount(t, count, 1)

	// The boot check fired; with Interval <= 0 the loop returns instead
	// of registering another timer.
	waitForPendingCount(t, fake, 0)

	fake.Advance(100 * DefaultCheckInterval)
	if got := count.Load(); got != 1 {
		t.Fatalf("check count after long advance = %d, want 1 (no recurring loop)", got)
	}
}

func TestPeriodicCheckerDisabledEntirely(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  0,
		BootDelay: -1, // negative skips the boot check entirely
		Clock:     fake,
		Jitter:    func() float64 { return 0 },
		Logger:    discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// No timer is ever registered: bootDelay < 0 skips the boot check and
	// Interval <= 0 skips the recurring loop.
	waitForPendingCount(t, fake, 0)

	fake.Advance(100 * DefaultCheckInterval)
	if got := count.Load(); got != 0 {
		t.Fatalf("check count = %d, want 0 (checker fully disabled)", got)
	}
}

func TestPeriodicCheckerStopBeforeAnyTick(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  time.Hour,
		BootDelay: time.Hour,
		Clock:     fake,
		Jitter:    func() float64 { return 0 },
		Logger:    discardLogger(),
	}

	p.Start(context.Background())
	waitForPendingCount(t, fake, 1)
	stopWithTimeout(t, p, 2*time.Second)

	if got := count.Load(); got != 0 {
		t.Fatalf("check count = %d, want 0 (stopped before boot delay elapsed)", got)
	}

	// Advancing after Stop must not panic or resurrect the goroutine: the
	// pending timer was cancelled by run()'s ctx.Done() branch.
	fake.Advance(10 * time.Hour)
	if got := count.Load(); got != 0 {
		t.Fatalf("check count after post-Stop advance = %d, want 0", got)
	}
}

func TestPeriodicCheckerStartTwiceIsNoOp(t *testing.T) {
	t.Parallel()

	srv, count := newCountingCheckServer(t)
	u := newPeriodicTestUpdater(t, srv)

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := &PeriodicChecker{
		Updater:   u,
		Interval:  0,
		BootDelay: time.Minute,
		Clock:     fake,
		Jitter:    func() float64 { return 0 },
		Logger:    discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)
	p.Start(ctx) // must be a no-op: only one goroutine should ever run

	waitForPendingCount(t, fake, 1)
	fake.Advance(time.Minute)
	waitForCheckCount(t, count, 1)

	// If a second loop had started, it would have registered its own
	// boot-delay timer and eventually double-fired the check.
	waitForPendingCount(t, fake, 0)

	stopWithTimeout(t, p, 2*time.Second)
	if got := count.Load(); got != 1 {
		t.Fatalf("check count = %d, want 1 (only one loop ran)", got)
	}
}
