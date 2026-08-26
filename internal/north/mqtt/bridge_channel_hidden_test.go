// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"
)

// TestPublishStateSkippedForHiddenChannel verifies that an operator-hidden
// channel (G12) has its whole publish (raw + discovery) skipped, and the call
// returns nil (a hidden channel is not an error).
func TestPublishStateSkippedForHiddenChannel(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"hidden"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = true
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = db
		c.ChannelHidden = func(_, _ string) bool { return true }
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState returned unexpected error: %v", err)
	}
	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes for a hidden channel, got %d: %v", n, rec.records())
	}
}

// TestPublishStateNotSkippedForVisibleChannel verifies a ChannelHidden gate
// that reports "not hidden" lets the publish through.
func TestPublishStateNotSkippedForVisibleChannel(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"visible"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = true
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = db
		c.ChannelHidden = func(_, _ string) bool { return false }
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}
	if n := rec.countPrefix("homeassistant/"); n == 0 {
		t.Fatalf("expected a discovery publish when the channel is not hidden, got 0")
	}
}
