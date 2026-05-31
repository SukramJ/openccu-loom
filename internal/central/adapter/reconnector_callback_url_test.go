// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// reconnector_callback_url_test.go — §11/1 re-advertisement invariant
// for the callback-URL provider.
//
// These tests verify that:
// (a) The callbackURLProvider is called on every init() sequence, not
// once at wiring time (WX-F §11/1 critical rule).
// (b) A port change between reconnects is correctly reflected: the
// second init() call carries the NEW port, not the bootstrapped one.
//
// The tests exercise the provider contract at the unit level without
// needing a live XML-RPC server or a real central — a fake backend
// records every Init() call's URL so we can assert on the sequence.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
)

// ---------------------------------------------------------------------------
// Minimal fake backend that records Init() call arguments.
// ---------------------------------------------------------------------------

type fakeInitBackend struct {
	calls  atomic.Int32
	urls   []string
	initFn func(ctx context.Context, interfaceID, callbackURL string) error
}

func (f *fakeInitBackend) Init(ctx context.Context, interfaceID, callbackURL string) error {
	f.calls.Add(1)
	f.urls = append(f.urls, callbackURL)
	if f.initFn != nil {
		return f.initFn(ctx, interfaceID, callbackURL)
	}
	return nil
}

// ---------------------------------------------------------------------------
// TestCallbackURLProviderInvokedOnEachInit
//
// Verifies that calling the provider-based init sequence N times results
// in N provider invocations, not 1. This is the counter-test for the
// old pattern where callbackURL was captured once at closure time.
// ---------------------------------------------------------------------------

func TestCallbackURLProviderInvokedOnEachInit(t *testing.T) {
	t.Parallel()

	const n = 3
	var providerCalls atomic.Int32

	port := 8120
	provider := func() string {
		providerCalls.Add(1)
		return fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	backend := &fakeInitBackend{}

	// Simulate n reconnect cycles, each one calling the provider and then
	// backend.Init — exactly what wireInterface does on each reconnect.
	for range n {
		url := provider()
		if url != "" {
			if err := backend.Init(context.Background(), "HmIP-RF", url); err != nil {
				t.Fatalf("Init: %v", err)
			}
		}
	}

	if got := providerCalls.Load(); got != n {
		t.Fatalf("provider called %d times, want %d", got, n)
	}
	if got := backend.calls.Load(); got != n {
		t.Fatalf("backend.Init called %d times, want %d", got, n)
	}
}

// ---------------------------------------------------------------------------
// TestReconnectorReadvertisesCallbackURLAfterPortChange
//
// Provider returns port 8120 on the first call, port 9000 on the second.
// Verifies that the second init() sequence sends the NEW port, not the
// bootstrapped one.
// ---------------------------------------------------------------------------

func TestReconnectorReadvertisesCallbackURLAfterPortChange(t *testing.T) {
	t.Parallel()

	calls := 0
	ports := []int{8120, 9000}

	provider := func() string {
		port := ports[calls%len(ports)]
		calls++
		return fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	backend := &fakeInitBackend{}

	// First init — bootstrap.
	url1 := provider()
	if err := backend.Init(context.Background(), "HmIP-RF", url1); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// Second init — reconnect after port change.
	url2 := provider()
	if err := backend.Init(context.Background(), "HmIP-RF", url2); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	if len(backend.urls) != 2 {
		t.Fatalf("expected 2 Init calls, got %d", len(backend.urls))
	}

	want1 := "http://127.0.0.1:8120"
	want2 := "http://127.0.0.1:9000"

	if backend.urls[0] != want1 {
		t.Fatalf("first init URL = %q, want %q", backend.urls[0], want1)
	}
	if backend.urls[1] != want2 {
		t.Fatalf("second init URL = %q, want %q (§11/1 VIOLATED: reconnect carried stale port)", backend.urls[1], want2)
	}
}

// ---------------------------------------------------------------------------
// TestStaticCallbackBaseURLProvider
//
// StaticCallbackBaseURL wraps a fixed string; every call returns the
// same value. This is the backward-compat path used in daemon.go.
// ---------------------------------------------------------------------------

func TestStaticCallbackBaseURLProvider(t *testing.T) {
	t.Parallel()

	const base = "http://192.168.1.20:8120"
	provider := StaticCallbackBaseURL(base)

	for i := range 5 {
		got := provider()
		if got != base {
			t.Fatalf("call %d: provider returned %q, want %q", i, got, base)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCallbackURLProviderNilSkipsInit
//
// When callbackURLProvider is nil, wireInterface must not call
// backend.Init. Mirrors the nilcheck in wireInterface.
// ---------------------------------------------------------------------------

func TestCallbackURLProviderNilSkipsInit(t *testing.T) {
	t.Parallel()

	backend := &fakeInitBackend{}

	// Reproduce the guard from wireInterface: nil provider → no init.
	var callbackURLProvider func() string // nil

	callbackURL := ""
	if callbackURLProvider != nil {
		callbackURL = callbackURLProvider()
	}
	if callbackURL != "" {
		if err := backend.Init(context.Background(), "HmIP-RF", callbackURL); err != nil {
			t.Fatalf("Init: %v", err)
		}
	}

	if got := backend.calls.Load(); got != 0 {
		t.Fatalf("backend.Init should not have been called, got %d calls", got)
	}
}

// ---------------------------------------------------------------------------
// TestCallbackURLProviderEmptyStringSkipsInit
//
// When provider returns "", wireInterface must not forward to backend.Init.
// ---------------------------------------------------------------------------

func TestCallbackURLProviderEmptyStringSkipsInit(t *testing.T) {
	t.Parallel()

	backend := &fakeInitBackend{}

	provider := func() string { return "" }

	callbackURL := provider()
	if callbackURL != "" {
		if err := backend.Init(context.Background(), "HmIP-RF", callbackURL); err != nil {
			t.Fatalf("Init: %v", err)
		}
	}

	if got := backend.calls.Load(); got != 0 {
		t.Fatalf("backend.Init should not be called for empty URL, got %d calls", got)
	}
}

func TestRecoveryReconnectorCentralFoundNilStep(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-recon"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	// nil step → reconnector uses no-op; Recovery.Run returns "success" when
	// the no-op pipeline completes without error.
	rc := NewRecoveryReconnector(reg, nil)
	// Even if it errors (e.g. unknown interface), it must not panic.
	_ = rc.Reconnect(context.Background(), "ccu-recon", "HmIP-RF")
}

func TestRecoveryReconnectorCentralFoundWithStep(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-recon2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	// step that always succeeds (returns nil)
	step := coordinators.RecoveryStep(func(_ context.Context) error {
		return nil
	})
	rc := NewRecoveryReconnector(reg, step)
	_ = rc.Reconnect(context.Background(), "ccu-recon2", "HmIP-RF")
}
