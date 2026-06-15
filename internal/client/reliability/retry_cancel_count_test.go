// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests that a cancellation increments CancelledRetries exactly once — at the
// cancelling call site — and is not double-counted when the superseded chain
// observes its closed cancel channel.
package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// parkedRetrier uses a long Initial backoff so a chain sits in the
// inter-attempt wait after its first failed attempt — the state where a
// cancellation deterministically lands.
func parkedRetrier() *Retrier {
	return NewRetrier(RetryConfig{
		MaxAttempts: 10,
		Initial:     30 * time.Second,
		Max:         30 * time.Second,
		Multiplier:  1,
		Jitter:      -1,
	})
}

func TestRetrierCancelKeyCountsCancelledRetriesOnce(t *testing.T) {
	r := parkedRetrier()
	key := retryKey("LEVEL")

	attempted := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			attempted <- struct{}{}
			return errors.New("transient")
		})
	}()

	<-attempted // first attempt ran → the chain is registered and parking in backoff
	r.CancelKey(key)

	select {
	case err := <-done:
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Fatalf("DoForKey returned %v, want ErrRetrySuperseded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CancelKey did not unblock the parked chain")
	}

	if got := r.Snapshot().CancelledRetries; got != 1 {
		t.Fatalf("CancelledRetries = %d, want exactly 1 (a cancellation must not be double-counted)", got)
	}
}

func TestRetrierCancelInterfaceCountsEachChainOnce(t *testing.T) {
	r := parkedRetrier()
	keys := []hmtypes.DataPointKey{retryKey("LEVEL"), retryKey("STATE")}

	attempted := make(chan struct{}, len(keys))
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Go(func() {
			_ = r.DoForKey(context.Background(), k, func(_ context.Context, _ int) error {
				attempted <- struct{}{}
				return errors.New("transient")
			})
		})
	}
	for range keys {
		<-attempted
	}

	if cancelled := r.CancelInterface(); cancelled != len(keys) {
		t.Fatalf("CancelInterface returned %d, want %d", cancelled, len(keys))
	}
	wg.Wait()

	if got := r.Snapshot().CancelledRetries; got != int64(len(keys)) {
		t.Fatalf("CancelledRetries = %d, want exactly %d (one per cancelled chain, no double-count)", got, len(keys))
	}
}
