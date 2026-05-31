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

// P0-4: PersistedRecorder layers durable persistence on top of the
// in-memory buffer. Both the buffer and the sink see every record;
// sink errors must not propagate to the producer.

func TestPersistedRecorderForwardsToBufferAndSink(t *testing.T) {
	t.Parallel()
	buf := NewBuffer(10)
	var sinkSeen []Entry
	var mu sync.Mutex
	sink := func(_ context.Context, e Entry) error {
		mu.Lock()
		sinkSeen = append(sinkSeen, e)
		mu.Unlock()
		return nil
	}
	rec := NewPersistedRecorder(buf, sink, nil)
	rec.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "AA"})

	if buf.Len() != 1 {
		t.Fatalf("buffer not updated: len=%d", buf.Len())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sinkSeen) != 1 || sinkSeen[0].DeviceAddress != "AA" {
		t.Fatalf("sink not called: %+v", sinkSeen)
	}
}

func TestPersistedRecorderSwallowsSinkError(t *testing.T) {
	t.Parallel()
	buf := NewBuffer(4)
	sink := func(_ context.Context, _ Entry) error { return errors.New("disk full") }
	rec := NewPersistedRecorder(buf, sink, nil)

	// Producer must not panic / err — the buffer remains the live path.
	rec.Record(Entry{Action: ActionParamsetWrite})
	if buf.Len() != 1 {
		t.Fatalf("buffer dropped after sink error: len=%d", buf.Len())
	}
}

func TestPersistedRecorderTimestampsZeroEntries(t *testing.T) {
	t.Parallel()
	buf := NewBuffer(4)
	var seen Entry
	rec := NewPersistedRecorder(buf, func(_ context.Context, e Entry) error {
		seen = e
		return nil
	}, nil)
	rec.Record(Entry{Action: ActionParamsetWrite})
	if seen.Timestamp.IsZero() {
		t.Fatal("timestamp not stamped")
	}
}

func TestAsyncSinkDeliversAndCloses(t *testing.T) {
	t.Parallel()
	var got atomic.Int32
	// Buffered with capacity 1 so the sink's send always succeeds even when
	// the background goroutine fires before the test's select is reached.
	// Without the buffer the non-blocking send falls through to `default`
	// and silently drops the signal, causing intermittent CI failures when
	// the scheduler happens to run the goroutine ahead of the test goroutine.
	done := make(chan struct{}, 1)
	sink := func(_ context.Context, _ Entry) error {
		got.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}
	enqueue, closer := AsyncSink(sink, 4, nil)
	defer closer()
	if err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite}); err != nil {
		t.Fatalf("enqueue err=%v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("async sink did not deliver in time")
	}
	if got.Load() != 1 {
		t.Fatalf("delivered=%d", got.Load())
	}
}

func TestAsyncSinkDropsWhenSaturated(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	sink := func(_ context.Context, _ Entry) error {
		<-hold
		return nil
	}
	enqueue, closer := AsyncSink(sink, 1, nil)
	defer closer()
	defer close(hold)

	// 1 fits in the queue, the worker grabs it and blocks on hold; the
	// next enqueue now occupies the queue slot. Subsequent calls are
	// dropped without error.
	for i := 0; i < 10; i++ {
		if err := enqueue(context.Background(), Entry{Action: ActionParamsetWrite}); err != nil {
			t.Fatalf("enqueue err=%v", err)
		}
	}
}

func TestAsyncSinkNilSinkReturnsNil(t *testing.T) {
	t.Parallel()
	enqueue, closer := AsyncSink(nil, 1, nil)
	defer closer()
	if enqueue != nil {
		t.Fatal("nil sink must produce nil enqueue")
	}
}
