// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newTestClient returns a minimal InterfaceClient for state-helper tests.
func newTestClientForState(t *testing.T) *InterfaceClient {
	t.Helper()
	c, err := New(Config{
		CentralName: "test",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestIsAvailableConnected verifies IsAvailable returns true only in
// CONNECTED or RECONNECTING state.
func TestIsAvailableConnected(t *testing.T) {
	c := newTestClientForState(t)
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateCreated,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateStopping,
		hmenum.ClientStateStopped,
		hmenum.ClientStateFailed,
	} {
		c.SetState(s)
		if c.IsAvailable() {
			t.Errorf("IsAvailable() = true for state %s; want false", s)
		}
	}
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateConnected,
		hmenum.ClientStateReconnecting,
	} {
		c.SetState(s)
		if !c.IsAvailable() {
			t.Errorf("IsAvailable() = false for state %s; want true", s)
		}
	}
}

// TestIsConnectedOnlyWhenConnected verifies IsConnected is true exactly
// in CONNECTED state ().
func TestIsConnectedOnlyWhenConnected(t *testing.T) {
	c := newTestClientForState(t)

	c.SetState(hmenum.ClientStateConnected)
	if !c.IsConnected() {
		t.Error("IsConnected() = false; want true in CONNECTED state")
	}
	c.SetState(hmenum.ClientStateReconnecting)
	if c.IsConnected() {
		t.Error("IsConnected() = true for RECONNECTING; want false")
	}
	c.SetState(hmenum.ClientStateDisconnected)
	if c.IsConnected() {
		t.Error("IsConnected() = true for DISCONNECTED; want false")
	}
}

// TestIsFailedAndIsStopped verifies IsFailed / IsStopped predicates
// ().
func TestIsFailedAndIsStopped(t *testing.T) {
	c := newTestClientForState(t)

	c.SetState(hmenum.ClientStateFailed)
	if !c.IsFailed() {
		t.Error("IsFailed() = false; want true in FAILED state")
	}
	if c.IsStopped() {
		t.Error("IsStopped() = true in FAILED state; want false")
	}

	c.SetState(hmenum.ClientStateStopped)
	if !c.IsStopped() {
		t.Error("IsStopped() = false; want true in STOPPED state")
	}
	if c.IsFailed() {
		t.Error("IsFailed() = true in STOPPED state; want false")
	}
}

// TestCanReconnect verifies CanReconnect only allows reconnect from
// DISCONNECTED or FAILED.
func TestCanReconnect(t *testing.T) {
	c := newTestClientForState(t)

	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateFailed,
	} {
		c.SetState(s)
		if !c.CanReconnect() {
			t.Errorf("CanReconnect() = false for %s; want true", s)
		}
	}
	for _, s := range []hmenum.ClientState{
		hmenum.ClientStateCreated,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateConnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateStopped,
	} {
		c.SetState(s)
		if c.CanReconnect() {
			t.Errorf("CanReconnect() = true for %s; want false", s)
		}
	}
}

// TestResetCircuitBreakers verifies ResetCircuitBreakers calls Reset
// on the attached CircuitBreaker.
func TestResetCircuitBreakers(t *testing.T) {
	c := newTestClientForState(t)
	// Trip the breaker.
	for i := 0; i < 10; i++ {
		c.cfg.Circuit.RecordFailure()
	}
	if c.cfg.Circuit.State() == hmenum.CircuitStateClosed {
		t.Fatal("expected circuit to be open after forced failures")
	}
	c.ResetCircuitBreakers()
	if c.cfg.Circuit.State() != hmenum.CircuitStateClosed {
		t.Error("ResetCircuitBreakers did not close the circuit")
	}
}

// TestCheckConnectionAvailability verifies the happy path returns true
// and the error path returns false.
func TestCheckConnectionAvailability(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := newTestClientForState(t) // default caller returns (nil, nil)
		if !c.CheckConnectionAvailability(context.Background(), false) {
			t.Error("CheckConnectionAvailability() = false; want true on success")
		}
	})

	t.Run("failure", func(t *testing.T) {
		c, _ := New(Config{
			CentralName: "test",
			Interface:   hmenum.InterfaceHmIPRF,
			Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, errAlwaysFail }),
		})
		if c.CheckConnectionAvailability(context.Background(), false) {
			t.Error("CheckConnectionAvailability() = true; want false on error")
		}
	})
}

// TestReinitProxy verifies Deinit+Init are both called in sequence
// and that a Deinit failure does not prevent Init.
func TestReinitProxy(t *testing.T) {
	var calls []string
	anno := &spyAnnouncer{recordFn: func(s string) { calls = append(calls, s) }}
	b := backends.NewCcuBackend(nil, nil, anno)
	c := newTestClientForState(t)
	ctx := context.Background()
	err := c.ReinitProxy(ctx, b, "HmIP-RF", "http://callback:8120/RPC2/test")
	if err != nil {
		t.Fatalf("ReinitProxy returned error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "deinit" || calls[1] != "init" {
		t.Fatalf("expected [deinit init], got %v", calls)
	}
}

// TestReinitProxyDeinitFailureStillInits verifies that a Deinit error
// Does not stop the subsequent Init ( — mirrors
// "keep going even on deinit failure" behaviour).
func TestReinitProxyDeinitFailureStillInits(t *testing.T) {
	var initCalled bool
	anno := &spyAnnouncer{
		deinitErr: errAlwaysFail,
		recordFn: func(s string) {
			if s == "init" {
				initCalled = true
			}
		},
	}
	b := backends.NewCcuBackend(nil, nil, anno)
	c := newTestClientForState(t)
	err := c.ReinitProxy(context.Background(), b, "HmIP-RF", "http://callback:8120/RPC2/test")
	if err != nil {
		t.Fatalf("ReinitProxy returned error despite Deinit failure: %v", err)
	}
	if !initCalled {
		t.Error("Init was not called after Deinit failure")
	}
}

// errAlwaysFail is a fixed sentinel used across sub-tests in this file.
var errAlwaysFail = errFixedSentinel("always fail")

type errFixedSentinel string

func (e errFixedSentinel) Error() string { return string(e) }

// spyAnnouncer records init/deinit calls for TestReinitProxy*.
type spyAnnouncer struct {
	deinitErr error
	recordFn  func(string)
}

func (a *spyAnnouncer) Init(_ context.Context, _, _ string) error {
	if a.recordFn != nil {
		a.recordFn("init")
	}
	return nil
}

func (a *spyAnnouncer) Deinit(_ context.Context, _ string) error {
	if a.recordFn != nil {
		a.recordFn("deinit")
	}
	return a.deinitErr
}

// TestCheckConnectionAvailabilityPingPongToken verifies that when
// handlePingPong=true and the backend declares PingPong capability, the
// caller_id sent to the backend embeds a "#<token>" suffix and the token is
// recorded in the pending tracker before the call is issued.
func TestCheckConnectionAvailabilityPingPongToken(t *testing.T) {
	t.Parallel()

	var capturedParams []any
	c, err := New(Config{
		CentralName: "ccu-01",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(_ context.Context, _ string, params []any) (any, error) {
			capturedParams = params
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(backends.KindCCU), // CCU backend advertises ping-pong
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Before the call the tracker must be empty.
	if c.PingPong().PendingCount() != 0 {
		t.Fatal("pre-condition: no pending pings expected")
	}

	ok := c.CheckConnectionAvailability(context.Background(), true)
	if !ok {
		t.Fatal("CheckConnectionAvailability returned false; want true on success")
	}

	// The caller_id passed to the backend must contain a '#' separator.
	if len(capturedParams) != 1 {
		t.Fatalf("expected 1 param, got %d", len(capturedParams))
	}
	callerID, ok2 := capturedParams[0].(string)
	if !ok2 {
		t.Fatalf("param[0] is not a string: %T", capturedParams[0])
	}
	if !strings.Contains(callerID, "#") {
		t.Fatalf("caller_id %q does not contain '#<token>'; ping-pong correlation requires a token", callerID)
	}

	// The token must have been recorded in the tracker.
	// After RecordPing the pending count must be 1.
	if c.PingPong().PendingCount() != 1 {
		t.Fatalf("PendingCount = %d after one CheckConnectionAvailability(true), want 1", c.PingPong().PendingCount())
	}

	// RecordPong with the embedded token must match.
	token := callerID[strings.LastIndex(callerID, "#")+1:]
	matched, _ := c.RecordPong(token)
	if !matched {
		t.Fatalf("RecordPong(%q) = false; expected to match the recorded ping", token)
	}
	if c.PingPong().PendingCount() != 0 {
		t.Fatal("PendingCount must be 0 after the pong is matched")
	}
}

// TestCheckConnectionAvailabilityNoPingPongWhenFlagFalse verifies that when
// handlePingPong=false the plain interface name is sent as caller_id (no token)
// and nothing is recorded in the tracker.
func TestCheckConnectionAvailabilityNoPingPongWhenFlagFalse(t *testing.T) {
	t.Parallel()

	var capturedCallerID string
	c, err := New(Config{
		CentralName: "ccu-02",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(_ context.Context, _ string, params []any) (any, error) {
			if len(params) > 0 {
				capturedCallerID, _ = params[0].(string)
			}
			return nil, nil
		}),
		Capabilities: backends.CapabilityFor(backends.KindCCU),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.CheckConnectionAvailability(context.Background(), false)

	if strings.Contains(capturedCallerID, "#") {
		t.Fatalf("caller_id %q must not contain '#' when handlePingPong=false", capturedCallerID)
	}
	if c.PingPong().PendingCount() != 0 {
		t.Fatal("no pings should be recorded when handlePingPong=false")
	}
}
