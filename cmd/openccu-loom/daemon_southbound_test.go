// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestRetainedOrphanSweepsArmWhenTheBrokerConnectsLate pins the sweeps against
// a broker that was down when the daemon booted.
//
// A failed boot connect leaves the supervisor's stable Wiring in place with no
// bridge behind it, and the background retry installs one into that same
// Wiring minutes later. Reading the bridge when the hook is WIRED — rather than
// when it FIRES — skipped the wiring for the process lifetime, so retained
// discovery configs of removed devices kept re-creating permanently
// `unavailable` entities with nothing left to evict them. Claiming the central
// as swept before the bridge check has the same effect one step later: the
// central's single sweep is spent on a pass that published nothing.
func TestRetainedOrphanSweepsArmWhenTheBrokerConnectsLate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := buildTestRegistry(t, "ccu")
	unit, ok := reg.Get("ccu")
	if !ok {
		t.Fatal("registry lost the central it just registered")
	}
	unit.MarkSouthboundReady()

	// The Wiring a failed boot connect leaves behind: real pointer, no bridge.
	wiring := mqtt.NewWiring(nil, slog.New(slog.DiscardHandler))
	eb := adapter.NewEventBridge(reg, nil, wiring)

	cfg := config.Default()
	cfg.North.MQTT.RetainCleanupWindowMs = 500 // the floor the config clamps to
	wireRetainedOrphanSweeps(ctx, southboundWiringDeps{
		mqttWiring: wiring,
		bridge:     eb,
	}, cfg, slog.New(slog.DiscardHandler))

	// Boot-time snapshot while the link is still down: it publishes nothing,
	// so it must not spend the central's one sweep either.
	eb.PublishInitialSnapshot(ctx)

	// The background retry connects and installs its bridge into the very
	// Wiring the hook closed over.
	client := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "test",
		CentralName:        "ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, client).WithSubscriber(client)
	wiring.SwapBridge(bridge)

	eb.PublishInitialSnapshot(ctx)

	// The sweeps run on their own goroutine and each opens a subscribe window;
	// DeliverInbound reports whether one is open, so poll for it instead of
	// timing the window.
	deadline := time.Now().Add(10 * time.Second)
	for !sweepSubscriptionOpen(client) {
		if time.Now().After(deadline) {
			t.Fatal("no retained-orphan sweep ran after the broker connected; " +
				"the sweeps never arm once the boot connect has failed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestBootRetainCleanupsRunOnTheFirstLiveBridge pins the once-guard: the boot
// path offers a bridge only if the broker was reachable then, so an offer with
// no bridge must leave the scrubs pending for the connect hook to run, and the
// first real bridge must consume them exactly once.
func TestBootRetainCleanupsRunOnTheFirstLiveBridge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := config.Default()
	cfg.North.MQTT.TopicBase = "test"
	cfg.North.MQTT.RetainCleanupWindowMs = 500
	cleanups := newBootRetainCleanups(cfg, slog.New(slog.DiscardHandler))

	// The broker was down when the boot path reached the scrubs.
	cleanups.run(ctx, nil)

	client := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "test",
		CentralName:        "ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, client).WithSubscriber(client)

	// A retired retained topic the broker replays into the scrub's window.
	const retired = "test/ccu/hub/programs/42/trigger"
	fed := make(chan struct{})
	go func() {
		defer close(fed)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if client.DeliverInbound("test/#", retired, []byte("true")) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	cleanups.run(ctx, bridge)
	<-fed

	if !evicted(client, retired) {
		t.Fatal("the retired retained topic was not evicted; the boot scrubs never ran against the recovered bridge")
	}

	before := len(client.Published())
	cleanups.run(ctx, bridge)
	if got := len(client.Published()); got != before {
		t.Errorf("a second run published %d more messages, want 0: the scrubs are once-per-process", got-before)
	}
}

// fakeRetainScrubber stands in for the MQTT bridge so the busy-slot retry
// behaviour can be driven without a live broker. While busy is true both
// passes abort with [mqtt.ErrSweepSlotBusy] — the "not attempted" signal.
type fakeRetainScrubber struct {
	mu            sync.Mutex
	busy          bool
	retainCalls   int
	unscopedCalls int
}

func (f *fakeRetainScrubber) RunRetainCleanupOnce(_ context.Context, _ time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retainCalls++
	if f.busy {
		return 0, fmt.Errorf("boot scrub aborted: %w", mqtt.ErrSweepSlotBusy)
	}
	return 0, nil
}

func (f *fakeRetainScrubber) RunUnscopedDiscoveryCleanupOnce(_ context.Context, _ time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unscopedCalls++
	if f.busy {
		return 0, fmt.Errorf("boot scrub aborted: %w", mqtt.ErrSweepSlotBusy)
	}
	return 0, nil
}

func (f *fakeRetainScrubber) calls() (retain, unscoped int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retainCalls, f.unscopedCalls
}

// TestBootRetainCleanupsRetryAfterBusySlot pins the once-guard's retry
// contract: an attempt aborted by a busy snapshot slot ([mqtt.ErrSweepSlotBusy])
// means "not attempted", so it must leave the guard UNLATCHED for the next
// (re)connect to retry. Latching on busy — as the code did before — skipped the
// scrub for the rest of the process life. Once both passes are actually
// attempted the guard latches and never repeats.
func TestBootRetainCleanupsRetryAfterBusySlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := config.Default()
	cfg.North.MQTT.RetainCleanupWindowMs = 500
	cleanups := newBootRetainCleanups(cfg, slog.New(slog.DiscardHandler))

	scrubber := &fakeRetainScrubber{busy: true}

	// First attempt: the slot stays busy for the whole budget, both passes abort.
	cleanups.runScrub(ctx, scrubber)
	if latched := cleanups.completed(); latched {
		t.Fatal("done latched after a busy-slot attempt; the scrub would never retry")
	}
	if retain, _ := scrubber.calls(); retain == 0 {
		t.Fatal("the busy attempt never called the retain scrub")
	}

	// The slot frees up; a later (re)connect calls run again and both passes are
	// attempted, so the guard latches this time.
	scrubber.busy = false
	cleanups.runScrub(ctx, scrubber)
	if latched := cleanups.completed(); !latched {
		t.Fatal("done did not latch after both scrubs were attempted")
	}

	// A third call is a no-op: once both passes ran they never repeat.
	retainBefore, unscopedBefore := scrubber.calls()
	cleanups.runScrub(ctx, scrubber)
	retainAfter, unscopedAfter := scrubber.calls()
	if retainAfter != retainBefore || unscopedAfter != unscopedBefore {
		t.Errorf("a third run re-invoked the scrubs (retain %d→%d, unscoped %d→%d); they are once-per-process",
			retainBefore, retainAfter, unscopedBefore, unscopedAfter)
	}
}

// evicted reports whether topic was cleared — an empty retained payload.
func evicted(client *mqtt.NoopClient, topic string) bool {
	for _, p := range client.Published() {
		if p.Topic == topic && len(p.Payload) == 0 && p.Retain {
			return true
		}
	}
	return false
}

// sweepSubscriptionOpen reports whether either retained-orphan sweep currently
// holds its snapshot subscription. Both messages are inert: the discovery
// handler ignores a topic outside the daemon's node-id namespace, and the raw
// handler ignores one that names no data point.
func sweepSubscriptionOpen(client *mqtt.NoopClient) bool {
	discovery := client.DeliverInbound("homeassistant/#", "homeassistant/sensor/foreign/entity/config", []byte("{}"))
	raw := client.DeliverInbound("test/#", "test/unrelated", []byte("{}"))
	return discovery || raw
}
