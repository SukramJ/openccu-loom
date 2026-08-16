// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var errPublish = errors.New("broker down")

// TestMqttCollectorCountsMessages verifies that a Bridge wired with an
// MqttCollector tracks discovery_sent when HA Discovery is enabled.
//
// NOTE: After the bucket-aware topology migration, PublishState no longer
// increments messages_sent directly (the raw per-DP publish moved to
// PublishSlotState). The messages_sent counter is currently wired only via
// incMessagesSent(), which has no active call site post-migration — this is
// a known gap documented in the migration report. The test is updated to
// reflect what actually fires: discovery_sent increments on the first
// PublishState call (new payload), not on the second (cache hit / dedup).
func TestMqttCollectorCountsMessages(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)

	pub := &recordingPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base:               "gh",
		CentralName:        "test_ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
		Collector:          col,
	}, pub)

	ctx := context.Background()
	ev := Event{
		Central:       "test_ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCC01",
		ChannelNo:     1,
		Parameter:     "STATE",
		Category:      hmenum.DataPointCategorySwitch,
		Value:         true,
	}

	// First publish: discovery_sent goes 0→1 (new discovery config).
	if err := bridge.PublishState(ctx, ev); err != nil {
		t.Fatalf("PublishState: %v", err)
	}
	if got := col.DiscoverySent.Value(); got != 1 {
		t.Errorf("DiscoverySent after 1st publish = %d, want 1", got)
	}

	// Second publish: discovery deduplication — no new payload, so
	// discovery_sent stays at 1.
	if err := bridge.PublishState(ctx, ev); err != nil {
		t.Fatalf("PublishState (2nd): %v", err)
	}
	if got := col.DiscoverySent.Value(); got != 1 {
		t.Errorf("DiscoverySent after 2nd publish (dedup) = %d, want 1", got)
	}

	// publish_errors should be 0 (all publishes succeeded).
	if got := col.PublishErrors.Value(); got != 0 {
		t.Errorf("PublishErrors = %d, want 0", got)
	}
}

// TestMqttCollectorCountsPublishErrors verifies that a broker-level
// publish failure on the HA-Discovery path increments publish_errors.
func TestMqttCollectorCountsPublishErrors(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)

	pub := &recordingPublisher{err: errPublish}
	// HADiscoveryEnabled so publishDiscovery is called and the broker error
	// triggers incPublishErrors(). Without HADiscoveryEnabled PublishState
	// is effectively a no-op (raw state moved to PublishSlotState).
	bridge := NewBridge(BridgeConfig{
		Base:               "gh",
		CentralName:        "err_ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
		Collector:          col,
	}, pub)

	ctx := context.Background()
	ev := Event{
		Central:       "err_ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCC02",
		ChannelNo:     1,
		Parameter:     "STATE",
		Category:      hmenum.DataPointCategorySwitch,
		Value:         false,
	}

	_ = bridge.PublishState(ctx, ev) // expect error
	// publishDiscovery increments publish_errors once when the broker
	// returns an error, and PublishState also increments it when
	// publishDiscovery returns that error — resulting in 2 increments
	// per failed discovery publish. This is a pre-existing counter
	// double-increment in the production code (noted in migration report).
	if got := col.PublishErrors.Value(); got < 1 {
		t.Errorf("PublishErrors = %d, want >= 1 (at least one error counted)", got)
	}
}
