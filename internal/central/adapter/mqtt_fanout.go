// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// mqttFanoutQueueDepth bounds the per-broker publish queue. A slow or
// half-open broker cannot grow it without limit: once full the oldest queued
// publish is dropped (drop-oldest) and counted, mirroring the WebSocket hub's
// drop-on-full backpressure in internal/north/rest/ws/client.go.
const mqttFanoutQueueDepth = 4096

// mqttFanout decouples the north-bound MQTT publish work from the event-bus
// dispatch goroutine. Live value-change fan-out is enqueued here — a
// non-blocking, bounded, drop-oldest handoff — and drained by a single worker
// goroutine. A broker that blocks (for example a QoS1 PUBACK wait up to the
// transport's AckTimeout on a half-open connection) therefore can never stall
// bus dispatch, and so can never freeze event delivery for the other centrals
// that share the same broker.
//
// The single worker per broker preserves the FIFO order in which publishes were
// enqueued, which keeps per-data-point ordering intact.
type mqttFanout struct {
	queue chan func()

	// enqueueMu serialises producers so the drop-oldest eviction (a paired
	// non-blocking receive + send) stays atomic against other producers. It is
	// only ever held around bounded channel operations, never around broker
	// I/O, so it cannot propagate broker latency back into bus dispatch.
	enqueueMu sync.Mutex

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
		queue:  make(chan func(), mqttFanoutQueueDepth),
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
		select {
		case <-f.ctx.Done():
			return
		case job := <-f.queue:
			job()
		}
	}
}

// enqueue hands job to the drain worker without ever blocking. When the queue
// is full the oldest pending job is evicted to make room (drop-oldest) and the
// drop is counted — the bus dispatch goroutine must never block here.
func (f *mqttFanout) enqueue(job func()) {
	f.enqueueMu.Lock()
	defer f.enqueueMu.Unlock()
	select {
	case f.queue <- job:
		return
	default:
	}
	// Full: evict the oldest queued job, then enqueue. With enqueueMu held the
	// only other actor on the queue is the worker (which only receives), so
	// after one successful eviction the send below always has room.
	select {
	case <-f.queue:
		f.recordDrop()
	default:
	}
	select {
	case f.queue <- job:
	default:
		// Producers are serialised by enqueueMu and the worker never fills the
		// queue, so this branch is unreachable in practice; drop defensively
		// rather than block the bus dispatch goroutine.
		f.recordDrop()
	}
}

func (f *mqttFanout) recordDrop() {
	f.dropped.Add(1)
	if f.warnedDrop.CompareAndSwap(false, true) {
		f.logger.Warn("mqtt.fanout.backpressure", slog.Int("queue_depth", mqttFanoutQueueDepth))
	}
}

// stop cancels in-flight broker I/O and waits for the worker to exit. Safe to
// call when start was never invoked.
func (f *mqttFanout) stop() {
	if f.cancel != nil {
		f.cancel()
	}
	f.wg.Wait()
}

// flush blocks until every job enqueued before this call has been drained by
// the worker. It is a test barrier — production code never needs it. Returns
// early if the worker is stopping.
func (f *mqttFanout) flush() {
	if f.ctx == nil {
		return
	}
	done := make(chan struct{})
	f.enqueue(func() { close(done) })
	select {
	case <-done:
	case <-f.ctx.Done():
	}
}

// droppedCount reports how many publishes were dropped because the queue was
// full. Monotonic.
func (f *mqttFanout) droppedCount() uint64 { return f.dropped.Load() }

// queueDepth reports the number of jobs currently waiting to be drained.
func (f *mqttFanout) queueDepth() int { return len(f.queue) }
