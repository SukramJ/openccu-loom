// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// P1-2: Retrier honours the injected clock. Tests can advance virtual
// time instead of sleeping the real backoff window — the entire retry
// schedule for a 30-second exhaustive run completes in microseconds.

func TestRetrierUsesInjectedClock(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	r := NewRetrier(RetryConfig{
		MaxAttempts: 3,
		Initial:     100 * time.Millisecond,
		Max:         time.Second,
		Multiplier:  2,
		Clock:       fake,
	})

	var attempts atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- r.Do(context.Background(), func(_ context.Context, _ int) error {
			attempts.Add(1)
			return errors.New("transient")
		})
	}()

	// Wait until the first attempt fired and the retrier parked on the
	// timer. Use the real clock here just for the test handshake.
	deadline := time.Now().Add(time.Second)
	for attempts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if attempts.Load() != 1 {
		t.Fatalf("first attempt did not fire: %d", attempts.Load())
	}

	// Advance virtual time past the first backoff (100ms) → second
	// attempt fires.
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		fake.Advance(150 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	if attempts.Load() < 2 {
		t.Fatalf("second attempt did not fire after Advance: %d", attempts.Load())
	}

	// Advance past the second backoff (200ms) → third attempt fires
	// and exhausts the schedule.
	for attempts.Load() < 3 && time.Now().Before(deadline) {
		fake.Advance(300 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected exhaustion error, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("Retrier.Do did not return after exhausting attempts")
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestRetrierClockDefaultsToReal(t *testing.T) {
	t.Parallel()
	r := NewRetrier(RetryConfig{MaxAttempts: 1})
	if r.cfg.Clock == nil {
		t.Fatal("default clock not wired")
	}
	if _, ok := r.cfg.Clock.(clock.Real); !ok {
		t.Fatalf("default clock not Real, got %T", r.cfg.Clock)
	}
}
