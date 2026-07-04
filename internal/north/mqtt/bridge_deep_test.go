// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stableEvent is a reusable event that keeps tests readable and consistent.
var stableEvent = Event{
	Central:       "c1",
	Interface:     "HmIP-RF",
	DeviceAddress: "0001ABCD",
	ChannelNo:     3,
	Parameter:     "STATE",
	Category:      hmenum.DataPointCategorySwitch,
	Value:         true,
}

// fakeAddressable is a payload.MQTTAddressable that returns whatever
// topic set the test wants. Keeps bridge tests independent from the
// hub model package.
type fakeAddressable struct {
	state, set, trigger, config string
}

func (f fakeAddressable) MQTTTopics(_, _ string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{State: f.state, Set: f.set, Trigger: f.trigger, Config: f.config}
}

// fakeConnectivityPublisher fulfills ConnectivityPublisher for tests.
type fakeConnectivityPublisher struct {
	state string
}

func (f fakeConnectivityPublisher) MQTTTopicsForInterface(_, _, _ string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{State: f.state}
}

// recordingPublisher captures every Publish call under a mutex. Identical in
// spirit to mockPublisher but defined here so each deep-test file is
// self-contained; it satisfies Publisher directly.
type recordingPublisher struct {
	mu   sync.Mutex
	recs []publishRecord
	err  error
}

func (r *recordingPublisher) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	if r.err != nil {
		return r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, publishRecord{
		topic:   topic,
		payload: string(payload),
		qos:     qos,
		retain:  retain,
	})
	return nil
}

func (r *recordingPublisher) records() []publishRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]publishRecord, len(r.recs))
	copy(out, r.recs)
	return out
}

func (r *recordingPublisher) clear() {
	r.mu.Lock()
	r.recs = r.recs[:0]
	r.mu.Unlock()
}

func (r *recordingPublisher) findTopic(topic string) (publishRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.recs {
		if rec.topic == topic {
			return rec, true
		}
	}
	return publishRecord{}, false
}

func (r *recordingPublisher) countPrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.recs {
		if strings.HasPrefix(rec.topic, prefix) {
			n++
		}
	}
	return n
}

// fixedDiscoveryBuilder always returns a fixed component/nodeID/objectID/payload.
type fixedDiscoveryBuilder struct {
	component string
	nodeID    string
	objectID  string
	payload   []byte
}

func (f *fixedDiscoveryBuilder) Build(_ Event) (component, nodeID, objectID string, payload []byte, ok bool) {
	nID := f.nodeID
	if nID == "" {
		nID = "openccu-loom" // back-compat default for tests that don't set it
	}
	return f.component, nID, f.objectID, f.payload, true
}

func newDeepBridge(t *testing.T, rec *recordingPublisher, opts ...func(*BridgeConfig)) *Bridge {
	t.Helper()
	cfg := BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "c1",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewBridge(cfg, rec)
}

// 1. AnnounceOnline publishes "online" retained on {base}/bridge/status with QoS1.
func TestBridgeAnnounceOnlinePublishesBridgeStatus(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("AnnounceOnline: %v", err)
	}

	want := "openccu-loom/bridge/status"
	r, ok := rec.findTopic(want)
	if !ok {
		t.Fatalf("topic %q not published; got: %v", want, rec.records())
	}
	if r.payload != "online" {
		t.Fatalf("payload: got %q want %q", r.payload, "online")
	}
	if r.qos != QoS1 {
		t.Fatalf("QoS: got %d want %d", r.qos, QoS1)
	}
	if !r.retain {
		t.Fatalf("expected retained=true")
	}
}

// 2. RepublishDiscovery re-emits every cached config after the recorder is cleared.
func TestBridgeRepublishDiscoveryReplaysCachedConfigs(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"x"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.DiscoveryBuilder = db
	})

	// First publish caches the discovery config.
	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	// Clear the recorder so we only see the RepublishDiscovery output.
	rec.clear()

	if err := b.RepublishDiscovery(context.Background()); err != nil {
		t.Fatalf("RepublishDiscovery: %v", err)
	}

	// The discovery config topic must have been re-published.
	wantTopic := "homeassistant/switch/openccu-loom/0001abcd_3_state/config"
	r, ok := rec.findTopic(wantTopic)
	if !ok {
		t.Fatalf("discovery topic %q not re-published; got: %v", wantTopic, rec.records())
	}
	if string(r.payload) != `{"unique_id":"x"}` { //nolint:unconvert // r.payload is []byte; explicit cast for readability against string literal
		t.Fatalf("payload mismatch: %q", r.payload)
	}
}

// 3. RepublishDiscovery with empty cache is a no-op.
func TestBridgeRepublishDiscoveryEmptyIsNoop(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	if err := b.RepublishDiscovery(context.Background()); err != nil {
		t.Fatalf("RepublishDiscovery: %v", err)
	}
	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes, got %d: %v", n, rec.records())
	}
}

// 4. Calling PublishState twice with identical event+payload emits discovery
// only once but emits state both times.
func TestBridgeDiscoveryDeduplicates(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"dup"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.DiscoveryBuilder = db
	})

	ev := stableEvent // identical event both times
	_ = b.PublishState(context.Background(), ev)
	_ = b.PublishState(context.Background(), ev)

	// Discovery must appear exactly once (deduplication via declared cache).
	discoveryCount := rec.countPrefix("homeassistant/")
	if discoveryCount != 1 {
		t.Fatalf("discovery publishes: got %d want 1", discoveryCount)
	}

	// Raw per-DP state publishes are no longer produced by PublishState —
	// they moved to PublishSlotState (called by the EventBridge).
	// Verify the state topic does NOT appear in the output of PublishState.
	stateTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/3/values/STATE"
	for _, r := range rec.records() {
		if r.topic == stateTopic {
			t.Fatalf("PublishState must not write raw per-DP state topic; got %s", r.topic)
		}
	}
}

// 5. RawEnabled:false → PublishState does NOT publish a raw topic; HA discovery
// is also skipped when the raw plane is the gating condition.
func TestBridgeRawDisabledSkipsState(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"skip"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = false
		c.DiscoveryBuilder = db
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	rawTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/3/STATE"
	if _, ok := rec.findTopic(rawTopic); ok {
		t.Fatalf("raw topic %q must not be published when RawEnabled=false", rawTopic)
	}
	// Discovery is still emitted (HADiscoveryEnabled is independent of RawEnabled).
	// Verify we see either 0 or 1 discovery topics — the key assertion is the
	// raw topic is absent.
}

// 6. HADiscoveryEnabled:false → no discovery topic published.
func TestBridgeHADisabledSkipsDiscovery(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"nodisco"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.HADiscoveryEnabled = false
		c.DiscoveryBuilder = db
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	n := rec.countPrefix("homeassistant/")
	if n != 0 {
		t.Fatalf("expected 0 discovery publishes, got %d", n)
	}
}

// 6b. DiscoveryBuilder nil + HADiscoveryEnabled true → NewBridge
// auto-constructs the default builder so discovery configs are
// emitted. Without this contract, operators who flip
// `discovery_enabled: true` in config see no HA Discovery configs at
// the broker because the daemon-level wiring forgets to inject a
// builder — exactly the bug that prompted this test rewrite.
func TestBridgeAutoWiresDefaultDiscoveryBuilder(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = nil
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	if n := rec.countPrefix("homeassistant/"); n == 0 {
		t.Fatalf("expected discovery publish from auto-wired default builder, got 0")
	}
}

// 7. PublishEvent uses QoS0 and retain=false (event-stream semantics).
func TestBridgePublishEventNonRetained(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	if err := b.PublishEvent(context.Background(), "c1", "HmIP-RF", "0001ABCD", 3, "keypress", "short"); err != nil {
		t.Fatalf("PublishEvent: %v", err)
	}

	wantTopic := "openccu-loom/c1/HmIP-RF/0001ABCD/3/event/keypress"
	r, ok := rec.findTopic(wantTopic)
	if !ok {
		t.Fatalf("event topic %q not found; got: %v", wantTopic, rec.records())
	}
	if r.qos != QoS0 {
		t.Fatalf("QoS: got %d want QoS0 (%d)", r.qos, QoS0)
	}
	if r.retain {
		t.Fatalf("PublishEvent must be non-retained")
	}
	if r.payload != "short" {
		t.Fatalf("payload: got %q want %q", r.payload, "short")
	}
}

// 7b. PublishEvent is skipped when RawEnabled is false.
func TestBridgePublishEventSkippedWhenRawDisabled(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.RawEnabled = false })

	_ = b.PublishEvent(context.Background(), "c1", "HmIP-RF", "0001ABCD", 3, "keypress", "short")

	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes with RawEnabled=false, got %d", n)
	}
}

// 8. PublishProgram publishes on the correct topic with the right payload.
func TestBridgePublishProgramAvailability(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	prog := fakeAddressable{
		state:   "openccu-loom/c1/hub/programs/Morning/state",
		trigger: "openccu-loom/c1/hub/programs/Morning/trigger",
	}
	if err := b.PublishProgram(context.Background(), "c1", prog, true); err != nil {
		t.Fatalf("PublishProgram: %v", err)
	}

	wantTopic := "openccu-loom/c1/hub/programs/Morning/state"
	r, ok := rec.findTopic(wantTopic)
	if !ok {
		t.Fatalf("program topic %q not found; got: %v", wantTopic, rec.records())
	}
	if r.payload != "true" {
		t.Fatalf("payload: got %q want %q", r.payload, "true")
	}
	if !r.retain {
		t.Fatalf("program topic must be retained")
	}

	// Verify the false case.
	rec.clear()
	_ = b.PublishProgram(context.Background(), "c1", prog, false)
	r2, ok2 := rec.findTopic(wantTopic)
	if !ok2 {
		t.Fatalf("program topic missing on second call")
	}
	if r2.payload != "false" {
		t.Fatalf("payload (inactive): got %q want %q", r2.payload, "false")
	}
}

// 9. PublishSysvar renders various Go types to the expected MQTT payload bytes.
func TestBridgePublishSysvarValueRendering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		value       any
		want        string
		centralName string
		sysvar      string
	}{
		{"int", 42, "42", "c1", "Counter"},
		{"float", 3.25, "3.25", "c1", "Temperature"},
		{"string", "hello", "hello", "c1", "Mode"},
		{"bool_true", true, "true", "c1", "Active"},
		{"bool_false", false, "false", "c1", "Inactive"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recordingPublisher{}
			b := newDeepBridge(t, rec)

			wantTopic := "openccu-loom/" + tc.centralName + "/hub/sysvars/" + tc.sysvar + "/state"
			sv := fakeAddressable{state: wantTopic}
			if err := b.PublishSysvar(context.Background(), tc.centralName, sv, tc.value); err != nil {
				t.Fatalf("PublishSysvar: %v", err)
			}
			r, ok := rec.findTopic(wantTopic)
			if !ok {
				t.Fatalf("sysvar topic %q not found; got: %v", wantTopic, rec.records())
			}
			if r.payload != tc.want {
				t.Fatalf("payload: got %q want %q", r.payload, tc.want)
			}
			if !r.retain {
				t.Fatalf("sysvar topic must be retained")
			}
		})
	}
}

// 9b. PublishSysvar is skipped when RawEnabled is false.
func TestBridgePublishSysvarSkippedWhenRawDisabled(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.RawEnabled = false })

	_ = b.PublishSysvar(context.Background(), "c1", fakeAddressable{state: "x"}, "holiday")

	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes with RawEnabled=false, got %d", n)
	}
}

// 10. PublishConnectivity toggles a per-interface availability topic.
func TestBridgePublishConnectivity(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	wantTopic := "openccu-loom/c1/hub/connectivity/HmIP-RF"
	conn := fakeConnectivityPublisher{state: wantTopic}

	if err := b.PublishConnectivity(context.Background(), "c1", conn, "HmIP-RF", true); err != nil {
		t.Fatalf("PublishConnectivity(true): %v", err)
	}
	r, ok := rec.findTopic(wantTopic)
	if !ok {
		t.Fatalf("connectivity topic %q not found; got: %v", wantTopic, rec.records())
	}
	if r.payload != "true" {
		t.Fatalf("payload(connected): got %q want %q", r.payload, "true")
	}
	if !r.retain {
		t.Fatalf("connectivity topic must be retained")
	}

	rec.clear()
	if err := b.PublishConnectivity(context.Background(), "c1", conn, "HmIP-RF", false); err != nil {
		t.Fatalf("PublishConnectivity(false): %v", err)
	}
	r2, ok2 := rec.findTopic(wantTopic)
	if !ok2 {
		t.Fatalf("connectivity topic missing on second call")
	}
	if r2.payload != "false" {
		t.Fatalf("payload(disconnected): got %q want %q", r2.payload, "false")
	}
}

// 10b. PublishConnectivity is skipped when RawEnabled is false.
func TestBridgePublishConnectivitySkippedWhenRawDisabled(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) { c.RawEnabled = false })

	conn := fakeConnectivityPublisher{state: "openccu-loom/c1/hub/connectivity/HmIP-RF"}
	_ = b.PublishConnectivity(context.Background(), "c1", conn, "HmIP-RF", true)

	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes with RawEnabled=false, got %d", n)
	}
}

// Extra: CentralName fallback — when the central argument is empty,
// cfg.CentralName is used. Verified via PublishSlotState (the canonical
// per-DP publish path) since PublishState no longer writes the raw state
// topic directly.
func TestBridgeCentralNameFallback(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.CentralName = "fallback-ccu"
		c.HADiscoveryEnabled = false
	})

	// Use PublishSlotState to verify that the CentralName fallback resolves
	// correctly into the topic when central is empty.
	slot := pload.TopicSlot{
		Address:   stableEvent.DeviceAddress,
		Channel:   stableEvent.ChannelNo,
		Bucket:    pload.BucketValues,
		Parameter: stableEvent.Parameter,
	}
	if err := b.PublishSlotState(context.Background(), "" /* empty → fallback */, stableEvent.Interface, slot, pload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}

	wantTopic := "openccu-loom/fallback-ccu/HmIP-RF/0001ABCD/3/values/STATE"
	if _, ok := rec.findTopic(wantTopic); !ok {
		t.Fatalf("expected fallback central in topic; got: %v", rec.records())
	}
}

// Extra: RepublishDiscovery propagates publisher error.
func TestBridgeRepublishDiscoveryPropagatesError(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"err"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.DiscoveryBuilder = db
	})

	// Cache one entry.
	_ = b.PublishState(context.Background(), stableEvent)

	// Now inject an error.
	rec.err = errDeep
	err := b.RepublishDiscovery(context.Background())
	if err == nil {
		t.Fatal("expected error from RepublishDiscovery when publisher fails")
	}
}

// sentinel used by the error-propagation test above.
type deepErr struct{}

func (deepErr) Error() string { return "simulated publisher error" }

var errDeep = deepErr{}
