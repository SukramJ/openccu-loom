// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// ADR 0057 §4 cadence defaults: a delayed boot check (so a co-booting
// CCU / network is not hammered at cold start), then every 24h ± up
// to 1h of jitter so a fleet does not stamp GitHub simultaneously.
const (
	DefaultBootDelay     = 5 * time.Minute
	DefaultCheckInterval = 24 * time.Hour
	DefaultJitterMax     = time.Hour
)

// PeriodicChecker drives the boot-delayed, jittered recurring
// addon-update check (ADR 0057 §4) on its own goroutine. It does not
// use [internal/scheduler].Scheduler directly: that scheduler's Job
// runs on one fixed Interval, with no boot-delay-distinct-from-cadence
// or per-tick jitter, both of which this feature's cadence needs.
type PeriodicChecker struct {
	Updater *Updater
	// Interval is the base recurring cadence. <= 0 disables the
	// recurring loop; the one-shot boot check (governed by BootDelay)
	// still runs — this only means "no timer after that first check".
	Interval time.Duration
	// BootDelay is how long to wait after Start before the first
	// check. Zero uses [DefaultBootDelay]; negative skips the boot
	// check entirely (test seam — production never sets this).
	BootDelay time.Duration
	// JitterMax bounds the +/- random offset applied to Interval on
	// every recurring cycle. Zero uses [DefaultJitterMax].
	JitterMax time.Duration
	// Clock is the time source for delays/timers. Nil uses the real
	// wall clock.
	Clock clock.Clock
	// Jitter returns a value in [-1, 1] scaling JitterMax on each
	// cycle. Nil uses a math/rand/v2-backed uniform source. Injectable
	// so tests can pin the offset instead of asserting a range.
	Jitter func() float64
	Logger *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start launches the boot-delayed, jittered check loop on its own
// goroutine and returns immediately. Calling Start again before a
// matching Stop is a no-op.
func (p *PeriodicChecker) Start(ctx context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	done := make(chan struct{})
	p.done = done
	p.mu.Unlock()

	go func() {
		defer close(done)
		p.run(runCtx)
	}()
}

// Stop cancels the loop and blocks until its goroutine exits. Safe to
// call on a PeriodicChecker that was never started, or more than once.
func (p *PeriodicChecker) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (p *PeriodicChecker) clock() clock.Clock {
	if p.Clock != nil {
		return p.Clock
	}
	return clock.New()
}

func (p *PeriodicChecker) jitterFunc() func() float64 {
	if p.Jitter != nil {
		return p.Jitter
	}
	return func() float64 { return rand.Float64()*2 - 1 } //nolint:gosec // cadence de-sync, not security-sensitive randomness
}

func (p *PeriodicChecker) jitterMax() time.Duration {
	if p.JitterMax > 0 {
		return p.JitterMax
	}
	return DefaultJitterMax
}

func (p *PeriodicChecker) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func (p *PeriodicChecker) run(ctx context.Context) {
	clk := p.clock()

	bootDelay := p.BootDelay
	if bootDelay == 0 {
		bootDelay = DefaultBootDelay
	}
	if bootDelay > 0 {
		timer := clk.NewTimer(bootDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C():
			p.checkOnce(ctx)
		}
	}

	if p.Interval <= 0 {
		return // recurring loop disabled; the boot check (if any) already ran
	}
	for {
		d := jitteredInterval(p.Interval, p.jitterMax(), p.jitterFunc())
		timer := clk.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C():
			p.checkOnce(ctx)
		}
	}
}

func (p *PeriodicChecker) checkOnce(ctx context.Context) {
	if p.Updater == nil {
		return
	}
	if err := p.Updater.Check(ctx); err != nil {
		p.logger().Warn("addon_update.periodic_check_failed", slog.String("err", err.Error()))
	}
}

// jitteredInterval returns base plus a random offset in [-max, max]
// (rnd is expected to return a value in [-1, 1]), clamped to never go
// negative regardless of rnd's output.
func jitteredInterval(base, jitterMax time.Duration, rnd func() float64) time.Duration {
	if jitterMax <= 0 {
		return base
	}
	offset := time.Duration(rnd() * float64(jitterMax))
	d := base + offset
	if d < 0 {
		return 0
	}
	return d
}
