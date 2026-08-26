// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"
)

// TestEvictStatePublishesEmptyRetainedPayload verifies that EvictState
// publishes an empty payload (len==0) with retain=true to the canonical
// raw-plane state topic. This is the MQTT specification's mechanism for
// deleting a retained message, and is the expected signal for HA to
// clear a stale entity state.
func TestEvictStatePublishesEmptyRetainedPayload(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t)
	err := b.EvictState(context.Background(), "ccu-01", "HmIP-RF", "000A", 1, "STATE")
	if err != nil {
		t.Fatalf("EvictState: %v", err)
	}

	if len(pub.sent) == 0 {
		t.Fatal("expected at least one publish from EvictState")
	}
	rec := pub.sent[0]

	wantTopic := "openccu-loom/ccu-01/HmIP-RF/000A/1/values/STATE"
	if rec.topic != wantTopic {
		t.Fatalf("topic = %q, want %q", rec.topic, wantTopic)
	}
	if rec.payload != "" {
		t.Fatalf("payload = %q, want empty (eviction)", rec.payload)
	}
	if !rec.retain {
		t.Fatalf("retain = false, want true (eviction requires retained=true)")
	}
}

// TestEvictStateNoopWhenRawDisabled verifies that EvictState is a
// no-op when the raw plane is disabled, matching the behaviour of
// PublishState so callers don't need separate guards.
func TestEvictStateNoopWhenRawDisabled(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t, func(c *BridgeConfig) {
		c.RawEnabled = false
	})
	err := b.EvictState(context.Background(), "ccu-01", "HmIP-RF", "000A", 1, "STATE")
	if err != nil {
		t.Fatalf("EvictState with raw disabled: %v", err)
	}
	if len(pub.sent) != 0 {
		t.Fatalf("expected no publishes when raw plane disabled, got %v", pub.sent)
	}
}

// TestEvictStateUsesDefaultCentralWhenEmpty verifies that EvictState
// falls back to the bridge's configured central name when an empty
// central is passed — matching the behaviour of PublishState's
// centralName helper.
func TestEvictStateUsesDefaultCentralWhenEmpty(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t) // bridge configured with central "ccu-01"
	err := b.EvictState(context.Background(), "" /* empty → use default */, "HmIP-RF", "000A", 1, "STATE")
	if err != nil {
		t.Fatalf("EvictState: %v", err)
	}
	if len(pub.sent) == 0 {
		t.Fatal("expected at least one publish")
	}
	wantTopic := "openccu-loom/ccu-01/HmIP-RF/000A/1/values/STATE"
	if pub.sent[0].topic != wantTopic {
		t.Fatalf("topic = %q, want %q (default central must be resolved)", pub.sent[0].topic, wantTopic)
	}
}
