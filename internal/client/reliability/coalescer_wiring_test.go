// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability_test

// Tests for the BackendCaller coalesce-key wiring (C40).
//
// These tests live in the reliability package (external test) so they can
// construct InterfaceClients via the public API and observe Coalescer stats.
// They exercise coalesceKeyFor indirectly through the full BackendCaller →
// InterfaceClient → Coalescer stack.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// syncStubCaller is a Caller that counts how many times it is called and
// returns a fixed value. All calls are serialised under a mutex so
// sequential tests get a deterministic call count.
type syncStubCaller struct {
	mu    sync.Mutex
	calls int
	val   any
}

func (s *syncStubCaller) Call(_ context.Context, _ string, _ []any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.val, nil
}

func (s *syncStubCaller) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newIClient(t *testing.T, caller client.Caller) *client.InterfaceClient {
	t.Helper()
	c, err := client.New(client.Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceBidCosRF,
		Caller:      caller,
		// Use a fresh Coalescer so tests are isolated.
		Coalescer: reliability.NewCoalescer(),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// C40-A: setValue with same (channel, parameter) → only one transport call
// ---------------------------------------------------------------------------

// TestBackendCallerCoalescesSetValueByChannelAndParameter fans two concurrent
// setValue calls with identical (interface, channel, parameter) args through
// BackendCaller and asserts that only one reaches the transport.
//
// Sync protocol:
// 1. The transport blocks until `release` is closed.
// 2. Both goroutines are launched; the leader enters the transport and signals
// `leaderIn`.
// 3. Main goroutine waits for `leaderIn`, then sleeps briefly so the follower
// goroutine has time to park inside the Coalescer as a follower.
// 4. Main goroutine closes `release`; both goroutines drain.
// 5. Assert transport was called exactly once.
func TestBackendCallerCoalescesSetValueByChannelAndParameter(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	leaderIn := make(chan struct{}, 1) // buffered so the leader doesn't block
	var transportCalls atomic.Int64

	blockingCaller := client.CallerFunc(func(ctx context.Context, _ string, _ []any) (any, error) {
		transportCalls.Add(1)
		// Signal that the transport has been entered (leader is here).
		select {
		case leaderIn <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return "ok", nil
	})

	// Deterministic follower-join signal: the Coalescer hook fires
	// synchronously when a concurrent caller piggy-backs on the in-flight
	// leader. Waiting on it (instead of a fixed sleep) removes a timing
	// flake that surfaced on loaded CI runners where 5 ms was not enough
	// for the follower goroutine to register before release.
	followerJoined := make(chan struct{}, 1)
	coalescer := reliability.NewCoalescer()
	coalescer.SetHook(func(string, int) {
		select {
		case followerJoined <- struct{}{}:
		default:
		}
	})
	ic, err := client.New(client.Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceBidCosRF,
		Caller:      blockingCaller,
		Coalescer:   coalescer,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	bc := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)

	const (
		iface   = "BidCos-RF"
		channel = "HEQ0123456:1"
		param   = "SET_POINT_TEMPERATURE"
	)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := range 2 {
		wg.Go(func() {
			_, errs[i] = bc.Call(context.Background(), "setValue", iface, channel, param, float64(21+i))
		})
	}

	// Wait for the leader to enter the transport, then wait until the
	// follower has registered as a coalesce waiter (hook fired) before
	// releasing. Deterministic — no sleep-based race.
	<-leaderIn
	<-followerJoined
	close(release)

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	got := transportCalls.Load()
	if got != 1 {
		t.Fatalf("transport called %d times, want 1 (coalesced)", got)
	}
}

// ---------------------------------------------------------------------------
// C40-B: setValue with different channels → both calls pass through
// ---------------------------------------------------------------------------

// TestBackendCallerDoesNotCoalesceDifferentChannels asserts that two setValue
// calls targeting different channel addresses are NOT coalesced — each reaches
// the transport independently.
func TestBackendCallerDoesNotCoalesceDifferentChannels(t *testing.T) {
	t.Parallel()

	sc := &syncStubCaller{val: "ok"}
	ic := newIClient(t, sc)
	bc := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)

	const (
		iface = "BidCos-RF"
		param = "SET_POINT_TEMPERATURE"
		chanA = "HEQ0000001:1"
		chanB = "HEQ0000002:1"
	)

	if _, err := bc.Call(context.Background(), "setValue", iface, chanA, param, 21.0); err != nil {
		t.Fatalf("call A: %v", err)
	}
	if _, err := bc.Call(context.Background(), "setValue", iface, chanB, param, 22.0); err != nil {
		t.Fatalf("call B: %v", err)
	}

	// Sequential calls on different channels must each go through.
	if got := sc.CallCount(); got != 2 {
		t.Fatalf("transport called %d times, want 2 (different channels not coalesced)", got)
	}
}

// ---------------------------------------------------------------------------
// C40-C: getValue and other read methods are never coalesced by method name
// ---------------------------------------------------------------------------

// TestBackendCallerDoesNotCoalesceNonSetValueMethods verifies that getValue,
// getParamset, and similar read-only methods are not coalesced — each call
// goes through to the transport regardless of argument identity.
func TestBackendCallerDoesNotCoalesceNonSetValueMethods(t *testing.T) {
	t.Parallel()

	sc := &syncStubCaller{val: "ok"}
	ic := newIClient(t, sc)
	bc := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)

	nonCoalescedMethods := []string{
		"getValue",
		"getParamset",
		"getParamsetDescription",
		"listDevices",
		"init",
	}

	const (
		iface   = "BidCos-RF"
		channel = "HEQ0123456:1"
		param   = "SET_POINT_TEMPERATURE"
	)

	for _, method := range nonCoalescedMethods {
		// Call the same method twice with identical args.
		if _, err := bc.Call(context.Background(), method, iface, channel, param); err != nil {
			t.Fatalf("method %q call 1: %v", method, err)
		}
		if _, err := bc.Call(context.Background(), method, iface, channel, param); err != nil {
			t.Fatalf("method %q call 2: %v", method, err)
		}
	}

	want := len(nonCoalescedMethods) * 2
	if got := sc.CallCount(); got != want {
		t.Fatalf("transport called %d times, want %d (no coalescing for non-write methods)", got, want)
	}
}
