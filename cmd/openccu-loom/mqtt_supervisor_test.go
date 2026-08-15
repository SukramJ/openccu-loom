// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestSupervisorSurvivesABrokerThatIsDownAtBoot pins the two halves of the
// boot-time recovery: the Wiring every north-bound consumer binds to must
// exist even though the first connect failed, and the supervisor must keep
// retrying so the link comes up without a daemon restart.
//
// Both mattered because Lifecycle.Start performs the first connect
// synchronously and only starts its reconnect loop afterwards — a refused
// CONNECT at boot left no bridge, no Wiring and nothing that would ever try
// again, so the whole MQTT plane stayed dark for the process lifetime.
func TestSupervisorSurvivesABrokerThatIsDownAtBoot(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A listener that accepts and immediately closes is a broker that is
	// reachable but not serving — every CONNECT fails, and each accept counts
	// one connect attempt.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dials atomic.Int64
	go func() {
		for {
			conn, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			dials.Add(1)
			_ = conn.Close()
		}
	}()

	cfg := mqttCfg(true)
	cfg.North.MQTT.BrokerURL = "tcp://" + ln.Addr().String()

	s := newSup(t)
	s.retryDelay = 10 * time.Millisecond
	s.retryMaxDelay = 10 * time.Millisecond
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	if err := s.Start(ctx, cfg); err == nil {
		t.Fatal("Start against a broker that refuses every CONNECT returned nil, want an error")
	}
	if s.Wiring() == nil {
		t.Fatal("Wiring() is nil after a failed boot connect; every consumer binds this pointer " +
			"once at composition time, so a nil here disables MQTT for the process lifetime")
	}

	deadline := time.Now().Add(5 * time.Second)
	for dials.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := dials.Load(); got < 3 {
		t.Fatalf("connect attempts = %d, want >= 3; the supervisor must keep retrying "+
			"because the lifecycle's own reconnect loop never started", got)
	}
}

// TestSupervisorSwapAfterAFailedBootConnectReachesBoundConsumers pins that the
// recovery actually reaches the consumers: they hold the Wiring pointer from
// boot, so a stack built later has to be installed INTO that pointer rather
// than into a fresh one.
func TestSupervisorSwapAfterAFailedBootConnectReachesBoundConsumers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A closed port: the dial itself fails, so no retry can succeed until the
	// config is swapped.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	cfg := mqttCfg(true)
	cfg.North.MQTT.BrokerURL = "tcp://" + addr

	s := newSup(t)
	s.retryDelay = time.Hour // the retry loop must not race the explicit Swap
	s.retryMaxDelay = time.Hour
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	if err := s.Start(ctx, cfg); err == nil {
		t.Fatal("Start against a closed port returned nil, want an error")
	}
	// What a consumer captured at composition time.
	bound := s.Wiring()
	if bound == nil {
		t.Fatal("Wiring() is nil after a failed boot connect")
	}
	if bound.Bridge() != nil {
		t.Fatal("the boot-failed Wiring must carry no bridge, so publishes are dropped rather than panicking")
	}

	if err := s.Swap(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Swap after a failed boot connect: %v", err)
	}
	if bound.Bridge() == nil {
		t.Fatal("the Wiring captured at boot still has no bridge after a successful Swap; " +
			"the recovered stack never reaches the publishers built at boot")
	}
}

// TestSupervisorConcurrentSwapsRetireEveryStackButTheLiveOne pins that a stack
// transition is atomic end to end.
//
// Swap snapshots the live stack, releases the lock to build and connect the
// replacement, then re-takes it to commit. Two callers exist in production and
// nothing else serialises them — the config watcher's tick and
// POST /api/v1/admin/mqtt/reload — so without a lifecycle lock both capture the
// same predecessor, both commit, and the loser's fully-built stack is
// referenced by nothing that could tear it down: a connected client and its
// subscribers leak until the process exits.
func TestSupervisorConcurrentSwapsRetireEveryStackButTheLiveOne(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var built, stopped atomic.Int64
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		built.Add(1)
		return func() { stopped.Add(1) }, nil
	})

	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const (
		swappers = 8
		rounds   = 20
	)
	var wg sync.WaitGroup
	for range swappers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				if err := s.Swap(ctx, mqttCfg(true)); err != nil {
					t.Errorf("Swap: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Exactly one generation — the live one — may still hold its subscribers.
	if live := built.Load() - stopped.Load(); live != 1 {
		t.Fatalf("%d subscriber sets are still live after %d swaps, want exactly 1 (built=%d stopped=%d)",
			live, swappers*rounds, built.Load(), stopped.Load())
	}

	s.Shutdown(ctx)
	if live := built.Load() - stopped.Load(); live != 0 {
		t.Fatalf("%d subscriber sets survived shutdown, want 0", live)
	}
}

// TestSupervisorSwapDuringShutdownStopsTheOrphanedSubscribers pins the other
// half of the same invariant: a stack whose commit loses to Shutdown must be
// torn down rather than left running behind a cleared s.current.
func TestSupervisorSwapDuringShutdownStopsTheOrphanedSubscribers(t *testing.T) {
	t.Parallel()
	s := newSup(t)
	ctx := context.Background()

	var built, stopped atomic.Int64
	s.SetSubscriberBuilder(func(_ context.Context, _ mqtt.Client, _ *mqtt.Bridge) (func(), error) {
		built.Add(1)
		return func() { stopped.Add(1) }, nil
	})
	if err := s.Start(ctx, mqttCfg(true)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = s.Swap(ctx, mqttCfg(true))
	}()
	go func() {
		defer wg.Done()
		s.Shutdown(ctx)
	}()
	wg.Wait()
	// Whichever order the two ran in, a stack built after the shutdown must
	// still be torn down by the next Shutdown call.
	s.Shutdown(ctx)

	if live := built.Load() - stopped.Load(); live != 0 {
		t.Fatalf("%d subscriber sets outlived shutdown (built=%d stopped=%d)",
			live, built.Load(), stopped.Load())
	}
}
