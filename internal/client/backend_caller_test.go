// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestBackendCallerPriorityIsNonCritical guards the zero-value trap: the
// backend wire path must not run at CommandPriorityCritical (the enum's zero
// value), because Critical bypasses the throttle's bounded queue + pacing.
// The wiring passes CommandPriorityLow explicitly; a bare 0 would silently
// disable the throttle for every read and write.
func TestBackendCallerPriorityIsNonCritical(t *testing.T) {
	t.Parallel()
	bc := NewBackendCaller(nil, hmenum.CommandPriorityLow)
	if bc.Priority() != hmenum.CommandPriorityLow {
		t.Fatalf("priority = %v, want Low", bc.Priority())
	}
	if bc.Priority() == hmenum.CommandPriorityCritical {
		t.Fatal("backend wire path must not default to Critical (bypasses throttle)")
	}
}

// TestCoalesceKeyForIsValueInclusive locks in that the setValue coalesce key
// includes the value. The wire layout is [address, parameter, value], so two
// concurrent writes to the same data point with DIFFERENT values must produce
// DIFFERENT keys — otherwise the coalescer collapses them into one wire call
// and silently drops the follower's value (last-write-lost).
func TestCoalesceKeyForIsValueInclusive(t *testing.T) {
	t.Parallel()

	const addr, param = "VCU0000001:1", "LEVEL"

	// Different float values → different, non-empty keys.
	k1 := coalesceKeyFor("setValue", []any{addr, param, 0.3}, hmenum.CommandPriorityLow)
	k2 := coalesceKeyFor("setValue", []any{addr, param, 0.7}, hmenum.CommandPriorityLow)
	if k1 == "" || k2 == "" {
		t.Fatalf("float-value writes produced empty keys (coalescing disabled): %q %q", k1, k2)
	}
	if k1 == k2 {
		t.Fatalf("different values share coalesce key %q — writes would collapse (last-write-lost)", k1)
	}

	// Identical (address, parameter, value) → equal keys (dedup benefit).
	if k3 := coalesceKeyFor("setValue", []any{addr, param, 0.3}, hmenum.CommandPriorityLow); k1 != k3 {
		t.Fatalf("identical writes produced different keys: %q vs %q", k1, k3)
	}

	// Non-string value types are supported. The previous implementation type-
	// asserted args[2] to string and returned "" for bool/int/float, disabling
	// coalescing for the most common cases (STATE, LEVEL).
	kTrue := coalesceKeyFor("setValue", []any{addr, "STATE", true}, hmenum.CommandPriorityLow)
	kFalse := coalesceKeyFor("setValue", []any{addr, "STATE", false}, hmenum.CommandPriorityLow)
	if kTrue == "" || kFalse == "" {
		t.Fatalf("bool STATE writes produced empty keys: %q %q", kTrue, kFalse)
	}
	if kTrue == kFalse {
		t.Fatal("STATE=true and STATE=false must not share a coalesce key")
	}

	// Type is encoded so bool true and string "true" never collide.
	if coalesceKeyFor("setValue", []any{addr, param, true}, hmenum.CommandPriorityLow) ==
		coalesceKeyFor("setValue", []any{addr, param, "true"}, hmenum.CommandPriorityLow) {
		t.Fatal("bool true and string \"true\" must not produce the same key")
	}

	// Non-setValue methods and short arg lists never coalesce.
	if got := coalesceKeyFor("getValue", []any{addr, param, 1}, hmenum.CommandPriorityLow); got != "" {
		t.Errorf("getValue coalesce key = %q, want empty", got)
	}
	if got := coalesceKeyFor("setValue", []any{addr}, hmenum.CommandPriorityLow); got != "" {
		t.Errorf("short-arg setValue coalesce key = %q, want empty", got)
	}
}

// TestBackendCallerDifferentValuesBothReachWire drives two concurrent setValue
// calls with different values through the full BackendCaller → InterfaceClient
// coalescer path and asserts BOTH values reach the transport. The transport
// blocks on a two-arrival barrier: if the two writes were coalesced only one
// arrival would occur and the barrier would never open (ctx timeout), so the
// test fails if the follower's write is dropped.
func TestBackendCallerDifferentValuesBothReachWire(t *testing.T) {
	t.Parallel()

	var arrived atomic.Int32
	var seen sync.Map // wire value -> struct{}
	barrier := make(chan struct{})
	caller := CallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		if method == "setValue" && len(params) >= 3 {
			seen.Store(params[2], struct{}{})
		}
		if arrived.Add(1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, nil
	})

	// Write pool must admit both writes concurrently for the barrier to open.
	ic, err := New(Config{
		CentralName:   "c",
		Interface:     hmenum.InterfaceHmIPRF,
		Caller:        caller,
		WriteThrottle: reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 2}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bc := NewBackendCaller(ic, hmenum.CommandPriorityCritical)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = bc.Call(ctx, "setValue", "VCU0000001:1", "LEVEL", 0.3) }()
	go func() { defer wg.Done(); _, _ = bc.Call(ctx, "setValue", "VCU0000001:1", "LEVEL", 0.7) }()
	wg.Wait()

	if n := arrived.Load(); n != 2 {
		t.Fatalf("wire calls = %d, want 2 (writes were coalesced — follower lost)", n)
	}
	if _, ok := seen.Load(0.3); !ok {
		t.Error("value 0.3 never reached the wire")
	}
	if _, ok := seen.Load(0.7); !ok {
		t.Error("value 0.7 never reached the wire")
	}
}

// TestBackendCallerCriticalWriteDoesNotAttachToLowPriorityLeader drives the
// alarm case: a Low-priority write of ACOUSTIC_ALARM_ACTIVE=false is already
// in flight and stalled on the wire when the alarm engine issues the same
// write at Critical. A coalesce follower never runs its own function, so if
// the two share a key the Critical command inherits the Low leader's throttle
// slot, backoff and breaker verdict instead of taking the Critical bypasses.
// The transport blocks until two calls have arrived, so a coalesced follower
// deadlocks the barrier and the arrival count stays at 1.
func TestBackendCallerCriticalWriteDoesNotAttachToLowPriorityLeader(t *testing.T) {
	t.Parallel()

	const addr, param = "VCU0000001:1", "ACOUSTIC_ALARM_ACTIVE"

	var arrived atomic.Int32
	leaderInFlight := make(chan struct{})
	barrier := make(chan struct{})
	caller := CallerFunc(func(ctx context.Context, _ string, _ []any) (any, error) {
		if n := arrived.Add(1); n == 1 {
			close(leaderInFlight)
		} else if n == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, nil
	})

	ic, err := New(Config{
		CentralName:   "c",
		Interface:     hmenum.InterfaceHmIPRF,
		Caller:        caller,
		WriteThrottle: reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 2}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bc := NewBackendCaller(ic, hmenum.CommandPriorityLow)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = bc.CallAt(ctx, hmenum.CommandPriorityLow, "setValue", addr, param, false)
	}()

	select {
	case <-leaderInFlight:
	case <-ctx.Done():
		t.Fatal("low-priority leader never reached the transport")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = bc.CallAt(ctx, hmenum.CommandPriorityCritical, "setValue", addr, param, false)
	}()
	wg.Wait()

	if n := arrived.Load(); n != 2 {
		t.Fatalf("wire calls = %d, want 2 — the Critical write coalesced onto the Low leader", n)
	}
}
