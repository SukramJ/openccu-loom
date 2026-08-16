// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestInterfaceClientCoalescesByKey(t *testing.T) {
	var calls atomic.Int32
	caller := CallerFunc(func(context.Context, string, []any) (any, error) {
		calls.Add(1)
		return 42, nil
	})
	c, err := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	done := make(chan struct{}, 5)
	for range 5 {
		go func() {
			_, _ = c.Call(ctx, "getValue", nil, hmenum.CommandPriorityHigh, "k")
			done <- struct{}{}
		}()
	}
	for range 5 {
		<-done
	}
	// Coalescing is best-effort: at least one but at most a handful of
	// actual calls depending on scheduler timing. Tight bound: ≤ 5.
	if n := calls.Load(); n < 1 || n > 5 {
		t.Fatalf("calls=%d", n)
	}
}

func TestInterfaceClientRetriesTransient(t *testing.T) {
	var attempts atomic.Int32
	caller := CallerFunc(func(context.Context, string, []any) (any, error) {
		n := attempts.Add(1)
		if n < 2 {
			return nil, errors.New("transient")
		}
		return "ok", nil
	})
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		Retrier:     reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 3, Initial: 1}),
	})
	got, err := c.Call(context.Background(), "getValue", nil, hmenum.CommandPriorityHigh, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" || attempts.Load() != 2 {
		t.Fatalf("got=%v attempts=%d", got, attempts.Load())
	}
}

func TestInterfaceClientCircuitRejectsAfterThreshold(t *testing.T) {
	caller := CallerFunc(func(context.Context, string, []any) (any, error) {
		return nil, errors.New("boom")
	})
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      caller,
		Circuit:     reliability.NewCircuit(reliability.CircuitConfig{FailureThreshold: 1}),
		Retrier:     reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1, Initial: 1}),
	})
	_, _ = c.Call(context.Background(), "x", nil, hmenum.CommandPriorityHigh, "")
	_, err := c.Call(context.Background(), "x", nil, hmenum.CommandPriorityHigh, "")
	if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("got %v, want ErrCircuitBreakerOpen", err)
	}
}

func TestInterfaceClientCapabilitiesFromInterface(t *testing.T) {
	for iface, wantPush := range map[hmenum.Interface]bool{
		hmenum.InterfaceHmIPRF: true,
		hmenum.InterfaceCUxD:   true,
	} {
		c, _ := New(Config{
			CentralName: "main",
			Interface:   iface,
			Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		})
		if got := c.Capabilities().RPCCallback; got != wantPush {
			t.Errorf("%s RPCCallback=%v, want %v", iface, got, wantPush)
		}
	}
}

func TestInterfaceClientClosedRejectsCalls(t *testing.T) {
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	c.Close()
	_, err := c.Call(context.Background(), "x", nil, hmenum.CommandPriorityHigh, "")
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

func TestInterfaceClientIsCallbackAlive(t *testing.T) {
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	// Before any callback the timestamp is zero; the post-init guard returns
	// true so a scheduler tick during startup does not falsely trigger a
	// ConnectionLost event.
	if !c.IsCallbackAlive() {
		t.Fatal("fresh client with zero timestamp must report alive (post-init guard)")
	}
	if !c.LastCallbackAt().IsZero() {
		t.Fatal("fresh client LastCallbackAt must be zero")
	}
	c.NotifyCallback()
	if !c.IsCallbackAlive() {
		t.Fatal("after NotifyCallback must report alive")
	}
	if c.LastCallbackAt().IsZero() {
		t.Fatal("after NotifyCallback timestamp must be set")
	}
}

func TestInterfaceClientClearJSONRPCSessionHook(t *testing.T) {
	c, _ := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	c.ClearJSONRPCSession()

	called := 0
	c.SetClearJSONRPCSessionHook(func() { called++ })
	c.ClearJSONRPCSession()
	c.ClearJSONRPCSession()
	if called != 2 {
		t.Fatalf("hook called %d times, want 2", called)
	}

	c.SetClearJSONRPCSessionHook(nil)
	c.ClearJSONRPCSession()
	if called != 2 {
		t.Fatalf("after unset hook called %d times, want still 2", called)
	}
}

func TestInterfaceClientVirtualRemote(t *testing.T) {
	cases := []struct {
		iface    hmenum.Interface
		wantAddr string
		wantHas  bool
	}{
		{hmenum.InterfaceHmIPRF, "HmIP-RF", true},
		{hmenum.InterfaceBidCosRF, "BidCoS-RF", true},
		{hmenum.InterfaceCUxD, "", false},
		{hmenum.InterfaceVirtualDevices, "", false},
	}
	for _, tc := range cases {
		c, _ := New(Config{
			CentralName: "main",
			Interface:   tc.iface,
			Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		})
		gotAddr, gotHas := c.VirtualRemote()
		if gotAddr != tc.wantAddr || gotHas != tc.wantHas {
			t.Errorf("%s VirtualRemote = (%q, %v), want (%q, %v)", tc.iface, gotAddr, gotHas, tc.wantAddr, tc.wantHas)
		}
	}
}

// TestInterfaceClientCloseCanelsActiveRetries verifies that Close()
// drains all in-flight DoForKey retry chains so ActiveRetryCount
// returns 0 after shutdown. This guards against stale retries keeping
// wire resources alive after an interface disconnect (Task #19).
func TestInterfaceClientCloseCanelsActiveRetries(t *testing.T) {
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: 10,
		Initial:     500 * time.Millisecond,
		Max:         500 * time.Millisecond,
		Multiplier:  1,
	})
	c, err := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		Retrier:     retrier,
	})
	if err != nil {
		t.Fatal(err)
	}

	dpk := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "VCU1234567:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}

	// Launch a retry chain that will block on backoff indefinitely.
	started := make(chan struct{})
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- retrier.DoForKey(context.Background(), dpk, func(_ context.Context, attempt int) error {
			if attempt == 1 {
				close(started)
			}
			return errors.New("transient")
		})
	}()

	// Wait until the chain has registered itself.
	<-started
	for range 100 {
		if retrier.ActiveRetryCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := retrier.ActiveRetryCount(); got != 1 {
		t.Fatalf("ActiveRetryCount before Close = %d, want 1", got)
	}

	// Close the client — must cancel all in-flight chains.
	c.Close()

	select {
	case <-retryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("retry chain did not terminate after Close")
	}

	if got := retrier.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after Close = %d, want 0", got)
	}
}

// TestInterfaceClientCloseReleasesCoalescedCallers pins that Close leaves no
// caller parked on a coalesced call. Their transport is gone, so the result
// they wait for can never arrive; Close releases them with
// [reliability.ErrCoalescerCleared] so the abandoned call is reported as the
// failure it is instead of hanging or reading as a success.
func TestInterfaceClientCloseReleasesCoalescedCallers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, err := New(Config{
		CentralName: "main",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: CallerFunc(func(ctx context.Context, _ string, _ []any) (any, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, nil
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	callDone := make(chan error, 1)
	go func() {
		_, callErr := c.Call(context.Background(), "setValue", []any{"VCU1234567:1", "STATE", true},
			hmenum.CommandPriorityLow, "setValue|0|VCU1234567:1|STATE|bool|true")
		callDone <- callErr
	}()
	<-started

	c.Close()

	select {
	case callErr := <-callDone:
		if !errors.Is(callErr, reliability.ErrCoalescerCleared) {
			t.Fatalf("Call after Close returned %v, want %v", callErr, reliability.ErrCoalescerCleared)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced caller was never released by Close")
	}
}
