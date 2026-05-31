// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
		{tb.BridgeStatus(), "openccu-loom/bridge/status"},
		{tb.DeviceAvailability("ccu", "HmIP-RF", "000A"), "openccu-loom/ccu/HmIP-RF/000A/availability"},
		{tb.DataPointState("ccu", "HmIP-RF", "000A", 1, "STATE"), "openccu-loom/ccu/HmIP-RF/000A/1/values/STATE"},
		{tb.DataPointCommand("ccu", "HmIP-RF", "000A", 1, "STATE"), "openccu-loom/ccu/HmIP-RF/000A/1/values/STATE/set"},
		{tb.DataPointEvent("ccu", "HmIP-RF", "000A", 1, "keypress"), "openccu-loom/ccu/HmIP-RF/000A/1/event/keypress"},
		{tb.HubStatus("ccu"), "openccu-loom/ccu/hub/status"},
		{tb.HubInfo("ccu"), "openccu-loom/ccu/hub/info"},
		{tb.HubDiagnostics("ccu"), "openccu-loom/ccu/hub/diagnostics"},
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
	want := "gh/ccu/HmIP_RF/000_A/1/values/STA_TE"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// --- bridge ---

type mockPublisher struct {
	mu   sync.Mutex
	sent []publishRecord
	err  error
}

type publishRecord struct {
	topic   string
	payload string
	qos     QoS
	retain  bool
}

func (m *mockPublisher) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, publishRecord{topic: topic, payload: string(payload), qos: qos, retain: retain})
	return nil
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
	// AnnounceOnline also publishes a one-shot
	// `bridge/health` snapshot. Find each by topic instead of
	// relying on it being the last record.
	var statusRec, healthRec *publishRecord
	for i := range pub.sent {
		switch pub.sent[i].topic {
		case "openccu-loom/bridge/status":
			statusRec = &pub.sent[i]
		case "openccu-loom/bridge/health":
			healthRec = &pub.sent[i]
		}
	}
	if statusRec == nil || statusRec.payload != "online" || !statusRec.retain {
		t.Fatalf("bridge/status missing/wrong: %+v", statusRec)
	}
	if healthRec == nil || !strings.Contains(healthRec.payload, `"status":"online"`) || !healthRec.retain {
		t.Fatalf("bridge/health missing/wrong: %+v", healthRec)
	}
}

// TestAnnounceOnlineHealthSupplierMergedAndStatusProtected verifies
// that HealthSupplier values appear in the bridge/health body and
// that the supplier cannot override the authoritative `status` field.
func TestAnnounceOnlineHealthSupplierMergedAndStatusProtected(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom",
		HealthSupplier: func() map[string]any {
			return map[string]any{
				"version":  "1.2.3",
				"centrals": []string{"GoOtto"},
				"status":   "DEFINITELY_NOT_AUTHORITATIVE",
			}
		},
	}, pub)
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("announce: %v", err)
	}
	var healthRec *publishRecord
	for i := range pub.sent {
		if pub.sent[i].topic == "openccu-loom/bridge/health" {
			healthRec = &pub.sent[i]
		}
	}
	if healthRec == nil {
		t.Fatal("bridge/health missing")
	}
	if !strings.Contains(healthRec.payload, `"version":"1.2.3"`) {
		t.Fatalf("supplier field missing: %s", healthRec.payload)
	}
	if !strings.Contains(healthRec.payload, `"centrals":["GoOtto"]`) {
		t.Fatalf("supplier list missing: %s", healthRec.payload)
	}
	if !strings.Contains(healthRec.payload, `"status":"online"`) {
		t.Fatalf("status not protected: %s", healthRec.payload)
	}
	if strings.Contains(healthRec.payload, "DEFINITELY_NOT_AUTHORITATIVE") {
		t.Fatalf("supplier shadowed authoritative status: %s", healthRec.payload)
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
	if doc["state_topic"] != "gh/ccu/HmIP-RF/000A/1/values/STATE" {
		t.Fatalf("state_topic=%v", doc["state_topic"])
	}
	if doc["command_topic"] != "gh/ccu/HmIP-RF/000A/1/values/STATE/set" {
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
		{nil, ""},
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
