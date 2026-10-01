// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
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

// handlerTransport serves every request from h in-process, so a
// PeriodicChecker test needs no network listener and can run inside a
// synctest bubble. Like a real transport it fails the round trip once the
// request context is done.
type handlerTransport struct{ h http.Handler }

func (tr handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	tr.h.ServeHTTP(rec, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}

const latestReleaseBody = `{"tag_name":"v1.0.0","html_url":"https://example.invalid","assets":[` +
	`{"name":"openccu-loom-ccu-1.0.0.tar.gz","browser_download_url":"https://example.invalid/a"},` +
	`{"name":"checksums.txt","browser_download_url":"https://example.invalid/c"}]}`

// newCountingCheckClient serves a minimal, always-valid "latest release"
// response in-process and counts every hit, so PeriodicChecker tests can
// observe Updater.Check having actually run without inspecting Updater
// internals.
func newCountingCheckClient() (*http.Client, *atomic.Int64) {
	var count atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, latestReleaseBody)
	})
	return &http.Client{Transport: handlerTransport{h: mux}}, &count
}

// newPeriodicTestUpdater builds a real *Updater whose Checker uses
// client. PeriodicChecker only ever calls Updater.Check, so the
// Downloader/Installer defaults (pointed at real paths) are never
// exercised here.
func newPeriodicTestUpdater(client *http.Client) *Updater {
	return NewUpdater(Deps{
		Capability: CapabilityProbe{
			IsAddonBuild: func() bool { return true },
			StatInstaller: func(string) (os.FileInfo, error) {
				return fakeFileInfo{mode: 0o755}, nil
			},
		},
		Checker:        &Checker{HTTPClient: client, BaseURL: "http://releases.invalid"},
		CurrentVersion: "0.1.0",
		Logger:         discardLogger(),
	})
}

func TestPeriodicCheckerBootDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  0,
			BootDelay: 5 * time.Minute,
			Jitter:    func() float64 { return 0 },
			Logger:    discardLogger(),
		}

		p.Start(t.Context())
		defer p.Stop()

		synctest.Wait()
		if got := count.Load(); got != 0 {
			t.Fatalf("check count before boot delay elapses = %d, want 0", got)
		}

		synctest.Sleep(5 * time.Minute)
		if got := count.Load(); got != 1 {
			t.Fatalf("check count after boot delay = %d, want 1", got)
		}
	})
}

func TestPeriodicCheckerRecurringInterval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  time.Hour,
			BootDelay: time.Minute,
			Jitter:    func() float64 { return 0 }, // pin the interval exact
			Logger:    discardLogger(),
		}

		p.Start(t.Context())
		defer p.Stop()

		synctest.Sleep(time.Minute)
		if got := count.Load(); got != 1 {
			t.Fatalf("check count after boot delay = %d, want 1", got)
		}

		const n = 3
		for i := range n {
			synctest.Sleep(time.Hour)
			if got, want := count.Load(), int64(2+i); got != want {
				t.Fatalf("check count after recurring tick %d = %d, want %d", i+1, got, want)
			}
		}
	})
}

func TestPeriodicCheckerNoRecurringWhenIntervalDisabled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  0,
			BootDelay: time.Minute,
			Jitter:    func() float64 { return 0 },
			Logger:    discardLogger(),
		}

		p.Start(t.Context())
		defer p.Stop()

		synctest.Sleep(time.Minute)
		if got := count.Load(); got != 1 {
			t.Fatalf("check count after boot delay = %d, want 1", got)
		}

		// The boot check fired; with Interval <= 0 the loop returns instead
		// of registering another timer.
		synctest.Sleep(100 * DefaultCheckInterval)
		if got := count.Load(); got != 1 {
			t.Fatalf("check count after long advance = %d, want 1 (no recurring loop)", got)
		}
	})
}

func TestPeriodicCheckerDisabledEntirely(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  0,
			BootDelay: -1, // negative skips the boot check entirely
			Jitter:    func() float64 { return 0 },
			Logger:    discardLogger(),
		}

		p.Start(t.Context())
		defer p.Stop()

		// No timer is ever registered: bootDelay < 0 skips the boot check and
		// Interval <= 0 skips the recurring loop.
		synctest.Sleep(100 * DefaultCheckInterval)
		if got := count.Load(); got != 0 {
			t.Fatalf("check count = %d, want 0 (checker fully disabled)", got)
		}
	})
}

func TestPeriodicCheckerStopBeforeAnyTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  time.Hour,
			BootDelay: time.Hour,
			Jitter:    func() float64 { return 0 },
			Logger:    discardLogger(),
		}

		p.Start(t.Context())
		synctest.Wait()
		p.Stop()

		if got := count.Load(); got != 0 {
			t.Fatalf("check count = %d, want 0 (stopped before boot delay elapsed)", got)
		}

		// Advancing after Stop must not panic or resurrect the goroutine: the
		// pending timer was cancelled by run()'s ctx.Done() branch.
		synctest.Sleep(10 * time.Hour)
		if got := count.Load(); got != 0 {
			t.Fatalf("check count after post-Stop advance = %d, want 0", got)
		}
	})
}

func TestPeriodicCheckerStartTwiceIsNoOp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client, count := newCountingCheckClient()
		u := newPeriodicTestUpdater(client)

		p := &PeriodicChecker{
			Updater:   u,
			Interval:  0,
			BootDelay: time.Minute,
			Jitter:    func() float64 { return 0 },
			Logger:    discardLogger(),
		}

		ctx := t.Context()

		p.Start(ctx)
		p.Start(ctx) // must be a no-op: only one goroutine should ever run

		// If a second loop had started, it would have registered its own
		// boot-delay timer and double-fired the check at the same instant.
		synctest.Sleep(time.Minute)

		p.Stop()
		if got := count.Load(); got != 1 {
			t.Fatalf("check count = %d, want 1 (only one loop ran)", got)
		}
	})
}

// TestPeriodicCheckerSurvivesAStalledCheck pins the cadence loop against
// a check that never answers: a server accepts the request and holds it
// open (a wedged proxy, a half-open connection). Without a per-tick
// bound the single loop goroutine parks inside Updater.Check forever and
// the recurring check silently stops firing for the rest of the daemon's
// uptime — no log line, no error, no later tick.
func TestPeriodicCheckerSurvivesAStalledCheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stalled := make(chan struct{})
		defer close(stalled)

		var hits atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
			if hits.Add(1) == 1 {
				// First tick: never answer. The handler unblocks when the
				// request context ends, which is what the per-tick bound causes.
				select {
				case <-r.Context().Done():
				case <-stalled:
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, latestReleaseBody)
		})

		u := newPeriodicTestUpdater(&http.Client{Transport: handlerTransport{h: mux}})

		const checkTimeout = 100 * time.Millisecond
		p := &PeriodicChecker{
			Updater:      u,
			Interval:     time.Hour,
			BootDelay:    time.Minute,
			CheckTimeout: checkTimeout,
			Jitter:       func() float64 { return 0 },
			Logger:       discardLogger(),
		}

		p.Start(t.Context())
		defer p.Stop()

		synctest.Sleep(time.Minute) // boot tick stalls inside the round trip
		if got := hits.Load(); got != 1 {
			t.Fatalf("hits after the boot tick = %d, want 1", got)
		}

		// The per-tick bound ends the stalled request, after which the loop
		// re-arms its interval timer.
		synctest.Sleep(checkTimeout)

		synctest.Sleep(time.Hour)
		if got := hits.Load(); got != 2 {
			t.Fatalf("hits after the next interval tick = %d, want 2", got)
		}
	})
}
