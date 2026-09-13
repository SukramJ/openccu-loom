// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// TestSuppressedRuntimeStateIsIndexedButNotCounted pins the split in
// [Bridge.publishRuntimeState] that an adversarial review found untested:
// the retained-topic index is written on every accepted call, while
// `messages_sent` is written only when the dedup gate actually let bytes
// out.
//
// Both halves matter, and they fail in opposite directions, which is why
// the review's mutations of each half AND of the swap all survived the
// suite — no test asked either question.
//
//   - Index the topic only when the gate published, and a topic whose
//     republish was suppressed drops out of the index while the broker
//     still holds its bytes. Nothing would then retract it: the device
//     removal sweep walks that index, so the entity survives its device
//     as a retained ghost.
//   - Count a suppressed publish, and `messages_sent` stops describing
//     traffic and starts describing intent — an operator reading it to
//     size broker load, or to notice that a plane went quiet, is reading
//     a number that no longer moves with the wire.
//
// One honest limit, measured rather than assumed. Of the review's three
// mutations this test kills two — "count always" and the swap. The third,
// "index only when sent", still survives, and that is a property of the
// code rather than a gap here: the first write for a topic is never
// suppressed (the gate is empty), so the topic is already indexed by the
// time any suppression can happen, and re-indexing it is a no-op. The
// unconditional call is defensive, not load-bearing, and the doc comment
// on publishRuntimeState justifies it with a case that cannot currently
// arise. Reaching it would need a suppressed publish for a topic absent
// from the index — which requires the index and the gate to disagree.
// Nothing in this package calls StatePublisher.Forget, and
// retractTopicsMatching deletes from the index while leaving the gate
// holding the payload it just cleared, so the two CAN diverge; whether
// any live path reaches that state is unestablished, and it is recorded
// as a suspicion rather than pinned as behaviour.
func TestSuppressedRuntimeStateIsIndexedButNotCounted(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	pub := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu-01", RawEnabled: true, Collector: col,
	}, pub)

	ctx := context.Background()
	const topic = "gh/ccu-01/hub/sysvars/split_guard/state"
	body := []byte(`{"value":1}`)

	// First write: the gate is empty, so the bytes go out.
	if err := b.publishRuntimeState(ctx, "ccu-01", topic, body); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if got := col.MessagesSent("ccu-01").Value(); got != 1 {
		t.Fatalf("after the first publish messages_sent = %d, want 1", got)
	}
	b.mu.Lock()
	_, indexed := b.rawTopics[topic]
	b.mu.Unlock()
	if !indexed {
		t.Fatal("the published topic is not in the retained-topic index")
	}

	// Second write, byte-identical: the gate suppresses it.
	if err := b.publishRuntimeState(ctx, "ccu-01", topic, body); err != nil {
		t.Fatalf("second publish: %v", err)
	}

	if got := col.MessagesSent("ccu-01").Value(); got != 1 {
		t.Errorf("messages_sent = %d after a suppressed publish, want 1: "+
			"the gate sent no bytes, so the counter must not move", got)
	}
	b.mu.Lock()
	_, stillIndexed := b.rawTopics[topic]
	b.mu.Unlock()
	if !stillIndexed {
		t.Error("a suppressed publish dropped the topic from the retained-topic index: " +
			"the broker still holds the bytes, so nothing would ever retract them")
	}

	// And the suppression is real rather than an artefact of the fixture:
	// the publisher saw exactly one message on that topic.
	seen := 0
	pub.mu.Lock()
	for _, r := range pub.sent {
		if r.topic == topic {
			seen++
		}
	}
	pub.mu.Unlock()
	if seen != 1 {
		t.Errorf("publisher saw %d messages on %q, want 1 — "+
			"without suppression this test proves nothing", seen, topic)
	}
}
