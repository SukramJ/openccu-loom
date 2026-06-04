// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package audit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDurableSinkPersistsEntries verifies the happy path: every
// enqueue eventually reaches the wrapped sink, and the stats reflect
// it.
func TestDurableSinkPersistsEntries(t *testing.T) {
	var sunk atomic.Int32
	sink := func(_ context.Context, _ Entry) error {
		sunk.Add(1)
		return nil
	}
	enqueue, stats, stop := NewDurableSink(sink, DurableSinkOptions{Capacity: 8})
	defer stop()

	for range 5 {
		if err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sunk.Load() == 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := sunk.Load(); got != 5 {
		t.Fatalf("sunk=%d want 5", got)
	}
	if stats.Enqueued() != 5 {
		t.Errorf("Enqueued=%d want 5", stats.Enqueued())
	}
	if stats.Dropped() != 0 {
		t.Errorf("Dropped=%d want 0", stats.Dropped())
	}
}

// TestDurableSinkOverflowReturnsError verifies that a saturated queue
// returns [ErrAuditOverflow] when BlockTimeout > 0 expires, instead
// of silently dropping the entry.
func TestDurableSinkOverflowReturnsError(t *testing.T) {
	// Sink blocks until released — guarantees the queue stays full.
	release := make(chan struct{})
	var blocked sync.WaitGroup
	blocked.Add(1)
	first := true
	sink := func(_ context.Context, _ Entry) error {
		if first {
			first = false
			blocked.Done()
			<-release
		}
		return nil
	}
	enqueue, stats, stop := NewDurableSink(sink, DurableSinkOptions{
		Capacity:     1,
		BlockTimeout: 50 * time.Millisecond,
	})
	defer func() { close(release); stop() }()

	// First entry: worker takes it immediately and blocks inside sink.
	if err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	blocked.Wait() // worker is now stuck in sink, queue cap=1 will fill on next call

	// Second entry: lands in the buffered channel slot.
	if err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite}); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	// Third entry: queue is now full, BlockTimeout expires → ErrAuditOverflow.
	err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite})
	if !errors.Is(err, ErrAuditOverflow) {
		t.Fatalf("third enqueue: got %v, want ErrAuditOverflow", err)
	}
	if stats.Dropped() != 1 {
		t.Errorf("Dropped=%d want 1", stats.Dropped())
	}
}

// TestDurableSinkBlockingDefault verifies that BlockTimeout=0 blocks
// the producer until the queue accepts (matching SPEC §13 append-
// only). The producer's context cancel is the escape hatch.
func TestDurableSinkBlockingDefault(t *testing.T) {
	release := make(chan struct{})
	released := false
	var mu sync.Mutex
	sink := func(_ context.Context, _ Entry) error {
		mu.Lock()
		r := released
		mu.Unlock()
		if !r {
			<-release
			mu.Lock()
			released = true
			mu.Unlock()
		}
		return nil
	}
	enqueue, _, stop := NewDurableSink(sink, DurableSinkOptions{Capacity: 1})
	defer func() { close(release); stop() }()

	// First enters worker, blocks. Second fills the slot.
	_ = enqueue(context.Background(), Entry{Action: ActionParamsetWrite})
	_ = enqueue(context.Background(), Entry{Action: ActionParamsetWrite})

	// Third producer: ctx cancels before release → ctx.Err returned.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := enqueue(ctx, Entry{Action: ActionParamsetWrite})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx-cancelled enqueue: got %v, want context.DeadlineExceeded", err)
	}
}
