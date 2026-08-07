// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
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
		// Reserved hub subtree with a numeric third segment — looks
		// like `<iface>/<addr>/<channel>/state` but is live hub state.
		"openccu-loom/GoOtto/hub/programs/12459/state",
		"openccu-loom/GoOtto/hub/sysvars/123/state",
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
// TestProgramTriggerMirrorMatcher pins the eviction scope for the
// retired program-trigger state mirror: exactly the command-topic
// shape, never the program's state plane.
func TestProgramTriggerMirrorMatcher(t *testing.T) {
	t.Parallel()
	base := "openccu-loom"
	hits := []string{
		"openccu-loom/GoOtto/hub/programs/12459/trigger",
		"openccu-loom/ccu-01/hub/programs/prgEnergyCounter_1/trigger",
	}
	for _, topic := range hits {
		if !ProgramTriggerMirrorMatcher(base, topic) {
			t.Errorf("expected match: %q", topic)
		}
	}
	misses := []string{
		// The program state plane — wiping it would blank live state.
		"openccu-loom/GoOtto/hub/programs/12459/state",
		"openccu-loom/GoOtto/hub/programs/12459/execute_available",
		// The activation command topic never carried a mirror.
		"openccu-loom/GoOtto/hub/programs/12459/set",
		// Wrong depth / wrong subtree.
		"openccu-loom/GoOtto/hub/programs/trigger",
		"openccu-loom/GoOtto/hub/sysvars/Anwesenheit/trigger",
		"openccu-loom/GoOtto/programs/12459/trigger",
		// Empty segments.
		"openccu-loom/GoOtto/hub/programs//trigger",
		"openccu-loom//hub/programs/12459/trigger",
		// Wrong base.
		"otherbase/GoOtto/hub/programs/12459/trigger",
	}
	for _, topic := range misses {
		if ProgramTriggerMirrorMatcher(base, topic) {
			t.Errorf("must not match: %q", topic)
		}
	}
}

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
		// Retired program-trigger state mirror parked on the command topic.
		base + "/GoOtto/hub/programs/12459/trigger",
	}
	currentTopics := [...]string{
		// Current shape — must NOT be evicted.
		base + "/GoOtto/HmIP-RF/0001ABCD/1/values/STATE",
		base + "/GoOtto/HmIP-RF/0001ABCD/availability",
		base + "/bridge/status",
		// Program state plane — must NOT be evicted.
		base + "/GoOtto/hub/programs/12459/state",
		base + "/GoOtto/hub/programs/12459/execute_available",
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

// TestRunRetainCleanupOnce_ProductionShape_BreakerWrappedPublisher pins
// the composition-root wiring: production hands the bridge a
// publish-only circuit-breaker decorator, so the subscribe capability
// the cleanup passes need must arrive via [Bridge.WithSubscriber]. This
// exact shape failed silently on every boot before the seam existed —
// the type assertion on the breaker never succeeded and no retained
// topic was ever evicted.
func TestRunRetainCleanupOnce_ProductionShape_BreakerWrappedPublisher(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	mc := &mockRetainClient{retained: []retainedMsg{
		{topic: base + "/GoOtto/hub/programs/12459/trigger", payload: []byte("true")},
	}}
	pub := NewBreaker(mc, BreakerConfig{})
	b := NewBridge(BridgeConfig{Base: base, RawEnabled: true}, pub).WithSubscriber(mc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := b.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunRetainCleanupOnce with breaker-wrapped publisher: %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted=%d want 1", n)
	}

	// Without the wired subscriber the breaker-wrapped bridge must
	// surface the capability error — the silent-no-op regression shape.
	b2 := NewBridge(BridgeConfig{Base: base, RawEnabled: true}, pub)
	if _, err := b2.RunRetainCleanupOnce(ctx, 50*time.Millisecond); !errors.Is(err, ErrCleanupNeedsSubscriber) {
		t.Fatalf("expected ErrCleanupNeedsSubscriber, got %v", err)
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

// TestDaemonLevelPlaneIsNotSweptBeforeItDeclares pins the gate that
// keeps the orphan sweep from deleting a plane's entities before that
// plane has published anything.
//
// The sweep runs during southbound bring-up, hundreds of lines before
// the security plane is constructed and long before the domain starts.
// At that moment none of its topics are in `declared`, so every one of
// its retained configs looked like an orphan — and with the domain not
// yet running, nothing re-declared them. The entities vanished from the
// consumer on every restart, taking every automation and dashboard card
// built on them.
func TestDaemonLevelPlaneIsNotSweptBeforeItDeclares(t *testing.T) {
	t.Parallel()
	b := &Bridge{}

	if b.planeDeclared(securityDiscoveryNodeID) {
		t.Fatal("a plane counts as declared before it published anything; the sweep would delete its entities on every restart")
	}
	b.MarkPlaneDeclared(securityDiscoveryNodeID)
	if !b.planeDeclared(securityDiscoveryNodeID) {
		t.Fatal("MarkPlaneDeclared did not take effect; the plane's orphans could never be swept")
	}
	// Marking one plane must not enable the sweep for the other.
	if b.planeDeclared(alarmDiscoveryNodeID) {
		t.Error("marking the security plane also enabled the alarm plane")
	}
}

// TestRawOrphanCandidateMatcher pins the sweep scope for the current per-DP
// bucket shape: exactly `<central>/<iface>/<addr>/<ch>/<bucket>/<PARAM>`
// (plus the `/config` companion), never the hub subtree, device-level topics,
// or another central's namespace.
func TestRawOrphanCandidateMatcher(t *testing.T) {
	t.Parallel()
	base, central := "openccu-loom", "GoOtto"
	hits := []string{
		base + "/GoOtto/HmIP-RF/0001ABCD/8/master/01_WP_WEEKDAY",
		base + "/GoOtto/HmIP-RF/0001ABCD/8/master/01_WP_WEEKDAY/config",
		base + "/GoOtto/HmIP-RF/0001ABCD/0/values/BOOTED",
		base + "/GoOtto/HmIP-RF/0001ABCD/1/calculated/DEW_POINT",
		base + "/GoOtto/HmIP-RF/0001ABCD/3/custom/switch",
	}
	for _, topic := range hits {
		if !RawOrphanCandidateMatcher(base, central, topic) {
			t.Errorf("expected match: %q", topic)
		}
	}
	misses := []string{
		// Device-level retained topics stay.
		base + "/GoOtto/HmIP-RF/0001ABCD/availability",
		base + "/GoOtto/HmIP-RF/0001ABCD/info",
		// Hub subtree stays.
		base + "/GoOtto/hub/sysvars/Anwesenheit/state",
		// Another central's namespace stays.
		base + "/Office/HmIP-RF/0001ABCD/8/master/01_WP_WEEKDAY",
		// Legacy shapes are the legacy pass's business.
		base + "/GoOtto/HmIP-RF/0001ABCD/1/STATE",
		base + "/GoOtto/HmIP-RF/0001ABCD/0/state",
		// Non-numeric channel / unknown bucket.
		base + "/GoOtto/HmIP-RF/0001ABCD/x/master/PARAM",
		base + "/GoOtto/HmIP-RF/0001ABCD/1/paramset/PARAM",
	}
	for _, topic := range misses {
		if RawOrphanCandidateMatcher(base, central, topic) {
			t.Errorf("must not match: %q", topic)
		}
	}
}

// TestRunRawOrphanCleanupOnce_EvictsUnpublishedBucketTopics verifies the
// raw-plane orphan sweep end to end: retained per-DP bucket topics the
// current boot did not (re)publish are evicted; topics present in the
// bridge's rawTopics / configCache bookkeeping survive.
func TestRunRawOrphanCleanupOnce_EvictsUnpublishedBucketTopics(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	keepState := base + "/GoOtto/HmIP-RF/0001ABCD/1/values/STATE"
	keepConfig := base + "/GoOtto/HmIP-RF/0001ABCD/1/values/STATE/config"
	orphans := []string{
		base + "/GoOtto/HmIP-RF/0001ABCD/8/master/01_WP_WEEKDAY",
		base + "/GoOtto/HmIP-RF/0001ABCD/8/master/01_WP_WEEKDAY/config",
		base + "/GoOtto/HmIP-RF/0001ABCD/0/values/BOOTED",
	}
	untouched := []string{
		base + "/GoOtto/HmIP-RF/0001ABCD/availability",
		base + "/GoOtto/hub/sysvars/Anwesenheit/state",
	}

	retained := make([]retainedMsg, 0, 2+len(orphans)+len(untouched))
	for _, topic := range append(append([]string{keepState, keepConfig}, orphans...), untouched...) {
		retained = append(retained, retainedMsg{topic: topic, payload: []byte(`{"value":1}`)})
	}
	mc := &mockRetainClient{retained: retained}
	b := NewBridge(BridgeConfig{Base: base, CentralName: "GoOtto", RawEnabled: true}, mc)
	// Simulate the snapshot pass's bookkeeping for the topics the current
	// model still publishes.
	b.rememberRawTopic(keepState)
	b.mu.Lock()
	b.configCache[keepConfig] = []byte(`{"unit":"x"}`)
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := b.RunRawOrphanCleanupOnce(ctx, "GoOtto", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunRawOrphanCleanupOnce: %v", err)
	}
	if n != len(orphans) {
		t.Errorf("evicted=%d want %d", n, len(orphans))
	}
	evictedSet := make(map[string]bool)
	for _, e := range mc.evicted() {
		evictedSet[e] = true
	}
	for _, topic := range orphans {
		if !evictedSet[topic] {
			t.Errorf("orphan %q was NOT evicted", topic)
		}
	}
	for _, topic := range append([]string{keepState, keepConfig}, untouched...) {
		if evictedSet[topic] {
			t.Errorf("topic %q was incorrectly evicted", topic)
		}
	}
}
