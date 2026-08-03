// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLegacyAggregateStateMatcherPositiveCases pins what counts as a
// legacy aggregate-state topic — the shape ADR 0011 phase 1b retired.
func TestLegacyAggregateStateMatcherPositiveCases(t *testing.T) {
	t.Parallel()
	base := "openccu-loom"
	hits := []string{
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/0/state",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/state",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/9/state",
		"openccu-loom/ccu-01/CUxD/JEQ0123456/12/state",
	}
	for _, topic := range hits {
		if !LegacyAggregateStateMatcher(base, topic) {
			t.Errorf("expected match: %q", topic)
		}
	}
}

// TestLegacyAggregateStateMatcherNegativeCases pins what must NOT
// match — most importantly the new per-DP topics introduced in
// phase 1b. Wiping any of these would destroy live state.
func TestLegacyAggregateStateMatcherNegativeCases(t *testing.T) {
	t.Parallel()
	base := "openccu-loom"
	misses := []string{
		// New per-DP topology — must NOT match.
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/values/ACTUAL_TEMPERATURE/state",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/master/TEMPERATURE_MINIMUM/state",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/custom/climate/state",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/channels/1/calculated/DEW_POINT/state",
		// Per-device shapes that don't end in /state.
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/availability",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/info",
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/diagnostics",
		// Bridge-level — different prefix depth.
		"openccu-loom/bridge/status",
		// Hub-level.
		"openccu-loom/GoOtto/sysvars/Anwesenheit",
		"openccu-loom/GoOtto/sysvars/Anwesenheit/set",
		// Wrong base.
		"otherbase/GoOtto/HmIP-RF/000C9709AEF157/0/state",
		// Channel segment that's not numeric.
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/foo/state",
		// Empty channel.
		"openccu-loom/GoOtto/HmIP-RF/000C9709AEF157//state",
	}
	for _, topic := range misses {
		if LegacyAggregateStateMatcher(base, topic) {
			t.Errorf("must NOT match: %q (would destroy live data)", topic)
		}
	}
}

// ---------------------------------------------------------------------------
// RetainCleanup integration-style test with mock MQTT client.
// ---------------------------------------------------------------------------

// mockRetainClient is a mock [Client] that returns a pre-configured set of
// retained messages to any Subscribe call and records every Publish call.
// It simulates the broker's retained-message delivery within a short window.
type mockRetainClient struct {
	mu       sync.Mutex
	retained []retainedMsg // messages the "broker" will deliver on subscribe

	published []publishedMsg // all Publish calls recorded
}

type retainedMsg struct {
	topic   string
	payload []byte
}

type publishedMsg struct {
	topic   string
	payload []byte
	retain  bool
}

func (m *mockRetainClient) Publish(_ context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, publishedMsg{topic: topic, payload: payload, retain: retain})
	return nil
}

func (m *mockRetainClient) Subscribe(_ context.Context, _ string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	// Deliver retained messages synchronously — the cleanup logic waits
	// with a timer, so we deliver before RunRetainCleanupOnce's snapshot
	// window expires.
	m.mu.Lock()
	retained := make([]retainedMsg, len(m.retained))
	copy(retained, m.retained)
	m.mu.Unlock()
	for _, msg := range retained {
		handler(&Message{Topic: msg.topic, Payload: msg.payload, Retain: true})
	}
	return SubscribeResult{}, nil
}

func (m *mockRetainClient) Unsubscribe(_ context.Context, _ string) error {
	return nil
}

func (m *mockRetainClient) evicted() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, p := range m.published {
		if p.retain && len(p.payload) == 0 {
			out = append(out, p.topic)
		}
	}
	return out
}

// TestRunRetainCleanupOnce_EvictsLegacyTopics verifies the full end-to-end
// cleanup flow: legacy retained topics are identified and cleared with
// empty retain=true publishes; current-schema topics are left untouched.
func TestRunRetainCleanupOnce_EvictsLegacyTopics(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"

	legacyTopics := [...]string{
		// Bucket-less per-DP (old phase 1a shape).
		base + "/GoOtto/HmIP-RF/0001ABCD/1/STATE",
		base + "/GoOtto/HmIP-RF/0001ABCD/1/ACTUAL_TEMPERATURE",
		// Aggregate channel state (old phase 1b shape).
		base + "/GoOtto/HmIP-RF/0001ABCD/0/state",
		// Legacy channels/-infix slot shape.
		base + "/GoOtto/HmIP-RF/0001ABCD/channels/1/values/STATE/state",
	}
	currentTopics := [...]string{
		// Current shape — must NOT be evicted.
		base + "/GoOtto/HmIP-RF/0001ABCD/1/values/STATE",
		base + "/GoOtto/HmIP-RF/0001ABCD/availability",
		base + "/bridge/status",
	}

	allMessages := make([]retainedMsg, 0, len(legacyTopics)+len(currentTopics))
	for _, t := range legacyTopics {
		allMessages = append(allMessages, retainedMsg{topic: t, payload: []byte(`{"value":true}`)})
	}
	for _, t := range currentTopics {
		allMessages = append(allMessages, retainedMsg{topic: t, payload: []byte(`{"value":true}`)})
	}

	mc := &mockRetainClient{retained: allMessages}
	b := NewBridge(BridgeConfig{
		Base:       base,
		RawEnabled: true,
	}, mc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	n, err := b.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunRetainCleanupOnce: unexpected error: %v", err)
	}
	if n != len(legacyTopics) {
		t.Errorf("evicted=%d want %d", n, len(legacyTopics))
	}

	evicted := mc.evicted()
	// Every legacy topic must appear in the eviction list.
	evictedSet := make(map[string]bool, len(evicted))
	for _, e := range evicted {
		evictedSet[e] = true
	}
	for _, lt := range legacyTopics {
		if !evictedSet[lt] {
			t.Errorf("legacy topic %q was NOT evicted", lt)
		}
	}
	// Current-schema topics must NOT be evicted.
	for _, ct := range currentTopics {
		if evictedSet[ct] {
			t.Errorf("current-schema topic %q was incorrectly evicted", ct)
		}
	}
}

// TestRunRetainCleanupOnce_RawDisabledSkips verifies that the cleanup
// is skipped when RawEnabled=false (non-raw deployments have no
// legacy raw topics to clean up).
func TestRunRetainCleanupOnce_RawDisabledSkips(t *testing.T) {
	t.Parallel()
	mc := &mockRetainClient{
		retained: []retainedMsg{
			{topic: "openccu-loom/GoOtto/HmIP-RF/0001ABCD/0/state", payload: []byte(`{}`)},
		},
	}
	b := NewBridge(BridgeConfig{Base: "openccu-loom", RawEnabled: false}, mc)
	ctx := context.Background()
	n, err := b.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("evicted=%d want 0 when RawEnabled=false", n)
	}
	if len(mc.evicted()) != 0 {
		t.Errorf("no publishes expected when RawEnabled=false; got %v", mc.evicted())
	}
}

// TestDaemonLevelNodeIDs_CoversBothPlanes locks the orphan sweep's
// escape hatch for the two daemon-level discovery planes.
//
// [Bridge.RunDiscoveryOrphanCleanupOnce] otherwise scopes every retained
// discovery config to the `<central>_` node prefix; the alarm engine and
// the Security & Safety domain deliberately do not carry that prefix
// (ADR 0052), so without an entry here a retracted zone panel or a
// class that lost its last source would be treated as belonging to some
// other integration and would keep its retained discovery config alive
// in every consumer forever — no cleanup pass could ever reach it.
//
// This check lives in-package rather than in tests/contract: both
// daemonLevelNodeIDs and the node ids it must contain are unexported, so
// an external package cannot name them.
func TestDaemonLevelNodeIDs_CoversBothPlanes(t *testing.T) {
	t.Parallel()
	if len(daemonLevelNodeIDs) == 0 {
		t.Fatal("daemonLevelNodeIDs is empty — the orphan sweep would treat every daemon-level discovery config as belonging to another integration")
	}
	for _, nodeID := range []string{alarmDiscoveryNodeID, securityDiscoveryNodeID} {
		if !daemonLevelNodeIDs[nodeID] {
			t.Errorf("daemonLevelNodeIDs is missing %q", nodeID)
		}
	}
}
