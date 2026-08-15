// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBoundedDispatcherEnqueueReturnsBeforeJobCompletes proves the whole
// point of the dispatcher: Enqueue hands the job to a worker and returns
// immediately, even while the job itself is still blocked. A caller on the
// MQTT client's synchronous read loop must never wait for downstream I/O.
func TestBoundedDispatcherEnqueueReturnsBeforeJobCompletes(t *testing.T) {
	t.Parallel()
	d := newBoundedDispatcher(2, 4, "test", nil)
	defer d.Close()

	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	d.Enqueue("key", func() {
		close(started)
		<-release
		close(done)
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job never started")
	}

	// The job is now blocked on <-release. Enqueue must already have
	// returned (it did, synchronously, above this select) — assert the job
	// has NOT finished yet, proving Enqueue did not wait for it.
	select {
	case <-done:
		t.Fatal("job completed before being released; Enqueue must not block on job execution")
	default:
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job never completed after release")
	}
}

// TestBoundedDispatcherEnqueueDoesNotBlockOnAFullQueue is the deadlock
// guard. Every Enqueue caller sits on the MQTT client's single read-loop
// goroutine, which is also the only goroutine that consumes PUBACK and
// PINGRESP. A worker that is blocked on a QoS1 publish therefore cannot
// make room in its queue unless the read loop keeps running, so an
// Enqueue that waits for room deadlocks the whole link: the in-flight
// publish times out, the keepalive watchdog tears the connection down,
// and Close cannot take its write lock either.
func TestBoundedDispatcherEnqueueDoesNotBlockOnAFullQueue(t *testing.T) {
	t.Parallel()
	const depth = 2
	d := newBoundedDispatcher(1, depth, "test", nil)

	release := make(chan struct{})
	started := make(chan struct{})
	var ran atomic.Int32
	// The first job occupies the single worker and never returns until
	// released — exactly the "worker parked on downstream I/O" state.
	d.Enqueue("k", func() {
		close(started)
		<-release
		ran.Add(1)
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first job never started")
	}

	// Overshoot the queue depth by a wide margin: with the worker parked,
	// none of these can run, so every one of them has to be accepted
	// without waiting.
	const overshoot = depth + 8
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range overshoot {
			d.Enqueue("k", func() { ran.Add(1) })
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Enqueue blocked on a full queue — the MQTT read loop cannot make room for itself")
	}

	close(release)
	d.Close()
	if got, want := ran.Load(), int32(overshoot+1); got != want {
		t.Fatalf("ran=%d, want %d — no job may be dropped", got, want)
	}
}

// TestBoundedDispatcherPreservesPerKeyOrder proves that jobs submitted for
// the same key never reorder, even across a worker pool with more than one
// worker.
func TestBoundedDispatcherPreservesPerKeyOrder(t *testing.T) {
	t.Parallel()
	d := newBoundedDispatcher(4, 16, "test", nil)
	defer d.Close()

	const n = 200
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		d.Enqueue("same-datapoint", func() {
			defer wg.Done()
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != n {
		t.Fatalf("got %d completions, want %d", len(order), n)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("order[%d] = %d, want %d — same-key jobs reordered", i, v, i)
		}
	}
}

// TestBoundedDispatcherCloseDrainsQueuedJobs proves Close waits for every
// already-queued job to run before returning — a clean drain, not an abrupt
// stop that would silently discard queued commands.
func TestBoundedDispatcherCloseDrainsQueuedJobs(t *testing.T) {
	t.Parallel()
	d := newBoundedDispatcher(1, 8, "test", nil)

	var ran atomic.Int32
	for range 5 {
		d.Enqueue("k", func() { ran.Add(1) })
	}
	d.Close()
	if got := ran.Load(); got != 5 {
		t.Fatalf("ran=%d, want 5 — Close must drain every queued job", got)
	}

	// A second Close must be a safe no-op.
	d.Close()
}

// TestBoundedDispatcherEnqueueAfterCloseDoesNotRun proves that once Close
// has run, a later Enqueue neither panics (send on closed channel) nor
// executes the job — the dispatcher has torn down its workers.
func TestBoundedDispatcherEnqueueAfterCloseDoesNotRun(t *testing.T) {
	t.Parallel()
	d := newBoundedDispatcher(1, 2, "test", nil)
	d.Close()

	var ran atomic.Int32
	d.Enqueue("k", func() { ran.Add(1) })
	if got := ran.Load(); got != 0 {
		t.Fatalf("ran=%d, want 0 — Enqueue after Close must not run the job", got)
	}
}

// TestBoundedDispatcherFlushWaitsForPriorJobs proves the test-only flush
// barrier waits for jobs enqueued before the call, across every worker.
func TestBoundedDispatcherFlushWaitsForPriorJobs(t *testing.T) {
	t.Parallel()
	d := newBoundedDispatcher(3, 4, "test", nil)
	defer d.Close()

	var ran atomic.Int32
	for i := range 30 {
		d.Enqueue(string(rune('a'+i%3)), func() { ran.Add(1) })
	}
	d.flush()
	if got := ran.Load(); got != 30 {
		t.Fatalf("ran=%d, want 30 after flush", got)
	}
}
