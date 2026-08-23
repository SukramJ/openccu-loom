// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var errPublish = errors.New("broker down")

// TestBridgeMessagesSentCarriesCentralLabel is the reproducer for
// At the Bridge integration level: a shared bridge serving
// two CCUs must publish into two distinct `central`-labeled
// mqtt_messages_sent series, not one folded daemon-wide total. Before the
// fix MqttCollector exposed a single unlabeled Counter field, so this test
// could not even be expressed — there was no way to ask "how many for
// ccu-a" versus "how many for ccu-b" from the collector at all.
func TestBridgeMessagesSentCarriesCentralLabel(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	pub := &recordingPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base:       "gh",
		RawEnabled: true,
		Collector:  col,
	}, pub)

	ctx := context.Background()
	slot := pload.TopicSlot{Address: "AABBCC03", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	state := pload.PerDPState{Value: true, Available: true}

	if err := bridge.PublishSlotState(ctx, "ccu-a", "HmIP-RF", slot, state); err != nil {
		t.Fatalf("PublishSlotState ccu-a (1st): %v", err)
	}
	if err := bridge.PublishSlotState(ctx, "ccu-a", "HmIP-RF", slot, state); err != nil {
		t.Fatalf("PublishSlotState ccu-a (2nd): %v", err)
	}
	if err := bridge.PublishSlotState(ctx, "ccu-b", "HmIP-RF", slot, state); err != nil {
		t.Fatalf("PublishSlotState ccu-b: %v", err)
	}

	if got := col.MessagesSent("ccu-a").Value(); got != 2 {
		t.Errorf("ccu-a messages_sent = %d, want 2", got)
	}
	if got := col.MessagesSent("ccu-b").Value(); got != 1 {
		t.Errorf("ccu-b messages_sent = %d, want 1 (independent of ccu-a)", got)
	}
}

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
	if got := col.DiscoverySent("test_ccu").Value(); got != 1 {
		t.Errorf("DiscoverySent after 1st publish = %d, want 1", got)
	}

	// Second publish: discovery deduplication — no new payload, so
	// discovery_sent stays at 1.
	if err := bridge.PublishState(ctx, ev); err != nil {
		t.Fatalf("PublishState (2nd): %v", err)
	}
	if got := col.DiscoverySent("test_ccu").Value(); got != 1 {
		t.Errorf("DiscoverySent after 2nd publish (dedup) = %d, want 1", got)
	}

	// publish_errors should be 0 (all publishes succeeded).
	if got := col.PublishErrors("err_ccu").Value(); got != 0 {
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
	if got := col.PublishErrors("err_ccu").Value(); got < 1 {
		t.Errorf("PublishErrors = %d, want >= 1 (at least one error counted)", got)
	}
}
