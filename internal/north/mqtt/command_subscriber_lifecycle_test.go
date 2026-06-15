// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ctxCapturingSink is a CommandSink that captures ctx.Err() at the moment
// each call is received — before the handler's defer cancel() fires.
// This lets tests assert whether the handler derived from a cancelled or live
// lifecycle context without being confused by the deferred per-call cancel.
type ctxCapturingSink struct {
	mu      sync.Mutex
	lastErr error // ctx.Err() captured inside the sink call
	called  bool
}

func (s *ctxCapturingSink) SetValue(ctx context.Context, _, _, _ string,
	_ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	s.mu.Lock()
	s.lastErr = ctx.Err()
	s.called = true
	s.mu.Unlock()
	return nil
}

func (s *ctxCapturingSink) SetMasterValue(ctx context.Context, _, _, _ string,
	_ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	s.mu.Lock()
	s.lastErr = ctx.Err()
	s.called = true
	s.mu.Unlock()
	return nil
}

func (s *ctxCapturingSink) SetSysvar(ctx context.Context, _, _ string, _ any) error {
	s.mu.Lock()
	s.lastErr = ctx.Err()
	s.called = true
	s.mu.Unlock()
	return nil
}

func (s *ctxCapturingSink) TriggerProgram(ctx context.Context, _, _ string) error {
	s.mu.Lock()
	s.lastErr = ctx.Err()
	s.called = true
	s.mu.Unlock()
	return nil
}

func (s *ctxCapturingSink) snapshot() (called bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called, s.lastErr
}

// TestCommandSubscriberLifecycleContextCancelledLifecycle proves that when a
// cancelled lifecycle context is wired via WithLifecycleContext, the context
// delivered to the sink already carries a non-nil Err() — the handler derived
// from the cancelled lifecycle ctx, not a fresh background context.
func TestCommandSubscriberLifecycleContextCancelledLifecycle(t *testing.T) {
	t.Parallel()

	lifecycleCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so every derived ctx is already done

	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &ctxCapturingSink{}

	sub := NewCommandSubscriber(noop, topics, sink, nil).
		WithLifecycleContext(lifecycleCtx)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Drive the data-point handler through the stub Subscriber.
	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"))
	if !ok {
		t.Fatal("subscription did not match topic filter")
	}

	called, ctxErr := sink.snapshot()
	if !called {
		t.Fatal("sink was not called; handler did not dispatch SetValue")
	}
	if ctxErr == nil {
		t.Error("ctx.Err() was nil inside the sink; handler used a live context instead of the cancelled lifecycle ctx")
	}
}

// TestCommandSubscriberLifecycleContextLiveLifecycle proves that when a live
// (uncancelled) lifecycle context is wired, the ctx received by the sink has
// Err() == nil while the handler executes synchronously.
func TestCommandSubscriberLifecycleContextLiveLifecycle(t *testing.T) {
	t.Parallel()

	lifecycleCtx, cancel := context.WithCancel(context.Background())
	defer cancel() // only cancelled after the test completes

	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &ctxCapturingSink{}

	sub := NewCommandSubscriber(noop, topics, sink, nil).
		WithLifecycleContext(lifecycleCtx)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("false"))
	if !ok {
		t.Fatal("subscription did not match topic filter")
	}

	called, ctxErr := sink.snapshot()
	if !called {
		t.Fatal("sink was not called; handler did not dispatch SetValue")
	}
	if ctxErr != nil {
		t.Errorf("ctx.Err() = %v; expected nil for a live lifecycle ctx", ctxErr)
	}
}

// TestCommandSubscriberLifecycleContextNilIgnored verifies that passing nil
// to WithLifecycleContext is a no-op — the subscriber defaults to
// context.Background(), so commands dispatch with a usable context.
func TestCommandSubscriberLifecycleContextNilIgnored(t *testing.T) {
	t.Parallel()

	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &ctxCapturingSink{}

	// Passing nil must be silently ignored; lifecycleCtx stays context.Background().
	sub := NewCommandSubscriber(noop, topics, sink, nil).
		WithLifecycleContext(nil) //nolint:staticcheck // SA1012: deliberately passes a nil Context to verify WithLifecycleContext ignores it
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"))
	if !ok {
		t.Fatal("subscription did not match topic filter")
	}

	called, ctxErr := sink.snapshot()
	if !called {
		t.Fatal("sink was not called; nil WithLifecycleContext broke dispatch")
	}
	// The fallback is context.Background(), which is never cancelled.
	if ctxErr != nil {
		t.Errorf("ctx.Err() = %v; expected nil after nil WithLifecycleContext", ctxErr)
	}
}
