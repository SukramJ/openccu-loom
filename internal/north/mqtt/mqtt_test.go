// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- topic builder ---

func TestTopicBuilder(t *testing.T) {
	tb := NewTopicBuilder("openccu-loom")
	cases := []struct {
		got, want string
	}{
		{tb.Connected(), "openccu-loom/connected"},
		{tb.Info(), "openccu-loom/info"},
		{tb.Maintenance("set", "restart"), "openccu-loom/maintenance/set/restart"},
		{tb.Maintenance("stats"), "openccu-loom/maintenance/stats"},
		{tb.DeviceAvailability("ccu", "HmIP-RF", "000A"), "openccu-loom/status/ccu/HmIP-RF/000A/online"},
		{tb.DataPointState("ccu", "HmIP-RF", "000A", 1, "STATE"), "openccu-loom/status/ccu/HmIP-RF/000A/1/values/STATE"},
		{tb.DataPointCommand("ccu", "HmIP-RF", "000A", 1, "STATE"), "openccu-loom/set/ccu/HmIP-RF/000A/1/values/STATE"},
		{tb.DataPointConfig("ccu", "HmIP-RF", "000A", 1, "STATE"), "openccu-loom/meta/ccu/HmIP-RF/000A/1/values/STATE"},
		{tb.HubStatus("ccu"), "openccu-loom/status/ccu/online"},
		{tb.HubInfo("ccu"), "openccu-loom/status/ccu/hub/info"},
		{tb.HubDiagnostics("ccu"), "openccu-loom/status/ccu/hub/diagnostics"},
		{tb.DiscoveryConfig("switch", "openccu-loom", "abc"), "homeassistant/switch/openccu-loom/abc/config"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Fatalf("[%d] got %q want %q", i, c.got, c.want)
		}
	}
}

func TestTopicBuilderSanitizesDisallowedChars(t *testing.T) {
	tb := NewTopicBuilder("gh")
	got := tb.DataPointState("ccu", "HmIP/RF", "000+A", 1, "STA#TE")
	want := "gh/status/ccu/HmIP_RF/000_A/1/values/STA_TE"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// --- bridge ---

type mockPublisher struct {
	mu   sync.Mutex
	sent []publishRecord
	err  error
	// onPublish runs inside Publish, before the record is kept, so a test
	// can observe the bridge's state at the moment the broker would be
	// fanning the message out to its subscribers.
	onPublish func(topic string)
	// afterPublish runs on the caller's goroutine once the write has been
	// recorded and the lock released — the window a recorder cannot see. The
	// bridge's retained-topic index insert and its counter increment happen
	// there, after the client call and on the same goroutine, so a worker
	// descheduled in it looks finished on the broker while the plane is not.
	// Tests set it to make that window wide and certain instead of rare and
	// load-dependent. The same knob [observedPlane] carries, for the fixtures
	// built on this recorder.
	afterPublish func()
}

type publishRecord struct {
	topic   string
	payload string
	qos     QoS
	retain  bool
}

func (m *mockPublisher) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	if m.err != nil {
		return m.err
	}
	if m.onPublish != nil {
		m.onPublish(topic)
	}
	m.mu.Lock()
	m.sent = append(m.sent, publishRecord{topic: topic, payload: string(payload), qos: qos, retain: retain})
	after := m.afterPublish
	m.mu.Unlock()
	if after != nil {
		after()
	}
	return nil
}

// recorded returns a copy of the publishes seen so far, under the lock they
// are appended with — a test that reads the slice directly races the
// publishers that hand their writes to a worker goroutine.
func (m *mockPublisher) recorded() []publishRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]publishRecord(nil), m.sent...)
}

// reset forgets every recorded publish, so a test can seed the bridge's
// declared set — which now costs a real publish, the runtime records only
// what the broker accepted — and still assert on what happens next.
func (m *mockPublisher) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}

func newTestBridge(t *testing.T, opts ...func(*BridgeConfig)) (*Bridge, *mockPublisher) {
	t.Helper()
	cfg := BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}
	for _, o := range opts {
		o(&cfg)
	}
	pub := &mockPublisher{}
	b := NewBridge(cfg, pub)
	return b, pub
}

func TestBridgePublishState(t *testing.T) {
	b, pub := newTestBridge(t)
	err := b.PublishState(context.Background(), Event{
		Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1, Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Value: true,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// PublishState no longer writes a raw per-DP state topic directly —
	// that moved to PublishSlotState (called by the EventBridge).
	// PublishState only emits HA-Discovery (when enabled) and the legacy
	// alias mirror (when wired). Verify no raw-plane "000A/1/values/STATE"
	// topic was published by PublishState alone.
	for _, s := range pub.sent {
		if s.topic == "openccu-loom/ccu-01/HmIP-RF/000A/1/values/STATE" {
			t.Fatalf("PublishState must not write raw per-DP state topic directly; got topic=%s", s.topic)
		}
	}
	// When HADiscoveryEnabled is true (set in newTestBridge), a discovery
	// config topic should be published.
	if len(pub.sent) == 0 {
		t.Fatalf("expected at least one discovery publish from PublishState")
	}
	for _, s := range pub.sent {
		if !startsWith(s.topic, "homeassistant/") {
			t.Fatalf("expected only homeassistant/ topics from PublishState; got %s", s.topic)
		}
	}
}

func TestBridgeAnnounceOnline(t *testing.T) {
	b, pub := newTestBridge(t)
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("announce: %v", err)
	}
	// AnnounceOnline publishes the instance level and the retained
	// `info` document (ADR 0083). Find each by topic instead of relying on
	// it being the last record.
	var statusRec, infoRec *publishRecord
	for i := range pub.sent {
		switch pub.sent[i].topic {
		case "openccu-loom/connected":
			statusRec = &pub.sent[i]
		case "openccu-loom/info":
			infoRec = &pub.sent[i]
		}
	}
	if statusRec == nil || statusRec.payload != "1" || !statusRec.retain {
		t.Fatalf("connected missing/wrong: %+v", statusRec)
	}
	if infoRec == nil || !strings.Contains(infoRec.payload, `"spec":"2.0"`) || !infoRec.retain {
		t.Fatalf("info missing/wrong: %+v", infoRec)
	}
}

// TestAnnounceOnlinePublishesConnectedLevelAndInfo pins the two instance
// topics every (re)connect republishes (ADR 0083): `<base>/connected` at the
// level the bridge last set — 1 until a central is reachable — and the
// retained `<base>/info` document, which folds what `bridge/health` carried:
// the live central list resolved per publish, the build commit, and the
// fields the shared instance publisher answers itself (`name`, `spec`,
// `maintenance`, …).
//
// Falsifiability: drop the instance announce from [Bridge.AnnounceOnline]
// and the info arm fails; capture the central list at construction and the
// second announce still names one central.
func TestAnnounceOnlinePublishesConnectedLevelAndInfo(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	centrals := make([]string, 0, 2)
	centrals = append(centrals, "GoOtto")
	var mu sync.Mutex
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom",
		CentralNamesSupplier: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), centrals...)
		},
	}, pub)
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if got := lastPublishedOn(pub, "openccu-loom/connected"); got != "1" {
		t.Fatalf("connected = %q, want 1 before any central is reachable", got)
	}
	info := lastPublishedOn(pub, "openccu-loom/info")
	var doc map[string]any
	if err := json.Unmarshal([]byte(info), &doc); err != nil {
		t.Fatalf("info is not JSON: %q (%v)", info, err)
	}
	for key, want := range map[string]any{"name": "openccu-loom", "spec": "2.0", "maintenance": true} {
		if doc[key] != want {
			t.Errorf("info.%s = %v, want %v", key, doc[key], want)
		}
	}
	for _, key := range []string{"version", "go", "pid", "started", "commit"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("info lacks %q: %s", key, info)
		}
	}
	if _, ok := doc["status"]; ok {
		t.Errorf("info carries the redundant bridge/health `status` field: %s", info)
	}

	mu.Lock()
	centrals = append(centrals, "Zweite")
	mu.Unlock()
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if got := lastPublishedOn(pub, "openccu-loom/info"); !strings.Contains(got, `"centrals":["GoOtto","Zweite"]`) {
		t.Errorf("info after a central was adopted = %s, want both centrals", got)
	}
}

func TestBridgeRespectsPlanesDisabled(t *testing.T) {
	b, pub := newTestBridge(t, func(c *BridgeConfig) {
		c.RawEnabled = false
		c.HADiscoveryEnabled = false
	})
	_ = b.PublishState(context.Background(), Event{Parameter: "STATE", Value: true})
	if len(pub.sent) != 0 {
		t.Fatalf("unexpected publishes: %v", pub.sent)
	}
}

func TestBridgeDiscoveryOnlyPublishedOnce(t *testing.T) {
	b, pub := newTestBridge(t, func(c *BridgeConfig) {
		c.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder(c.Base), c.CentralName)
	})
	ev := Event{Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1, Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Value: true}
	_ = b.PublishState(context.Background(), ev)
	_ = b.PublishState(context.Background(), ev)
	discovery := 0
	for _, s := range pub.sent {
		if startsWith(s.topic, "homeassistant/") {
			discovery++
		}
	}
	if discovery != 1 {
		t.Fatalf("discovery publishes=%d, want 1", discovery)
	}
}

func TestBridgePropagatesError(t *testing.T) {
	pub := &mockPublisher{err: errors.New("broker down")}
	// HADiscoveryEnabled triggers the discovery publish path, which now
	// calls pub.Publish and returns the error. Without HADiscoveryEnabled
	// PublishState is a no-op (raw per-DP publish moved to PublishSlotState).
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, HADiscoveryEnabled: true}, pub)
	err := b.PublishState(context.Background(), Event{
		Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1, Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Value: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- discovery payloads ---

// TestDiscoveryBuilderReadOnlyStateBecomesBinarySensor pins the
// HmIP-PSM-2 ch2 case: STATE is a read-only relay-status output
// (operator drives the switch from ch3-5). The classifier maps STATE
// → switch by default, but ev.Writable=false flips it to
// binary_sensor so HA renders a status entity instead of a non-
// functional switch that throws RPC errors on toggle.
func TestDiscoveryBuilderReadOnlyStateBecomesBinarySensor(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	component, _, _, _, ok := db.Build(Event{
		Interface: "HmIP-RF", DeviceAddress: "0034DF2991C3E4", ChannelNo: 2,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, DeviceName: "Steckdose", Model: "HmIP-PSM-2",
		Writable: false, // read-only on ch2.
	})
	if !ok {
		t.Fatal("classifier rejected STATE")
	}
	if component != "binary_sensor" {
		t.Fatalf("component=%q want binary_sensor (read-only STATE)", component)
	}
}

func TestDiscoveryBuilderSwitchPayload(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	component, _, objectID, payload, ok := db.Build(Event{
		Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, DeviceName: "Flur Licht", Model: "HmIP-PS",
		Writable: true, // writable wire DP — STATE must classify as switch
	})
	if !ok {
		t.Fatal("should be classifiable")
	}
	if component != "switch" {
		t.Fatalf("component=%s", component)
	}
	if objectID != "1_state" {
		t.Fatalf("objectID=%s", objectID)
	}
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["state_topic"] != "gh/status/ccu/HmIP-RF/000A/1/values/STATE" {
		t.Fatalf("state_topic=%v", doc["state_topic"])
	}
	if doc["command_topic"] != "gh/set/ccu/HmIP-RF/000A/1/values/STATE" {
		t.Fatalf("command_topic=%v", doc["command_topic"])
	}
	if doc["payload_on"] != "true" || doc["payload_off"] != "false" {
		t.Fatalf("payload_on/off: %+v", doc)
	}
	if dev, _ := doc["device"].(map[string]any); dev["model"] != "HmIP-PS" {
		t.Fatalf("device descriptor: %+v", doc["device"])
	}
}

func TestDiscoveryBuilderSensorWithUnit(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, payload, ok := db.Build(Event{Interface: "HmIP-RF", DeviceAddress: "000A", ChannelNo: 1, Parameter: "ACTUAL_TEMPERATURE", Category: hmenum.DataPointCategorySensor, Descriptor: &pload.GenericConfig{Unit: "°C"}})
	if !ok {
		t.Fatal("expected classification")
	}
	var doc map[string]any
	_ = json.Unmarshal(payload, &doc)
	if doc["unit_of_measurement"] != "°C" {
		t.Fatalf("unit: %+v", doc["unit_of_measurement"])
	}
}

func TestDiscoveryBuilderFallsThrough(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, _, ok := db.Build(Event{Parameter: "SOMETHING_UNKNOWN"})
	if ok {
		t.Fatal("unknown parameter must not classify")
	}
}

// --- renderValue ---

func TestRenderValuePrimitives(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "true"},
		{false, "false"},
		{"hello", "hello"},
		{42, "42"},
		{int64(-3), "-3"},
		{3.25, "3.25"},
	}
	for _, c := range cases {
		got, err := renderValue(c.in)
		if err != nil {
			t.Fatalf("render %v: %v", c.in, err)
		}
		if string(got) != c.want {
			t.Fatalf("render %v got %q want %q", c.in, string(got), c.want)
		}
	}
}

func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// statusEnvelope is an mqtt-smarthome 2.0 status object as it reaches the
// broker (ADR 0083): `val`, the integer millisecond `ts` and `lc`, and this
// daemon's optional `hm` extension.
type statusEnvelope struct {
	Val json.RawMessage `json:"val"`
	TS  *int64          `json:"ts"`
	LC  *int64          `json:"lc"`
	HM  json.RawMessage `json:"hm"`
}

// decodeStatus parses payload as a status object and reports whether it is
// one: a JSON object with `val`, and integer `ts` and `lc` with lc <= ts.
func decodeStatus(payload string) (statusEnvelope, bool) {
	var env statusEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return env, false
	}
	if env.Val == nil || env.TS == nil || env.LC == nil || *env.LC > *env.TS {
		return env, false
	}
	return env, true
}

// statusVal returns a status object's `val` for comparison: a JSON string
// unquoted, anything else as its JSON literal. It returns "" for a payload
// that is not a status object, so an empty retraction never compares equal
// to a value.
func statusVal(payload string) string {
	env, ok := decodeStatus(payload)
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(env.Val, &s); err == nil {
		return s
	}
	return string(env.Val)
}
