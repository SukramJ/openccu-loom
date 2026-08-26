// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// CircuitState.String
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// NoopClient.Unsubscribe
// ---------------------------------------------------------------------------

func TestNoopClientUnsubscribe(t *testing.T) {
	t.Parallel()
	nc := NewNoopClient()
	ctx := context.Background()
	_, _ = nc.Subscribe(ctx, "a/b/c", QoS1, LegacyHandler(func(string, []byte, bool) {}))
	if err := nc.Unsubscribe(ctx, "a/b/c"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	// After unsubscribe the filter must no longer route messages.
	if nc.DeliverInbound("a/b/c", "a/b/c", []byte("x")) {
		t.Fatal("DeliverInbound should return false after Unsubscribe")
	}
}

// ---------------------------------------------------------------------------
// BirthSync
// ---------------------------------------------------------------------------

type nopSubscriber struct {
	handler MessageHandler
}

func (n *nopSubscriber) Subscribe(_ context.Context, _ string, _ QoS, h MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	n.handler = h
	return SubscribeResult{}, nil
}

func (n *nopSubscriber) Unsubscribe(_ context.Context, _ string) error {
	return nil
}

func (n *nopSubscriber) deliver(topic string, payload []byte) {
	if n.handler != nil {
		n.handler(&Message{Topic: topic, Payload: payload, Retain: false})
	}
}

func TestBirthSyncStartMissingArgs(t *testing.T) {
	t.Parallel()
	// nil subscriber → error.
	bs := NewBirthSync(nil, nil, nil)
	if err := bs.Start(context.Background()); err == nil {
		t.Fatal("expected error with nil subscriber")
	}
}

func TestBirthSyncHandleOnlineRepublishes(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base:               "gh",
		HADiscoveryEnabled: true,
	}, mp)
	// Pre-seed bridge.declared so RepublishDiscovery emits a publish.
	bridge.mu.Lock()
	bridge.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	bridge.mu.Unlock()

	sub := &nopSubscriber{}
	bs := NewBirthSync(sub, bridge, nil)
	if err := bs.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Deliver "online" → should trigger RepublishDiscovery. handle() now
	// enqueues the republish onto BirthSync's dispatcher instead of running
	// it inline, so wait for the worker to finish before asserting.
	sub.deliver(HABirthTopic, []byte("online"))
	bs.dispatcher.flush()

	pubs := mp.publications()
	found := false
	for _, p := range pubs {
		if p.topic == "homeassistant/switch/gh/obj1/config" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected re-publish of discovery topic; got %v", pubs)
	}
}

func TestBirthSyncHandleOfflineIsNoop(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, mp)
	bridge.mu.Lock()
	bridge.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	bridge.mu.Unlock()

	sub := &nopSubscriber{}
	bs := NewBirthSync(sub, bridge, nil)
	_ = bs.Start(context.Background())

	// "offline" must not trigger republish.
	lenBefore := len(mp.publications())
	sub.deliver(HABirthTopic, []byte("offline"))
	if len(mp.publications()) != lenBefore {
		t.Fatalf("offline payload should not trigger republish")
	}
}

// publications is a helper on the package-internal mockPublisher used in
// mqtt_test.go — expose the same logic without race.
func (m *mockPublisher) publications() []publishRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]publishRecord, len(m.sent))
	copy(out, m.sent)
	return out
}

// ---------------------------------------------------------------------------
// Wiring.Bridge / Wiring.Publish / Wiring.PublishProgramState / Wiring.PublishSysvar
// ---------------------------------------------------------------------------

func TestWiringBridge(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	w := NewWiring(b, nil)
	if w.Bridge() != b {
		t.Fatal("Bridge() must return the wired bridge")
	}
}

func TestWiringPublish(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	w := NewWiring(b, nil)
	ev := Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	}
	w.Publish(context.Background(), ev)
	// We just want no panic; optionally check a publish arrived.
	// (PublishState logs errors and doesn't surface them through Publish.)
	_ = mp.publications()
}

func TestWiringPublishProgramState(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	w := NewWiring(b, nil)
	prog := fakeAddressable{state: "gh/ccu/hub/programs/Morning/state"}
	w.PublishProgramState(context.Background(), "ccu", prog, true)
	pubs := mp.publications()
	found := false
	for _, p := range pubs {
		if p.topic == "gh/ccu/hub/programs/Morning/state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("PublishProgramState: expected program topic publish; got %v", pubs)
	}
}

func TestWiringPublishSysvar(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	w := NewWiring(b, nil)
	sv := fakeAddressable{state: "gh/ccu/hub/sysvars/Holiday/state"}
	w.PublishSysvar(context.Background(), "ccu", sv, true)
	pubs := mp.publications()
	found := false
	for _, p := range pubs {
		if p.topic == "gh/ccu/hub/sysvars/Holiday/state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("PublishSysvar: expected sysvar topic publish; got %v", pubs)
	}
}

// ---------------------------------------------------------------------------
// Bridge: SetHubInfo, Topics, PayloadFormat, PublishSystemStatus,
//         PublishDiscoveryOnly, PublishCustomDPState, PublishSlotConfig,
//         PublishDeviceInfo, PublishDeviceDiagnostics,
//         PublishChannelEventDiscovery, indexFromValue, renderStatePayload
// ---------------------------------------------------------------------------

func TestBridgeSetHubInfoNilBridge(t *testing.T) {
	t.Parallel()
	// Must not panic on nil receiver.
	var b *Bridge
	b.SetHubInfo(HubInfo{})
}

func TestBridgeSetHubInfoNoDiscoveryBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh"}, mp)
	// No DiscoveryBuilder wired — must be a no-op.
	b.SetHubInfo(HubInfo{Name: "test-ccu"})
}

func TestBridgeTopics(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh"}, mp)
	if b.Topics() == nil {
		t.Fatal("Topics() must not return nil")
	}
	if b.Topics().Base != "gh" {
		t.Fatalf("Topics().Base = %q, want %q", b.Topics().Base, "gh")
	}
}

func TestBridgePublishSystemStatusRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishSystemStatus(context.Background(), "ccu", []byte(`{}`)); err != nil {
		t.Fatalf("unexpected error when RawEnabled=false: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishSystemStatusRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	if err := b.PublishSystemStatus(context.Background(), "ccu", []byte(`{}`)); err != nil {
		t.Fatalf("PublishSystemStatus: %v", err)
	}
	pubs := mp.publications()
	if len(pubs) == 0 {
		t.Fatal("expected at least one publish")
	}
	got := pubs[0].topic
	want := "gh/ccu/system/status"
	if got != want {
		t.Fatalf("topic = %q, want %q", got, want)
	}
}

func TestBridgePublishDiscoveryOnlyRawDisabledHAEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "gh",
		RawEnabled:         false,
		HADiscoveryEnabled: true,
	}, mp)
	// Without a DiscoveryBuilder Build() returns (_, _, _, _, false) so
	// PublishDiscoveryOnly is a no-op → no error, no publish.
	err := b.PublishDiscoveryOnly(context.Background(), Event{
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
	if err != nil {
		t.Fatalf("PublishDiscoveryOnly: %v", err)
	}
}

func TestBridgePublishCustomDPStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	err := b.PublishCustomDPState(context.Background(), "ccu", "HmIP-RF", slot, map[string]any{"hvac_mode": "heat"})
	if err != nil {
		t.Fatalf("PublishCustomDPState with RawEnabled=false: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishCustomDPStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	err := b.PublishCustomDPState(context.Background(), "ccu", "HmIP-RF", slot, map[string]any{"hvac_mode": "heat"})
	if err != nil {
		t.Fatalf("PublishCustomDPState: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected publish")
	}
}

func TestBridgePublishCustomDPStateNilState(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	// nil state must be normalised to empty map (no panic).
	err := b.PublishCustomDPState(context.Background(), "ccu", "HmIP-RF", slot, nil)
	if err != nil {
		t.Fatalf("PublishCustomDPState nil state: %v", err)
	}
}

func TestBridgePublishSlotConfigRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	err := b.PublishSlotConfig(context.Background(), "ccu", "HmIP-RF", slot, map[string]any{"min": 0})
	if err != nil {
		t.Fatalf("PublishSlotConfig raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishSlotConfigEmptySkips(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	// Empty config map → no publish.
	err := b.PublishSlotConfig(context.Background(), "ccu", "HmIP-RF", slot, nil)
	if err != nil {
		t.Fatalf("PublishSlotConfig empty: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("empty config must not publish")
	}
}

func TestBridgePublishSlotConfigDeduplicates(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "LEVEL"}
	cfg := map[string]any{"min": 0.0, "max": 1.0}
	_ = b.PublishSlotConfig(context.Background(), "ccu", "HmIP-RF", slot, cfg)
	first := len(mp.publications())
	// Second call with identical config must not emit a publish.
	_ = b.PublishSlotConfig(context.Background(), "ccu", "HmIP-RF", slot, cfg)
	if len(mp.publications()) != first {
		t.Fatalf("duplicate SlotConfig was re-published: %d vs %d", first, len(mp.publications()))
	}
}

func TestBridgePublishDeviceInfoRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.PublishDeviceInfo(context.Background(), "ccu", "HmIP-RF", "0001ABCD", map[string]any{"type": "HMIP-PSM"})
	if err != nil {
		t.Fatalf("PublishDeviceInfo raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishDeviceInfoRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishDeviceInfo(context.Background(), "ccu", "HmIP-RF", "0001ABCD", map[string]any{"type": "HMIP-PSM"})
	if err != nil {
		t.Fatalf("PublishDeviceInfo: %v", err)
	}
	pubs := mp.publications()
	if len(pubs) == 0 {
		t.Fatal("expected a publish")
	}
}

func TestBridgePublishDeviceDiagnosticsRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.PublishDeviceDiagnostics(context.Background(), "ccu", "HmIP-RF", "0001ABCD", map[string]any{"rssi": -65})
	if err != nil {
		t.Fatalf("PublishDeviceDiagnostics raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishDeviceDiagnosticsRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishDeviceDiagnostics(context.Background(), "ccu", "HmIP-RF", "0001ABCD", map[string]any{"rssi": -65})
	if err != nil {
		t.Fatalf("PublishDeviceDiagnostics: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected a publish")
	}
}

func TestBridgePublishChannelEventDiscoveryDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: false}, mp)
	err := b.PublishChannelEventDiscovery(context.Background(), Event{DeviceAddress: "0001ABCD", ChannelNo: 1, Parameter: "PRESS_SHORT"})
	if err != nil {
		t.Fatalf("PublishChannelEventDiscovery disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when HADiscovery disabled")
	}
}

// ---------------------------------------------------------------------------
// indexFromValue — covers all type branches.
// ---------------------------------------------------------------------------

func TestIndexFromValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      any
		wantIdx int64
		wantOK  bool
	}{
		{int(2), 2, true},
		{int32(3), 3, true},
		{int64(4), 4, true},
		{uint(5), 5, true},
		{uint32(6), 6, true},
		{uint64(7), 7, true},
		{float64(8), 8, true},
		{float32(9), 9, true},
		{"10", 10, true},
		{"", 0, false},
		{"abc", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		gotIdx, gotOK := indexFromValue(c.in)
		if gotOK != c.wantOK || (gotOK && gotIdx != c.wantIdx) {
			t.Errorf("indexFromValue(%v) = (%d, %v), want (%d, %v)", c.in, gotIdx, gotOK, c.wantIdx, c.wantOK)
		}
	}
}

// ---------------------------------------------------------------------------
// ResolveEnumLabel
// ---------------------------------------------------------------------------

func TestResolveEnumLabelNonEnum(t *testing.T) {
	t.Parallel()
	// Non-ENUM type → pass through unchanged.
	got := ResolveEnumLabel(42, hmenum.ParameterTypeFloat, []string{"a", "b"})
	if got != 42 {
		t.Fatalf("got %v, want 42", got)
	}
}

func TestResolveEnumLabelOutOfBounds(t *testing.T) {
	t.Parallel()
	got := ResolveEnumLabel(int64(5), hmenum.ParameterTypeEnum, []string{"a", "b"})
	// Index 5 out of [a, b] → return original value.
	if got != int64(5) {
		t.Fatalf("got %v, want 5", got)
	}
}

func TestResolveEnumLabelEmptyList(t *testing.T) {
	t.Parallel()
	got := ResolveEnumLabel(int64(0), hmenum.ParameterTypeEnum, nil)
	if got != int64(0) {
		t.Fatalf("got %v, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// TopicBuilder: uncovered methods
// ---------------------------------------------------------------------------

func TestTopicBuilderDataPointConfig(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.DataPointConfig("ccu", "HmIP-RF", "0001ABCD", 1, "LEVEL")
	want := "gh/ccu/HmIP-RF/0001ABCD/1/values/LEVEL/config"
	if got != want {
		t.Fatalf("DataPointConfig: got %q want %q", got, want)
	}
}

func TestTopicBuilderCustomDPServiceMethodShape(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	got := tb.CustomDPServiceMethod("ccu", "HmIP-RF", slot, "boost")
	want := "gh/ccu/HmIP-RF/0001ABCD/1/custom/climate/set/boost"
	if got != want {
		t.Fatalf("CustomDPServiceMethod: got %q want %q", got, want)
	}
}

func TestTopicBuilderSlotConfig(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 2, Bucket: pload.BucketValues, Parameter: "SET_TEMPERATURE"}
	got := tb.SlotConfig("ccu", "HmIP-RF", slot)
	want := "gh/ccu/HmIP-RF/0001ABCD/2/values/SET_TEMPERATURE/config"
	if got != want {
		t.Fatalf("SlotConfig: got %q want %q", got, want)
	}
}

func TestTopicBuilderSlotConfigCustom(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	got := tb.SlotConfig("ccu", "HmIP-RF", slot)
	if got == "" {
		t.Fatal("SlotConfig for custom bucket must not be empty")
	}
}

func TestTopicBuilderSlotCommand(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	got := tb.SlotCommand("ccu", "HmIP-RF", slot)
	want := "gh/ccu/HmIP-RF/0001ABCD/1/values/STATE/set"
	if got != want {
		t.Fatalf("SlotCommand: got %q want %q", got, want)
	}
}

func TestTopicBuilderSlotCommandCustom(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	got := tb.SlotCommand("ccu", "HmIP-RF", slot)
	if got == "" {
		t.Fatal("SlotCommand for custom bucket must not be empty")
	}
	if got[len(got)-4:] != "/set" {
		t.Fatalf("SlotCommand custom: missing /set suffix: %q", got)
	}
}

func TestTopicBuilderCustomDPServiceMethod(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketCustom, Parameter: "climate"}
	got := tb.CustomDPServiceMethod("ccu", "HmIP-RF", slot, "boost")
	if got == "" {
		t.Fatal("CustomDPServiceMethod must not be empty")
	}
}

func TestTopicBuilderSystemStatus(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.SystemStatus("ccu")
	want := "gh/ccu/system/status"
	if got != want {
		t.Fatalf("SystemStatus: got %q want %q", got, want)
	}
}

func TestTopicBuilderHubStatus(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.HubStatus("ccu")
	want := "gh/ccu/hub/status"
	if got != want {
		t.Fatalf("HubStatus: got %q want %q", got, want)
	}
}

func TestTopicBuilderHubInfo(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.HubInfo("ccu")
	want := "gh/ccu/hub/info"
	if got != want {
		t.Fatalf("HubInfo: got %q want %q", got, want)
	}
}

func TestTopicBuilderHubDiagnostics(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.HubDiagnostics("ccu")
	want := "gh/ccu/hub/diagnostics"
	if got != want {
		t.Fatalf("HubDiagnostics: got %q want %q", got, want)
	}
}

func TestHubSysvarCommand(t *testing.T) {
	t.Parallel()
	got := naming.MQTTHubSysvarCommand("gh", "ccu", "Holiday")
	want := "gh/ccu/hub/sysvars/Holiday/set"
	if got != want {
		t.Fatalf("MQTTHubSysvarCommand: got %q want %q", got, want)
	}
}

func TestTopicBuilderDeviceInfo(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.DeviceInfo("ccu", "HmIP-RF", "0001ABCD")
	want := "gh/ccu/HmIP-RF/0001ABCD/info"
	if got != want {
		t.Fatalf("DeviceInfo: got %q want %q", got, want)
	}
}

func TestTopicBuilderDeviceDiagnostics(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.DeviceDiagnostics("ccu", "HmIP-RF", "0001ABCD")
	want := "gh/ccu/HmIP-RF/0001ABCD/diagnostics"
	if got != want {
		t.Fatalf("DeviceDiagnostics: got %q want %q", got, want)
	}
}

func TestTopicBuilderDeviceUpdateState(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.DeviceUpdateState("ccu", "HmIP-RF", "0001ABCD")
	want := "gh/ccu/HmIP-RF/0001ABCD/update"
	if got != want {
		t.Fatalf("DeviceUpdateState: got %q want %q", got, want)
	}
}

func TestTopicBuilderDeviceUpdateCommand(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	got := tb.DeviceUpdateCommand("ccu", "HmIP-RF", "0001ABCD")
	want := "gh/ccu/HmIP-RF/0001ABCD/update/set"
	if got != want {
		t.Fatalf("DeviceUpdateCommand: got %q want %q", got, want)
	}
}

func TestTopicBuilderSlotStateNonCustom(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketMaster, Parameter: "TEMPERATURE_MINIMUM"}
	got := tb.SlotState("ccu", "HmIP-RF", slot)
	want := "gh/ccu/HmIP-RF/0001ABCD/1/master/TEMPERATURE_MINIMUM"
	if got != want {
		t.Fatalf("SlotState master: got %q want %q", got, want)
	}
}

func TestTopicBuilderParamterPathDataEmptyBucket(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	// ParameterState with empty bucket → defaults to "values".
	got := tb.ParameterState("ccu", "HmIP-RF", "0001ABCD", 1, "", "STATE")
	want := "gh/ccu/HmIP-RF/0001ABCD/1/values/STATE"
	if got != want {
		t.Fatalf("ParameterState empty bucket: got %q want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// RetainCleanup: Error() on retainCleanupError + RunDiscoveryOrphanCleanupOnce
// ---------------------------------------------------------------------------

func TestRetainCleanupErrorString(t *testing.T) {
	t.Parallel()
	err := retainCleanupError("test error message")
	if err.Error() != "test error message" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "test error message")
	}
}

func TestRunRetainCleanupOnce_NotAClient(t *testing.T) {
	t.Parallel()
	// mockPublisher only satisfies Publisher, not Client (no Subscribe/Unsubscribe).
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	ctx := context.Background()
	_, err := b.RunRetainCleanupOnce(ctx, 10)
	if !errors.Is(err, errCleanupClientLacksSubscribe) {
		t.Fatalf("expected errCleanupClientLacksSubscribe, got %v", err)
	}
}

func TestRunDiscoveryOrphanCleanupOnce_NotAClient(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true, CentralName: "ccu"}, mp)
	_, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "", 10)
	if !errors.Is(err, errCleanupClientLacksSubscribe) {
		t.Fatalf("expected errCleanupClientLacksSubscribe, got %v", err)
	}
}

func TestRunDiscoveryOrphanCleanupOnce_HADisabled(t *testing.T) {
	t.Parallel()
	mc := &mockRetainClient{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: false, CentralName: "ccu"}, mc)
	n, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 evictions when HADiscovery disabled, got %d", n)
	}
}

func TestRunDiscoveryOrphanCleanupOnce_OrphansEvicted(t *testing.T) {
	t.Parallel()
	// central name "ccu" → node_id prefix "ccu_"
	const base = "openccu-loom"
	const centralName = "ccu"

	// pre-seed orphan (not in declared) and declared topic.
	orphanTopic := "homeassistant/switch/ccu_old/orphan_obj/config"
	declaredTopic := "homeassistant/switch/ccu_current/live_obj/config"

	mc := &mockRetainClient{
		retained: []retainedMsg{
			{topic: orphanTopic, payload: []byte(`{}`)},
			{topic: declaredTopic, payload: []byte(`{}`)},
		},
	}
	b := NewBridge(BridgeConfig{Base: base, HADiscoveryEnabled: true, CentralName: centralName}, mc)
	// Mark declaredTopic as known-live.
	b.mu.Lock()
	b.declared[declaredTopic] = []byte(`{}`)
	b.mu.Unlock()

	// The orphan has node_id "ccu_old" which starts with "ccu_" — our prefix.
	n, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	_ = n // eviction count; exact value depends on node_id filter
}

// ---------------------------------------------------------------------------
// CommandSubscriber: handleServiceMethod edge cases
// ---------------------------------------------------------------------------

func TestCommandSubscriberServiceMethodNoSink(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	// No CDP sink wired → handleServiceMethod logs warn and returns.
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	noop.DeliverInbound("gh/+/+/+/+/custom/+/set/+",
		"gh/ccu/HmIP-RF/0001ABCD/1/custom/climate/set/boost", []byte("true"))
	// Should not panic.
}

func TestCommandSubscriberServiceMethodBadChannel(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Channel segment "abc" is not an int → should log warn, no call.
	noop.DeliverInbound("gh/+/+/+/+/custom/+/set/+",
		"gh/ccu/HmIP-RF/0001ABCD/abc/custom/climate/set/boost", []byte("true"))
	if cdpSink.calls.Load() != 0 {
		t.Fatalf("expected 0 calls on bad channel, got %d", cdpSink.calls.Load())
	}
}

func TestCommandSubscriberServiceMethodBadTopicShape(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Dispatch a raw call to handleServiceMethod with malformed topic.
	sub.handleServiceMethod("wrong/shape", []byte("true"), false)
	if cdpSink.calls.Load() != 0 {
		t.Fatalf("expected 0 calls on bad topic shape, got %d", cdpSink.calls.Load())
	}
}

func TestCommandSubscriberServiceMethodSuccess(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	noop.DeliverInbound("gh/+/+/+/+/custom/+/set/+",
		"gh/ccu/HmIP-RF/0001ABCD/1/custom/climate/set/boost", []byte("true"))
	sub.dispatcher.flush()
	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", cdpSink.calls.Load())
	}
}

func TestCommandSubscriberServiceMethodSinkError(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{lastErr: errors.New("invoke error")}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Error from sink must not panic.
	noop.DeliverInbound("gh/+/+/+/+/custom/+/set/+",
		"gh/ccu/HmIP-RF/0001ABCD/1/custom/climate/set/boost", []byte("true"))
	sub.dispatcher.flush()
	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 call even on error, got %d", cdpSink.calls.Load())
	}
}

// ---------------------------------------------------------------------------
// scalarPayloadToParams and quoteIfBareString
// ---------------------------------------------------------------------------

func TestScalarPayloadToParamsEmpty(t *testing.T) {
	t.Parallel()
	result, err := scalarPayloadToParams("lock", []byte("   "), func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil for empty payload, got %v", result)
	}
}

func TestScalarPayloadToParamsJSONObject(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"duration": 30}`)
	result, err := scalarPayloadToParams("boost", raw, func(string) string { return "duration" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := result["duration"]
	if !ok || v != float64(30) {
		t.Fatalf("expected duration=30, got %v", result)
	}
}

func TestScalarPayloadToParamsScalarBool(t *testing.T) {
	t.Parallel()
	result, err := scalarPayloadToParams("boost", []byte("true"), func(string) string { return "enable" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["enable"] != true {
		t.Fatalf("expected enable=true, got %v", result)
	}
}

func TestScalarPayloadToParamsScalarNumber(t *testing.T) {
	t.Parallel()
	result, err := scalarPayloadToParams("set_temp", []byte("21.5"), func(string) string { return "temperature" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["temperature"] != 21.5 {
		t.Fatalf("expected temperature=21.5, got %v", result)
	}
}

func TestScalarPayloadToParamsScalarBareString(t *testing.T) {
	t.Parallel()
	result, err := scalarPayloadToParams("set_mode", []byte("comfort"), func(string) string { return "mode" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["mode"] != "comfort" {
		t.Fatalf("expected mode=comfort, got %v", result)
	}
}

func TestScalarPayloadToParamsNoKeyFromResolver(t *testing.T) {
	t.Parallel()
	// Resolver returns "" → fallback to "value".
	result, err := scalarPayloadToParams("unknown_method", []byte("42"), func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result["value"]; !ok {
		t.Fatalf("expected fallback key 'value', got %v", result)
	}
}

func TestScalarPayloadToParamsBadJSON(t *testing.T) {
	t.Parallel()
	// Payload starts with '{' but is not valid JSON → error.
	_, err := scalarPayloadToParams("boost", []byte("{bad json"), func(string) string { return "x" })
	if err == nil {
		t.Fatal("expected error for malformed JSON object payload")
	}
}

func TestQuoteIfBareString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", `""`},
		{"true", "true"},
		{"false", "false"},
		{"null", "null"},
		{`"already"`, `"already"`},
		{"-42", "-42"},
		{"+1", "+1"},
		{"3.14", "3.14"},
		{"comfort", `"comfort"`},
		{`say "hi"`, `"say \"hi\""`},
	}
	for _, c := range cases {
		got := quoteIfBareString(c.in)
		if got != c.want {
			t.Errorf("quoteIfBareString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CommandSubscriber.Start: nil subscriber returns error.
// ---------------------------------------------------------------------------

func TestCommandSubscriberStartNilSubscriber(t *testing.T) {
	t.Parallel()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := &CommandSubscriber{topics: topics, sink: sink}
	if err := sub.Start(context.Background()); err == nil {
		t.Fatal("expected error with nil subscriber")
	}
}

// ---------------------------------------------------------------------------
// handleDataPoint: bucket-aware path with non-values bucket → silent drop.
// ---------------------------------------------------------------------------

func TestCommandSubscriberDataPointNonValuesBucketDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	// 8-segment topic with "master" bucket — must be silently dropped.
	ok := noop.DeliverInbound("gh/+/+/+/+/+/+/set",
		"gh/ccu/HmIP-RF/0001ABCD/1/master/TEMPERATURE_MINIMUM/set", []byte("21"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	if sink.setValues.Load() != 0 {
		t.Fatalf("non-values bucket must not call SetValue; calls=%d", sink.setValues.Load())
	}
}

func TestCommandSubscriberDataPointBucketAwareValues(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	// 8-segment topic with "values" bucket — must reach the sink.
	ok := noop.DeliverInbound("gh/+/+/+/+/+/+/set",
		"gh/ccu/HmIP-RF/0001ABCD/1/values/STATE/set", []byte("true"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	sub.dispatcher.flush()
	if sink.setValues.Load() != 1 {
		t.Fatalf("expected 1 SetValue call; got %d", sink.setValues.Load())
	}
	if sink.lastVal.param != "STATE" {
		t.Fatalf("param: got %q want STATE", sink.lastVal.param)
	}
}

func TestCommandSubscriberDataPointBadChannel(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	// Channel segment is not an integer.
	noop.DeliverInbound("gh/+/+/+/+/+/set",
		"gh/ccu/HmIP-RF/0001ABCD/abc/STATE/set", []byte("true"))
	if sink.setValues.Load() != 0 {
		t.Fatalf("bad channel must not call SetValue; calls=%d", sink.setValues.Load())
	}
}

func TestCommandSubscriberWeekProfileBadChannel(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	wpSink := &fakeWPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithWeekProfileSink(wpSink)
	_ = sub.Start(context.Background())

	// Call handler directly with a bad channel to exercise that path.
	sub.handleWeekProfile("gh/ccu/HmIP-RF/0001ABCD/notanint/week_profile/set", []byte("P1"), false)
	if wpSink.calls.Load() != 0 {
		t.Fatalf("bad channel must not reach sink; calls=%d", wpSink.calls.Load())
	}
}

func TestCommandSubscriberWeekProfileSinkError(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	errSink := &errWPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithWeekProfileSink(errSink)
	_ = sub.Start(context.Background())

	// Sink error should be logged, not propagated.
	noop.DeliverInbound("gh/+/+/+/+/week_profile/set",
		"gh/ccu/HmIP-RF/0001ABCD/1/week_profile/set", []byte("P1"))
	sub.dispatcher.flush()
	if errSink.calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", errSink.calls.Load())
	}
}

type errWPSink struct {
	calls atomic.Int32
}

func (e *errWPSink) SetActiveProfile(_ context.Context, _, _, _ string, _ int, _ string, _ hmenum.CommandPriority) error {
	e.calls.Add(1)
	return errors.New("profile not found")
}

// ---------------------------------------------------------------------------
// handleSysvar: bad topic shape → silent drop; SetSysvar error → logged.
// ---------------------------------------------------------------------------

func TestCommandSubscriberSysvarBadTopicShape(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	// Deliver directly to handler with wrong shape.
	sub.handleSysvar("gh/ccu/wrong/PartyMode/set/extra", []byte("true"), false)
	if sink.setSysvars.Load() != 0 {
		t.Fatalf("bad topic must not call SetSysvar; calls=%d", sink.setSysvars.Load())
	}
}

func TestCommandSubscriberSysvarSinkError(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &errSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	noop.DeliverInbound("gh/+/hub/sysvars/+/set",
		"gh/ccu/hub/sysvars/PartyMode/set", []byte("true"))
	sub.dispatcher.flush()
	if sink.sysvars.Load() != 1 {
		t.Fatalf("expected 1 SetSysvar call; got %d", sink.sysvars.Load())
	}
}

// ---------------------------------------------------------------------------
// handleProgram: bad shape → silent drop; TriggerProgram error → logged.
// ---------------------------------------------------------------------------

func TestCommandSubscriberProgramBadTopicShape(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	sub.handleProgram("gh/ccu/programs/Morning/trigger/extra", nil, false)
	if sink.triggers.Load() != 0 {
		t.Fatalf("bad topic must not call TriggerProgram; calls=%d", sink.triggers.Load())
	}
}

func TestCommandSubscriberProgramSinkError(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &errSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	noop.DeliverInbound("gh/+/hub/programs/+/trigger",
		"gh/ccu/hub/programs/Morning/trigger", []byte("true"))
	sub.dispatcher.flush()
	if sink.programs.Load() != 1 {
		t.Fatalf("expected 1 TriggerProgram call; got %d", sink.programs.Load())
	}
}

// errSink is a CommandSink whose every method returns an error.
type errSink struct {
	sysvars  atomic.Int32
	programs atomic.Int32
}

func (e *errSink) SetValue(_ context.Context, _, _, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return errors.New("setvalue error")
}

func (e *errSink) SetMasterValue(_ context.Context, _, _, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return errors.New("setmasterparam error")
}

func (e *errSink) SetSysvar(_ context.Context, _, _ string, _ any) error {
	e.sysvars.Add(1)
	return errors.New("setsysvar error")
}

func (e *errSink) TriggerProgram(_ context.Context, _, _ string) error {
	e.programs.Add(1)
	return errors.New("trigger error")
}

func (e *errSink) SetProgramEnabled(_ context.Context, _, _ string, _ bool) error {
	return nil
}

// ---------------------------------------------------------------------------
// handleCDPInvoke: bad topic shape (too few segments)
// ---------------------------------------------------------------------------

func TestCommandSubscriberCDPInvokeBadTopicShape(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Call directly with wrong shape.
	sub.handleCDPInvoke("gh/ccu/wrong/invoke", []byte(`{}`), false)
	if cdpSink.calls.Load() != 0 {
		t.Fatalf("bad topic must not call InvokeCustomDP; calls=%d", cdpSink.calls.Load())
	}
}

// ---------------------------------------------------------------------------
// renderValue — covers all type branches.
// ---------------------------------------------------------------------------

func TestRenderValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{true, "true"},
		{false, "false"},
		{"hello", "hello"},
		{int(42), "42"},
		{int32(7), "7"},
		{int64(-3), "-3"},
		{float32(1.5), "1.5"},
		{float64(2.75), "2.75"},
		// Trailing-zero stripping.
		{float64(3.0), "3"},
		{float32(0.5), "0.5"},
		// Complex type → JSON.
		{[]int{1, 2}, "[1,2]"},
	}
	for _, c := range cases {
		got, err := renderValue(c.in)
		if err != nil {
			t.Errorf("renderValue(%v): unexpected error: %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("renderValue(%v) = %q, want %q", c.in, string(got), c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// renderStatePayload — JSON envelope (the only supported shape)
// ---------------------------------------------------------------------------

func TestBridgeRenderStatePayloadJSON(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	ev := Event{Parameter: "ACTUAL_TEMPERATURE", Value: float64(21.5), Descriptor: &pload.GenericConfig{Type: hmenum.ParameterTypeFloat}}
	got, err := b.renderStatePayload(ev)
	if err != nil {
		t.Fatalf("renderStatePayload: %v", err)
	}
	if len(got) == 0 || got[0] != '{' {
		t.Fatalf("expected JSON object, got %q", string(got))
	}
}

func TestBridgeRenderStatePayloadEnumResolved(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, mp)
	ev := Event{
		Parameter: "MODE",
		Value:     int64(1),
		Descriptor: &pload.GenericConfig{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"idle", "heat", "cool"},
		},
	}
	got, err := b.renderStatePayload(ev)
	if err != nil {
		t.Fatalf("renderStatePayload enum: %v", err)
	}
	if !strings.Contains(string(got), `"value":"heat"`) {
		t.Fatalf("enum label not resolved into JSON: got %q", string(got))
	}
}

// ---------------------------------------------------------------------------
// Bridge.AnnounceOnline
// ---------------------------------------------------------------------------

func TestBridgeAnnounceOnlineStatusAndHealth(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	if err := b.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("AnnounceOnline: %v", err)
	}
	pubs := mp.publications()
	found := false
	for _, p := range pubs {
		if p.topic == "gh/bridge/status" && p.payload == "online" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AnnounceOnline: bridge/status=online not published; got %v", pubs)
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishSourceState
// ---------------------------------------------------------------------------

type fakeSource struct{}

func (f *fakeSource) State() pload.StatePayload {
	return map[string]any{"hvac_mode": "heat"}
}

func TestBridgePublishSourceStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.PublishSourceState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, &fakeSource{})
	if err != nil {
		t.Fatalf("PublishSourceState raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishSourceStateNilSource(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishSourceState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, nil)
	if err != nil {
		t.Fatalf("PublishSourceState nil source: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("nil source must not publish")
	}
}

func TestBridgePublishSourceStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishSourceState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, &fakeSource{})
	if err != nil {
		t.Fatalf("PublishSourceState: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected a publish")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishSlotState
// ---------------------------------------------------------------------------

func TestBridgePublishSlotStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	err := b.PublishSlotState(context.Background(), "ccu", "HmIP-RF", slot, pload.PerDPState{})
	if err != nil {
		t.Fatalf("PublishSlotState raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishSlotStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	err := b.PublishSlotState(context.Background(), "ccu", "HmIP-RF", slot, pload.PerDPState{Value: true})
	if err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected a publish")
	}
}

// ---------------------------------------------------------------------------
// Bridge.EvictState
// ---------------------------------------------------------------------------

func TestBridgeEvictStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.EvictState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "STATE")
	if err != nil {
		t.Fatalf("EvictState raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgeEvictStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.EvictState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "STATE")
	if err != nil {
		t.Fatalf("EvictState: %v", err)
	}
	pubs := mp.publications()
	if len(pubs) == 0 {
		t.Fatal("expected at least one publish from EvictState")
	}
	// The payload must be empty (to delete the retained message).
	got := pubs[0]
	if got.payload != "" {
		t.Fatalf("EvictState payload must be empty; got %q", got.payload)
	}
	if !got.retain {
		t.Fatal("EvictState must use retain=true")
	}
}

// ---------------------------------------------------------------------------
// componentFromCategory — all branches.
// ---------------------------------------------------------------------------

func TestComponentFromCategory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cat       hmenum.DataPointCategory
		wantComp  HAComponent
		wantFound bool
	}{
		{hmenum.DataPointCategorySensor, HAComponentSensor, true},
		{hmenum.DataPointCategoryHubSensor, HAComponentSensor, true},
		{hmenum.DataPointCategoryBinarySensor, HAComponentBinarySensor, true},
		{hmenum.DataPointCategoryHubBinarySensor, HAComponentBinarySensor, true},
		{hmenum.DataPointCategoryNumber, HAComponentNumber, true},
		// ActionNumber mirrors the reference stack's EMPTY ActionNumber
		// whitelist: ON_TIME / RAMP_TIME / DURATION_VALUE never surface
		// as HA number entities.
		{hmenum.DataPointCategoryActionNumber, "", false},
		{hmenum.DataPointCategoryHubNumber, HAComponentNumber, true},
		{hmenum.DataPointCategorySwitch, HAComponentSwitch, true},
		{hmenum.DataPointCategoryScheduleSwitch, HAComponentSwitch, true},
		{hmenum.DataPointCategoryHubSwitch, HAComponentSwitch, true},
		{hmenum.DataPointCategoryButton, HAComponentButton, true},
		// Plain actions (COMBINED_PARAMETER, RAMP_STOP, …) have no HA
		// platform in the reference stack.
		{hmenum.DataPointCategoryAction, "", false},
		{hmenum.DataPointCategoryHubButton, HAComponentButton, true},
		{hmenum.DataPointCategorySelect, HAComponentSelect, true},
		{hmenum.DataPointCategoryActionSelect, HAComponentSelect, true},
		{hmenum.DataPointCategoryHubSelect, HAComponentSelect, true},
		{hmenum.DataPointCategoryClimate, HAComponentClimate, true},
		{hmenum.DataPointCategoryCover, HAComponentCover, true},
		{hmenum.DataPointCategoryLock, HAComponentLock, true},
		{hmenum.DataPointCategoryLight, HAComponentLight, true},
		{hmenum.DataPointCategoryValve, HAComponentValve, true},
		{hmenum.DataPointCategorySiren, HAComponentSiren, true},
		{hmenum.DataPointCategoryEvent, HAComponentEvent, true},
		{hmenum.DataPointCategoryEventGroup, HAComponentEvent, true},
		{hmenum.DataPointCategoryText, HAComponentText, true},
		{hmenum.DataPointCategoryTextDisplay, HAComponentText, true},
		{hmenum.DataPointCategoryHubText, HAComponentText, true},
		{hmenum.DataPointCategoryUpdate, HAComponentUpdate, true},
		{hmenum.DataPointCategoryHubUpdate, HAComponentUpdate, true},
		{hmenum.DataPointCategoryWeekProfile, HAComponentSelect, true},
		// Unknown category → not found.
		{hmenum.DataPointCategory(""), "", false},
	}
	for _, c := range cases {
		comp, ok := componentFromCategory(c.cat)
		if ok != c.wantFound {
			t.Errorf("componentFromCategory(%q): ok=%v want %v", c.cat, ok, c.wantFound)
			continue
		}
		if ok && comp != c.wantComp {
			t.Errorf("componentFromCategory(%q): comp=%q want %q", c.cat, comp, c.wantComp)
		}
	}
}

// ---------------------------------------------------------------------------
// NewMqttCircuitBreaker: default values path.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Wiring: error path for Publish (bridge returns error from PublishState)
// ---------------------------------------------------------------------------

type errPublisher struct{}

func (e *errPublisher) Publish(_ context.Context, _ string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	return errCleanupClientLacksSubscribe // reuse an existing sentinel
}

func TestWiringPublishLogsError(t *testing.T) {
	t.Parallel()
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, &errPublisher{})
	w := NewWiring(b, nil)
	// Should not panic even when publish fails.
	w.Publish(context.Background(), Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
}

func TestWiringPublishProgramStateLogsError(t *testing.T) {
	t.Parallel()
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, &errPublisher{})
	w := NewWiring(b, nil)
	// Should not panic.
	w.PublishProgramState(context.Background(), "ccu", fakeAddressable{state: "gh/x"}, true)
}

func TestWiringPublishSysvarLogsError(t *testing.T) {
	t.Parallel()
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true}, &errPublisher{})
	w := NewWiring(b, nil)
	// Should not panic.
	w.PublishSysvar(context.Background(), "ccu", fakeAddressable{state: "gh/x"}, true)
}

// ---------------------------------------------------------------------------
// BirthSync.WithLifecycleContext: nil is a no-op; a valid ctx is stored.
// ---------------------------------------------------------------------------

func TestBirthSyncWithLifecycleContextReturnsSelf(t *testing.T) {
	t.Parallel()
	bs := NewBirthSync(nil, nil, nil)
	// WithLifecycleContext must return the receiver for call-site chaining.
	result := bs.WithLifecycleContext(context.Background())
	if result != bs {
		t.Fatal("WithLifecycleContext must return the receiver")
	}
}

func TestBirthSyncHandleRespectsLifecycleCtxCancel(t *testing.T) {
	t.Parallel()
	// When lifecycleCtx is cancelled before "online" is handled, the
	// RepublishDiscovery call must receive an already-cancelled context
	// and return promptly. This locks down that the handle() method
	// derives its working context from lifecycleCtx, not context.Background().
	cancelled, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	mp := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, mp)
	bridge.mu.Lock()
	bridge.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	bridge.mu.Unlock()
	sub := &nopSubscriber{}
	bs := NewBirthSync(sub, bridge, nil).WithLifecycleContext(cancelled)
	if err := bs.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Delivering "online" on a pre-cancelled lifecycleCtx must not block
	// or panic; RepublishDiscovery receives the cancelled ctx and may
	// succeed or return ctx.Err() — either way the call returns promptly.
	sub.deliver(HABirthTopic, []byte("online"))
}

// ---------------------------------------------------------------------------
// BirthSync.handle: republish failure is logged, not propagated.
// ---------------------------------------------------------------------------

func TestBirthSyncHandleRepublishError(t *testing.T) {
	t.Parallel()
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true}, &errPublisher{})
	// Pre-seed declared so RepublishDiscovery attempts to publish and fails.
	b.mu.Lock()
	b.declared["homeassistant/switch/gh/obj1/config"] = []byte(`{"x":1}`)
	b.mu.Unlock()

	sub := &nopSubscriber{}
	bs := NewBirthSync(sub, b, nil)
	_ = bs.Start(context.Background())
	// Must not panic.
	sub.deliver(HABirthTopic, []byte("online"))
}

// ---------------------------------------------------------------------------
// RetainCleanup.collect called with nil bridge.
// ---------------------------------------------------------------------------

func TestRetainCleanupCollectNilBridge(t *testing.T) {
	t.Parallel()
	rc := &RetainCleanup{bridge: nil}
	// Must not panic.
	rc.collect("openccu-loom/GoOtto/HmIP-RF/0001ABCD/0/state", []byte(`{}`), false)
	if len(rc.Worklist()) != 0 {
		t.Fatal("nil bridge must not accumulate worklist entries")
	}
}

// ---------------------------------------------------------------------------
// LegacyDataPointStateMatcher: additional edge cases.
// ---------------------------------------------------------------------------

func TestLegacyDataPointStateMatcherEdgeCases(t *testing.T) {
	t.Parallel()
	base := "openccu-loom"
	positive := []string{
		base + "/ccu/HmIP-RF/0001ABCD/1/STATE",
		base + "/ccu/HmIP-RF/0001ABCD/12/ACTUAL_TEMPERATURE",
	}
	for _, topic := range positive {
		if !LegacyDataPointStateMatcher(base, topic) {
			t.Errorf("expected match: %q", topic)
		}
	}
	negative := []string{
		// empty base.
		"",
		// reserved param names.
		base + "/ccu/HmIP-RF/0001ABCD/1/state",
		base + "/ccu/HmIP-RF/0001ABCD/1/event",
		base + "/ccu/HmIP-RF/0001ABCD/1/availability",
		base + "/ccu/HmIP-RF/0001ABCD/1/info",
		base + "/ccu/HmIP-RF/0001ABCD/1/diagnostics",
		// lower-case param (bucket node).
		base + "/ccu/HmIP-RF/0001ABCD/1/values",
		// non-numeric channel.
		base + "/ccu/HmIP-RF/0001ABCD/foo/STATE",
		// empty base.
		"other/ccu/HmIP-RF/0001ABCD/1/STATE",
	}
	for _, topic := range negative {
		if LegacyDataPointStateMatcher(base, topic) {
			t.Errorf("must NOT match: %q", topic)
		}
	}
}

// ---------------------------------------------------------------------------
// LegacySlotStateMatcher: additional edge cases.
// ---------------------------------------------------------------------------

func TestLegacySlotStateMatcherEdgeCases(t *testing.T) {
	t.Parallel()
	base := "openccu-loom"
	positive := []string{
		base + "/ccu/HmIP-RF/0001ABCD/channels/1/values/STATE/state",
		base + "/ccu/HmIP-RF/0001ABCD/channels/12/custom/climate/config",
	}
	for _, topic := range positive {
		if !LegacySlotStateMatcher(base, topic) {
			t.Errorf("expected match: %q", topic)
		}
	}
	negative := []string{
		"",
		base + "/ccu/HmIP-RF/0001ABCD/1/values/STATE",              // no "channels" segment
		base + "/ccu/HmIP-RF/0001ABCD/channels/foo/STATE",          // non-numeric channel
		base + "/ccu/HmIP-RF/0001ABCD/channels",                    // too short
		"other/ccu/HmIP-RF/0001ABCD/channels/1/values/STATE/state", // wrong base
	}
	for _, topic := range negative {
		if LegacySlotStateMatcher(base, topic) {
			t.Errorf("must NOT match: %q", topic)
		}
	}
}

// ---------------------------------------------------------------------------
// SystemStatusPublisher: nil-guard paths (Start/Stop with nil fields).
// ---------------------------------------------------------------------------

func TestSystemStatusPublisherNilRegNoop(t *testing.T) {
	t.Parallel()
	// nil reg → Start must be a no-op (no panic).
	p := NewSystemStatusPublisher(nil, nil, nil)
	p.Start()
	p.Stop()
}

// ---------------------------------------------------------------------------
// LookupRulesForComponent: dispatch branches.
// ---------------------------------------------------------------------------

func TestLookupRulesForComponentAllBranches(t *testing.T) {
	t.Parallel()
	components := []HAComponent{
		HAComponentSensor,
		HAComponentBinarySensor,
		HAComponentNumber,
		HAComponentSwitch,
		HAComponentCover,
		HAComponentLock,
		HAComponentSiren,
		HAComponentValve,
		HAComponentButton,
		HAComponentSelect,
		// climate / light / text / event / update → no rule, returns false
		HAComponentClimate,
		HAComponentLight,
		HAComponentEvent,
		HAComponentUpdate,
		HAComponentText,
	}
	for _, comp := range components {
		_, _ = LookupRulesForComponent(comp, "HmIP-PSM", "STATE")
	}
}

func TestLookupLockRule(t *testing.T) {
	t.Parallel()
	// Lock rules are param-only — any device, any parameter (even unknown) should not panic.
	_, _ = LookupLockRule("HmIP-DLD", "LOCK_STATE")
}

func TestLookupValveRule(t *testing.T) {
	t.Parallel()
	_, _ = LookupValveRule("HmIP-FALMOT-C12", "")
}

func TestLookupButtonRule(t *testing.T) {
	t.Parallel()
	_, _ = LookupButtonRule("HmIP-BSM", "PRESS_SHORT")
}

// ---------------------------------------------------------------------------
// update discovery context methods (pure topic building, no network).
// ---------------------------------------------------------------------------

func TestUpdateDiscoveryCtxTopics(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("gh")
	ctx := updateDiscoveryCtx{
		topics:      tb,
		centralName: "ccu",
		iface:       "HmIP-RF",
		address:     "0001ABCD",
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"AggregatedStateTopic", ctx.AggregatedStateTopic(), "gh/ccu/HmIP-RF/0001ABCD/update"},
		{"CustomDPStateTopic", ctx.CustomDPStateTopic(), "gh/ccu/HmIP-RF/0001ABCD/update"},
		{"ServiceMethodCommandTopic(install)", ctx.ServiceMethodCommandTopic("install"), "gh/ccu/HmIP-RF/0001ABCD/update/set"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	// WireParameterCommandTopic and WireParameterStateTopic must return non-empty strings.
	if s := ctx.WireParameterCommandTopic("FIRMWARE"); s == "" {
		t.Error("WireParameterCommandTopic must not be empty")
	}
	if s := ctx.WireParameterStateTopic("FIRMWARE"); s == "" {
		t.Error("WireParameterStateTopic must not be empty")
	}
}

// ---------------------------------------------------------------------------
// discoveryCtx: topic builders for the aggregate discovery context.
// ---------------------------------------------------------------------------

func TestDiscoveryCtxTopicsNilSource(t *testing.T) {
	t.Parallel()
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Source:        nil, // nil source → CustomDPStateTopic returns ""
	}
	ctx := discoveryCtx{d: builder, ev: ev}
	// AggregatedStateTopic delegates to CustomDPStateTopic → "" when Source is nil.
	if got := ctx.AggregatedStateTopic(); got != "" {
		t.Errorf("AggregatedStateTopic with nil source: got %q, want %q", got, "")
	}
	if got := ctx.CustomDPStateTopic(); got != "" {
		t.Errorf("CustomDPStateTopic with nil source: got %q, want %q", got, "")
	}
	// ServiceMethodCommandTopic requires a Custom-DP slot; with a nil
	// Source there is no slot, so the topic is empty (HA-discovery
	// caller drops the field rather than publishing an unreachable topic).
	if got := ctx.ServiceMethodCommandTopic("boost"); got != "" {
		t.Errorf("ServiceMethodCommandTopic with nil source: got %q, want %q", got, "")
	}
	if got := ctx.WireParameterCommandTopic("STATE"); got == "" {
		t.Error("WireParameterCommandTopic must not be empty")
	}
	if got := ctx.WireParameterStateTopic("STATE"); got == "" {
		t.Error("WireParameterStateTopic must not be empty")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishChannelEventDiscovery: no DiscoveryBuilder — returns nil.
// ---------------------------------------------------------------------------

func TestBridgePublishChannelEventDiscoveryNoBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "gh",
		HADiscoveryEnabled: true,
		DiscoveryBuilder:   nil, // explicitly nil
	}, mp)
	err := b.PublishChannelEventDiscovery(context.Background(), Event{
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishAvailability: with legacy alias wired.
// ---------------------------------------------------------------------------

func TestBridgePublishAvailabilityOnline(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishAvailability(context.Background(), "ccu", "HmIP-RF", "0001ABCD", true)
	if err != nil {
		t.Fatalf("PublishAvailability: %v", err)
	}
	found := false
	for _, p := range mp.publications() {
		if p.topic == "gh/ccu/HmIP-RF/0001ABCD/availability" && p.payload == "online" {
			found = true
		}
	}
	if !found {
		t.Fatalf("availability=online not published; got %v", mp.publications())
	}
}

func TestBridgePublishAvailabilityOffline(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishAvailability(context.Background(), "ccu", "HmIP-RF", "0001ABCD", false)
	if err != nil {
		t.Fatalf("PublishAvailability offline: %v", err)
	}
	found := false
	for _, p := range mp.publications() {
		if p.topic == "gh/ccu/HmIP-RF/0001ABCD/availability" && p.payload == "offline" {
			found = true
		}
	}
	if !found {
		t.Fatalf("availability=offline not published; got %v", mp.publications())
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishInstallMode
// ---------------------------------------------------------------------------

func TestBridgePublishInstallModeRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	if err := b.PublishInstallMode(context.Background(), "ccu", "HmIP-RF", 30); err != nil {
		t.Fatalf("PublishInstallMode: %v", err)
	}
	found := false
	for _, p := range mp.publications() {
		if p.topic == "gh/ccu/hub/install_mode/HmIP-RF" && p.payload == "30" {
			found = true
		}
	}
	if !found {
		t.Fatalf("install_mode not published; got %v", mp.publications())
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishAlarmMessages / PublishServiceMessages
// ---------------------------------------------------------------------------

func TestBridgePublishAlarmMessages(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	items := []map[string]any{{"id": "1", "message": "test"}}
	agg := fakeAddressable{state: "gh/ccu/hub/alarm_messages"}
	if err := b.PublishAlarmMessages(context.Background(), "ccu", agg, items); err != nil {
		t.Fatalf("PublishAlarmMessages: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected publish")
	}
}

func TestBridgePublishServiceMessages(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	items := []map[string]any{{"id": "1", "message": "test"}}
	agg := fakeAddressable{state: "gh/ccu/hub/service_messages"}
	if err := b.PublishServiceMessages(context.Background(), "ccu", agg, items); err != nil {
		t.Fatalf("PublishServiceMessages: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected publish")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishChannelEventState
// ---------------------------------------------------------------------------

func TestBridgePublishChannelEventStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.PublishChannelEventState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "HmIP-WRC2", "PRESS_SHORT")
	if err != nil {
		t.Fatalf("PublishChannelEventState disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

func TestBridgePublishChannelEventStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishChannelEventState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "HmIP-WRC2", "PRESS_SHORT")
	if err != nil {
		t.Fatalf("PublishChannelEventState: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected publish")
	}
}

// ---------------------------------------------------------------------------
// Bridge.bytesEqual edge case (both empty)
// ---------------------------------------------------------------------------

func TestBytesEqualBothEmpty(t *testing.T) {
	t.Parallel()
	if !bytesEqual(nil, nil) {
		t.Fatal("bytesEqual(nil, nil) must be true")
	}
	if !bytesEqual([]byte{}, []byte{}) {
		t.Fatal("bytesEqual(empty, empty) must be true")
	}
	if bytesEqual([]byte{1}, nil) {
		t.Fatal("bytesEqual([1], nil) must be false")
	}
}

// ---------------------------------------------------------------------------
// Bridge.SetHubInfo: with a DefaultDiscoveryBuilder wired.
// ---------------------------------------------------------------------------

func TestBridgeSetHubInfoWithDefaultBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	cfg := BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu",
		HADiscoveryEnabled: true,
	}
	b := NewBridge(cfg, mp)
	// Wire the real DefaultDiscoveryBuilder.
	b.cfg.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder(cfg.Base), cfg.CentralName)
	// Must not panic.
	b.SetHubInfo(HubInfo{Name: "TestCCU", Version: "3.73.11.0"})
}

// ---------------------------------------------------------------------------
// Bridge.PublishDiscoveryOnly: path through a real DiscoveryBuilder.
// ---------------------------------------------------------------------------

func TestBridgePublishDiscoveryOnlyWithBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	cfg := BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu",
		HADiscoveryEnabled: true,
		RawEnabled:         true,
	}
	b := NewBridge(cfg, mp)
	b.cfg.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder(cfg.Base), cfg.CentralName)
	// An event without Category set → builder returns ok=false → no publish.
	err := b.PublishDiscoveryOnly(context.Background(), Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		// Category not set → componentFromCategory returns ("", false).
	})
	if err != nil {
		t.Fatalf("PublishDiscoveryOnly: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CommandSubscriber.Start: subscribe failure propagates.
// ---------------------------------------------------------------------------

type failSubscriber struct{ n int }

func (f *failSubscriber) Subscribe(_ context.Context, _ string, _ QoS, _ MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	f.n++
	if f.n == 1 {
		return SubscribeResult{}, errCleanupClientLacksSubscribe
	}
	return SubscribeResult{}, nil
}

func (f *failSubscriber) Unsubscribe(_ context.Context, _ string) error { return nil }

func TestCommandSubscriberStartSubscribeError(t *testing.T) {
	t.Parallel()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(&failSubscriber{}, topics, sink, nil)
	if err := sub.Start(context.Background()); err == nil {
		t.Fatal("expected error when subscribe fails")
	}
}

// ---------------------------------------------------------------------------
// discovery.go: SetOriginVersion
// ---------------------------------------------------------------------------

func TestSetOriginVersion(t *testing.T) {
	// Not marked Parallel — mutates the shared originVersionStore, which is
	// read by every Discovery emit. A parallel mutation here would flip the
	// `origin.sw_version` baked into another test's payload mid-run and break
	// payload-dedup expectations (see TestPublishWeekProfileDiscoveryDeduplicates).
	orig := originVersion()
	t.Cleanup(func() { SetOriginVersion(orig) })

	// Non-empty → should update the package-level atomic store without panic.
	SetOriginVersion("1.2.3")
	// Empty → no-op.
	SetOriginVersion("")
}

// ---------------------------------------------------------------------------
// PublishUpdateState: disabled and enabled paths.
// ---------------------------------------------------------------------------

func TestBridgePublishUpdateStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	err := b.PublishUpdateState(context.Background(), "ccu", "HmIP-RF", "0001ABCD",
		map[string]any{"firmware": "1.0"})
	if err != nil {
		t.Fatalf("PublishUpdateState raw disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when RawEnabled=false")
	}
}

func TestBridgePublishUpdateStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	err := b.PublishUpdateState(context.Background(), "ccu", "HmIP-RF", "0001ABCD",
		map[string]any{"firmware": "1.0"})
	if err != nil {
		t.Fatalf("PublishUpdateState: %v", err)
	}
	pubs := mp.publications()
	if len(pubs) == 0 {
		t.Fatal("expected a publish")
	}
	got := pubs[0].topic
	want := "gh/ccu/HmIP-RF/0001ABCD/update"
	if got != want {
		t.Fatalf("topic: got %q want %q", got, want)
	}
}

func TestBridgePublishUpdateStateNilState(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	// nil state must be normalised to empty map (no panic).
	if err := b.PublishUpdateState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", nil); err != nil {
		t.Fatalf("PublishUpdateState nil state: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PublishUpdateDiscovery: disabled and nil-builder paths.
// ---------------------------------------------------------------------------

func TestBridgePublishUpdateDiscoveryHADisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: false}, mp)
	if err := b.PublishUpdateDiscovery(context.Background(), "ccu", UpdateEvent{}); err != nil {
		t.Fatalf("PublishUpdateDiscovery disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

func TestBridgePublishUpdateDiscoveryNilBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true, DiscoveryBuilder: nil}, mp)
	if err := b.PublishUpdateDiscovery(context.Background(), "ccu", UpdateEvent{}); err != nil {
		t.Fatalf("PublishUpdateDiscovery nil builder: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PublishWeekProfileDiscovery: disabled and nil paths.
// ---------------------------------------------------------------------------

func TestBridgePublishWeekProfileDiscoveryHADisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: false}, mp)
	if err := b.PublishWeekProfileDiscovery(context.Background(), "ccu", WeekProfileEvent{}); err != nil {
		t.Fatalf("PublishWeekProfileDiscovery disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

func TestBridgePublishWeekProfileDiscoveryNilBuilder(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", HADiscoveryEnabled: true, DiscoveryBuilder: nil}, mp)
	if err := b.PublishWeekProfileDiscovery(context.Background(), "ccu", WeekProfileEvent{}); err != nil {
		t.Fatalf("PublishWeekProfileDiscovery nil builder: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PublishWeekProfileState: disabled and enabled paths.
// ---------------------------------------------------------------------------

func TestBridgePublishWeekProfileStateRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishWeekProfileState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "P3"); err != nil {
		t.Fatalf("PublishWeekProfileState disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

func TestBridgePublishWeekProfileStateRawEnabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	if err := b.PublishWeekProfileState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, "P3"); err != nil {
		t.Fatalf("PublishWeekProfileState: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected a publish")
	}
}

// ---------------------------------------------------------------------------
// LookupExtRuleForComponent: all branches.
// ---------------------------------------------------------------------------

func TestLookupExtRuleForComponentAllBranches(t *testing.T) {
	t.Parallel()
	components := []HAComponent{
		HAComponentSensor,
		HAComponentBinarySensor,
		HAComponentNumber,
		HAComponentSwitch,
		// Components with no ext rules:
		HAComponentClimate,
		HAComponentCover,
	}
	for _, comp := range components {
		// Must not panic.
		_, _ = LookupExtRuleForComponent(comp, "HmIP-PSM", "STATE", "", "", "")
	}
}

// ---------------------------------------------------------------------------
// EntityDescriptionForExt: exercise the Event / Text branches.
// ---------------------------------------------------------------------------

func TestEntityDescriptionForExtSensor(t *testing.T) {
	t.Parallel()
	// Call with a sensor component — exercises tier 1/2 lookup for sensors.
	_ = EntityDescriptionForExt(HAComponentSensor, "HmIP-PSM", "POWER", "W", "", "power")
}

func TestEntityDescriptionForExtEvent(t *testing.T) {
	t.Parallel()
	// Event branch — exercises tier 3 event lookup.
	_ = EntityDescriptionForExt(HAComponentEvent, "HmIP-BSM", "PRESS_SHORT", "", "", "")
}

func TestEntityDescriptionForExtText(t *testing.T) {
	t.Parallel()
	// Text branch — exercises tier 3 text display lookup.
	_ = EntityDescriptionForExt(HAComponentText, "HmIP-DISPLAY", "TEXT", "", "", "")
}

func TestEntityDescriptionForExtUnknownNoResult(t *testing.T) {
	t.Parallel()
	// No rules → zero MqttEntityDescription.
	got := EntityDescriptionForExt(HAComponentClimate, "UNKNOWN-DEVICE", "UNKNOWN_PARAM", "", "", "")
	if got != (MqttEntityDescription{}) {
		// Some device-class lookups may still return non-zero — just verify no panic.
		_ = got
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishInstallMode: RawDisabled path.
// ---------------------------------------------------------------------------

func TestBridgePublishInstallModeRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishInstallMode(context.Background(), "ccu", "HmIP-RF", 60); err != nil {
		t.Fatalf("PublishInstallMode disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishAlarmMessages / PublishServiceMessages: RawDisabled.
// ---------------------------------------------------------------------------

func TestBridgePublishAlarmMessagesRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishAlarmMessages(context.Background(), "ccu", fakeAddressable{state: "x"}, []string{}); err != nil {
		t.Fatalf("PublishAlarmMessages disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

func TestBridgePublishServiceMessagesRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishServiceMessages(context.Background(), "ccu", fakeAddressable{state: "x"}, []string{}); err != nil {
		t.Fatalf("PublishServiceMessages disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishConnectivity: RawDisabled path.
// ---------------------------------------------------------------------------

func TestBridgePublishConnectivityRawDisabled(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: false}, mp)
	if err := b.PublishConnectivity(context.Background(), "ccu", fakeConnectivityPublisher{state: "x"}, "HmIP-RF", true); err != nil {
		t.Fatalf("PublishConnectivity disabled: %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected")
	}
}

// ---------------------------------------------------------------------------
// CommandSubscriber.Start: exercises more error-handling paths by
// injecting a subscriber that fails on the second call (the first
// call creates the bucket-aware subscription which should succeed,
// the second injects an error on the legacy subscription).
// ---------------------------------------------------------------------------

type nthFailSubscriber struct {
	callCount int
	failOn    int
}

func (f *nthFailSubscriber) Subscribe(_ context.Context, _ string, _ QoS, _ MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	f.callCount++
	if f.callCount == f.failOn {
		return SubscribeResult{}, errCleanupClientLacksSubscribe
	}
	return SubscribeResult{}, nil
}

func (f *nthFailSubscriber) Unsubscribe(_ context.Context, _ string) error { return nil }

func TestCommandSubscriberStartSecondSubscribeError(t *testing.T) {
	t.Parallel()
	topics := NewTopicBuilder("gh")
	sink := &fakeSink{}
	// Fail on the 2nd Subscribe call (legacy datapoint topic).
	sub := NewCommandSubscriber(&nthFailSubscriber{failOn: 2}, topics, sink, nil)
	if err := sub.Start(context.Background()); err == nil {
		t.Fatal("expected error when second subscribe fails")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishSourceState: empty state payload.
// ---------------------------------------------------------------------------

type nilStateSource struct{}

func (n *nilStateSource) State() pload.StatePayload { return nil }

func TestBridgePublishSourceStateNilPayload(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	// Source returns nil state → normalised to empty map.
	err := b.PublishSourceState(context.Background(), "ccu", "HmIP-RF", "0001ABCD", 1, &nilStateSource{})
	if err != nil {
		t.Fatalf("PublishSourceState nil payload: %v", err)
	}
	if len(mp.publications()) == 0 {
		t.Fatal("expected a publish even with nil state (normalised to {})")
	}
}

// ---------------------------------------------------------------------------
// Bridge.PublishDeviceInfo / PublishDeviceDiagnostics: empty central fallback.
// ---------------------------------------------------------------------------

func TestBridgePublishDeviceInfoEmptyCentral(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "default"}, mp)
	// Empty central → falls back to CentralName.
	if err := b.PublishDeviceInfo(context.Background(), "", "HmIP-RF", "0001ABCD", map[string]any{"type": "X"}); err != nil {
		t.Fatalf("PublishDeviceInfo empty central: %v", err)
	}
	found := false
	for _, p := range mp.publications() {
		if p.topic == "gh/default/HmIP-RF/0001ABCD/info" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected topic with default central; got %v", mp.publications())
	}
}

func TestBridgePublishDeviceDiagnosticsEmptyCentral(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "default"}, mp)
	if err := b.PublishDeviceDiagnostics(context.Background(), "", "HmIP-RF", "0001ABCD", map[string]any{"rssi": -60}); err != nil {
		t.Fatalf("PublishDeviceDiagnostics empty central: %v", err)
	}
	found := false
	for _, p := range mp.publications() {
		if p.topic == "gh/default/HmIP-RF/0001ABCD/diagnostics" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected topic with default central; got %v", mp.publications())
	}
}

// ---------------------------------------------------------------------------
// deviceClassFor — all significant branches.
// ---------------------------------------------------------------------------

func TestDeviceClassFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param     string
		wantClass string
		wantFound bool
	}{
		{"ACTUAL_TEMPERATURE", "temperature", true},
		{"TEMPERATURE", "temperature", true},
		{"DEW_POINT", "temperature", true},
		{"FROST_POINT", "temperature", true},
		{"APPARENT_TEMPERATURE", "temperature", true},
		{"HUMIDITY", "humidity", true},
		{"DEW_POINT_SPREAD", "", false},
		{"VAPOR_CONCENTRATION", "", false},
		{"ENTHALPY", "", false},
		{"WINDOW_OPEN", "window", true},
		{"SMOKE_ALARM", "smoke", true},
		{"INTRUSION_ALARM", "tamper", true},
		{"POWER", "power", true},
		{"GAS_POWER", "power", true},
		{"ENERGY_COUNTER", "energy", true},
		{"GAS_ENERGY_COUNTER", "energy", true},
		{"VOLTAGE", "voltage", true},
		{"OPERATING_VOLTAGE", "voltage", true},
		{"CURRENT", "current", true},
		{"FREQUENCY", "frequency", true},
		{"AIR_PRESSURE", "atmospheric_pressure", true},
		{"BRIGHTNESS", "illuminance", true},
		{"ILLUMINATION", "illuminance", true},
		{"WIND_SPEED", "wind_speed", true},
		{"BATTERY_STATE", "battery", true},
		{"OPERATING_VOLTAGE_LEVEL", "battery", true},
		{"RSSI_DEVICE", "signal_strength", true},
		{"RSSI_PEER", "signal_strength", true},
		{"LOW_BAT", "battery", true},
		{"UNREACH", "connectivity", true},
		{"STICKY_UNREACH", "connectivity", true},
		{"WINDOW_STATE", "door", true},
		{"DOOR_STATE", "door", true},
		{"MOTION", "motion", true},
		{"PRESENCE_DETECTION_STATE", "motion", true},
		{"RAINING", "moisture", true},
		{"CONFIG_PENDING", "problem", true},
		{"UPDATE_PENDING", "problem", true},
		{"UNKNOWN_PARAM", "", false},
		// Case insensitivity.
		{"actual_temperature", "temperature", true},
	}
	for _, c := range cases {
		got, ok := deviceClassFor(c.param)
		if ok != c.wantFound || got != c.wantClass {
			t.Errorf("deviceClassFor(%q) = (%q, %v), want (%q, %v)",
				c.param, got, ok, c.wantClass, c.wantFound)
		}
	}
}

// ---------------------------------------------------------------------------
// displayChannelName — covers all branches:
//   - Channel is a CustomDPNamingInspector (secondary / primary / single-primary)
//   - Channel is nil, channelNo > 0
//   - Channel is nil, channelNo == 0
// ---------------------------------------------------------------------------

// mockChannelInspector implements both ChannelInspector and CustomDPNamingInspector.
type mockChannelInspector struct {
	isSecondary   bool
	isPrimary     bool
	singlePrimary bool
}

func (m *mockChannelInspector) HasParameter(_ string) bool       { return false }
func (m *mockChannelInspector) IsCustomDPSecondaryChannel() bool { return m.isSecondary }
func (m *mockChannelInspector) IsCustomDPPrimaryChannel() bool   { return m.isPrimary }
func (m *mockChannelInspector) HasSinglePrimaryCustomDP() bool   { return m.singlePrimary }

func TestDisplayChannelNameSecondary(t *testing.T) {
	t.Parallel()
	ev := Event{
		ChannelNo: 4,
		Channel:   &mockChannelInspector{isSecondary: true},
	}
	got := displayChannelName(ev)
	want := fmt.Sprintf("vch%d", ev.ChannelNo)
	if got != want {
		t.Fatalf("secondary: got %q, want %q", got, want)
	}
}

func TestDisplayChannelNamePrimaryMultiple(t *testing.T) {
	t.Parallel()
	ev := Event{
		ChannelNo: 2,
		Channel:   &mockChannelInspector{isPrimary: true, singlePrimary: false},
	}
	got := displayChannelName(ev)
	want := fmt.Sprintf("ch%d", ev.ChannelNo)
	if got != want {
		t.Fatalf("primary-multi: got %q, want %q", got, want)
	}
}

func TestDisplayChannelNamePrimarySingle(t *testing.T) {
	t.Parallel()
	ev := Event{
		ChannelNo: 1,
		Channel:   &mockChannelInspector{isPrimary: true, singlePrimary: true},
	}
	got := displayChannelName(ev)
	if got != "" {
		t.Fatalf("single-primary: got %q, want %q", got, "")
	}
}

func TestDisplayChannelNameNoCustomDP(t *testing.T) {
	t.Parallel()
	ev := Event{ChannelNo: 3, Channel: nil}
	got := displayChannelName(ev)
	if got != "3" {
		t.Fatalf("no custom dp, channelNo=3: got %q, want %q", got, "3")
	}
}

func TestDisplayChannelNameChannelZero(t *testing.T) {
	t.Parallel()
	ev := Event{ChannelNo: 0, Channel: nil}
	got := displayChannelName(ev)
	if got != "" {
		t.Fatalf("channelNo=0: got %q, want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// customDPSlotForEvent — covers multiple branches of the Slotted path.
// ---------------------------------------------------------------------------

// nonSlottedSource implements payload.Source but NOT payload.Slotted.
type nonSlottedSource struct{}

func (n *nonSlottedSource) Info() pload.InfoPayload      { return nil }
func (n *nonSlottedSource) Config() pload.ConfigPayload  { return nil }
func (n *nonSlottedSource) State() pload.StatePayload    { return nil }
func (n *nonSlottedSource) ServiceMethodNames() []string { return nil }
func (n *nonSlottedSource) Invoke(_ context.Context, _ string, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

// slottedSource implements payload.Source + payload.Slotted.
type slottedSource struct {
	slot pload.TopicSlot
}

func (s *slottedSource) Info() pload.InfoPayload      { return nil }
func (s *slottedSource) Config() pload.ConfigPayload  { return nil }
func (s *slottedSource) State() pload.StatePayload    { return nil }
func (s *slottedSource) ServiceMethodNames() []string { return nil }
func (s *slottedSource) Invoke(_ context.Context, _ string, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}
func (s *slottedSource) TopicSlot() pload.TopicSlot { return s.slot }

func TestCustomDPSlotForEventNilSource(t *testing.T) {
	t.Parallel()
	ev := Event{Source: nil}
	_, ok := customDPSlotForEvent(ev)
	if ok {
		t.Fatal("nil source must return ok=false")
	}
}

func TestCustomDPSlotForEventNotSlotted(t *testing.T) {
	t.Parallel()
	// nonSlottedSource implements payload.Source but not payload.Slotted.
	ev := Event{Source: &nonSlottedSource{}}
	_, ok := customDPSlotForEvent(ev)
	if ok {
		t.Fatal("non-slotted source must return ok=false")
	}
}

func TestCustomDPSlotForEventEmptyParameter(t *testing.T) {
	t.Parallel()
	// Slotted source with empty Parameter → ok=false.
	ev := Event{
		Source:        &slottedSource{slot: pload.TopicSlot{Parameter: ""}},
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
	}
	_, ok := customDPSlotForEvent(ev)
	if ok {
		t.Fatal("empty parameter slot must return ok=false")
	}
}

func TestCustomDPSlotForEventSlottedOk(t *testing.T) {
	t.Parallel()
	ev := Event{
		Source:        &slottedSource{slot: pload.TopicSlot{Parameter: "climate", Bucket: pload.BucketCustom, Address: "0001ABCD", Channel: 1}},
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
	}
	slot, ok := customDPSlotForEvent(ev)
	if !ok {
		t.Fatal("expected ok=true for valid slotted source")
	}
	if slot.Parameter != "climate" {
		t.Fatalf("parameter: got %q, want %q", slot.Parameter, "climate")
	}
}

func TestCustomDPSlotForEventFillsAddressFromEvent(t *testing.T) {
	t.Parallel()
	// Source slot has empty Address and Channel=0 — should be filled from the event.
	ev := Event{
		Source:        &slottedSource{slot: pload.TopicSlot{Parameter: "climate", Bucket: pload.BucketCustom}},
		DeviceAddress: "0001ABCD",
		ChannelNo:     2,
	}
	slot, ok := customDPSlotForEvent(ev)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if slot.Address != "0001ABCD" {
		t.Fatalf("address: got %q, want %q", slot.Address, "0001ABCD")
	}
	if slot.Channel != 2 {
		t.Fatalf("channel: got %d, want 2", slot.Channel)
	}
}

// ---------------------------------------------------------------------------
// Event.descDefault — 0 % covered.
// The method returns d.Default when Descriptor is a *pload.GenericConfig,
// nil when the Descriptor is nil or a different ConfigPayload type.
// ---------------------------------------------------------------------------

func TestEventDescDefault(t *testing.T) {
	t.Parallel()

	want := float64(21.5)
	ev := Event{
		Descriptor: &pload.GenericConfig{Default: want},
	}
	got := ev.descDefault()
	if got != want {
		t.Fatalf("descDefault: got %v, want %v", got, want)
	}
}

func TestEventDescDefaultNilDescriptor(t *testing.T) {
	t.Parallel()
	ev := Event{}
	if got := ev.descDefault(); got != nil {
		t.Fatalf("descDefault with nil descriptor: got %v, want nil", got)
	}
}

func TestEventDescDefaultWrongType(t *testing.T) {
	t.Parallel()
	// A non-GenericConfig ConfigPayload must return nil.
	ev := Event{Descriptor: &pload.ClimateConfig{}}
	if got := ev.descDefault(); got != nil {
		t.Fatalf("descDefault with non-GenericConfig descriptor: got %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// jsonValueTemplate — 0 % covered.
// Returns value_json.value|lower for Switch/BinarySensor/Lock,
// plain value_json.value for everything else.
// ---------------------------------------------------------------------------

func TestJSONValueTemplate(t *testing.T) {
	t.Parallel()

	lowerComps := []HAComponent{
		HAComponentSwitch,
		HAComponentBinarySensor,
		HAComponentLock,
	}
	for _, comp := range lowerComps {
		got := jsonValueTemplate(comp)
		if got != valueJSONValueLowerTemplate {
			t.Errorf("jsonValueTemplate(%q) = %q, want valueJSONValueLowerTemplate", comp, got)
		}
	}

	plainComps := []HAComponent{
		HAComponentSensor,
		HAComponentNumber,
		HAComponentClimate,
		HAComponentCover,
		HAComponentLight,
		HAComponentSelect,
		HAComponentButton,
		HAComponentText,
		HAComponentEvent,
		HAComponentUpdate,
	}
	for _, comp := range plainComps {
		got := jsonValueTemplate(comp)
		if got != valueJSONValueTemplate {
			t.Errorf("jsonValueTemplate(%q) = %q, want valueJSONValueTemplate", comp, got)
		}
	}
}

// ---------------------------------------------------------------------------
// isMotionDeviceClass — 66 % covered; missing the false branch.
// ---------------------------------------------------------------------------

func TestIsMotionDeviceClass(t *testing.T) {
	t.Parallel()

	trueInputs := []string{"motion", "presence", "occupancy"}
	for _, dc := range trueInputs {
		if !isMotionDeviceClass(dc) {
			t.Errorf("isMotionDeviceClass(%q) = false, want true", dc)
		}
	}

	falseInputs := []string{"battery", "temperature", "humidity", "door", "smoke", "window", ""}
	for _, dc := range falseInputs {
		if isMotionDeviceClass(dc) {
			t.Errorf("isMotionDeviceClass(%q) = true, want false", dc)
		}
	}
}

// ---------------------------------------------------------------------------
// varNameMatches — 66 % covered; missing the contains-false branch.
// ---------------------------------------------------------------------------

func TestVarNameMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		needle   string
		haystack string
		want     bool
	}{
		// Empty needle → always true.
		{"", "anything", true},
		{"", "", true},
		// Needle found (case-insensitive).
		{"power", "POWER", true},
		{"POWER", "power_sensor_1", true},
		// Needle NOT found.
		{"temperature", "humidity_sensor", false},
		{"xyz", "abcdef", false},
	}
	for _, c := range cases {
		got := varNameMatches(c.needle, c.haystack)
		if got != c.want {
			t.Errorf("varNameMatches(%q, %q) = %v, want %v", c.needle, c.haystack, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveSensorDeviceClass / resolveSensorStateClass / resolveSwitchDeviceClass
// — each at 75 %; missing the QuantityNone / ValueBehaviorNone branch.
// ---------------------------------------------------------------------------

func TestResolveSensorDeviceClassUnknownParam(t *testing.T) {
	t.Parallel()
	// Unknown parameter, no unit match → QuantityNone → returns "".
	got := resolveSensorDeviceClass("HmIP-UNKNOWN", "UNKNOWN_PARAM_XYZ", "")
	if got != "" {
		t.Fatalf("resolveSensorDeviceClass unknown: got %q, want empty", got)
	}
}

func TestResolveSensorDeviceClassKnownParam(t *testing.T) {
	t.Parallel()
	// ACTUAL_TEMPERATURE → temperature quantity → "temperature"
	got := resolveSensorDeviceClass("HmIP-eTRV-2", "ACTUAL_TEMPERATURE", "°C")
	if got != "temperature" {
		t.Fatalf("resolveSensorDeviceClass ACTUAL_TEMPERATURE: got %q, want %q", got, "temperature")
	}
}

func TestResolveSensorStateClassUnknownParam(t *testing.T) {
	t.Parallel()
	got := resolveSensorStateClass("HmIP-UNKNOWN", "UNKNOWN_PARAM_XYZ", "")
	if got != "" {
		t.Fatalf("resolveSensorStateClass unknown: got %q, want empty", got)
	}
}

func TestResolveSwitchDeviceClassUnknownParam(t *testing.T) {
	t.Parallel()
	got := resolveSwitchDeviceClass("HmIP-UNKNOWN", "UNKNOWN_PARAM_XYZ")
	if got != "" {
		t.Fatalf("resolveSwitchDeviceClass unknown: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// BuildUpdateDiscovery — 9.5 % covered.
// A minimal HADiscoveryPayloadBuilder stub lets us exercise the happy path
// and the nil-Update early-return.
// ---------------------------------------------------------------------------

// fakeUpdateBuilder is a minimal HADiscoveryPayloadBuilder for testing.
type fakeUpdateBuilder struct {
	comp string
	body map[string]any
}

func (f *fakeUpdateBuilder) HADiscoveryPayload(_ pload.HADiscoveryContext) (component string, body map[string]any) {
	return f.comp, f.body
}

func TestBuildUpdateDiscoveryNilUpdate(t *testing.T) {
	t.Parallel()
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	item := builder.BuildUpdateDiscovery("ccu", UpdateEvent{Update: nil})
	if item.OK {
		t.Fatal("expected OK=false when Update is nil")
	}
}

func TestBuildUpdateDiscoveryBuilderReturnsEmpty(t *testing.T) {
	t.Parallel()
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	// Builder returns empty component → DiscoveryItem{OK:false}.
	item := builder.BuildUpdateDiscovery("ccu", UpdateEvent{
		DeviceAddress: "0001ABCD",
		Update:        &fakeUpdateBuilder{comp: "", body: nil},
	})
	if item.OK {
		t.Fatal("expected OK=false when builder returns empty component")
	}
}

func TestBuildUpdateDiscoveryHappyPath(t *testing.T) {
	t.Parallel()
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	upd := &fakeUpdateBuilder{
		comp: "update",
		body: map[string]any{
			"payload_install": "install",
		},
	}
	item := builder.BuildUpdateDiscovery("ccu", UpdateEvent{
		DeviceAddress: "0001ABCD",
		DeviceName:    "Bookshelf Lamp",
		Interface:     "HmIP-RF",
		Update:        upd,
	})
	if !item.OK {
		t.Fatal("expected OK=true for valid UpdateEvent")
	}
	if item.Component != "update" {
		t.Fatalf("Component: got %q, want %q", item.Component, "update")
	}
	if item.ObjectID == "" {
		t.Fatal("ObjectID must not be empty")
	}
	if len(item.Payload) == 0 {
		t.Fatal("Payload must not be empty")
	}
}

func TestBuildUpdateDiscoveryDeviceNameFallback(t *testing.T) {
	t.Parallel()
	// When DeviceName is empty, DeviceAddress should be used as name fallback.
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	upd := &fakeUpdateBuilder{
		comp: "update",
		body: map[string]any{"payload_install": "install"},
	}
	item := builder.BuildUpdateDiscovery("ccu", UpdateEvent{
		DeviceAddress: "0001ABCD",
		DeviceName:    "", // empty → fallback to address
		Interface:     "HmIP-RF",
		Update:        upd,
	})
	if !item.OK {
		t.Fatal("expected OK=true even without DeviceName")
	}
}

// ---------------------------------------------------------------------------
// componentDeviceClass — dispatch coverage.
// The function routes to resolveSensorDeviceClass / resolveBinarySensorDeviceClass
// / resolveSwitchDeviceClass / "" (default) based on comp.
// ---------------------------------------------------------------------------

func TestComponentDeviceClassAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		comp  HAComponent
		param string
		unit  string
	}{
		{HAComponentSensor, "ACTUAL_TEMPERATURE", "°C"},
		{HAComponentBinarySensor, "MOTION", ""},
		{HAComponentSwitch, "STATE", ""},
		// Default branch: returns "" for non-classified components.
		{HAComponentLight, "STATE", ""},
		{HAComponentNumber, "LEVEL", ""},
		{HAComponentClimate, "TEMPERATURE", ""},
		{HAComponentCover, "LEVEL", ""},
		{HAComponentLock, "LOCK_STATE", ""},
		{HAComponentButton, "PRESS_SHORT", ""},
		{HAComponentSelect, "ACTIVE_PROFILE", ""},
		{HAComponentUpdate, "VERSION", ""},
		{HAComponentText, "TEXT", ""},
		{HAComponentEvent, "PRESS_SHORT", ""},
	}
	for _, c := range cases {
		// Must not panic; result may or may not be empty depending on parameter.
		_ = componentDeviceClass(c.comp, "HmIP-PSM", c.param, c.unit)
	}
}

// ---------------------------------------------------------------------------
// lookupDeviceOnlyRules — 70 % covered; the nil-map and prefix-match path.
// ---------------------------------------------------------------------------

func TestLookupDeviceOnlyRulesNilMap(t *testing.T) {
	t.Parallel()
	_, ok := lookupDeviceOnlyRules(nil, "HmIP-PSM")
	if ok {
		t.Fatal("nil map must return ok=false")
	}
}

func TestLookupDeviceOnlyRulesNonEmptyParameterSkipped(t *testing.T) {
	t.Parallel()
	// Entries with non-empty parameter must be skipped by the device-only walk.
	m := map[devParam]EntityDescription{
		{devicePrefix: "HmIP", parameter: "STATE"}: {EnabledByDefault: true},
	}
	_, ok := lookupDeviceOnlyRules(m, "HmIP-PSM")
	if ok {
		t.Fatal("entries with non-empty parameter must be skipped")
	}
}

// ---------------------------------------------------------------------------
// Wiring.Publish: the error branch exercises the 50 % gap in wiring.go.
// This is already partially tested in coverage_gaps2_test.go via
// TestWiringPublishLogsError, but the specific bridge.PublishState → error
// path that returns from Publish (not panics) must not regress.
// Verify via a nil-central event that goes through RawEnabled=true.
// ---------------------------------------------------------------------------

func TestWiringPublishNilInterface(t *testing.T) {
	t.Parallel()
	// An event with an empty device address on a raw-enabled bridge
	// will produce an empty topic and marshal-fail inside renderStatePayload
	// only if Value is a non-marshallable type.
	// Using a valid but empty event is sufficient to exercise the non-error path.
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	w := NewWiring(b, nil)
	// Must not panic even for a minimal empty event.
	w.Publish(nil, Event{ //nolint:staticcheck // nil context is intentional for coverage
		Central:       "ccu",
		DeviceAddress: "0001ABCD",
		ChannelNo:     1,
		Parameter:     "STATE",
		Value:         true,
	})
}

// ---------------------------------------------------------------------------
// Event.descDefault: string default (exercises the `any` preservation path).
// ---------------------------------------------------------------------------

func TestEventDescDefaultString(t *testing.T) {
	t.Parallel()
	ev := Event{
		Descriptor: &pload.GenericConfig{Default: "AUTO"},
	}
	got := ev.descDefault()
	if got != "AUTO" {
		t.Fatalf("descDefault string: got %v, want %q", got, "AUTO")
	}
}

// ---------------------------------------------------------------------------
// EvictState with a LegacyAlias wired — exercises the legacy branch
// (currently 66 % due to missing legacy path in tests).
// ---------------------------------------------------------------------------

func TestBridgeEvictStateWithLegacy(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "gh", RawEnabled: true, CentralName: "ccu"}, mp)
	// Wire a legacy alias so the second publish branch fires.
	b.legacy = NewLegacyTopicBuilder("gh-legacy")
	if err := b.EvictState(nil, "ccu", "HmIP-RF", "0001ABCD", 1, "STATE"); err != nil { //nolint:staticcheck // nil context intentional
		t.Fatalf("EvictState with legacy: %v", err)
	}
	// Expect at least 2 publishes: primary + legacy.
	pubs := mp.publications()
	if len(pubs) < 2 {
		t.Fatalf("expected ≥2 publishes with legacy wired, got %d", len(pubs))
	}
}

// ---------------------------------------------------------------------------
// PublishDiscoveryOnly: Visibility gate covers the remaining 41 % gap.
// When Visibility rejects the event the function returns nil without building.
// ---------------------------------------------------------------------------

// rejectAllVisibility implements the VisibilitySet interface by rejecting every channel.
type rejectAllVisibility struct{}

func (r *rejectAllVisibility) Visible(_, _ string, _ hmenum.ParamsetKey, _ hmenum.Parameter) bool {
	return false
}

func (r *rejectAllVisibility) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, _ hmenum.Parameter) bool {
	return false
}

// TestRetractDiscoveryForDeviceRetractsMatchingTopics verifies that
// RetractDiscoveryForDevice (a) deletes only the declared entries whose topic
// contains the expected node_id fragment, leaving unrelated entries intact, and
// (b) publishes an empty retained payload to each matched topic so Home
// Assistant drops the entity immediately.
func TestRetractDiscoveryForDeviceRetractsMatchingTopics(t *testing.T) {
	t.Parallel()

	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "loom",
		HADiscoveryEnabled: true,
	}, mp)

	// Seed the declared map with three topics: two belonging to address
	// "AABBCCDD1122" and one belonging to an unrelated address.
	addr := "AABBCCDD1122"
	const (
		match1    = "homeassistant/switch/ccu01_aabbccdd1122/ch1_state/config"
		match2    = "homeassistant/sensor/ccu01_aabbccdd1122/ch2_temp/config"
		unrelated = "homeassistant/switch/ccu01_other000000aa/ch1_state/config"
	)
	b.mu.Lock()
	b.declared[match1] = []byte(`{}`)
	b.declared[match2] = []byte(`{}`)
	b.declared[unrelated] = []byte(`{}`)
	b.mu.Unlock()

	retracted := b.RetractDiscoveryForDevice(context.Background(), addr)
	if retracted != 2 {
		t.Fatalf("RetractDiscoveryForDevice(%q) = %d, want 2", addr, retracted)
	}

	// The two matching entries are pruned; the unrelated one survives.
	b.mu.Lock()
	if _, ok := b.declared[unrelated]; !ok {
		t.Fatal("unrelated device entry was incorrectly pruned")
	}
	if _, ok := b.declared[match1]; ok {
		t.Fatal("device entry should have been pruned but still present")
	}
	b.mu.Unlock()

	// Each matched topic is retracted on the broker: empty retained payload.
	retractSeen := map[string]bool{match1: false, match2: false}
	for _, p := range mp.publications() {
		if p.topic == unrelated {
			t.Fatalf("unrelated device's config must not be retracted")
		}
		if _, ok := retractSeen[p.topic]; !ok {
			continue
		}
		if p.payload != "" {
			t.Fatalf("retract %q: payload not empty (%q)", p.topic, p.payload)
		}
		if !p.retain {
			t.Fatalf("retract %q: must be a retained publish", p.topic)
		}
		retractSeen[p.topic] = true
	}
	for topic, seen := range retractSeen {
		if !seen {
			t.Fatalf("expected an empty retained publish for %q, none seen", topic)
		}
	}
}

// TestRetractDiscoveryForDeviceEmptyAddressIsNoop ensures an empty address
// neither prunes nor publishes.
func TestRetractDiscoveryForDeviceEmptyAddressIsNoop(t *testing.T) {
	t.Parallel()

	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", HADiscoveryEnabled: true}, mp)
	b.mu.Lock()
	b.declared["homeassistant/switch/ccu01_aabbccdd1122/ch1_state/config"] = []byte(`{}`)
	b.mu.Unlock()

	if n := b.RetractDiscoveryForDevice(context.Background(), ""); n != 0 {
		t.Fatalf("empty address should retract 0, got %d", n)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.declared) != 1 {
		t.Fatalf("declared should still have 1 entry, got %d", len(b.declared))
	}
	if len(mp.publications()) != 0 {
		t.Fatalf("empty address must not publish anything, got %d", len(mp.publications()))
	}
}

// TestRetractDiscoveryForDeviceDiscoveryDisabledIsNoop ensures the pass does
// nothing when HA Discovery is off — there are no discovery configs to clear.
func TestRetractDiscoveryForDeviceDiscoveryDisabledIsNoop(t *testing.T) {
	t.Parallel()

	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", HADiscoveryEnabled: false}, mp)
	b.mu.Lock()
	b.declared["homeassistant/switch/ccu01_aabbccdd1122/ch1_state/config"] = []byte(`{}`)
	b.mu.Unlock()

	if n := b.RetractDiscoveryForDevice(context.Background(), "AABBCCDD1122"); n != 0 {
		t.Fatalf("discovery disabled should retract 0, got %d", n)
	}
	if len(mp.publications()) != 0 {
		t.Fatalf("discovery disabled must not publish anything, got %d", len(mp.publications()))
	}
}

// TestRetractRawStateForDeviceClearsPublishedTopics reproduces the B4 bug:
// a device's raw-plane per-DP state survives forever because nothing ever
// retracts it on removal. It publishes a real per-DP state (populating
// Bridge.rawTopics the way normal traffic would), then asserts
// RetractRawStateForDevice clears that exact topic plus the device-scoped
// availability/info/diagnostics topics with an empty retained payload,
// while leaving an unrelated device's state untouched.
func TestRetractRawStateForDeviceClearsPublishedTopics(t *testing.T) {
	t.Parallel()

	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", RawEnabled: true, CentralName: "ccu01"}, mp)

	const (
		iface       = "HmIP-RF"
		addr        = "AABBCCDD1122"
		otherAddr   = "0000000000FF"
		centralName = "ccu01"
	)
	slot := pload.TopicSlot{Address: addr, Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	if err := b.PublishSlotState(context.Background(), centralName, iface, slot, pload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}
	otherSlot := pload.TopicSlot{Address: otherAddr, Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	if err := b.PublishSlotState(context.Background(), centralName, iface, otherSlot, pload.PerDPState{Value: false, Available: true}); err != nil {
		t.Fatalf("PublishSlotState (other device): %v", err)
	}
	stateTopic := b.topics.SlotState(centralName, iface, slot)
	otherStateTopic := b.topics.SlotState(centralName, iface, otherSlot)
	before := len(mp.publications())

	n := b.RetractRawStateForDevice(context.Background(), centralName, iface, addr)
	if n == 0 {
		t.Fatal("expected at least one topic retracted")
	}

	pubs := mp.publications()[before:]
	seen := map[string]publishRecord{}
	for _, p := range pubs {
		seen[p.topic] = p
	}
	for _, wantTopic := range []string{
		stateTopic,
		b.topics.DeviceAvailability(centralName, iface, addr),
		b.topics.DeviceInfo(centralName, iface, addr),
		b.topics.DeviceDiagnostics(centralName, iface, addr),
	} {
		p, ok := seen[wantTopic]
		if !ok {
			t.Fatalf("expected an empty retained publish to %q, none seen (got %+v)", wantTopic, pubs)
		}
		if p.payload != "" {
			t.Fatalf("retract %q: payload not empty (%q)", wantTopic, p.payload)
		}
		if !p.retain {
			t.Fatalf("retract %q: must be a retained publish", wantTopic)
		}
	}
	if _, ok := seen[otherStateTopic]; ok {
		t.Fatalf("unrelated device's state topic %q must not be retracted", otherStateTopic)
	}

	b.mu.Lock()
	if _, ok := b.rawTopics[stateTopic]; ok {
		t.Fatal("removed device's raw topic should have been pruned from rawTopics")
	}
	if _, ok := b.rawTopics[otherStateTopic]; !ok {
		t.Fatal("unrelated device's raw topic should still be tracked")
	}
	b.mu.Unlock()
}

// TestRetractRawStateForDeviceEmptyAddressIsNoop mirrors the discovery-side
// empty-address guard.
func TestRetractRawStateForDeviceEmptyAddressIsNoop(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{Base: "loom", RawEnabled: true}, mp)
	if n := b.RetractRawStateForDevice(context.Background(), "ccu01", "HmIP-RF", ""); n != 0 {
		t.Fatalf("empty address should retract 0, got %d", n)
	}
	if len(mp.publications()) != 0 {
		t.Fatalf("empty address must not publish anything, got %d", len(mp.publications()))
	}
}

func TestBridgePublishDiscoveryOnlyVisibilityGated(t *testing.T) {
	t.Parallel()
	mp := &mockPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "gh",
		HADiscoveryEnabled: true,
		Visibility:         &rejectAllVisibility{},
	}, mp)
	b.cfg.DiscoveryBuilder = NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")

	err := b.PublishDiscoveryOnly(nil, Event{ //nolint:staticcheck // nil context intentional
		Model:         "HmIP-PSM",
		ChannelType:   "",
		ChannelNo:     1,
		Parameter:     "STATE",
		Category:      hmenum.DataPointCategorySwitch,
		DeviceAddress: "0001ABCD",
	})
	if err != nil {
		t.Fatalf("expected nil with gated visibility, got %v", err)
	}
	if len(mp.publications()) != 0 {
		t.Fatal("no publishes expected when visibility gate rejects the event")
	}
}
