// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// mqttFanoutQueueDepth is the soft bound on the per-broker publish queue. A
// slow or half-open broker cannot grow it without limit as long as evictable
// work is pending: once the depth is reached the oldest *evictable* publish is
// dropped and counted, mirroring the WebSocket hub's drop-on-full backpressure
// in internal/north/rest/ws/client.go.
const mqttFanoutQueueDepth = 4096

// fanoutJob is one queued broker interaction plus the drop policy that applies
// to it when the queue overflows.
//
// The two classes exist because losing a retained MQTT publish has two very
// different consequences:
//
//   - A live value-change state publish is *self-healing*: the topic is
//     retained and the next sample of the same data point overwrites it, so a
//     dropped sample costs at most one stale reading for one refresh interval.
//     Those jobs are evictable.
//   - A discovery config, a device/central snapshot or a hub-plane aggregate
//     replacement is *declarative*: nothing re-sends it. Dropping one makes an
//     entity absent from Home Assistant — or frozen on a stale aggregate —
//     until the operator restarts the daemon. Those jobs are durable and are
//     never dropped; the queue is allowed to grow past the soft bound instead,
//     because their arrival rate is bounded by CCU bring-up and hub-refresh
//     cadence rather than by device event traffic.
type fanoutJob struct {
	run       func()
	evictable bool
}

// mqttFanout decouples the north-bound MQTT publish work from the event-bus
// dispatch goroutine. Publishes are enqueued here — a non-blocking handoff —
// and drained by a single worker goroutine. A broker that blocks (for example
// a QoS1 PUBACK wait up to the transport's AckTimeout on a half-open
// connection) therefore can never stall bus dispatch, and so can never freeze
// event delivery for the other centrals that share the same broker.
//
// The single worker preserves the FIFO order in which publishes were enqueued.
// That is what keeps discovery ahead of state and per-data-point ordering
// intact, and it is also why any state a handler owns (a discovery dedup map,
// for instance) stays race-free once every access is enqueued: the worker is
// the only goroutine that touches it.
type mqttFanout struct {
	// mu guards queue and evictableCount. It is only ever held around bounded
	// slice bookkeeping, never around broker I/O, so it cannot propagate broker
	// latency back into bus dispatch.
	mu    sync.Mutex
	queue []fanoutJob
	// evictableCount tracks how many queued jobs may be dropped, so an
	// overflowing queue that holds only durable work skips the eviction scan.
	evictableCount int

	// notify wakes the worker when work arrives. Buffered with depth one: the
	// worker always drains the queue before it waits again, so a single pending
	// wakeup is enough and a full buffer is not a lost signal.
	notify chan struct{}

	dropped atomic.Uint64
	// warnedDrop fires a single slog.Warn the first time the queue overflows,
	// then self-suppresses so a persistently slow broker does not flood the log
	// stream; the dropped counter stays available for scraping.
	warnedDrop atomic.Bool

	logger *slog.Logger

	//nolint:containedctx // worker lifetime; passed to publishes so stop() cancels in-flight broker I/O
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newMQTTFanout builds a fanout with the shared queue depth. Call start before
// enqueue.
func newMQTTFanout() *mqttFanout {
	return &mqttFanout{
		notify: make(chan struct{}, 1),
		logger: slog.Default(),
	}
}

// start launches the drain worker bound to a child of parent. The worker owns
// the child context and passes it to every publish, so stop() aborts in-flight
// broker I/O promptly. Each fanout instance is started at most once.
func (f *mqttFanout) start(parent context.Context) {
	f.ctx, f.cancel = context.WithCancel(parent)
	f.wg.Add(1)
	go f.run()
}

func (f *mqttFanout) run() {
	defer f.wg.Done()
	for {
		// Bail out promptly once cancelled even if the queue still holds work:
		// a cancelled context makes every remaining publish fail fast, so
		// draining them would only spin.
		if f.ctx.Err() != nil {
			return
		}
		if job, ok := f.pop(); ok {
			job()
			continue
		}
		select {
		case <-f.ctx.Done():
			return
		case <-f.notify:
		}
	}
}

// pop removes the head of the queue. Reports false when the queue is empty.
func (f *mqttFanout) pop() (func(), bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return nil, false
	}
	job := f.queue[0]
	// Clear the slot before re-slicing so the closure (and everything it
	// captures) is not pinned by the backing array.
	f.queue[0] = fanoutJob{}
	f.queue = f.queue[1:]
	if job.evictable {
		f.evictableCount--
	}
	return job.run, true
}

// enqueue hands an evictable publish to the drain worker without ever blocking.
// Use it for self-healing state publishes; see [fanoutJob].
func (f *mqttFanout) enqueue(job func()) {
	f.push(fanoutJob{run: job, evictable: true})
}

// enqueueDurable hands a publish that must not be dropped to the drain worker,
// again without ever blocking. Use it for discovery, snapshots and aggregate
// replacements; see [fanoutJob].
func (f *mqttFanout) enqueueDurable(job func()) {
	f.push(fanoutJob{run: job, evictable: false})
}

// push appends job, evicting the oldest evictable entry first when the queue
// has reached its soft bound. It never blocks the calling goroutine — the bus
// dispatch goroutine must always return promptly.
func (f *mqttFanout) push(job fanoutJob) {
	f.mu.Lock()
	evicted := false
	if len(f.queue) >= mqttFanoutQueueDepth && f.evictableCount > 0 {
		evicted = f.evictOldestEvictableLocked()
	}
	f.queue = append(f.queue, job)
	if job.evictable {
		f.evictableCount++
	}
	f.mu.Unlock()

	// Log outside the lock so a slow log handler cannot serialise producers.
	if evicted {
		f.recordDrop()
	}
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

// evictOldestEvictableLocked drops the oldest evictable job, preserving the
// relative order of everything that stays queued. Callers hold f.mu.
func (f *mqttFanout) evictOldestEvictableLocked() bool {
	for i := range f.queue {
		if !f.queue[i].evictable {
			continue
		}
		last := len(f.queue) - 1
		copy(f.queue[i:], f.queue[i+1:])
		// Clear the vacated tail slot so the evicted closure is not pinned by
		// the backing array.
		f.queue[last] = fanoutJob{}
		f.queue = f.queue[:last]
		f.evictableCount--
		return true
	}
	return false
}

func (f *mqttFanout) recordDrop() {
	f.dropped.Add(1)
	if f.warnedDrop.CompareAndSwap(false, true) {
		f.logger.Warn("mqtt.fanout.backpressure", slog.Int("queue_depth", mqttFanoutQueueDepth))
	}
}

// stop cancels in-flight broker I/O and waits for the worker to exit. Safe to
// call when start was never invoked. Whatever is still queued is discarded:
// run() returns on a cancelled context without draining, by design, because
// every remaining publish would fail fast under that same context anyway.
//
// "AND EVERY DURABLE JOB IS RE-ISSUED FROM SCRATCH BY THE NEXT START" USED TO
// STAND HERE, AND IT IS FALSE FOR ONE CLASS OF JOB. A RETRACTION is not
// re-issued by the next Start — the next Start publishes the DECLARE instead,
// which is the opposite write. So a generation swap landing between
// [HubMQTTPublisher.RetractCentral]'s enqueue and the worker's drain discards
// the retract for good, and the removed CCU's retained discovery configs and
// gate topic stay on the broker forever (the orphan sweep is scoped to
// REGISTERED centrals, so nothing ever reaches them again). Reproduced in
// TestAQueuedRetractSurvivesABrokerReconnect. Callers that can be holding a
// retraction must use [mqttFanout.stopDraining].
func (f *mqttFanout) stop() {
	if f.cancel != nil {
		f.cancel()
	}
	f.wg.Wait()
}

// fanoutStopGrace bounds how long a generation swap waits for the durable
// remainder of the retiring queue before it cancels anyway.
//
// SHORT ON PURPOSE. It covers the case that actually loses a retract — the
// worker mid-publish against a broker that is answering, which is what a
// reconnect implies in the first place, and which takes milliseconds — and it
// does NOT try to outlast a broker that accepts bytes and never acknowledges.
// A retract is unwritable to such a broker anyway, and this same teardown is
// the daemon's shutdown path, whose contract (pinned by
// TestHubMQTTPublisherStopCancelsInflightPublish) is that Stop returns even
// while the worker is wedged in a publish. Half a second is a politeness
// window, not a deadline anything relies on.
const fanoutStopGrace = 500 * time.Millisecond

// stopDraining is [mqttFanout.stop] with a bounded chance for the queued
// DURABLE jobs to run first. Use it wherever the queue may hold a write that
// the next generation will not re-issue — a retraction above all.
//
// Evictable jobs are dropped rather than waited for: they are self-healing
// state publishes, the next Start re-issues every one of them, and waiting on
// a backlog of them would put a full hub-plane republish in front of the
// on-connect re-wire that triggered the swap.
//
// A context that is ALREADY cancelled means the parent is going away (daemon
// shutdown), so there is nothing to drain into and no grace is spent.
func (f *mqttFanout) stopDraining(grace time.Duration) {
	if f.ctx == nil || f.ctx.Err() != nil {
		f.stop()
		return
	}
	f.dropEvictable()
	// The barrier is itself durable, so an overflowing queue cannot evict it,
	// and it runs after everything enqueued before this call.
	done := make(chan struct{})
	f.enqueueDurable(func() { close(done) })
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		f.logger.Warn("mqtt.fanout.drain_timeout", slog.Duration("grace", grace))
	case <-f.ctx.Done():
	}
	f.stop()
}

// dropEvictable removes every evictable job from the queue, preserving the
// order of the durable remainder.
func (f *mqttFanout) dropEvictable() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.evictableCount == 0 {
		return
	}
	kept := f.queue[:0]
	for _, job := range f.queue {
		if !job.evictable {
			kept = append(kept, job)
		}
	}
	// Clear the vacated tail so dropped closures are not pinned by the
	// backing array.
	for i := len(kept); i < len(f.queue); i++ {
		f.queue[i] = fanoutJob{}
	}
	f.queue = kept
	f.evictableCount = 0
}

// flush blocks until every job enqueued before this call has been drained by
// the worker. It is a test barrier — production code never needs it. The
// barrier itself is durable so an overflowing queue cannot evict it. Returns
// early if the worker is stopping.
func (f *mqttFanout) flush() {
	if f.ctx == nil {
		return
	}
	done := make(chan struct{})
	f.enqueueDurable(func() { close(done) })
	select {
	case <-done:
	case <-f.ctx.Done():
	}
}

// droppedCount reports how many publishes were dropped because the queue was
// full. Monotonic.
func (f *mqttFanout) droppedCount() uint64 { return f.dropped.Load() }

// queueDepth reports the number of jobs currently waiting to be drained.
func (f *mqttFanout) queueDepth() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}
