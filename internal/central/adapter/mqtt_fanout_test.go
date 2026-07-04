// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestMQTTFanoutDrainsInFIFOOrder verifies the single worker preserves the
// enqueue order, which is what keeps per-data-point publish ordering intact.
func TestMQTTFanoutDrainsInFIFOOrder(t *testing.T) {
	t.Parallel()
	f := newMQTTFanout()
	f.start(context.Background())
	defer f.stop()

	const n = 200
	var mu sync.Mutex
	got := make([]int, 0, n)
	for i := range n {
		idx := i
		f.enqueue(func() {
			mu.Lock()
			got = append(got, idx)
			mu.Unlock()
		})
	}
	f.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("drained %d jobs, want %d", len(got), n)
	}
	for i := range n {
		if got[i] != i {
			t.Fatalf("out-of-order drain at %d: got %d", i, got[i])
		}
	}
}

// TestMQTTFanoutDropsOldestOnOverflow verifies the bounded queue never blocks
// the producer: once full it drops the OLDEST pending job, counts the drop, and
// keeps the freshest publishes. The worker is started only after the queue has
// overflowed so the fill is deterministic.
func TestMQTTFanoutDropsOldestOnOverflow(t *testing.T) {
	t.Parallel()
	f := newMQTTFanout()

	const extra = 5
	total := mqttFanoutQueueDepth + extra

	var mu sync.Mutex
	got := make([]int, 0, mqttFanoutQueueDepth)
	for i := range total {
		idx := i
		f.enqueue(func() {
			mu.Lock()
			got = append(got, idx)
			mu.Unlock()
		})
	}

	if d := f.droppedCount(); d != extra {
		t.Fatalf("dropped=%d, want %d", d, extra)
	}
	if q := f.queueDepth(); q != mqttFanoutQueueDepth {
		t.Fatalf("queueDepth=%d, want %d", q, mqttFanoutQueueDepth)
	}

	// Drain the survivors and confirm the oldest `extra` were the ones dropped:
	// the queue should now hold indices [extra, total). Poll until the backlog
	// clears before flushing — enqueuing the flush barrier while the queue is
	// still full would itself drop-oldest a survivor and skew the assertion.
	f.start(context.Background())
	defer f.stop()
	deadline := time.Now().Add(2 * time.Second)
	for f.queueDepth() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("fan-out queue did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	f.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != mqttFanoutQueueDepth {
		t.Fatalf("drained %d survivors, want %d", len(got), mqttFanoutQueueDepth)
	}
	if got[0] != extra {
		t.Fatalf("oldest survivor is %d, want %d (drop-oldest not honoured)", got[0], extra)
	}
	if got[len(got)-1] != total-1 {
		t.Fatalf("newest survivor is %d, want %d", got[len(got)-1], total-1)
	}
}

// TestMQTTFanoutStopCancelsInflightJob verifies stop() unblocks a worker that
// is stuck in a publish by cancelling the context it passed in, so a shutdown
// never hangs behind a slow / half-open broker.
func TestMQTTFanoutStopCancelsInflightJob(t *testing.T) {
	t.Parallel()
	f := newMQTTFanout()
	f.start(context.Background())

	entered := make(chan struct{})
	f.enqueue(func() {
		close(entered)
		<-f.ctx.Done() // simulate a broker publish that only returns on cancel
	})
	<-entered

	stopped := make(chan struct{})
	go func() {
		f.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() hung behind an in-flight job")
	}
}

// TestMQTTFanoutFlushBeforeStartIsNoop guards the test barrier against being
// called before the worker exists.
func TestMQTTFanoutFlushBeforeStartIsNoop(t *testing.T) {
	t.Parallel()
	f := newMQTTFanout()
	done := make(chan struct{})
	go func() {
		f.flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flush() blocked before start")
	}
}
