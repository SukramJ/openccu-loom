// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newGatedIC builds a client whose reconnect path consults gate before
// re-registering with the CCU.
func newGatedIC(t *testing.T, gate func(context.Context) bool) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName: "test",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
		WaitCCUReady: gate,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = ic.TransitionTo(hmenum.ClientStateInitialized, "", true, hmenum.FailureReasonNone)
	_ = ic.TransitionTo(hmenum.ClientStateDisconnected, "", true, hmenum.FailureReasonNone)
	return ic
}

func fastReconnectConfig() *client.ReconnectConfig {
	return &client.ReconnectConfig{
		InitialDelay:  1 * time.Millisecond,
		MaxDelay:      10 * time.Millisecond,
		BackoffFactor: 2.0,
	}
}

// TestReconnectWaitsForCCUReadiness pins the fix for CCU events arriving
// twice after a CCU restart.
//
// A rebooting CCU serves XML-RPC before it is fully up. Re-registering in that
// window fails the `deinit` but lands the `init`, so the CCU keeps the previous
// registration alongside the new one (it suffixes the interface id) and pushes
// every event once per registration. Whatever reacts to those events then runs
// twice. The reconnect must therefore not re-register while the CCU reports
// itself unready.
func TestReconnectWaitsForCCUReadiness(t *testing.T) {
	ic := newGatedIC(t, func(context.Context) bool { return false })

	b := &orchBackend{}
	attempts := 0
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", fastReconnectConfig(), &attempts)
	if err != nil {
		t.Fatalf("a not-ready CCU is not an error, just a deferral: %v", err)
	}
	if ok {
		t.Error("reconnect must not report success while the CCU is unready")
	}
	if b.deinitOK {
		t.Error("no re-registration may be attempted against an unready CCU")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 so the backoff keeps growing across deferrals", attempts)
	}
	// A deferral must not wedge the client: once the CCU comes up, the very
	// next attempt has to go through. Without the DISCONNECTED transition the
	// follow-up fails its CanReconnect guard and the client never recovers.
	ready := false
	ic2 := newGatedIC(t, func(context.Context) bool { return ready })
	b2 := &orchBackend{}
	att := 0
	if _, err := ic2.Reconnect(context.Background(), b2, "id", "url", fastReconnectConfig(), &att); err != nil {
		t.Fatalf("first (deferred) attempt: %v", err)
	}
	ready = true
	ok, err = ic2.Reconnect(context.Background(), b2, "id", "url", fastReconnectConfig(), &att)
	if err != nil {
		t.Fatalf("attempt after the CCU became ready: %v", err)
	}
	if !ok || !b2.deinitOK {
		t.Error("a deferral must leave the client able to reconnect on the next attempt")
	}
}

// TestReconnectProceedsWhenCCUReady is the other half: once readiness is
// observed the re-registration runs as before.
func TestReconnectProceedsWhenCCUReady(t *testing.T) {
	gateCalls := 0
	ic := newGatedIC(t, func(context.Context) bool {
		gateCalls++
		return true
	})

	b := &orchBackend{}
	attempts := 0
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", fastReconnectConfig(), &attempts)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if !ok {
		t.Error("a ready CCU must reconnect")
	}
	if !b.deinitOK {
		t.Error("the re-registration must still deinit before init")
	}
	if gateCalls != 1 {
		t.Errorf("gate consulted %d times, want exactly 1", gateCalls)
	}
}

// TestReconnectWithoutGateIsUnchanged keeps the gate optional: a client wired
// without one (tests, tooling, a central with no probe host) reconnects
// exactly as before.
func TestReconnectWithoutGateIsUnchanged(t *testing.T) {
	ic := newGatedIC(t, nil)

	b := &orchBackend{}
	attempts := 0
	ok, err := ic.Reconnect(context.Background(), b, "id", "url", fastReconnectConfig(), &attempts)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if !ok || !b.deinitOK {
		t.Error("a nil gate must not block the reconnect")
	}
}
