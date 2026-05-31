// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

// Tests for the per-class throttle routing wired in interface_client.go.
// Each test constructs an InterfaceClient with tailored throttle layouts
// and asserts that the correct throttle pool is selected by throttleForMethod.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordingCaller is a Caller that records invocations and returns ("ok", nil).
type recordingCaller struct {
	calls atomic.Int64
}

func (r *recordingCaller) Call(_ context.Context, _ string, _ []any) (any, error) {
	r.calls.Add(1)
	return "ok", nil
}

// newPerClassClient is a test helper that builds an InterfaceClient with
// per-class throttles all set to distinct pools.
func newPerClassClient(t *testing.T, caller Caller, read, write, control *reliability.CommandThrottle) *InterfaceClient {
	t.Helper()
	c, err := New(Config{
		CentralName:     "test-central",
		Interface:       hmenum.InterfaceHmIPRF,
		Caller:          caller,
		ReadThrottle:    read,
		WriteThrottle:   write,
		ControlThrottle: control,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestPerClassThrottleReadDoesNotBlockWrite verifies that holding the read
// throttle (MaxInFlight=1) does not block a concurrent write call, because
// writes use their own independent pool.
func TestPerClassThrottleReadDoesNotBlockWrite(t *testing.T) {
	t.Parallel()

	rec := &recordingCaller{}
	readThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	writeThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	ic := newPerClassClient(t, rec, readThrottle, writeThrottle, nil)

	// Hold the entire read pool from outside.
	if err := readThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("readThrottle.Acquire: %v", err)
	}
	defer readThrottle.Release()

	// A write call should complete immediately — it uses writeThrottle, not readThrottle.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := ic.Call(ctx, "setValue", nil, hmenum.CommandPriorityCritical, ""); err != nil {
		t.Fatalf("Call(setValue) blocked or errored unexpectedly: %v", err)
	}
	if rec.calls.Load() != 1 {
		t.Errorf("caller invocations = %d, want 1", rec.calls.Load())
	}
}

// TestPerClassThrottleReadBlockedByOwnPool verifies that a read call is
// blocked when the read throttle (MaxInFlight=1) is fully acquired, and
// returns a context-deadline error when the context expires.
func TestPerClassThrottleReadBlockedByOwnPool(t *testing.T) {
	t.Parallel()

	rec := &recordingCaller{}
	readThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	writeThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	ic := newPerClassClient(t, rec, readThrottle, writeThrottle, nil)

	// Hold the entire read pool from outside.
	if err := readThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("readThrottle.Acquire: %v", err)
	}
	defer readThrottle.Release()

	// A read call must block and eventually time out.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := ic.Call(ctx, "getValue", nil, hmenum.CommandPriorityCritical, "")
	if err == nil {
		t.Fatal("Call(getValue) expected to block and return an error, got nil")
	}
	// Accept either context.DeadlineExceeded or context.Canceled.
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context deadline/cancelled error, got: %v", err)
	}
}

// TestPerClassThrottleFallsBackToLegacyThrottle verifies that when only the
// legacy cfg.Throttle is set (Read/Write/Control all nil), all three classes
// share the single pool — holding it blocks every method class.
func TestPerClassThrottleFallsBackToLegacyThrottle(t *testing.T) {
	t.Parallel()

	rec := &recordingCaller{}
	legacyThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      rec,
		Throttle:    legacyThrottle,
		// ReadThrottle, WriteThrottle, ControlThrottle all nil → fall back to Throttle.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Hold the legacy pool from outside.
	if err := legacyThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("legacyThrottle.Acquire: %v", err)
	}
	defer legacyThrottle.Release()

	for _, method := range []string{"getValue", "setValue", "ping"} {
		method := method
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := c.Call(ctx, method, nil, hmenum.CommandPriorityCritical, "")
		cancel()
		if err == nil {
			t.Errorf("Call(%q) expected to block (legacy pool full), got nil error", method)
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("Call(%q): expected context error, got: %v", method, err)
		}
	}
}

// TestPerClassThrottleCloseDoesNotDoubleCloseSharedPool verifies that Close()
// on a client with the legacy single-pool layout (all four slots aliased to one
// throttle) does not panic, and that a subsequent Call returns an error.
func TestPerClassThrottleCloseDoesNotDoubleCloseSharedPool(t *testing.T) {
	t.Parallel()

	rec := &recordingCaller{}
	// Let New() build its own default Throttle; all three per-class slots will
	// alias it after construction.
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      rec,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Must not panic even though all four cfg.Throttle / Read / Write / Control
	// point at the same *CommandThrottle.
	c.Close()

	// After Close, every Call should return an error (client reports closed).
	_, callErr := c.Call(context.Background(), "getValue", nil, hmenum.CommandPriorityCritical, "")
	if callErr == nil {
		t.Fatal("Call after Close returned nil error, expected an error")
	}
}

// TestPerClassThrottleControlMethodGoesToControlPool verifies that control
// methods (e.g. "ping") use the control throttle and are not blocked by a
// fully-acquired read or write throttle.
func TestPerClassThrottleControlMethodGoesToControlPool(t *testing.T) {
	t.Parallel()

	rec := &recordingCaller{}
	readThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	writeThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	controlThrottle := reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 1})
	ic := newPerClassClient(t, rec, readThrottle, writeThrottle, controlThrottle)

	// Hold both read and write pools from outside.
	if err := readThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("readThrottle.Acquire: %v", err)
	}
	defer readThrottle.Release()
	if err := writeThrottle.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("writeThrottle.Acquire: %v", err)
	}
	defer writeThrottle.Release()

	// A control call ("ping") must still succeed — it uses controlThrottle.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := ic.Call(ctx, "ping", nil, hmenum.CommandPriorityCritical, ""); err != nil {
		t.Fatalf("Call(ping) blocked or errored unexpectedly: %v", err)
	}
	if rec.calls.Load() != 1 {
		t.Errorf("caller invocations = %d, want 1", rec.calls.Load())
	}
}
