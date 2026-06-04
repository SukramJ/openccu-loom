// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

func supervisorLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func mqttCfg(enabled bool) *config.Config {
	c := config.Default()
	c.North.MQTT.Enabled = enabled
	c.North.MQTT.BrokerURL = "" // forces NoopClient path
	c.North.MQTT.TopicBase = "test"
	return c
}

func newSup(t *testing.T) *mqttSupervisor {
	t.Helper()
	return newMQTTSupervisor(supervisorLogger(), health.NewTracker())
}

func TestSupervisor_StartDisabled_NoStack(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Wiring() != nil {
		t.Fatal("Wiring() must be nil when MQTT is disabled")
	}
	if s.CurrentClient() != nil {
		t.Fatal("CurrentClient() must be nil when MQTT is disabled")
	}
}

func TestSupervisor_StartEnabled_NoopClient_HasWiring(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Wiring() == nil {
		t.Fatal("Wiring() must be non-nil after Start with MQTT enabled")
	}
	if s.CurrentClient() == nil {
		t.Fatal("CurrentClient() must be non-nil after Start")
	}
	if _, ok := s.CurrentClient().(*mqtt.NoopClient); !ok {
		t.Fatalf("expected *mqtt.NoopClient, got %T", s.CurrentClient())
	}
	if s.CurrentBridge() == nil {
		t.Fatal("CurrentBridge() must be non-nil after Start")
	}
}

func TestSupervisor_AttachSubscribers_NoBuilder_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.AttachSubscribers(ctx); err != nil {
		t.Fatalf("AttachSubscribers without builder: %v", err)
	}
}

func TestSupervisor_AttachSubscribers_BuilderInvoked(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var buildCalls, stopCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		buildCalls.Add(1)
		return func() { stopCalls.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start already invoked the builder if one was set; AttachSubscribers
	// must be idempotent (see idempotency test). We verify that the builder
	// was called exactly once in total after Start.
	_ = s.AttachSubscribers(ctx) // no-op: stopSubs already set by Start

	if buildCalls.Load() != 1 {
		t.Fatalf("builder called %d times, want 1", buildCalls.Load())
	}

	s.Shutdown(ctx)
	if stopCalls.Load() != 1 {
		t.Fatalf("stop func called %d times after Shutdown, want 1", stopCalls.Load())
	}
}

func TestSupervisor_AttachSubscribers_BuilderError_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	buildErr := errors.New("broker not ready")
	var stopCalls atomic.Int32

	// Install the error builder before Start so it is also invoked by Start.
	// Start logs the error but does not abort (comment in Start confirms this).
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return nil, buildErr
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start must not fail on subscriber builder error: %v", err)
	}

	// Replace builder for the explicit AttachSubscribers call.
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return nil, buildErr
	})

	// stopSubs is nil because the builder errored during Start; AttachSubscribers
	// will invoke the builder and get the error again.
	if err := s.AttachSubscribers(ctx); err == nil {
		t.Fatal("AttachSubscribers must return error when builder fails")
	}

	s.Shutdown(ctx)
	if stopCalls.Load() != 0 {
		t.Fatalf("stop func must not be called when builder returned error, got %d calls", stopCalls.Load())
	}
}

func TestSupervisor_AttachSubscribers_Idempotent(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var buildCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		buildCalls.Add(1)
		return func() {}, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Start already wired subscribers; AttachSubscribers must be a no-op.
	_ = s.AttachSubscribers(ctx)
	_ = s.AttachSubscribers(ctx)

	if buildCalls.Load() != 1 {
		t.Fatalf("builder called %d times, want exactly 1", buildCalls.Load())
	}
}

func TestSupervisor_Swap_DisabledToEnabled(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Wiring() != nil {
		t.Fatal("Wiring() must be nil after disabled Start")
	}

	if err := s.Swap(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Swap to enabled: %v", err)
	}
	if s.Wiring() == nil {
		t.Fatal("Wiring() must be non-nil after Swap to enabled")
	}
	if _, ok := s.CurrentClient().(*mqtt.NoopClient); !ok {
		t.Fatalf("expected *mqtt.NoopClient after swap, got %T", s.CurrentClient())
	}
}

func TestSupervisor_Swap_EnabledToDisabled(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var stopCalls atomic.Int32
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		return func() { stopCalls.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = s.AttachSubscribers(ctx)

	if err := s.Swap(ctx, mqttCfg(false)); err != nil {
		t.Fatalf("Swap to disabled: %v", err)
	}

	if s.CurrentClient() != nil {
		t.Fatal("CurrentClient() must be nil after swap to disabled")
	}
	// The stable Wiring pointer survives across swaps.
	if s.Wiring() == nil {
		t.Fatal("Wiring() must survive swap-to-disabled")
	}
	// The bridge behind Wiring is nil after disabling.
	if s.Wiring().Bridge() != nil {
		t.Fatalf("Wiring().Bridge() must be nil after swap to disabled, got %p", s.Wiring().Bridge())
	}
	// Stop was called during teardown.
	if stopCalls.Load() != 1 {
		t.Fatalf("stop func called %d times after swap to disabled, want 1", stopCalls.Load())
	}
}

func TestSupervisor_Swap_NilConfig_ReturnsError(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()
	if err := s.Swap(ctx, nil); err == nil {
		t.Fatal("Swap(nil) must return an error")
	}
}

func TestSupervisor_Shutdown_Idempotent(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.Shutdown(ctx)
	s.Shutdown(ctx) // idempotent: second call is safe
}

func TestSupervisor_Shutdown_NoStart_NoOp(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()
	s.Shutdown(ctx) // safe to call without a prior Start
}

func TestSupervisor_RedactBrokerURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"tcp://user:pass@host:1883", "tcp://***@host:1883"},
		{"tcp://host:1883", "tcp://host:1883"},
		{"", ""},
		{"host:1883", "host:1883"},
	}
	for _, c := range cases {
		got := redactBrokerURL(c.in)
		if got != c.want {
			t.Errorf("redactBrokerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
