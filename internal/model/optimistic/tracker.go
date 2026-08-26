// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package optimistic owns the public optimistic-update value tracker.
//
// — a small state machine per data point that keeps the user-visible
// value coherent while a SetValue call is in flight to the CCU. The
// tracker pins a rollback anchor on the *first* send, bumps a pending
// counter on each subsequent send, and either drains the counter on
// confirmation events or rolls the anchor back on timeout / failure.
//
// The package is intentionally generic over the concrete value type so
// switches, dimmers, and analogue sensors share the same machinery.
// The `internal/model/generic` package embeds [Tracker] directly —
// there is a single implementation here.
package optimistic

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// DefaultTimeout mirrors
// `TimeoutConfig.optimistic_update_timeout = 30.0`. Callers usually
// pin a per-data-point override; the default is the safety net.
const DefaultTimeout = 30 * time.Second

// DefaultBurstWindow is the default window within which consecutive
// Apply calls are treated as a burst — they share a single rollback
// anchor and increment the PendingSends counter rather than updating
// the anchor. Callers that need a per-data-point override should pass
// a non-zero value to [Config] (future work). The window mirrors the
// typical CCU-callback round-trip latency; 500 ms is a conservative
// default that covers most RF propagation delays without masking
// legitimately distinct commands.
//
// Note: the current [Tracker] implementation does not enforce a time
// limit on burst membership — any Apply while active is treated as a
// burst. This constant is provided as a documented configuration
// anchor for callers that want to implement time-bounded burst windows
// in future.
const DefaultBurstWindow = 500 * time.Millisecond

// RollbackReason labels why a Tracker rolled back. Stable strings —
// they travel through events / logs to clients (REST, MQTT, audit)
// without translation.
type RollbackReason string

// RollbackReason values.
const (
	// RollbackReasonTimeout — no CCU confirmation arrived within the
	// configured optimistic-update-timeout window.
	RollbackReasonTimeout RollbackReason = "timeout"
	// RollbackReasonSendError — the wire-level SetValue call failed
	// outright.
	RollbackReasonSendError RollbackReason = "send_error"
	// RollbackReasonValueMismatch — a confirmation event arrived but
	// carried a different value (CCU rounded / clamped / refused).
	RollbackReasonValueMismatch RollbackReason = "mismatch"
)

// Snapshot is the public read-side view of a Tracker. The bool tells
// the caller whether the tracker currently holds an outstanding
// optimistic value.
type Snapshot[T comparable] struct {
	Value         T
	PendingSends  int
	Age           time.Duration
	Active        bool
	PreviousValue T
	PreviousSet   bool
}

// Tracker is one optimistic-update state machine. Methods are concur-
// rency-safe; one Tracker per (DataPoint, parameter) pair.
//
// Lifecycle:
//
//  1. Apply(v, current) — first call captures `current` as the
//     rollback anchor. Subsequent calls bump PendingSends and refresh
//     the wall-clock `sentAt`.
//  2. ConfirmOne() — called when a CCU event arrives. Decrements
//     PendingSends; once it hits zero the caller clears the tracker
//     (or relies on the wrapper's automatic clear).
//  3. Rollback() — called on timeout / send error. Resets the tracker
//     and returns the (rolledBack, restored, restoredSet) tuple so
//     the caller can revert the cached data-point value.
//
// Burst guarantee: three quick Apply calls only pin the *first* send's
// `current` as the rollback anchor.
type Tracker[T comparable] struct {
	clk clock.Clock

	mu sync.Mutex

	active        bool
	value         T
	previousValue T
	previousSet   bool
	pendingSends  int
	sentAt        time.Time
	timeout       clock.Timer
	timeoutStop   chan struct{}

	done chan struct{}
}

// New constructs a Tracker. Pass nil for clk to use the real wall
// clock (the production default).
func New[T comparable](clk clock.Clock) *Tracker[T] {
	if clk == nil {
		clk = clock.New()
	}
	return &Tracker[T]{clk: clk}
}

// IsActive reports whether the tracker holds an outstanding optimistic
// value.
func (t *Tracker[T]) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

// Snapshot returns a coherent read of the tracker. Cheap; safe to
// call concurrently with Apply / ConfirmOne / Rollback.
func (t *Tracker[T]) Snapshot() Snapshot[T] {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		var zero T
		return Snapshot[T]{
			Value:        zero,
			PendingSends: 0,
		}
	}
	age := time.Duration(0)
	if !t.sentAt.IsZero() {
		age = t.clk.Now().Sub(t.sentAt)
	}
	return Snapshot[T]{
		Value:         t.value,
		PendingSends:  t.pendingSends,
		Age:           age,
		Active:        true,
		PreviousValue: t.previousValue,
		PreviousSet:   t.previousSet,
	}
}

// Apply records a new optimistic value. The first apply on a fresh
// tracker captures `current` as the rollback anchor; subsequent
// applies bump PendingSends and refresh `sentAt`.
func (t *Tracker[T]) Apply(value, current T, currentSet bool) {
	t.ApplyBurst(value, current, currentSet, 0)
}

// ApplyBurst records a new optimistic value where the caller already
// knows a burst of N additional confirmations is in flight (e.g. a
// dimmer ramp-up command that the CCU echoes once per ramp step).
// PendingSends is bumped by `1 + additionalPending` instead of 1, so
// the rollback timer waits for *all* expected echoes before firing.
//
// additionalPending<=0 is identical to [Apply].
func (t *Tracker[T]) ApplyBurst(value, current T, currentSet bool, additionalPending int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active && t.pendingSends == 0 {
		t.previousValue = current
		t.previousSet = currentSet
		t.done = make(chan struct{})
	}
	t.active = true
	t.value = value
	t.pendingSends++
	if additionalPending > 0 {
		t.pendingSends += additionalPending
	}
	t.sentAt = t.clk.Now()
}

// Done returns the channel callers block on for the tracker to settle
// (rollback or final confirm). Nil when the tracker is inactive.
func (t *Tracker[T]) Done() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// ConfirmOne decrements PendingSends and reports whether the counter
// hit zero — that's the caller's signal to clear the tracker.
func (t *Tracker[T]) ConfirmOne() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return false
	}
	if t.pendingSends > 0 {
		t.pendingSends--
	}
	return t.pendingSends == 0
}

// Rollback resets the tracker and returns (rolledBack, restored,
// restoredSet, ok). ok=false means the tracker was already inactive
// and the caller has nothing to do.
func (t *Tracker[T]) Rollback() (rolledBack, restored T, restoredSet, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		var zero T
		return zero, zero, false, false
	}
	rolledBack = t.value
	restored = t.previousValue
	restoredSet = t.previousSet
	t.resetLocked()
	return rolledBack, restored, restoredSet, true
}

// Clear discards the tracker state without rolling back. Used on the
// final confirm, value-mismatch (CCU is authoritative), or shutdown.
func (t *Tracker[T]) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetLocked()
}

func (t *Tracker[T]) resetLocked() {
	var zero T
	t.active = false
	t.value = zero
	t.previousValue = zero
	t.previousSet = false
	t.pendingSends = 0
	t.sentAt = time.Time{}
	if t.timeout != nil {
		t.timeout.Stop()
		t.timeout = nil
	}
	if t.timeoutStop != nil {
		close(t.timeoutStop)
		t.timeoutStop = nil
	}
	if t.done != nil {
		close(t.done)
		t.done = nil
	}
}

// ScheduleRollback (re)arms a timeout-driven rollback callback. A
// previous timer (if any) is cancelled. The callback runs in its own
// goroutine — the caller is responsible for serialising downstream
// access.
func (t *Tracker[T]) ScheduleRollback(d time.Duration, fn func()) {
	t.mu.Lock()
	if t.timeout != nil {
		t.timeout.Stop()
	}
	if t.timeoutStop != nil {
		close(t.timeoutStop)
	}
	stop := make(chan struct{})
	t.timeoutStop = stop
	timer := t.clk.NewTimer(d)
	t.timeout = timer
	t.mu.Unlock()

	go func() {
		select {
		case <-timer.C():
			fn()
		case <-stop:
		}
	}()
}
