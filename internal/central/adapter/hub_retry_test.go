// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestRetryHubWiringSucceedsAfterTransientFailures is the regression tripwire
// for the permanent hub-wiring trap. WireHub runs exactly once at boot; if the
// CCU's ReGa is not yet reachable during the daemon's startup window it fails,
// leaving that central's entire hub surface (programs / sysvars / inbox /
// service+alarm messages) AND the refresh_client_data safety net dead until a
// manual restart. retryWithBackoff re-attempts until the hub comes up, so a
// transient boot failure self-heals.
func TestRetryHubWiringSucceedsAfterTransientFailures(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	attempt := func(_ context.Context) error {
		if calls.Add(1) < 3 {
			return errors.New("rega not ready")
		}
		return nil
	}

	// Tiny backoff keeps the test fast; the last value is reused once the
	// slice is exhausted.
	ok := retryWithBackoff(context.Background(), []time.Duration{time.Millisecond}, attempt)
	if !ok {
		t.Fatal("retryWithBackoff must report success once the attempt succeeds")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("attempt called %d times, want 3 (two failures then success)", got)
	}
}

// TestStartHubRetryWiresOnRecovery is the end-to-end tripwire for the
// background hub recovery: a WireHub that fails on the first boot attempt but
// succeeds on a later one must eventually run onWired (which wires the refresh
// safety net + registers the hub-session closer). Without the retry the
// central would stay hub-less and "refresh not wired" until a restart.
func TestStartHubRetryWiresOnRecovery(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "retry-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	var attempts atomic.Int32
	hubFn := func(_ context.Context, _ config.CentralConfig, _ *central.Unit, _ *slog.Logger) (*rega.Runner, HubData, func(), error) {
		if attempts.Add(1) < 2 {
			return nil, HubData{}, nil, errors.New("rega not ready")
		}
		return nil, HubData{}, func() {}, nil
	}

	wired := make(chan struct{}, 1)
	cc := config.CentralConfig{Name: "retry-central"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := startHubRetry(ctx, cc, c, nil, hubFn,
		[]time.Duration{time.Millisecond},
		func(_ *rega.Runner, _ func()) { wired <- struct{}{} })

	select {
	case <-wired:
		if got := attempts.Load(); got != 2 {
			t.Fatalf("hubFn attempts = %d, want 2 (one failure then success)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onWired was never invoked — background hub recovery did not complete")
	}
	// Cancel and drain so no goroutine outlives the test.
	cancel()
	wg.Wait()
}

// TestRetryHubWiringStopsOnContextCancel verifies the retry loop is bounded by
// the context so a never-recovering hub does not leak a goroutine for the
// daemon's lifetime — cancelling (shutdown) ends the loop and reports failure.
func TestRetryHubWiringStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	attempt := func(_ context.Context) error {
		calls.Add(1)
		return errors.New("never recovers")
	}

	done := make(chan bool, 1)
	go func() {
		done <- retryWithBackoff(ctx, []time.Duration{50 * time.Millisecond}, attempt)
	}()

	// Let at least one attempt run, then cancel.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("retryWithBackoff must report failure when the context is cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retryWithBackoff did not return after context cancel — goroutine leak")
	}
	if calls.Load() == 0 {
		t.Fatal("expected at least one attempt before cancel")
	}
}

// TestRetryHubWiringCancelDuringLongBackoffExitsPromptly verifies that a
// context-cancel during a long backoff window returns quickly rather than
// blocking for the full backoff duration. This locks the time.NewTimer
// (not time.After) approach: time.After creates a timer goroutine that
// cannot be cancelled, so ctx-cancel would have to wait out the full
// backoff before the goroutine exits.
func TestRetryHubWiringCancelDuringLongBackoffExitsPromptly(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	// Backoff is intentionally long — context cancel must short-circuit it.
	longBackoff := []time.Duration{10 * time.Second}
	attempt := func(_ context.Context) error {
		return errors.New("always fails")
	}

	done := make(chan bool, 1)
	go func() {
		done <- retryWithBackoff(ctx, longBackoff, attempt)
	}()

	// One attempt runs synchronously; we are now mid-backoff (10 s timer).
	// Cancel immediately — the loop must unblock within a short window,
	// not after the 10 s timer fires.
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("retryWithBackoff must report failure on context cancel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("retryWithBackoff blocked for > 500 ms after cancel — timer not stopped")
	}
}
