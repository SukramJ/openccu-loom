// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// slowSink is a CommandSink whose SetValue blocks until release is closed,
// simulating a CCU write stuck behind the circuit breaker/retry stack
// during a CCU stall. Every call is recorded, in arrival order, so tests
// can assert both liveness (handleDataPoint returns before SetValue
// completes) and ordering (same-topic writes never reorder).
type slowSink struct {
	release chan struct{}
	started chan struct{} // signalled once per SetValue call, buffered

	mu    sync.Mutex
	order []any // the `value` argument of each completed SetValue call, in completion order
}

func newSlowSink(buf int) *slowSink {
	return &slowSink{release: make(chan struct{}), started: make(chan struct{}, buf)}
}

func (s *slowSink) SetValue(ctx context.Context, _, _, _ string,
	_ hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	s.started <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.order = append(s.order, v)
	s.mu.Unlock()
	return nil
}

func (s *slowSink) SetMasterValue(context.Context, string, string, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}

func (s *slowSink) SetSysvar(context.Context, string, string, any) error { return nil }

func (s *slowSink) TriggerProgram(context.Context, string, string) error { return nil }

func (s *slowSink) snapshotOrder() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]any, len(s.order))
	copy(out, s.order)
	return out
}

// TestCommandSubscriberHandleDataPointReturnsPromptlyWithSlowSink is the
// test-first reproducer for the read-loop stall: handleDataPoint must
// return well before a slow downstream SetValue call completes, because in
// the real go-mqtt transport handleDataPoint runs on the same goroutine
// that also processes PUBACK/PINGRESP for every other in-flight message.
func TestCommandSubscriberHandleDataPointReturnsPromptlyWithSlowSink(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := newSlowSink(1)
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	defer sub.Close()
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		sub.handleDataPoint("gh/ccu/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"), false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleDataPoint did not return; it must dispatch SetValue off the calling goroutine")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("handleDataPoint took %v to return while SetValue was still blocked; want near-instant return", elapsed)
	}

	// The work must still eventually run: SetValue was already entered (it
	// is parked on <-release right now).
	select {
	case <-sink.started:
	case <-time.After(2 * time.Second):
		t.Fatal("SetValue was never called")
	}
	close(sink.release)
	sub.dispatcher.flush()
	if got := sink.snapshotOrder(); len(got) != 1 || got[0] != true {
		t.Fatalf("order=%v, want [true]", got)
	}
}

// TestCommandSubscriberPreservesOrderPerTopic proves that a burst of writes
// to the SAME data point (the same MQTT topic) is never reordered, even
// though the dispatcher runs multiple workers so unrelated data points can
// proceed concurrently.
func TestCommandSubscriberPreservesOrderPerTopic(t *testing.T) {
	t.Parallel()
	const n = 100
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := newSlowSink(n)
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	defer sub.Close()
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Release immediately — this test is about ordering, not blocking.
	close(sink.release)
	for i := range n {
		sub.handleDataPoint("gh/ccu/HmIP-RF/0001ABCD/1/LEVEL/set", []byte(intPayload(i)), false)
	}
	sub.dispatcher.flush()

	got := sink.snapshotOrder()
	if len(got) != n {
		t.Fatalf("got %d completions, want %d", len(got), n)
	}
	for i, v := range got {
		want := int64(i)
		if v != want {
			t.Fatalf("order[%d] = %v, want %v — same-topic commands reordered", i, v, want)
		}
	}
}

func intPayload(i int) string {
	// parseCommandPayload parses decimal integers to int64.
	digits := []byte{}
	if i == 0 {
		return "0"
	}
	n := i
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestCommandSubscriberCloseDrainsCleanly proves Close waits for an
// in-flight command to finish (no queued write is abandoned) and that the
// worker goroutines it owns actually exit.
func TestCommandSubscriberCloseDrainsCleanly(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := newSlowSink(1)
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	sub.handleDataPoint("gh/ccu/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"), false)
	select {
	case <-sink.started:
	case <-time.After(2 * time.Second):
		t.Fatal("SetValue was never called")
	}

	closeDone := make(chan struct{})
	go func() {
		sub.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight command was released; it must drain, not abandon, running work")
	case <-time.After(100 * time.Millisecond):
	}

	close(sink.release)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close never returned after the blocked SetValue was released — worker goroutine leaked")
	}

	// Close is idempotent.
	sub.Close()
}
