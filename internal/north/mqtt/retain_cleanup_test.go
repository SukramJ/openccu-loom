// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
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

// TestRunDiscoveryOrphanCleanupOnce_NonSlugCentralName pins the sweep's
// scoping prefix against the spellings its own producers put on the
// wire.
//
// Node ids are slugged (`naming.DiscoverySlug`): `CCU Küche` becomes
// `ccu_kueche`. The sweep used to rebuild the prefix by hand with
// `strings.ToLower`, yielding `ccu küche_`, which matches nothing — so
// the once-per-boot cleanup was a complete no-op for every central
// whose name is not already a bare ASCII slug, and retired entities
// kept being re-created by Home Assistant on every restart.
//
// The legacy row covers the configs an older build retained under
// `strings.ToLower(naming.TopicSafe(name))`; they must stay reachable
// so they can be swept once.
func TestRunDiscoveryOrphanCleanupOnce_NonSlugCentralName(t *testing.T) {
	t.Parallel()
	const centralName = "CCU Küche"
	var (
		deviceOrphan = "homeassistant/switch/" + naming.DiscoverySlug(centralName) + "_0001d3c99c1234/state/config"
		hubOrphan    = "homeassistant/sensor/" + hubNodeID(centralName, "system") + "/connection_latency/config"
		legacyOrphan = "homeassistant/switch/" + strings.ToLower(naming.TopicSafe(centralName)) + "_0001d3c99c9999/state/config"
		liveTopic    = "homeassistant/switch/" + naming.DiscoverySlug(centralName) + "_0001d3c99c1234/level/config"
		foreign      = "homeassistant/light/zigbee2mqtt_0x1234/state/config"
	)
	if naming.DiscoverySlug(centralName) == strings.ToLower(centralName) {
		t.Fatalf("fixture central %q is invariant under the slug and cannot detect the drift", centralName)
	}

	mc := &mockRetainClient{retained: []retainedMsg{
		{topic: deviceOrphan, payload: []byte(`{}`)},
		{topic: hubOrphan, payload: []byte(`{}`)},
		{topic: legacyOrphan, payload: []byte(`{}`)},
		{topic: liveTopic, payload: []byte(`{}`)},
		{topic: foreign, payload: []byte(`{}`)},
	}}
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom", HADiscoveryEnabled: true, CentralName: centralName,
	}, mc)
	b.mu.Lock()
	b.declared[liveTopic] = []byte(`{}`)
	b.mu.Unlock()

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "", 50*time.Millisecond); err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	evicted := map[string]bool{}
	for _, topic := range mc.evicted() {
		evicted[topic] = true
	}
	for _, topic := range []string{deviceOrphan, hubOrphan, legacyOrphan} {
		if !evicted[topic] {
			t.Errorf("orphan %q survived the sweep — the phantom entity is re-created on every restart", topic)
		}
	}
	if evicted[liveTopic] {
		t.Errorf("the sweep evicted a declared entity: %q", liveTopic)
	}
	if evicted[foreign] {
		t.Errorf("the sweep evicted another integration's config: %q", foreign)
	}
}

// TestRawOrphanCandidateMatcherEscapesCentralName: the raw plane writes
// the central name TopicSafe-escaped, so the sweep has to compare
// against the escaped spelling. Matching the configured name verbatim
// made the raw orphan pass miss every topic of a central whose name
// contains a space.
func TestRawOrphanCandidateMatcherEscapesCentralName(t *testing.T) {
	t.Parallel()
	const (
		base        = "openccu-loom"
		centralName = "Wohn Zimmer"
	)
	topic := base + "/" + naming.TopicSafe(centralName) + "/HmIP-RF/0001ABCD/1/values/STATE"
	if !RawOrphanCandidateMatcher(base, centralName, topic) {
		t.Fatalf("the escaped topic %q of central %q was not recognised as this daemon's", topic, centralName)
	}
	// Another central's subtree stays out of scope.
	if RawOrphanCandidateMatcher(base, "Schlaf Zimmer", topic) {
		t.Error("a foreign central's topic matched")
	}
}

// filteringRetainClient is [mockRetainClient] with a broker's actual
// subscribe semantics: it replays a retained message only to a subscription
// whose filter matches it. The non-filtering double cannot see a wrong
// subscribe filter at all, which is exactly the defect below.
type filteringRetainClient struct {
	mockRetainClient
}

func (m *filteringRetainClient) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	m.mu.Lock()
	retained := make([]retainedMsg, len(m.retained))
	copy(retained, m.retained)
	m.mu.Unlock()
	for _, msg := range retained {
		if !mqttFilterMatches(filter, msg.topic) {
			continue
		}
		handler(&Message{Topic: msg.topic, Payload: msg.payload, Retain: true})
	}
	return SubscribeResult{}, nil
}

// TestRunRawOrphanCleanupOnceSweepsTheTopicsTheBridgeWrote pins that the raw
// orphan sweep looks where the raw plane actually publishes.
//
// The sweep derives its subscribe filter and its candidate matcher from the
// configured topic base, while every publisher goes through the topic builder,
// which trims the base's slashes. An operator who writes `topic_base` with a
// trailing slash therefore had a sweep that subscribed to a prefix nothing
// writes: it reported zero evicted on every boot and retained topics from a
// previous build kept feeding stale values forever.
//
// The fixture asserts against a topic the bridge itself wrote, not a
// hand-built literal, so the two halves cannot drift apart again.
func TestRunRawOrphanCleanupOnceSweepsTheTopicsTheBridgeWrote(t *testing.T) {
	t.Parallel()

	const (
		configuredBase = "home/hm/"
		central        = "Wohn Zimmer"
		iface          = "HmIP-RF"
		addr           = "0001ABCD"
	)
	mc := &filteringRetainClient{}
	b := NewBridge(BridgeConfig{Base: configuredBase, CentralName: central, RawEnabled: true}, mc)

	ctx := context.Background()
	live := pload.TopicSlot{Address: addr, Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	if err := b.PublishSlotState(ctx, central, iface, live, pload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}
	liveTopic := b.topics.SlotState(central, iface, live)
	// The orphan is a sibling of the live topic — same device, a parameter
	// this build no longer publishes.
	stale := pload.TopicSlot{Address: addr, Channel: 8, Bucket: pload.BucketMaster, Parameter: "01_WP_WEEKDAY"}
	orphanTopic := b.topics.SlotState(central, iface, stale)

	mc.mu.Lock()
	mc.retained = []retainedMsg{
		{topic: liveTopic, payload: []byte(`{"value":true}`)},
		{topic: orphanTopic, payload: []byte(`{"value":1}`)},
	}
	mc.mu.Unlock()

	n, err := b.RunRawOrphanCleanupOnce(ctx, central, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunRawOrphanCleanupOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted = %d, want 1 — the sweep never reached the topics the bridge wrote", n)
	}
	evicted := mc.evicted()
	if len(evicted) != 1 || evicted[0] != orphanTopic {
		t.Fatalf("evicted = %v, want [%s]", evicted, orphanTopic)
	}
}

// retiredMetricSpellings returns the three central-wide metric topics as
// the build before the escaping fix wrote them: the central segment
// lower-cased instead of [naming.TopicSafe]-escaped. Spelled out here
// because no production code produces this shape any more — it exists
// only in the retained stores of deployments that ran that build.
func retiredMetricSpellings(base, centralName string) []string {
	segment := strings.ToLower(centralName)
	return []string{
		base + "/" + segment + "/system/health_score",
		base + "/" + segment + "/system/latency",
		base + "/" + segment + "/system/last_event_age",
	}
}

// TestRunRetainCleanupOnceClearsTheRetiredMetricSpelling pins that the
// opt-in sweep clears the metric topics the escaping fix orphaned.
//
// The three central-wide metric sensors used to lower-case the central
// segment while every other topic on the plane escapes it. For a CCU
// whose name the escaping rewrites, the values on the old topics are
// frozen at the moment of the upgrade and no publisher will ever touch
// them again — a consumer subscribing the base wildcard keeps reading a
// health score from the previous build forever.
func TestRunRetainCleanupOnceClearsTheRetiredMetricSpelling(t *testing.T) {
	t.Parallel()

	const (
		base    = "openccu-loom"
		central = "Haus CCÜ"
	)
	topics := NewTopicBuilder(base)
	live := []string{
		topics.HubSystemHealthScore(central),
		topics.HubConnectionLatency(central),
		topics.HubLastEventAge(central),
	}
	retired := retiredMetricSpellings(base, central)
	if retired[0] == live[0] {
		t.Fatalf("fixture central %q is invariant under the escaping and cannot detect the orphan", central)
	}

	retained := make([]retainedMsg, 0, len(live)+len(retired))
	for _, topic := range append(append([]string{}, live...), retired...) {
		retained = append(retained, retainedMsg{topic: topic, payload: []byte(`{"value":42}`)})
	}
	mc := &mockRetainClient{retained: retained}
	b := NewBridge(BridgeConfig{
		Base:         base,
		CentralName:  central,
		CentralNames: []string{central},
		RawEnabled:   true,
	}, mc)

	n, err := b.RunRetainCleanupOnce(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunRetainCleanupOnce: %v", err)
	}
	if n != len(retired) {
		t.Fatalf("evicted = %d, want %d — the orphaned metric topics keep serving a value from the previous build", n, len(retired))
	}
	evicted := make(map[string]bool)
	for _, topic := range mc.evicted() {
		evicted[topic] = true
	}
	for _, topic := range retired {
		if !evicted[topic] {
			t.Errorf("retired metric topic %q survived the sweep", topic)
		}
	}
	for _, topic := range live {
		if evicted[topic] {
			t.Errorf("the sweep cleared the live metric topic %q", topic)
		}
	}
}

// TestRunRetainCleanupOnceKeepsMetricTopicsTheEscapingNeverMoved is the
// data-loss guard on the sweep above.
//
// For a CCU whose name the escaping leaves alone — every plain
// lower-case ASCII name, which is the common case — the "old" spelling
// and the current one are the identical topic. An unguarded sweep would
// blank the live health score, latency and last-event-age of exactly the
// deployments the rename never affected. The multi-CCU row covers the
// same collision across centrals: one CCU's retired spelling is another
// CCU's live topic.
func TestRunRetainCleanupOnceKeepsMetricTopicsTheEscapingNeverMoved(t *testing.T) {
	t.Parallel()

	const base = "openccu-loom"
	for _, tc := range []struct {
		name     string
		centrals []string
		// owner is the central whose live metric topics the sweep must
		// leave untouched.
		owner string
	}{
		{name: "name unchanged by the escaping", centrals: []string{"ccu1"}, owner: "ccu1"},
		{name: "another central's live topics", centrals: []string{"CCU1", "ccu1"}, owner: "ccu1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			topics := NewTopicBuilder(base)
			live := []string{
				topics.HubSystemHealthScore(tc.owner),
				topics.HubConnectionLatency(tc.owner),
				topics.HubLastEventAge(tc.owner),
			}
			retained := make([]retainedMsg, 0, len(live))
			for _, topic := range live {
				retained = append(retained, retainedMsg{topic: topic, payload: []byte(`{"value":42}`)})
			}
			mc := &mockRetainClient{retained: retained}
			b := NewBridge(BridgeConfig{
				Base:         base,
				CentralName:  tc.centrals[0],
				CentralNames: tc.centrals,
				RawEnabled:   true,
			}, mc)

			n, err := b.RunRetainCleanupOnce(context.Background(), 50*time.Millisecond)
			if err != nil {
				t.Fatalf("RunRetainCleanupOnce: %v", err)
			}
			if n != 0 {
				t.Fatalf("evicted = %d, want 0 — the sweep blanked live metric values", n)
			}
			if cleared := mc.evicted(); len(cleared) != 0 {
				t.Fatalf("cleared %v — those are the live metric topics of %q", cleared, tc.owner)
			}
		})
	}
}

// TestRunRetainCleanupOnceKeepsTheTopicsOfARuntimeAdoptedCentral pins that the
// sweep judges candidates against the fleet the daemon serves right now, not
// against the one it booted with.
//
// A CCU adopted through the SPA never reaches the boot config, so it is absent
// from the snapshot the bridge was built with. The retired spelling of a
// configured CCU's metric topic is the live topic of a CCU whose name differs
// only in case — so a sweep that does not know the adopted CCU blanks its
// health score, latency and last-event-age, and no publisher rewrites them
// until the value changes.
func TestRunRetainCleanupOnceKeepsTheTopicsOfARuntimeAdoptedCentral(t *testing.T) {
	t.Parallel()

	const (
		base      = "openccu-loom"
		booted    = "CCU1"
		adopted   = "ccu1" // adopted at runtime; collides with booted's retired spelling
		windowLen = 50 * time.Millisecond
	)
	topics := NewTopicBuilder(base)
	live := []string{
		topics.HubSystemHealthScore(adopted),
		topics.HubConnectionLatency(adopted),
		topics.HubLastEventAge(adopted),
	}
	retained := make([]retainedMsg, 0, len(live))
	for _, topic := range live {
		retained = append(retained, retainedMsg{topic: topic, payload: []byte(`{"value":42}`)})
	}
	mc := &mockRetainClient{retained: retained}
	b := NewBridge(BridgeConfig{
		Base:         base,
		CentralName:  booted,
		CentralNames: []string{booted},
		CentralNamesSupplier: func() []string {
			return []string{booted, adopted}
		},
		RawEnabled: true,
	}, mc)

	n, err := b.RunRetainCleanupOnce(context.Background(), windowLen)
	if err != nil {
		t.Fatalf("RunRetainCleanupOnce: %v", err)
	}
	if n != 0 {
		t.Fatalf("evicted = %d, want 0 — the sweep blanked the adopted CCU's live metric values", n)
	}
	if cleared := mc.evicted(); len(cleared) != 0 {
		t.Fatalf("cleared %v — those are the live metric topics of %q", cleared, adopted)
	}
}

// unsubscribeTrackingClient is a [Client] that records every filter it
// was asked to unsubscribe from, so a test can prove no sweep leaves its
// wildcard subscription installed on the shared client.
type unsubscribeTrackingClient struct {
	mu           sync.Mutex
	unsubscribed []string
}

func (c *unsubscribeTrackingClient) Publish(_ context.Context, _ string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	return nil
}

func (c *unsubscribeTrackingClient) Subscribe(_ context.Context, _ string, _ QoS, _ MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	return SubscribeResult{}, nil
}

func (c *unsubscribeTrackingClient) Unsubscribe(_ context.Context, filter string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubscribed = append(c.unsubscribed, filter)
	return nil
}

func (c *unsubscribeTrackingClient) filters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.unsubscribed...)
}

// TestRetainSweepsUnsubscribeWhenTheWindowIsCancelled pins the teardown
// of every one-shot sweep against a cancelled context.
//
// The sweeps are boot-time passes over broad wildcards whose handler
// closes over a growing worklist. Returning from the snapshot window
// without unsubscribing left that handler attached to the shared client
// for the rest of the process — running on the daemon's own publishes,
// accumulating forever, and replayed onto every reconnect by the client's
// own subscription bookkeeping.
func TestRetainSweepsUnsubscribeWhenTheWindowIsCancelled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(ctx context.Context, b *Bridge) error
	}{
		{"retain", func(ctx context.Context, b *Bridge) error {
			_, err := b.RunRetainCleanupOnce(ctx, time.Minute)
			return err
		}},
		{"discovery_orphan", func(ctx context.Context, b *Bridge) error {
			_, err := b.RunDiscoveryOrphanCleanupOnce(ctx, "", time.Minute)
			return err
		}},
		{"raw_orphan", func(ctx context.Context, b *Bridge) error {
			_, err := b.RunRawOrphanCleanupOnce(ctx, "", time.Minute)
			return err
		}},
		{"unscoped_discovery", func(ctx context.Context, b *Bridge) error {
			_, err := b.RunUnscopedDiscoveryCleanupOnce(ctx, time.Minute)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &unsubscribeTrackingClient{}
			b := NewBridge(BridgeConfig{
				Base: "openccu-loom", CentralName: "ccu-01",
				RawEnabled: true, HADiscoveryEnabled: true,
			}, client)
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancel()
			}()
			if err := tc.run(ctx, b); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if got := client.filters(); len(got) != 1 {
				t.Fatalf("unsubscribed = %v, want exactly one filter — a stranded wildcard runs for the daemon's lifetime", got)
			}
		})
	}
}

// TestRunDiscoveryOrphanCleanupOnceSweepsEveryCentral pins the sweep's
// scoping against a multi-CCU daemon.
//
// Both node-id producers slug the central they belong to, so a pass that
// derived its ownership filter from the default central alone classified
// every other CCU's retained config as "not ours" and skipped it. Those
// orphans then had no automatic path off the broker at all: Home
// Assistant re-created them as permanently unavailable phantoms on every
// integration restart.
func TestRunDiscoveryOrphanCleanupOnceSweepsEveryCentral(t *testing.T) {
	t.Parallel()
	const (
		defaultCentral = "ccu-01"
		secondCentral  = "ccu-02"
	)
	var (
		orphan = "homeassistant/switch/" + naming.DiscoverySlug(secondCentral) + "_0001d3c99c1234/state/config"
		live   = "homeassistant/switch/" + naming.DiscoverySlug(secondCentral) + "_0001d3c99c1234/level/config"
		other  = "homeassistant/switch/" + naming.DiscoverySlug(defaultCentral) + "_0001d3c99c5678/state/config"
	)
	mc := &mockRetainClient{retained: []retainedMsg{
		{topic: orphan, payload: []byte(`{}`)},
		{topic: live, payload: []byte(`{}`)},
		{topic: other, payload: []byte(`{}`)},
	}}
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom", HADiscoveryEnabled: true, CentralName: defaultCentral,
	}, mc)
	b.mu.Lock()
	b.declared[live] = []byte(`{}`)
	b.mu.Unlock()

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), secondCentral, 50*time.Millisecond); err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	evicted := map[string]bool{}
	for _, topic := range mc.evicted() {
		evicted[topic] = true
	}
	if !evicted[orphan] {
		t.Errorf("orphan %q survived — a non-default CCU's phantom entities can never be removed", orphan)
	}
	if evicted[live] {
		t.Errorf("the sweep evicted a declared entity: %q", live)
	}
	if evicted[other] {
		t.Errorf("the sweep judged another central's topic: %q — that central's snapshot may not have run yet", other)
	}
}

// TestRunDiscoveryOrphanCleanupOnce_PrefixSiblingCentralNotEvicted pins that a
// central whose slug is a prefix of a sibling's does not evict the sibling's
// retained discovery configs.
//
// `CCU` slugs to `ccu`, `CCU Wohnung` to `ccu_wohnung`. The sweep matched the
// node id with an unbounded HasPrefix("ccu_") test, so `ccu`'s boot sweep
// judged every `ccu_wohnung_*` node id as its own. The sibling's entities are
// not yet in `declared` at that moment (its snapshot has not run), so all of
// them looked like orphans and were evicted — they never returned until the
// daemon restarted. Anchoring on the last-underscore boundary makes the sibling
// segment `ccu_wohnung`, not `ccu`, so it stays untouched while `ccu`'s own
// orphans are still swept.
//
// The sibling is deliberately NOT configured on this bridge: the fix is
// structural, so it must protect the sibling even before the daemon knows the
// second CCU exists — which is exactly the boot-time race.
func TestRunDiscoveryOrphanCleanupOnce_PrefixSiblingCentralNotEvicted(t *testing.T) {
	t.Parallel()
	const (
		central = "CCU"         // slug: ccu
		sibling = "CCU Wohnung" // slug: ccu_wohnung
	)
	if !strings.HasPrefix(naming.DiscoverySlug(sibling)+"_", naming.DiscoverySlug(central)+"_") {
		t.Fatalf("fixture invalid: %q slug %q is not a prefix of %q slug %q",
			central, naming.DiscoverySlug(central), sibling, naming.DiscoverySlug(sibling))
	}
	var (
		ownOrphan    = "homeassistant/switch/" + naming.DiscoverySlug(central) + "_0001aaaa/state/config"
		ownHubOrphan = "homeassistant/sensor/" + hubNodeID(central, "system") + "/connection_latency/config"
		siblingDev   = "homeassistant/switch/" + naming.DiscoverySlug(sibling) + "_0001bbbb/state/config"
		siblingHub   = "homeassistant/sensor/" + hubNodeID(sibling, "system") + "/connection_latency/config"
	)
	mc := &mockRetainClient{retained: []retainedMsg{
		{topic: ownOrphan, payload: []byte(`{}`)},
		{topic: ownHubOrphan, payload: []byte(`{}`)},
		{topic: siblingDev, payload: []byte(`{}`)},
		{topic: siblingHub, payload: []byte(`{}`)},
	}}
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom", HADiscoveryEnabled: true, CentralName: central,
	}, mc)

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), central, 50*time.Millisecond); err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	evicted := map[string]bool{}
	for _, topic := range mc.evicted() {
		evicted[topic] = true
	}
	for _, topic := range []string{siblingDev, siblingHub} {
		if evicted[topic] {
			t.Errorf("sweep for %q evicted prefix-sibling %q's retained config %q — its entities vanish until the daemon restarts", central, sibling, topic)
		}
	}
	for _, topic := range []string{ownOrphan, ownHubOrphan} {
		if !evicted[topic] {
			t.Errorf("sweep for %q failed to evict its own genuine orphan %q", central, topic)
		}
	}
}

// filterKeyedBroker models the one property of the shared subscribe client
// that makes concurrent sweeps unsafe: subscriptions are keyed by filter,
// so a second Subscribe on the same filter replaces the first handler, and
// an Unsubscribe removes the subscription for whoever holds it.
//
// Retained delivery is asynchronous — the broker flushes its retained
// queue after the subscription exists — which is what lets an overlapping
// window steal the first sweep's deliveries.
type filterKeyedBroker struct {
	mu        sync.Mutex
	handlers  map[string]MessageHandler
	retained  []retainedMsg
	published []publishedMsg
	wg        sync.WaitGroup
}

func newFilterKeyedBroker(retained []retainedMsg) *filterKeyedBroker {
	return &filterKeyedBroker{handlers: map[string]MessageHandler{}, retained: retained}
}

func (b *filterKeyedBroker) Publish(_ context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, publishedMsg{topic: topic, payload: payload, retain: retain})
	return nil
}

func (b *filterKeyedBroker) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	b.mu.Lock()
	b.handlers[filter] = handler
	msgs := make([]retainedMsg, len(b.retained))
	copy(msgs, b.retained)
	b.mu.Unlock()

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		time.Sleep(20 * time.Millisecond)
		for _, msg := range msgs {
			b.mu.Lock()
			h := b.handlers[filter]
			b.mu.Unlock()
			if h == nil {
				return
			}
			h(&Message{Topic: msg.topic, Payload: msg.payload, Retain: true})
		}
	}()
	return SubscribeResult{}, nil
}

func (b *filterKeyedBroker) Unsubscribe(_ context.Context, filter string) error {
	b.mu.Lock()
	delete(b.handlers, filter)
	b.mu.Unlock()
	return nil
}

func (b *filterKeyedBroker) evicted() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for _, p := range b.published {
		if p.retain && len(p.payload) == 0 {
			out[p.topic] = true
		}
	}
	return out
}

// TestConcurrentDiscoveryOrphanSweepsDoNotTruncateEachOther pins the
// per-broker serialisation of the retained-snapshot window.
//
// The daemon runs one discovery-orphan sweep per central, and every one of
// them subscribes the same client to `homeassistant/#`. Overlapping
// windows used to replace each other's handler and tear the subscription
// down early, so both sweeps saw an empty broker and reported zero
// orphans — the phantom entities they exist to remove survived, silently,
// on every multi-CCU installation.
func TestConcurrentDiscoveryOrphanSweepsDoNotTruncateEachOther(t *testing.T) {
	t.Parallel()
	const (
		orphanA = "homeassistant/sensor/ccu-a_000a/1_state/config"
		orphanB = "homeassistant/sensor/ccu-b_000b/1_state/config"
	)
	broker := newFilterKeyedBroker([]retainedMsg{
		{topic: orphanA, payload: []byte(`{"name":"A"}`)},
		{topic: orphanB, payload: []byte(`{"name":"B"}`)},
	})
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		CentralNames:       []string{"ccu-a", "ccu-b"},
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, broker).WithSubscriber(broker)

	var wg sync.WaitGroup
	counts := make([]int, 2)
	errs := make([]error, 2)
	for i, central := range []string{"ccu-a", "ccu-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts[i], errs[i] = bridge.RunDiscoveryOrphanCleanupOnce(
				context.Background(), central, 120*time.Millisecond,
			)
		}()
	}
	wg.Wait()
	broker.wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
	}
	evicted := broker.evicted()
	if !evicted[orphanA] {
		t.Errorf("central ccu-a's orphan %q survived the sweep (counts=%v)", orphanA, counts)
	}
	if !evicted[orphanB] {
		t.Errorf("central ccu-b's orphan %q survived the sweep (counts=%v)", orphanB, counts)
	}
}
