// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// ErrThrottleClosed is returned when Acquire is called on a closed
// throttle.
var ErrThrottleClosed = errors.New("throttle: closed")

// ErrSuperseded is returned to a waiter that was cancelled by
// [CommandThrottle.Purge].
var ErrSuperseded = errors.New("throttle: command superseded")

// ErrThrottleQueueFull is returned by Acquire when the throttle's
// pending-waiter queue is at [ThrottleConfig.MaxQueueDepth]. CRITICAL
// commands bypass the depth gate; only non-critical work fails fast.
//
// Backpressure rationale: a slow or paused CCU (firmware update,
// reboot, network blip) drains the in-flight semaphore at zero rate.
// Without a depth cap, every queued REST or scheduler call piles new
// waiters onto the heap until the daemon OOMs. Returning this error
// gives the caller a clean cancellation signal with bounded memory
// cost — retries fall to the [Retrier]'s exponential backoff.
var ErrThrottleQueueFull = errors.New("throttle: queue full")

// DefaultInterCommandDelay is the minimum gap enforced between consecutive
// non-critical Acquires on the same interface. Zero disables the
// inter-command delay.
//
// Aliased from [hmreliability.ThrottleInterCommandDelay] so the reliability
// snapshot test owns the value.
const DefaultInterCommandDelay = hmreliability.ThrottleInterCommandDelay

// ThrottleConfig configures a [CommandThrottle].
type ThrottleConfig struct {
	// MaxInFlight caps concurrent acquired permits.
	MaxInFlight int

	// BurstThreshold is the maximum number of non-critical Acquire calls allowed
	// within BurstWindow. Zero or negative disables burst protection — every
	// Acquire flows through the regular MaxInFlight semaphore.
	BurstThreshold int

	// BurstWindow is the sliding window across which BurstThreshold is counted.
	// Ignored when BurstThreshold <= 0.
	BurstWindow time.Duration

	// InterCommandDelay is the minimum gap between consecutive non-critical
	// Acquire completions. If time.Since(lastCommandAt) < InterCommandDelay the
	// next Acquire sleeps for the remainder before proceeding. CRITICAL-priority
	// Acquires bypass the delay entirely. Zero disables the guard.
	InterCommandDelay time.Duration

	// MaxQueueDepth caps the number of pending non-critical waiters.
	// When the queue is at the cap, new non-critical Acquire calls
	// return [ErrThrottleQueueFull] immediately instead of joining
	// the heap. CRITICAL-priority Acquires bypass the cap. Zero or
	// negative disables the guard (unbounded queue — historic
	// behaviour). Recommended setting: 4× MaxInFlight.
	//
	// Backpressure rationale (SPECIFICATION §8.4 + audit R5): a stalled
	// CCU drains the in-flight semaphore at zero rate; without a depth
	// cap, scheduler bursts pile new waiters onto the heap until the
	// daemon OOMs. The cap converts that failure mode into a fail-fast
	// error path that the caller's [Retrier] handles with backoff.
	MaxQueueDepth int

	// Clock is the time source for the burst-window pruning + the
	// wait timer. Nil falls back to the real wall clock.
	Clock clock.Clock
}

// CommandThrottle is a priority-aware semaphore: at most MaxInFlight
// callers hold a permit at any time, and waiters are released in
// priority order (CRITICAL first, then HIGH, then LOW). Within the
// same priority, order is FIFO.
//
// Optionally, BurstThreshold + BurstWindow add RF-duty-cycle
// protection: when more than BurstThreshold non-critical Acquires
// happen inside the rolling window, additional Acquires wait until
// the oldest entry expires. CRITICAL-priority Acquires bypass the
// burst guard entirely so emergency commands (alarm, dead-man) get
// through even during a flood.
//
// Optionally, InterCommandDelay enforces a minimum gap between
// Consecutive non-critical Acquires — mirrors
// `command_throttle_interval` worker-loop delay.
type CommandThrottle struct {
	mu        sync.Mutex
	closeOnce sync.Once
	closeCh   chan struct{} // closed once when Close() is called; signals burst-slot waiters

	inFlight int
	capacity int
	waiters  waiterHeap
	closed   bool
	nextSeq  uint64
	clk      clock.Clock

	burstThreshold int
	burstWindow    time.Duration
	burstSamples   []time.Time // sliding window of non-critical Acquires

	// maxQueueDepth bounds the size of waiters; 0 = unbounded.
	maxQueueDepth int
	// queueRejectedCount tracks non-critical Acquires rejected because
	// the queue was full. Mirrors throttledCount but for the fail-fast
	// path.
	queueRejectedCount int64

	// inter-command delay
	interCommandDelay time.Duration
	lastCommandAt     time.Time // wall-clock time of the last non-critical Acquire completion

	// telemetry counters (cumulative since construction)
	waitedForBurstSlot    int64 // number of Acquires that actually blocked in waitForBurstSlot
	waitedForCommandDelay int64 // number of Acquires that actually blocked in waitForCommandDelay
	suspended             int64 // number of waiters forcibly released by Close()

	// Parity counters mirroring
	// (command_throttle.py: _critical_count, _purged_count, _throttled_count).
	criticalCount        int64 // CRITICAL-priority Acquires served (bypass path)
	purgedCount          int64 // waiters cancelled by Purge()
	throttledCount       int64 // non-critical Acquires that waited (burst+delay combined)
	burstDowngradedCount int64 // non-critical HIGH-priority acquires that had to wait for a
	// burst-window slot — mirrors the HIGH→LOW downgrade path in
	// Py:243. Incremented when a HIGH-priority
	// call is delayed by the burst guard.
}

// NewThrottle returns a throttle with capacity MaxInFlight (default 1).
func NewThrottle(cfg ThrottleConfig) *CommandThrottle {
	capacity := cfg.MaxInFlight
	if capacity <= 0 {
		capacity = 1
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	t := &CommandThrottle{
		capacity:          capacity,
		clk:               clk,
		closeCh:           make(chan struct{}),
		interCommandDelay: cfg.InterCommandDelay,
		maxQueueDepth:     cfg.MaxQueueDepth,
	}
	if cfg.BurstThreshold > 0 && cfg.BurstWindow > 0 {
		t.burstThreshold = cfg.BurstThreshold
		t.burstWindow = cfg.BurstWindow
		t.burstSamples = make([]time.Time, 0, cfg.BurstThreshold*2)
	}
	return t
}

// Acquire blocks until a permit is available or ctx is cancelled.
// Release must be called exactly once per Acquire.
//
// Burst-throttle behaviour: when configured, Acquire counts the
// timestamps of non-critical calls in a sliding window. If the
// window already holds BurstThreshold entries, Acquire waits the
// remaining lifetime of the oldest entry before continuing.
// CRITICAL Acquires never wait for the burst window; they only
// observe the regular semaphore.
func (t *CommandThrottle) Acquire(ctx context.Context, prio hmenum.CommandPriority) error {
	return t.acquireFor(ctx, prio, "")
}

// AcquireFor is like [Acquire] but tags the waiter with addr so a
// later [Purge](addr) call can cancel it. Useful when the same
// channel address might receive a newer command before the older
// one drains the queue — calling Purge on the address cancels every
// pending waiter for that address with [ErrSuperseded].
func (t *CommandThrottle) AcquireFor(ctx context.Context, prio hmenum.CommandPriority, addr string) error {
	return t.acquireFor(ctx, prio, addr)
}

func (t *CommandThrottle) acquireFor(ctx context.Context, prio hmenum.CommandPriority, addr string) error {
	// track CRITICAL bypasses.
	if prio == hmenum.CommandPriorityCritical {
		t.mu.Lock()
		t.criticalCount++
		t.mu.Unlock()
	}

	if err := t.waitForBurstSlot(ctx, prio); err != nil {
		return err
	}
	if err := t.waitForCommandDelay(ctx, prio); err != nil {
		return err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrThrottleClosed
	}
	if t.inFlight < t.capacity && t.waiters.Len() == 0 {
		t.inFlight++
		t.recordBurstLocked(prio)
		t.mu.Unlock()
		return nil
	}
	// Non-critical Acquire had to wait — count as throttled.
	if prio != hmenum.CommandPriorityCritical {
		t.throttledCount++
	}
	// MaxQueueDepth backpressure (audit R5): when the heap is at the
	// cap, fail-fast on non-critical work instead of growing the queue
	// unboundedly during a CCU stall. CRITICAL bypasses the gate.
	if t.maxQueueDepth > 0 &&
		prio != hmenum.CommandPriorityCritical &&
		t.waiters.Len() >= t.maxQueueDepth {
		t.queueRejectedCount++
		t.mu.Unlock()
		return ErrThrottleQueueFull
	}
	w := &waiter{
		ready: make(chan struct{}),
		prio:  prio,
		seq:   t.nextSeq,
		addr:  addr,
	}
	t.nextSeq++
	heap.Push(&t.waiters, w)
	t.mu.Unlock()

	select {
	case <-w.ready:
		if w.purged {
			return ErrSuperseded
		}
		if w.closedOut {
			// Woken by Close(): unlike wakeNextLocked, Close does NOT
			// reserve an inFlight permit for the drained waiter, so we
			// must return WITHOUT running the release path below.
			// Releasing a permit we were never handed would underflow
			// the permit accounting and steal a live caller's slot.
			return ErrThrottleClosed
		}
		// We hold the inFlight permit reserved by wakeNextLocked, but
		// we have not yet honoured the rate limits. Re-check the burst
		// window and inter-command delay so a flood of simultaneous
		// callers cannot bypass them just by being queued before any
		// sample was recorded — the queue path must observe the same
		// rate as a fresh caller would.
		if err := t.waitForBurstSlot(ctx, prio); err != nil {
			t.releaseAdmittedSlot()
			return err
		}
		if err := t.waitForCommandDelay(ctx, prio); err != nil {
			t.releaseAdmittedSlot()
			return err
		}
		t.mu.Lock()
		t.recordBurstLocked(prio)
		t.mu.Unlock()
		return nil
	case <-ctx.Done():
		t.cancelWaiter(w)
		return ctx.Err()
	}
}

// releaseAdmittedSlot returns an inFlight permit that was reserved
// by wakeNextLocked when a waiter was woken, but the caller never
// proceeded (typically because ctx cancelled while it was honouring
// the burst-window or inter-command delay on the wake path).
func (t *CommandThrottle) releaseAdmittedSlot() {
	t.mu.Lock()
	if t.inFlight > 0 {
		t.inFlight--
	}
	t.wakeNextLocked()
	t.mu.Unlock()
}

// Purge cancels every queued waiter whose address equals any of the
// provided addrs, returning [ErrSuperseded] from their Acquire calls.
// Newer commands that already hold a permit are unaffected — the caller
// must additionally cancel any in-flight retries via context. The total
// number of cancelled waiters is returned for telemetry.
//
// Passing zero arguments or only empty strings is a no-op and returns 0.
// Passing a single address is identical to the pre-variadic single-address
// form; passing multiple addresses purges all matching waiters in one
// lock acquisition.
func (t *CommandThrottle) Purge(addrs ...string) int {
	// Build a set of non-empty addresses to match against.
	addrSet := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		if a != "" {
			addrSet[a] = struct{}{}
		}
	}
	if len(addrSet) == 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	// Walk the heap linearly; the queue is bounded so O(n) is fine.
	survivors := t.waiters[:0]
	for _, w := range t.waiters {
		if _, match := addrSet[w.addr]; match {
			w.purged = true
			close(w.ready)
			n++
			continue
		}
		survivors = append(survivors, w)
	}
	t.waiters = survivors
	heap.Init(&t.waiters)
	// track purge count.
	t.purgedCount += int64(n)
	return n
}

// waitForBurstSlot blocks until the rolling burst window has room
// for a non-critical Acquire. CRITICAL bypasses the wait entirely.
// The function loops because the window may admit other Acquires
// while we wait — the oldest sample changes accordingly.
func (t *CommandThrottle) waitForBurstSlot(ctx context.Context, prio hmenum.CommandPriority) error {
	if prio == hmenum.CommandPriorityCritical {
		return nil
	}
	waited := false // track whether this call ever actually blocked
	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return ErrThrottleClosed
		}
		if t.burstThreshold == 0 {
			t.mu.Unlock()
			return nil
		}
		now := t.clk.Now()
		t.pruneBurstLocked(now)
		if len(t.burstSamples) < t.burstThreshold {
			t.mu.Unlock()
			return nil
		}
		// Wait until the oldest sample falls out of the window.
		wait := t.burstSamples[0].Add(t.burstWindow).Sub(now)
		t.mu.Unlock()
		if wait <= 0 {
			continue
		}
		// Count this Acquire as a waiting one (at most once per call).
		if !waited {
			waited = true
			t.mu.Lock()
			t.waitedForBurstSlot++
			// HIGH-priority Acquires that block here are counted as
			// "burst-downgraded" — mirrors the HIGH→LOW downgrade
			// metric (command_throttle.py:243).
			if prio == hmenum.CommandPriorityHigh {
				t.burstDowngradedCount++
			}
			t.mu.Unlock()
		}
		timer := t.clk.NewTimer(wait)
		select {
		case <-timer.C():
		case <-t.closeCh:
			timer.Stop()
			t.mu.Lock()
			t.suspended++
			t.mu.Unlock()
			return ErrThrottleClosed
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// waitForCommandDelay enforces the minimum inter-command gap. If
// time.Since(lastCommandAt) < InterCommandDelay the caller sleeps for
// the remainder. CRITICAL-priority Acquires skip the delay entirely.
// The method loops because multiple goroutines may wake simultaneously
// and only one can claim the slot; after waking, the delay must be
// re-checked.
func (t *CommandThrottle) waitForCommandDelay(ctx context.Context, prio hmenum.CommandPriority) error {
	if prio == hmenum.CommandPriorityCritical || t.interCommandDelay <= 0 {
		return nil
	}
	waited := false
	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return ErrThrottleClosed
		}
		now := t.clk.Now()
		elapsed := now.Sub(t.lastCommandAt)
		if elapsed >= t.interCommandDelay || t.lastCommandAt.IsZero() {
			t.mu.Unlock()
			return nil
		}
		remain := t.interCommandDelay - elapsed
		t.mu.Unlock()

		if !waited {
			waited = true
			t.mu.Lock()
			t.waitedForCommandDelay++
			t.mu.Unlock()
		}

		timer := t.clk.NewTimer(remain)
		select {
		case <-timer.C():
			// Re-check in the next iteration.
		case <-t.closeCh:
			timer.Stop()
			return ErrThrottleClosed
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// recordBurstLocked appends a non-critical Acquire timestamp to the
// sliding window and updates lastCommandAt. CRITICAL is excluded so
// emergency traffic doesn't inflate the counter.
func (t *CommandThrottle) recordBurstLocked(prio hmenum.CommandPriority) {
	if prio == hmenum.CommandPriorityCritical {
		return
	}
	now := t.clk.Now()
	t.lastCommandAt = now
	if t.burstThreshold == 0 {
		return
	}
	t.pruneBurstLocked(now)
	t.burstSamples = append(t.burstSamples, now)
}

// pruneBurstLocked drops samples that fell out of the window.
func (t *CommandThrottle) pruneBurstLocked(now time.Time) {
	cutoff := now.Add(-t.burstWindow)
	idx := 0
	for ; idx < len(t.burstSamples); idx++ {
		if t.burstSamples[idx].After(cutoff) {
			break
		}
	}
	if idx > 0 {
		t.burstSamples = append(t.burstSamples[:0], t.burstSamples[idx:]...)
	}
}

// Release frees the permit and admits the highest-priority waiter.
func (t *CommandThrottle) Release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inFlight > 0 {
		t.inFlight--
	}
	t.wakeNextLocked()
}

// Close refuses further Acquire calls and releases every waiter with
// [ErrThrottleClosed]. Goroutines blocked in waitForBurstSlot are also
// woken via the internal close channel; they increment suspended themselves.
func (t *CommandThrottle) Close() {
	t.mu.Lock()
	t.closed = true
	for t.waiters.Len() > 0 {
		w := mustPopWaiter(&t.waiters)
		t.suspended++
		// Flag the drained waiter so its wake path in acquireFor returns
		// ErrThrottleClosed without calling releaseAdmittedSlot — Close
		// pops the waiter without reserving an inFlight permit for it, so
		// the wake path must not release one (mirrors how Purge flags
		// waiters with purged).
		w.closedOut = true
		close(w.ready)
	}
	t.mu.Unlock()
	// Signal goroutines blocked in waitForBurstSlot (idempotent via Once).
	t.closeOnce.Do(func() { close(t.closeCh) })
}

// InFlight returns the current permit count (for metrics / tests).
func (t *CommandThrottle) InFlight() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inFlight
}

// Waiting returns the current waiter count (for metrics / tests).
func (t *CommandThrottle) Waiting() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waiters.Len()
}

// QueueRejectedCount returns the cumulative number of non-critical
// Acquire calls that were rejected with [ErrThrottleQueueFull] because
// the queue was at [ThrottleConfig.MaxQueueDepth]. Surfaces the
// backpressure pressure so operators can distinguish a stalled CCU
// (high reject rate) from healthy throttling (low reject rate, high
// throttledCount).
func (t *CommandThrottle) QueueRejectedCount() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.queueRejectedCount
}

// WaitedForBurstSlot returns how many Acquire calls were ever delayed
// by the burst window (cumulative since construction).
func (t *CommandThrottle) WaitedForBurstSlot() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waitedForBurstSlot
}

// WaitedForCommandDelay returns how many Acquire calls were ever
// delayed by the inter-command gap (cumulative since construction).
func (t *CommandThrottle) WaitedForCommandDelay() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waitedForCommandDelay
}

// Suspended returns how many waiters were forcibly released by [Close]
// (cumulative since construction).
func (t *CommandThrottle) Suspended() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suspended
}

// CriticalCount returns the number of CRITICAL-priority Acquires served since
// construction (cumulative). CRITICAL commands bypass the burst guard and
// inter-command delay entirely.
func (t *CommandThrottle) CriticalCount() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.criticalCount
}

// PurgedCount returns the cumulative number of waiters cancelled by [Purge]
// since construction.
func (t *CommandThrottle) PurgedCount() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.purgedCount
}

// ThrottledCount returns the cumulative number of non-critical Acquires that
// waited behind another in-flight command (i.e. were queued).
func (t *CommandThrottle) ThrottledCount() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.throttledCount
}

// AcquireAndPurge atomically purges any pending waiters for addr and
// then calls [AcquireFor] with the same address. This is the atomic
// parameter (command_throttle.py:acquire): the purge and the
// new acquire happen as a single logical operation so a newer command
// for the same address can preempt all older pending commands without
// a race window between the Purge and the AcquireFor calls.
//
// Returns the same errors as [AcquireFor]: nil on success,
// [ErrThrottleClosed] when the throttle is closed, or ctx.Err() on
// cancellation.
func (t *CommandThrottle) AcquireAndPurge(ctx context.Context, prio hmenum.CommandPriority, addr string) error {
	if addr != "" {
		t.Purge(addr)
	}
	return t.AcquireFor(ctx, prio, addr)
}

// IsEnabled reports whether throttling is active (i.e. a positive
// InterCommandDelay or BurstThreshold has been configured). When false, every
// Acquire returns immediately without waiting.
func (t *CommandThrottle) IsEnabled() bool {
	// No lock needed — interCommandDelay and burstThreshold are set at
	// construction and never mutated.
	return t.interCommandDelay > 0 || t.burstThreshold > 0
}

// BurstThresholdValue returns the configured burst threshold (maximum number
// of non-critical Acquires allowed within the burst window). Zero means burst
// protection is disabled. No lock needed — set at construction and never
// mutated.
func (t *CommandThrottle) BurstThresholdValue() int {
	return t.burstThreshold
}

// BurstWindowValue returns the configured burst detection window duration.
// Zero means burst protection is disabled. No lock needed — set at
// construction and never mutated.
func (t *CommandThrottle) BurstWindowValue() time.Duration {
	return t.burstWindow
}

// IntervalValue returns the configured inter-command delay (minimum gap
// between consecutive non-critical Acquires). Zero means the delay is
// disabled. No lock needed — set at construction and never mutated.
func (t *CommandThrottle) IntervalValue() time.Duration {
	return t.interCommandDelay
}

// BurstCount returns the number of Acquire calls that were ever delayed
// by the burst-window guard. Alias for [WaitedForBurstSlot] provided
// For py:burst_count).
func (t *CommandThrottle) BurstCount() int64 {
	return t.WaitedForBurstSlot()
}

// BurstDowngraded returns the cumulative number of HIGH-priority Acquire
// calls that were delayed by the burst-window guard. Mirrors the
// HIGH→LOW downgrade metric in
// (command_throttle.py:243): when a HIGH call is delayed by the burst guard
// it is counted as a downgrade because it is effectively treated at LOW level
// until a burst slot is free.
//
// This count is a subset of [WaitedForBurstSlot] (HIGH-priority calls only,
// not all non-critical).
func (t *CommandThrottle) BurstDowngraded() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.burstDowngradedCount
}

// QueueSize returns the current number of waiters queued behind the
// Capacity limit. Alias for [Waiting] provided for
// parity (command_throttle.py:queue_size).
func (t *CommandThrottle) QueueSize() int {
	return t.Waiting()
}

func (t *CommandThrottle) wakeNextLocked() {
	if t.inFlight >= t.capacity {
		return
	}
	if t.waiters.Len() == 0 {
		return
	}
	w := mustPopWaiter(&t.waiters)
	t.inFlight++
	close(w.ready)
}

func (t *CommandThrottle) cancelWaiter(w *waiter) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Walk the heap linearly — O(n) is acceptable because ctx cancels
	// are rare and the queue is bounded.
	for i, other := range t.waiters {
		if other == w {
			heap.Remove(&t.waiters, i)
			return
		}
	}
	// Already admitted; emulate a Release so we don't leak a permit.
	select {
	case <-w.ready:
		t.inFlight--
		t.wakeNextLocked()
	default:
	}
}

// --- waiter priority queue ---

type waiter struct {
	ready chan struct{}
	prio  hmenum.CommandPriority
	seq   uint64
	addr  string // optional: used by Purge to cancel address-scoped waiters
	// purged is set to true when Purge cancelled this waiter; the
	// goroutine in Acquire reads it after select to surface
	// [ErrSuperseded].
	purged bool
	// closedOut is set to true when Close() drained this queued waiter.
	// Unlike a wakeNextLocked wake, Close reserves no inFlight permit, so
	// the wake path reads this after select and returns [ErrThrottleClosed]
	// without releasing an unheld slot.
	closedOut bool
}

type waiterHeap []*waiter

func (h waiterHeap) Len() int { return len(h) }
func (h waiterHeap) Less(i, j int) bool {
	// Lower priority value = higher urgency (CRITICAL=0 wins).
	if h[i].prio != h[j].prio {
		return h[i].prio < h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h waiterHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *waiterHeap) Push(x any) {
	if w, ok := x.(*waiter); ok {
		*h = append(*h, w)
	}
}

func (h *waiterHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// mustPopWaiter pops a *waiter from the heap. Guaranteed to succeed
// because only *waiter items are pushed; panics on mismatch to make
// any future bug loud.
func mustPopWaiter(h *waiterHeap) *waiter {
	w, ok := heap.Pop(h).(*waiter)
	if !ok {
		panic("throttle: heap holds non-waiter entry")
	}
	return w
}
