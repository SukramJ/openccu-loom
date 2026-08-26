// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// --- LegacyTopicBuilder ---

func TestLegacyTopicBuilderDefaultBase(t *testing.T) {
	t.Parallel()
	b := NewLegacyTopicBuilder("")
	if b.Base != "aiohomematic2mqtt" {
		t.Fatalf("expected default base %q, got %q", "aiohomematic2mqtt", b.Base)
	}
	topic := b.DataPointState("0001ABCD", 1, "STATE")
	if !strings.HasPrefix(topic, "aiohomematic2mqtt/") {
		t.Fatalf("expected aiohomematic2mqtt prefix, got %q", topic)
	}
}

func TestLegacyTopicBuilderCustomBase(t *testing.T) {
	t.Parallel()
	b := NewLegacyTopicBuilder("custom")
	if b.Base != "custom" {
		t.Fatalf("expected base %q, got %q", "custom", b.Base)
	}
	topic := b.DataPointState("0001ABCD", 1, "STATE")
	if !strings.HasPrefix(topic, "custom/") {
		t.Fatalf("expected custom/ prefix, got %q", topic)
	}
	avail := b.DeviceAvailability("0001ABCD")
	if !strings.HasPrefix(avail, "custom/") {
		t.Fatalf("expected custom/ prefix for availability, got %q", avail)
	}
}

func TestLegacyTopicBuilderTopicShapes(t *testing.T) {
	t.Parallel()
	b := NewLegacyTopicBuilder("")

	cases := []struct {
		got, want string
	}{
		{
			b.DataPointState("0001ABCD", 3, "STATE"),
			"aiohomematic2mqtt/device/status/0001ABCD/0001ABCD_3_STATE",
		},
		{
			b.DeviceAvailability("0001ABCD"),
			"aiohomematic2mqtt/device/availability/0001ABCD",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("got %q want %q", c.got, c.want)
		}
	}
}

// --- Bridge integration ---

// newLegacyTestBridge builds a bridge with RawEnabled + optional legacy alias.
func newLegacyTestBridge(t *testing.T, opts ...func(*BridgeConfig)) (*Bridge, *mockPublisher) {
	t.Helper()
	cfg := BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "c1",
		RawEnabled:  true,
	}
	for _, o := range opts {
		o(&cfg)
	}
	pub := &mockPublisher{}
	b := NewBridge(cfg, pub)
	return b, pub
}

func hasTopic(records []publishRecord, topic string) bool {
	for _, r := range records {
		if r.topic == topic {
			return true
		}
	}
	return false
}

func hasTopicPrefix(records []publishRecord, prefix string) bool {
	for _, r := range records {
		if strings.HasPrefix(r.topic, prefix) {
			return true
		}
	}
	return false
}

func payloadForTopic(records []publishRecord, topic string) (string, bool) {
	for _, r := range records {
		if r.topic == topic {
			return r.payload, true
		}
	}
	return "", false
}

// TestBridgeLegacyDisabledByDefault verifies that without LegacyAlias configured
// No... topic is published. The primary per-DP state topic
// is emitted via PublishSlotState (not PublishState — which now only publishes
// HA-Discovery and legacy mirrors).
func TestBridgeLegacyDisabledByDefault(t *testing.T) {
	t.Parallel()
	b, pub := newLegacyTestBridge(t)
	err := b.PublishState(context.Background(), Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
	if err != nil {
		t.Fatalf("PublishState: %v", err)
	}
	pub.mu.Lock()
	records := pub.sent
	pub.mu.Unlock()
	if hasTopicPrefix(records, "aiohomematic2mqtt/") {
		t.Fatal("expected no aiohomematic2mqtt topic, but one was published")
	}
	// PublishState no longer writes the raw per-DP state topic; that moved
	// to PublishSlotState. Verify the canonical new-shape topic also does
	// NOT appear (it hasn't been published yet — only slot publishes do that).
	if hasTopic(records, "openccu-loom/c1/HmIP-RF/0001ABCD/1/values/STATE") {
		t.Fatal("PublishState must not write raw per-DP state; use PublishSlotState")
	}
}

// TestBridgeLegacyEnabledMirrorsState verifies that the legacy alias mirror
// is published when LegacyAlias is enabled. The canonical primary per-DP
// state topic is published by PublishSlotState; PublishState publishes only
// the legacy alias mirror.
func TestBridgeLegacyEnabledMirrorsState(t *testing.T) {
	t.Parallel()
	b, pub := newLegacyTestBridge(t, func(c *BridgeConfig) {
		c.LegacyAlias = LegacyAliasConfig{Enabled: true}
	})

	// Publish primary via PublishSlotState (canonical path).
	slot := pload.TopicSlot{
		Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE",
	}
	if err := b.PublishSlotState(context.Background(), "c1", "HmIP-RF", slot, pload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}
	// Trigger legacy mirror via PublishState.
	if err := b.PublishState(context.Background(), Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	pub.mu.Lock()
	records := pub.sent
	pub.mu.Unlock()

	primaryTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/1/values/STATE"
	legacyTopic := "aiohomematic2mqtt/device/status/0001ABCD/0001ABCD_1_STATE"

	if !hasTopic(records, primaryTopic) {
		t.Fatalf("primary topic %q not published; got: %v", primaryTopic, records)
	}
	legacyPayload, ok := payloadForTopic(records, legacyTopic)
	if !ok {
		t.Fatalf("legacy topic %q not published; got: %v", legacyTopic, records)
	}
	// The legacy mirror now carries the same JSON envelope as the
	// canonical slot-state topic (ADR-0011 payload unification).
	if !strings.Contains(legacyPayload, `"value":true`) {
		t.Fatalf("expected legacy payload to contain value:true, got %q", legacyPayload)
	}
}

// TestBridgeLegacyEnabledMirrorsAvailability verifies that both primary and
// legacy availability topics are published when the alias is enabled.
func TestBridgeLegacyEnabledMirrorsAvailability(t *testing.T) {
	t.Parallel()
	b, pub := newLegacyTestBridge(t, func(c *BridgeConfig) {
		c.LegacyAlias = LegacyAliasConfig{Enabled: true}
	})
	err := b.PublishAvailability(context.Background(), "c1", "HmIP-RF", "0001ABCD", true)
	if err != nil {
		t.Fatalf("PublishAvailability: %v", err)
	}
	pub.mu.Lock()
	records := pub.sent
	pub.mu.Unlock()

	primaryTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/availability"
	legacyTopic := "aiohomematic2mqtt/device/availability/0001ABCD"

	primaryPayload, ok := payloadForTopic(records, primaryTopic)
	if !ok {
		t.Fatalf("primary topic %q not published; got: %v", primaryTopic, records)
	}
	legacyPayload, ok := payloadForTopic(records, legacyTopic)
	if !ok {
		t.Fatalf("legacy topic %q not published; got: %v", legacyTopic, records)
	}
	if primaryPayload != "online" {
		t.Fatalf("expected primary payload %q, got %q", "online", primaryPayload)
	}
	if legacyPayload != "online" {
		t.Fatalf("expected legacy payload %q, got %q", "online", legacyPayload)
	}
}

// selectivePublisher wraps mockPublisher and returns an error for topics
// matching a given prefix, simulating a broker failure only for legacy topics.
type selectivePublisher struct {
	inner     *mockPublisher
	failOnce  string // topic prefix that triggers one error
	failCount int
}

func (s *selectivePublisher) Publish(ctx context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	if strings.HasPrefix(topic, s.failOnce) {
		s.failCount++
		return errSelectiveFail
	}
	return s.inner.Publish(ctx, topic, payload, qos, retain)
}

type selectiveError struct{ msg string }

func (e *selectiveError) Error() string { return e.msg }

var errSelectiveFail = &selectiveError{"simulated legacy broker failure"}

// TestBridgeLegacyMirrorErrorIsBestEffort verifies that a failure on the
// legacy topic does not propagate — PublishState returns nil. The primary
// per-DP state topic is emitted by PublishSlotState; PublishState only
// attempts the legacy mirror when LegacyAlias is enabled.
func TestBridgeLegacyMirrorErrorIsBestEffort(t *testing.T) {
	t.Parallel()
	inner := &mockPublisher{}
	sel := &selectivePublisher{inner: inner, failOnce: "aiohomematic2mqtt/"}

	b := NewBridge(BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "c1",
		RawEnabled:  true,
		LegacyAlias: LegacyAliasConfig{Enabled: true},
	}, sel)

	err := b.PublishState(context.Background(), Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
	if err != nil {
		t.Fatalf("expected nil error (best-effort), got %v", err)
	}
	// Verify the legacy publish was attempted and failed (best-effort semantics).
	if sel.failCount == 0 {
		t.Fatal("expected legacy publish to be attempted (and fail), but it was never tried")
	}

	// Publish primary via PublishSlotState and verify it reaches the broker.
	slot := pload.TopicSlot{
		Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE",
	}
	if err2 := b.PublishSlotState(context.Background(), "c1", "HmIP-RF", slot, pload.PerDPState{Value: true, Available: true}); err2 != nil {
		t.Fatalf("PublishSlotState: %v", err2)
	}
	inner.mu.Lock()
	records := inner.sent
	inner.mu.Unlock()
	primaryTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/1/values/STATE"
	if !hasTopic(records, primaryTopic) {
		t.Fatalf("primary topic %q not published; got: %v", primaryTopic, records)
	}
}

// TestBridgeLegacyCustomBase verifies that a custom LegacyAlias.Base is used
// for the mirrored topic.
func TestBridgeLegacyCustomBase(t *testing.T) {
	t.Parallel()
	b, pub := newLegacyTestBridge(t, func(c *BridgeConfig) {
		c.LegacyAlias = LegacyAliasConfig{Enabled: true, Base: "compat"}
	})
	err := b.PublishState(context.Background(), Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
	if err != nil {
		t.Fatalf("PublishState: %v", err)
	}
	pub.mu.Lock()
	records := pub.sent
	pub.mu.Unlock()

	legacyTopic := "compat/device/status/0001ABCD/0001ABCD_1_STATE"
	if !hasTopic(records, legacyTopic) {
		t.Fatalf("expected custom-base legacy topic %q; got: %v", legacyTopic, records)
	}
	if hasTopicPrefix(records, "aiohomematic2mqtt/") {
		t.Fatal("default base aiohomematic2mqtt/ should not appear when custom base is set")
	}
}
