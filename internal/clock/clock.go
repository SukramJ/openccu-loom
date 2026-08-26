// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package clock provides an injectable wall-clock + timer abstraction.
//
// Production code keeps using `time.Now()` / `time.NewTimer()` for the 80 %
// case where calls do not need to be deterministic in tests. The Clock
// interface in this package is the alternative for the reli- ability layer
// (retry, throttle, coalesce, circuit breaker), where timing is the
// *behaviour under test*. The fake implementation lets those tests advance
// virtual time on demand instead of sleeping.
package clock

import (
	"sync"
	"time"
)

// Timer is the subset of [time.Timer] the reliability layer needs.
// The fake implementation can deliver fires synchronously when tests
// call [Fake.Advance].
type Timer interface {
	// C returns the channel on which the timer fires exactly once.
	C() <-chan time.Time
	// Stop cancels a pending fire and reports whether it managed to
	// stop the timer before it fired.
	Stop() bool
}

// Clock is the interface production callers depend on.
type Clock interface {
	// Now returns the current time. Equivalent to [time.Now] for the
	// real clock; advances on [Fake.Advance] for the fake.
	Now() time.Time
	// NewTimer creates a timer that fires after d. Negative d fires
	// immediately.
	NewTimer(d time.Duration) Timer
	// Sleep blocks for d. Honours the underlying clock so a fake
	// clock's Sleep returns when [Fake.Advance] crosses the deadline.
	Sleep(d time.Duration)
	// After is a convenience: returns the channel of a one-shot timer
	// that fires after d.
	After(d time.Duration) <-chan time.Time
}

// Real wraps the standard library calls. The zero value is ready to
// use.
type Real struct{}

// New returns a [Real] clock — the production default.
func New() Clock { return Real{} }

// Now implements [Clock].
func (Real) Now() time.Time { return time.Now() }

// NewTimer implements [Clock].
func (Real) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }

// Sleep implements [Clock].
func (Real) Sleep(d time.Duration) { time.Sleep(d) }

// After implements [Clock].
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// Fake is a deterministic clock for tests. The current time advances
// only on explicit [Fake.Advance] / [Fake.Set] calls. Pending timers
// fire when the clock crosses their deadline.
//
// Fake is safe for concurrent use; one mutex guards both `now` and
// the pending-timer list. Tests that need parallelism keep one Fake
// per goroutine instead of sharing.
type Fake struct {
	mu  sync.Mutex
	now time.Time
	// pending holds timers that have not yet fired. The slice is sorted
	// by deadline ascending — Advance walks it head-first.
	pending []*fakeTimer
}

// NewFake constructs a fake clock at the given start time. Pass the
// zero value for "epoch start"; tests usually pin a specific moment so
// log timestamps are readable.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

// Now implements [Clock].
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer implements [Clock].
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		deadline: f.now.Add(d),
		ch:       make(chan time.Time, 1),
		clock:    f,
	}
	if d <= 0 {
		// Fire immediately.
		t.ch <- f.now
		t.fired = true
		return t
	}
	f.insertPending(t)
	return t
}

// After implements [Clock].
func (f *Fake) After(d time.Duration) <-chan time.Time {
	return f.NewTimer(d).C()
}

// Sleep implements [Clock] — implemented by parking on a fake timer.
// Tests usually drive Sleep callers via [Fake.Advance] in a separate
// goroutine.
func (f *Fake) Sleep(d time.Duration) { <-f.After(d) }

// Advance moves the clock forward by d, firing every timer whose
// deadline is at or before the new now. Calling Advance with a
// negative d is a no-op (clocks do not run backwards).
func (f *Fake) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	f.mu.Lock()
	target := f.now.Add(d)
	for len(f.pending) > 0 && !f.pending[0].deadline.After(target) {
		t := f.pending[0]
		f.pending = f.pending[1:]
		f.now = t.deadline
		t.fired = true
		// Non-blocking send: the timer's channel is buffered, so a
		// caller that hasn't read yet still receives the fire.
		select {
		case t.ch <- t.deadline:
		default:
		}
	}
	f.now = target
	f.mu.Unlock()
}

// Set repositions the clock to t and fires every timer whose deadline
// is at or before t. Set may move backwards in time, but it never
// un-fires a timer that already fired.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	if t.After(f.now) {
		d := t.Sub(f.now)
		f.mu.Unlock()
		f.Advance(d)
		return
	}
	f.now = t
	f.mu.Unlock()
}

// insertPending preserves the deadline-sorted order. Callers must hold
// f.mu.
func (f *Fake) insertPending(t *fakeTimer) {
	for i, existing := range f.pending {
		if t.deadline.Before(existing.deadline) {
			f.pending = append(f.pending[:i], append([]*fakeTimer{t}, f.pending[i:]...)...)
			return
		}
	}
	f.pending = append(f.pending, t)
}

// PendingCount returns the number of timers waiting to fire. Useful
// for tests that want to assert "no other timers are scheduled".
func (f *Fake) PendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

// fakeTimer implements [Timer] against a [Fake].
type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
	clock    *Fake
	fired    bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired {
		return false
	}
	for i, p := range t.clock.pending {
		if p == t {
			t.clock.pending = append(t.clock.pending[:i], t.clock.pending[i+1:]...)
			return true
		}
	}
	return false
}
