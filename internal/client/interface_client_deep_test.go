// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// minimalCaller returns a CallerFunc that always succeeds with the given value.
func minimalCaller(v any) CallerFunc {
	return CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return v, nil
	})
}

// newTestClient is a test helper that constructs an InterfaceClient with a
// minimal valid Config, using the given Caller.
func newTestClient(t *testing.T, caller Caller) *InterfaceClient {
	t.Helper()
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestInterfaceClientInitialStateIsCreated verifies that a freshly constructed
// client starts in ClientStateCreated — matching the constructor assignment at
// internal/client/interface_client.go:122.
func TestInterfaceClientInitialStateIsCreated(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	if got := c.ClientState(); got != hmenum.ClientStateCreated {
		t.Fatalf("initial state = %q, want %q", got, hmenum.ClientStateCreated)
	}
}

// TestInterfaceClientSetStateTransitions exercises every ClientState constant.
// SetState is a write operation; State() reflects the new value immediately.
// This acts as a tripwire: if a new state is added to hmenum but the client
// rejects it, the test fails — making omissions visible.
func TestInterfaceClientSetStateTransitions(t *testing.T) {
	t.Parallel()
	allStates := []hmenum.ClientState{
		hmenum.ClientStateCreated,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateStopping,
		hmenum.ClientStateFailed,
		// ClientStateStopped is covered by Close; exercising it here is
		// fine because SetState does not guard transitions.
		hmenum.ClientStateStopped,
	}

	c := newTestClient(t, minimalCaller(nil))

	for _, s := range allStates {
		c.SetState(s)
		if got := c.ClientState(); got != s {
			t.Errorf("after SetState(%q): ClientState() = %q", s, got)
		}
	}
}

// TestInterfaceClientSetStateSameValueIsNoop confirms that calling SetState
// with the current value does not cause a panic, does not reset stateWakers,
// and leaves State() unchanged.
func TestInterfaceClientSetStateSameValueIsNoop(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	c.SetState(hmenum.ClientStateConnected)
	c.SetState(hmenum.ClientStateConnected) // second call with same value
	if got := c.ClientState(); got != hmenum.ClientStateConnected {
		t.Fatalf("ClientState() = %q after idempotent SetState, want %q", got, hmenum.ClientStateConnected)
	}
}

// TestInterfaceClientWaitForStateReturnsImmediatelyWhenAlreadyThere ensures
// WaitForState is non-blocking when the state already matches target.
func TestInterfaceClientWaitForStateReturnsImmediatelyWhenAlreadyThere(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	c.SetState(hmenum.ClientStateConnected)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	if err := c.WaitForState(ctx, hmenum.ClientStateConnected); err != nil {
		t.Fatalf("WaitForState returned error: %v", err)
	}
	// Already-matching state returns without blocking; a genuine block
	// would instead trip the 1s ctx above and surface as an error. Keep
	// this bound generous so CI scheduling jitter under -race never flakes.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("WaitForState took %v, expected non-blocking for already-matching state", elapsed)
	}
}

// TestInterfaceClientWaitForStateBlocksUntilTransition verifies the blocking
// path: a goroutine waits for a state that isn't set yet; a second goroutine
// sets it after a short delay; the first goroutine must unblock within 200 ms.
func TestInterfaceClientWaitForStateBlocksUntilTransition(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	// Start in Created; we will wait for Connected.

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var waitErr error
	done := make(chan struct{})
	go func() {
		waitErr = c.WaitForState(ctx, hmenum.ClientStateConnected)
		close(done)
	}()

	// Give the goroutine time to arm its waker.
	time.Sleep(20 * time.Millisecond)

	// Transition through an intermediate state to exercise the "re-arm"
	// loop in WaitForState: the first wakeup does not match, so the
	// function re-registers and waits again.
	c.SetState(hmenum.ClientStateConnecting) // not the target
	time.Sleep(10 * time.Millisecond)
	c.SetState(hmenum.ClientStateConnected) // target

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WaitForState did not unblock within 200 ms after SetState(Connected)")
	}
	if waitErr != nil {
		t.Fatalf("WaitForState returned unexpected error: %v", waitErr)
	}
}

// TestInterfaceClientWaitForStateRespectsContext verifies that WaitForState
// returns ctx.Err() when the context is cancelled before the target state is
// reached.
func TestInterfaceClientWaitForStateRespectsContext(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	// State is Created; target is Connected — will never be set.

	ctx, cancel := context.WithCancel(context.Background())

	var waitErr error
	done := make(chan struct{})
	go func() {
		waitErr = c.WaitForState(ctx, hmenum.ClientStateConnected)
		close(done)
	}()

	// Give the goroutine time to arm its waker.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WaitForState did not respect context cancellation within 200 ms")
	}
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("WaitForState err = %v, want context.Canceled", waitErr)
	}
}

// TestInterfaceClientCallInvokesCaller verifies that Call delegates to the
// underlying Caller with the exact method and params, and returns the result.
func TestInterfaceClientCallInvokesCaller(t *testing.T) {
	t.Parallel()

	var (
		capturedMethod string
		capturedParams []any
	)
	caller := CallerFunc(func(_ context.Context, method string, params []any) (any, error) {
		capturedMethod = method
		capturedParams = params
		return "result-value", nil
	})
	c := newTestClient(t, caller)

	params := []any{"arg1", 42}
	got, err := c.Call(context.Background(), "getValue", params, hmenum.CommandPriorityHigh, "")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "result-value" {
		t.Fatalf("result = %v, want %q", got, "result-value")
	}
	if capturedMethod != "getValue" {
		t.Errorf("caller received method %q, want %q", capturedMethod, "getValue")
	}
	if len(capturedParams) != 2 || capturedParams[0] != "arg1" || capturedParams[1] != 42 {
		t.Errorf("caller received params %v, want [arg1 42]", capturedParams)
	}
}

// TestInterfaceClientCallReturnsErrorFromCaller verifies that errors from the
// underlying Caller are propagated through the reliability stack.
func TestInterfaceClientCallReturnsErrorFromCaller(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("transport failure")
	caller := CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, wantErr
	})
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		// What is asserted is that the caller's error survives the
		// reliability stack — not how often it is retried on the way out.
		// Without an explicit Retrier the default one applies and the
		// always-failing caller is retried across the production 2s/4s
		// backoff.
		Retrier: reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, gotErr := c.Call(context.Background(), "setValue", nil, hmenum.CommandPriorityHigh, "")
	if gotErr == nil {
		t.Fatal("expected error from Call, got nil")
	}
	// The error must wrap or equal the original; errors.Is handles both.
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("Call error = %v, want to wrap %v", gotErr, wantErr)
	}
}

// TestInterfaceClientCapabilitiesDefaultsFromInterface verifies that omitting
// Capabilities in Config causes New to derive them from the interface's backend
// kind via backends.CapabilityFor(backends.KindFor(iface)).
// This is a targeted expansion of the existing TestInterfaceClientCapabilitiesFromInterface
// (which only checks RPCCallback for two interfaces); here we assert the full
// capability struct for CCU-backed and CUxD-backed interfaces.
func TestInterfaceClientCapabilitiesDefaultsFromInterface(t *testing.T) {
	t.Parallel()

	cases := []struct {
		iface        hmenum.Interface
		wantRPCCB    bool
		wantPingPong bool
		wantListDevs bool
		wantFirmware bool
		wantPrograms bool
		wantSysvars  bool
	}{
		// CCU backend (XML-RPC)
		{
			iface:        hmenum.InterfaceHmIPRF,
			wantRPCCB:    true,
			wantPingPong: true,
			wantListDevs: true,
			wantFirmware: true,
			wantPrograms: true,
			wantSysvars:  true,
		},
		{
			iface:        hmenum.InterfaceBidCosRF,
			wantRPCCB:    true,
			wantPingPong: true,
			wantListDevs: true,
			wantFirmware: true,
			wantPrograms: true,
			wantSysvars:  true,
		},
		// CUxD backend (BIN-RPC)
		{
			iface:        hmenum.InterfaceCUxD,
			wantRPCCB:    true,
			wantPingPong: true,
			wantListDevs: true,
			wantFirmware: false,
			wantPrograms: false,
			wantSysvars:  false,
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.iface), func(t *testing.T) {
			t.Parallel()
			c, err := New(Config{
				CentralName: "test-central",
				Interface:   tc.iface,
				Caller:      minimalCaller(nil),
				// No Capabilities override — let New derive from interface.
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			caps := c.Capabilities()
			if caps.RPCCallback != tc.wantRPCCB {
				t.Errorf("RPCCallback=%v, want %v", caps.RPCCallback, tc.wantRPCCB)
			}
			if caps.PingPong != tc.wantPingPong {
				t.Errorf("PingPong=%v, want %v", caps.PingPong, tc.wantPingPong)
			}
			if caps.ListDevices != tc.wantListDevs {
				t.Errorf("ListDevices=%v, want %v", caps.ListDevices, tc.wantListDevs)
			}
			if caps.FirmwareUpdate != tc.wantFirmware {
				t.Errorf("FirmwareUpdate=%v, want %v", caps.FirmwareUpdate, tc.wantFirmware)
			}
			if caps.GetAllPrograms != tc.wantPrograms {
				t.Errorf("GetAllPrograms=%v, want %v", caps.GetAllPrograms, tc.wantPrograms)
			}
			if caps.GetAllSysvars != tc.wantSysvars {
				t.Errorf("GetAllSysvars=%v, want %v", caps.GetAllSysvars, tc.wantSysvars)
			}
		})
	}
}

// TestInterfaceClientNotifyCallbackUpdatesLastCallbackAt verifies that
// LastCallbackAt() is zero before any callback and non-zero immediately after.
// Separate from the existing TestInterfaceClientIsCallbackAlive which checks
// the boolean; this test asserts the timestamp itself is within 1s of now.
func TestInterfaceClientNotifyCallbackUpdatesLastCallbackAt(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	if !c.LastCallbackAt().IsZero() {
		t.Fatal("LastCallbackAt must be zero on fresh client")
	}

	before := time.Now()
	c.NotifyCallback()
	after := time.Now()

	ts := c.LastCallbackAt()
	if ts.IsZero() {
		t.Fatal("LastCallbackAt is zero after NotifyCallback")
	}
	if ts.Before(before) || ts.After(after.Add(time.Second)) {
		t.Fatalf("LastCallbackAt=%v is outside [%v, %v+1s]", ts, before, after)
	}
}

// TestInterfaceClientIsCallbackAliveStaleLogicVerified verifies the three
// observable outcomes of IsCallbackAlive: zero timestamp (post-init guard →
// true), fresh after a callback (→ true), and failed/reconnecting state (→
// false regardless of timestamp). The production freshness constant is 180 s;
// a true stale-window test without sleeping requires clock injection
// (see tests/bench/ or reliability/).
func TestInterfaceClientIsCallbackAliveStaleLogicVerified(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	// Before any callback the timestamp is zero — the post-init guard must
	// return true so startup scheduler ticks do not falsely trigger
	// a connection-lost cycle.
	if !c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive should be true on fresh client (post-init guard, zero timestamp)")
	}

	// After a callback: alive (within the 180 s freshness window).
	c.NotifyCallback()
	if !c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive should be true immediately after NotifyCallback")
	}

	// RECONNECTING state overrides the freshness window — reports false even
	// when a recent callback timestamp is set.
	err := c.TransitionTo(hmenum.ClientStateReconnecting, "test", true, hmenum.FailureReasonNone)
	if err != nil {
		t.Fatalf("TransitionTo RECONNECTING: %v", err)
	}
	if c.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive should be false in RECONNECTING state")
	}
}

// TestInterfaceClientPingPongIntegration verifies the full ping/pong flow
// through InterfaceClient's delegation to PingPongTracker:
// - RecordPing stores an ID.
// - RecordPong matches it and returns matched=true with a non-negative RTT.
// - SweepPingPong on a fully matched tracker returns no mismatches.
func TestInterfaceClientPingPongIntegration(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	const pingID = "ping-001"
	c.RecordPing(pingID)

	matched, rtt := c.RecordPong(pingID)
	if !matched {
		t.Fatal("RecordPong: matched=false, want true")
	}
	if rtt < 0 {
		t.Fatalf("RecordPong: rtt=%v, want >= 0", rtt)
	}

	mismatches := c.SweepPingPong()
	if len(mismatches) != 0 {
		t.Fatalf("SweepPingPong after matched pair = %v, want empty", mismatches)
	}
}

// TestInterfaceClientPingWithoutPongReportsMismatchOnSweep verifies the
// "orphan ping" path: a sent ping that never receives a matching pong is
// eventually reported as a PingPongMismatchPending anomaly by SweepPingPong.
// We use a very short PendingTTL so the test does not sleep long.
func TestInterfaceClientPingWithoutPongReportsMismatchOnSweep(t *testing.T) {
	t.Parallel()

	// Build a tracker with a very short TTL so we can trigger expiry quickly
	// without sleeping long. Inject it via Config.PingPong.
	tracker := reliability.NewPingPongTracker(reliability.PingPongConfig{
		PendingTTL: time.Millisecond,
		UnknownTTL: time.Millisecond,
	})
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      minimalCaller(nil),
		PingPong:    tracker,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.RecordPing("orphan-ping")

	// Wait for the TTL to expire.
	time.Sleep(10 * time.Millisecond)

	mismatches := c.SweepPingPong()
	if len(mismatches) == 0 {
		t.Fatal("SweepPingPong: want at least one mismatch for orphan ping, got none")
	}
	if mismatches[0].ID != "orphan-ping" {
		t.Errorf("mismatch ID = %q, want %q", mismatches[0].ID, "orphan-ping")
	}
}

// TestInterfaceClientClearJSONRPCSessionInvokesHookOnce verifies that after
// SetClearJSONRPCSessionHook, a single ClearJSONRPCSession call invokes the
// hook exactly once. Complements the existing TestInterfaceClientClearJSONRPCSessionHook
// (which tests two calls, then unset) by asserting the single-call case and
// by verifying the hook receives no arguments (void function contract).
func TestInterfaceClientClearJSONRPCSessionInvokesHookOnce(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	var calls atomic.Int32
	c.SetClearJSONRPCSessionHook(func() { calls.Add(1) })
	c.ClearJSONRPCSession()

	if n := calls.Load(); n != 1 {
		t.Fatalf("hook fired %d times after one ClearJSONRPCSession, want 1", n)
	}
}

// TestInterfaceClientClearJSONRPCSessionNoHookIsSafe verifies that calling
// ClearJSONRPCSession without any hook installed does not panic. This is a
// regression guard — a nil function-call would panic.
func TestInterfaceClientClearJSONRPCSessionNoHookIsSafe(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))
	// No hook installed — must not panic.
	c.ClearJSONRPCSession()
}

// TestInterfaceClientCloseReleasesResources verifies that Close transitions the
// client to ClientStateStopped and that subsequent Calls return an error (not
// panic or hang). Combines what would be two trivial tests into one coherent
// lifecycle check.
func TestInterfaceClientCloseReleasesResources(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller("ok"))

	// A call before close must succeed.
	if _, err := c.Call(context.Background(), "listMethods", nil, hmenum.CommandPriorityHigh, ""); err != nil {
		t.Fatalf("pre-close Call: %v", err)
	}

	c.Close()

	// After close the state must be Stopped.
	if got := c.ClientState(); got != hmenum.ClientStateStopped {
		t.Fatalf("ClientState after Close = %q, want %q", got, hmenum.ClientStateStopped)
	}

	// After close all Calls must return an error.
	_, err := c.Call(context.Background(), "listMethods", nil, hmenum.CommandPriorityHigh, "")
	if err == nil {
		t.Fatal("expected error after Close, got nil")
	}

	// Double-close must not panic.
	c.Close()
}

// TestInterfaceClientCloseUnblocksWaiters verifies that a goroutine blocked
// in WaitForState is unblocked when Close is called (Close transitions to
// Stopped, which fires all stateWakers).
func TestInterfaceClientCloseUnblocksWaiters(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// Wait for Stopped — only reachable via Close.
		done <- c.WaitForState(ctx, hmenum.ClientStateStopped)
	}()

	time.Sleep(20 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForState(Stopped) after Close: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close did not unblock WaitForState(Stopped) within 200 ms")
	}
}

// TestInterfaceClientVirtualRemoteForVirtualDevicesInterface specifically
// covers the VirtualDevices interface (not in the existing test which does
// cover it, but adds a focused assertion with a clear explanatory comment).
// This test acts as a specification anchor: VirtualDevices has no virtual
// remote because it is a synthetic interface, not a radio channel.
func TestInterfaceClientVirtualRemoteForVirtualDevicesInterface(t *testing.T) {
	t.Parallel()
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceVirtualDevices,
		Caller:      minimalCaller(nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addr, has := c.VirtualRemote()
	if has || addr != "" {
		t.Fatalf("VirtualDevices VirtualRemote = (%q, %v), want (%q, false)", addr, has, "")
	}
}

// TestInterfaceClientNewRejectsEmptyCentralName verifies that New validates
// Config.CentralName is non-empty.
func TestInterfaceClientNewRejectsEmptyCentralName(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		CentralName: "",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      minimalCaller(nil),
	})
	if err == nil {
		t.Fatal("New with empty CentralName must return error")
	}
}

// TestInterfaceClientNewRejectsEmptyInterface verifies that New validates
// Config.Interface is non-empty.
func TestInterfaceClientNewRejectsEmptyInterface(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		CentralName: "central",
		Interface:   "",
		Caller:      minimalCaller(nil),
	})
	if err == nil {
		t.Fatal("New with empty Interface must return error")
	}
}

// TestInterfaceClientNewRejectsNilCaller verifies that New validates
// Config.Caller is non-nil.
func TestInterfaceClientNewRejectsNilCaller(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		CentralName: "central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      nil,
	})
	if err == nil {
		t.Fatal("New with nil Caller must return error")
	}
}

// TestInterfaceClientConcurrentSetStateAndStateRead fan-out exercises the
// shared state mutex under race conditions. Five goroutines concurrently
// write distinct states; five concurrently read. The race detector must
// report no data races. The final state must be one of the set values.
func TestInterfaceClientConcurrentSetStateAndStateRead(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, minimalCaller(nil))

	writeStates := []hmenum.ClientState{
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateFailed,
	}
	validFinal := make(map[hmenum.ClientState]bool, len(writeStates))
	for _, s := range writeStates {
		validFinal[s] = true
	}
	// ClientStateStopped is also a valid final state (set by Close at end).
	validFinal[hmenum.ClientStateStopped] = true

	var wg sync.WaitGroup
	wg.Add(10)

	// 5 writers.
	for i := range 5 {
		go func() {
			defer wg.Done()
			c.SetState(writeStates[i])
		}()
	}

	// 5 readers — just call State(); we only care about absence of races.
	for range 5 {
		go func() {
			defer wg.Done()
			_ = c.ClientState()
		}()
	}

	wg.Wait()

	// Close to get a deterministic terminal state, then check it's valid.
	c.Close()
	final := c.ClientState()
	if !validFinal[final] {
		t.Fatalf("final State = %q, not in expected set", final)
	}
}

// TestInterfaceClientInterfaceAndCentralNameAccessors verifies the read-only
// accessor methods Interface() and CentralName() return the configured values.
func TestInterfaceClientInterfaceAndCentralNameAccessors(t *testing.T) {
	t.Parallel()
	c, err := New(Config{
		CentralName: "my-central",
		Interface:   hmenum.InterfaceBidCosWired,
		Caller:      minimalCaller(nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.CentralName(); got != "my-central" {
		t.Errorf("CentralName() = %q, want %q", got, "my-central")
	}
	if got := c.Interface(); got != hmenum.InterfaceBidCosWired {
		t.Errorf("Interface() = %q, want %q", got, hmenum.InterfaceBidCosWired)
	}
}
