// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package scheduler runs periodic background jobs for the daemon.
//
// Each job is a named closure that runs on a fixed interval. The
// scheduler tracks per-job lifecycle state (scheduled, running,
// failed) and honours the parent context for cancellation.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// JobFunc is the signature every scheduled job satisfies.
type JobFunc func(ctx context.Context) error

// Job describes one scheduled unit of work.
type Job struct {
	Name     string
	Interval time.Duration
	Run      JobFunc

	// RunOnStart invokes the job once immediately after Start, before
	// the first interval tick.
	RunOnStart bool

	// OnStart is an optional hook called just before each job invocation begins.
	// The argument is the job name. Used by north-bound adapters to emit
	// DataRefreshTriggeredEvent. Closes C-SCHED-2 lifecycle hook.
	OnStart func(name string)

	// OnComplete is an optional hook called after each job invocation
	// completes (whether or not it returned an error). Arguments are
	// the job name, the wall-clock duration in milliseconds, a
	// success flag (true = nil error), and the underlying error
	// (nil on success). Used to emit DataRefreshCompletedEvent.
	OnComplete func(name string, durationMs int64, success bool, err error)
}

// Scheduler owns a set of periodic jobs and a shared context.
type Scheduler struct {
	logger *slog.Logger
	clk    clock.Clock

	mu       sync.Mutex
	jobs     []Job
	started  bool
	stopping bool            // true once Stop/StopWithTimeout begins; late Add calls are no-ops
	runCtx   context.Context // set by Start; used for late Add calls
	stopFunc context.CancelFunc
	wg       sync.WaitGroup

	// failuresMu guards the failures map. Counters themselves are
	// atomic so the hot read path (TotalFailures via the gauge)
	// avoids the lock entirely.
	failuresMu sync.RWMutex
	failures   map[string]*atomic.Uint64
}

// JobFailures returns the cumulative number of failed runs for the
// named job since daemon start. Counts both returned errors and
// recovered panics — anything that prevented the job from completing
// cleanly. Unknown jobs return 0.
func (s *Scheduler) JobFailures(name string) uint64 {
	s.failuresMu.RLock()
	c, ok := s.failures[name]
	s.failuresMu.RUnlock()
	if !ok {
		return 0
	}
	return c.Load()
}

// TotalFailures aggregates [JobFailures] across every job that has
// reported at least one failure. Used as the source for the
// `scheduler.failures` health gauge.
func (s *Scheduler) TotalFailures() uint64 {
	s.failuresMu.RLock()
	defer s.failuresMu.RUnlock()
	var total uint64
	for _, c := range s.failures {
		total += c.Load()
	}
	return total
}

// recordFailure increments the per-job failure counter, allocating
// the atomic on first miss.
func (s *Scheduler) recordFailure(name string) {
	s.failuresMu.RLock()
	c, ok := s.failures[name]
	s.failuresMu.RUnlock()
	if ok {
		c.Add(1)
		return
	}
	s.failuresMu.Lock()
	if s.failures == nil {
		s.failures = make(map[string]*atomic.Uint64)
	}
	c, ok = s.failures[name]
	if !ok {
		c = new(atomic.Uint64)
		s.failures[name] = c
	}
	s.failuresMu.Unlock()
	c.Add(1)
}

// New returns a fresh scheduler. logger may be nil (falls back to
// slog.Default). clk may be nil (falls back to the real wall clock).
// Pass [clock.NewFake] in tests for deterministic tick control.
func New(logger *slog.Logger, clk clock.Clock) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.New()
	}
	return &Scheduler{logger: logger, clk: clk}
}

// Add registers a job. When called before Start the job is queued and launched
// together with all other jobs at Start time. When called after Start the job
// is launched immediately on the running context so that components that wire
// up post-connect (e.g. hub device-details refresh) still get a periodic loop.
func (s *Scheduler) Add(j Job) error {
	if j.Name == "" {
		return errors.New("scheduler: job name is required")
	}
	if j.Interval <= 0 {
		return errors.New("scheduler: job interval must be positive")
	}
	if j.Run == nil {
		return errors.New("scheduler: job Run is required")
	}

	s.mu.Lock()
	s.jobs = append(s.jobs, j)
	alreadyStarted := s.started
	isStopping := s.stopping
	runCtx := s.runCtx
	// Increment wg while still holding the lock so Stop()/wg.Wait() cannot
	// return before this goroutine is registered. Skip when Stop has already
	// been initiated — adding to a draining WaitGroup is not safe.
	if alreadyStarted && !isStopping {
		s.wg.Add(1)
	}
	s.mu.Unlock()

	if alreadyStarted && !isStopping {
		go s.runJob(runCtx, j)
	}
	return nil
}

// Jobs returns a snapshot of the registered jobs. Callers that need
// to invoke job logic directly in tests (without starting the
// scheduler) can use the returned slice to locate a job by name and
// call its Run function. Must not be called concurrently with Add.
func (s *Scheduler) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// Start launches every registered job on its own goroutine. Returns a
// non-nil error only when Start was already called.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("scheduler: already started")
	}
	s.started = true
	ctx, s.stopFunc = context.WithCancel(ctx)
	s.runCtx = ctx
	jobs := append([]Job(nil), s.jobs...)
	s.mu.Unlock()

	for _, j := range jobs {
		s.wg.Add(1)
		go s.runJob(ctx, j)
	}
	return nil
}

// Stop cancels every job and blocks until all goroutines exit.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	stop := s.stopFunc
	s.stopping = true
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	s.wg.Wait()
}

// StopWithTimeout cancels every job and waits at most timeout for all
// goroutines to exit. When timeout is zero or negative the method
// defaults to 5 seconds. If not all goroutines exit within the window
// StopWithTimeout logs a diagnostic message and returns with the
// remaining goroutines still running. Callers that require guaranteed
// drain should call the regular [Stop] (which blocks indefinitely).
func (s *Scheduler) StopWithTimeout(timeout time.Duration) {
	const defaultTimeout = 5 * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	s.mu.Lock()
	stop := s.stopFunc
	s.stopping = true
	s.mu.Unlock()
	if stop != nil {
		stop()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All goroutines exited cleanly.
	case <-time.After(timeout):
		s.logger.Warn(
			"scheduler: StopWithTimeout: some jobs did not finish within the drain window",
			slog.Duration("timeout", timeout),
		)
	}
}

func (s *Scheduler) runJob(ctx context.Context, j Job) {
	defer s.wg.Done()

	if j.RunOnStart {
		s.invoke(ctx, j)
	}

	for {
		timer := s.clk.NewTimer(j.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C():
			s.invoke(ctx, j)
		}
	}
}

func (s *Scheduler) invoke(ctx context.Context, j Job) {
	if j.OnStart != nil {
		j.OnStart(j.Name)
	}
	start := time.Now()
	var err error
	// Recover from panics so a buggy job cannot kill its own loop
	// goroutine. The recovered value is converted to an error so the
	// outcome flows through the regular failure-counting path.
	func() {
		defer func() {
			if rv := recover(); rv != nil {
				err = fmt.Errorf("scheduler: job %q panicked: %v", j.Name, rv)
				s.logger.Error(
					"scheduler: job panicked",
					slog.String("job", j.Name),
					slog.Any("panic", rv),
				)
			}
		}()
		err = j.Run(ctx)
	}()
	durationMs := time.Since(start).Milliseconds()
	if err != nil && ctx.Err() == nil {
		s.logger.Warn(
			"scheduler: job failed",
			slog.String("job", j.Name),
			slog.Duration("duration", time.Duration(durationMs)*time.Millisecond),
			slog.String("err", err.Error()),
		)
		s.recordFailure(j.Name)
	}
	if j.OnComplete != nil {
		j.OnComplete(j.Name, durationMs, err == nil, err)
	}
}
