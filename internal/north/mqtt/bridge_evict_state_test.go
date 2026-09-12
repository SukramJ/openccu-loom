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

// TestEvictStateRemovesTopicFromRawIndex pins eviction as symmetric with
// retraction.
//
// rawTopics means "this topic carries a retained payload we wrote". An
// evicted topic carries nothing, so leaving its entry behind made the
// device-removal sweep retract an already-cleared topic a second time:
// a retained-message delete for a message that no longer exists,
// counted as a publish and, on a broker that refuses it, as a publish
// error. retractTopicsMatching deletes from its maps for this reason;
// EvictState now does too.
func TestEvictStateRemovesTopicFromRawIndex(t *testing.T) {
	t.Parallel()

	b, pub := newTestBridge(t)
	ctx := context.Background()

	// Publish through the canonical retained path so the topic enters the
	// index the way production puts it there.
	topic := b.dataPointStateTopic("ccu-01", "HmIP-RF", "000A", 1, "STATE")
	if err := b.publishRawRetained(ctx, topic, []byte(`{"value":true}`)); err != nil {
		t.Fatalf("publishRawRetained: %v", err)
	}
	b.mu.Lock()
	_, indexed := b.rawTopics[topic]
	b.mu.Unlock()
	if !indexed {
		t.Fatalf("precondition: %q not in rawTopics", topic)
	}

	if err := b.EvictState(ctx, "ccu-01", "HmIP-RF", "000A", 1, "STATE"); err != nil {
		t.Fatalf("EvictState: %v", err)
	}
	b.mu.Lock()
	_, stillIndexed := b.rawTopics[topic]
	b.mu.Unlock()
	if stillIndexed {
		t.Fatalf("EvictState left %q in rawTopics; device removal will retract it again", topic)
	}

	// And the second retraction must not happen.
	before := len(pub.sent)
	if n := b.RetractRawStateForDevice(ctx, "ccu-01", "HmIP-RF", "000A"); n != 3 {
		t.Fatalf("RetractRawStateForDevice cleared %d topics, want 3 (availability/info/diagnostics only)", n)
	}
	for _, s := range pub.sent[before:] {
		if s.topic == topic {
			t.Fatalf("evicted topic %q retracted a second time on device removal", topic)
		}
	}
}
