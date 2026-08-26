// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// holdSweepSlot occupies the bridge's retained-snapshot slot for d, the
// way a per-central orphan sweep does while its own window is open, and
// releases it again. It returns once the slot is held.
func holdSweepSlot(t *testing.T, b *Bridge, d time.Duration) {
	t.Helper()
	if !b.acquireSweepSlot(context.Background()) {
		t.Fatal("could not take the sweep slot")
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(d)
		b.releaseSweepSlot()
		close(released)
	}()
	t.Cleanup(func() { <-released })
}

// TestRetainCleanupRunsWithTheBudgetLeftAfterWaitingForTheSlot pins the
// boot scrub against the sweeps it shares the bridge with.
//
// The boot scrubs run on a budget of their configured window plus a small
// margin, so any wait for the snapshot slot leaves them with less than the
// window they asked for. Opening the full window anyway spends the whole
// budget waiting and returns empty-handed — and the caller's once-guard
// means the scrub never runs again in that process. A shortened window
// sees fewer retained messages; the pass evicts exactly what it saw, so a
// short pass is strictly better than no pass.
func TestRetainCleanupRunsWithTheBudgetLeftAfterWaitingForTheSlot(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	legacy := base + "/GoOtto/HmIP-RF/0001ABCD/1/STATE"
	mc := &mockRetainClient{retained: []retainedMsg{{topic: legacy, payload: []byte(`{"value":true}`)}}}
	b := NewBridge(BridgeConfig{Base: base, RawEnabled: true}, mc)

	holdSweepSlot(t, b, 250*time.Millisecond)

	// The production shape: budget = window + a small margin, and the slot
	// is already held by another sweep, so the wait alone leaves less than
	// the requested window.
	window := 400 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), window+150*time.Millisecond)
	defer cancel()

	n, err := b.RunRetainCleanupOnce(ctx, window)
	if err != nil {
		t.Fatalf("RunRetainCleanupOnce after waiting for the slot: %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted=%d, want 1 — the scrub returned without clearing the legacy topic", n)
	}
}

// TestRetainCleanupReportsSlotBusyRatherThanADeadline pins the signal a
// caller needs to distinguish "swept, found nothing" from "never ran": a
// pass that never got the slot must be retryable, and a bare
// context.DeadlineExceeded does not say which of the two happened.
func TestRetainCleanupReportsSlotBusyRatherThanADeadline(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	mc := &mockRetainClient{}
	b := NewBridge(BridgeConfig{Base: base, RawEnabled: true}, mc)

	holdSweepSlot(t, b, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	n, err := b.RunRetainCleanupOnce(ctx, time.Second)
	if !errors.Is(err, ErrSweepSlotBusy) {
		t.Fatalf("err = %v, want ErrSweepSlotBusy", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the wrapped context cause to survive", err)
	}
	if n != 0 {
		t.Fatalf("evicted=%d, want 0", n)
	}
}

// trickleRetainClient keeps delivering retained messages on its own
// goroutine until the sweep unsubscribes, which is how a real broker
// behaves: deliveries do not stop at the window's edge, they stop when
// the subscription is taken down.
type trickleRetainClient struct {
	mockRetainClient

	stop chan struct{}
	done chan struct{}
}

func newTrickleRetainClient(topics ...string) *trickleRetainClient {
	c := &trickleRetainClient{stop: make(chan struct{}), done: make(chan struct{})}
	for _, topic := range topics {
		c.retained = append(c.retained, retainedMsg{topic: topic, payload: []byte("{}")})
	}
	return c
}

func (c *trickleRetainClient) Subscribe(_ context.Context, _ string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	go func() {
		defer close(c.done)
		for {
			select {
			case <-c.stop:
				return
			default:
			}
			for _, msg := range c.retained {
				handler(&Message{Topic: msg.topic, Payload: msg.payload, Retain: true})
			}
		}
	}()
	return SubscribeResult{}, nil
}

func (c *trickleRetainClient) Unsubscribe(_ context.Context, _ string) error {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	<-c.done
	return nil
}

// TestDiscoveryOrphanCleanupCollectsWhileTheBrokerKeepsDelivering runs
// the sweep against a broker that never stops pushing, which is the shape
// the handler's bookkeeping exists for: the window is closed by an atomic
// flag rather than by the subscription, so deliveries overlap the moment
// the sweep reads what it collected. Both the orphan list and the
// inspected counter are therefore read under the handler's mutex — this
// exercises that path end to end, under -race in the race target.
func TestDiscoveryOrphanCleanupCollectsWhileTheBrokerKeepsDelivering(t *testing.T) {
	t.Parallel()

	const central = "GoOtto"
	orphan := "homeassistant/sensor/" + naming.DiscoverySlug(central) + "_0001abcd/state/config"
	mc := newTrickleRetainClient(orphan)
	b := NewBridge(BridgeConfig{Base: "openccu-loom", CentralName: central, HADiscoveryEnabled: true}, mc)

	n, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), central, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	if n == 0 {
		t.Fatal("the sweep saw none of the retained orphans")
	}
}
