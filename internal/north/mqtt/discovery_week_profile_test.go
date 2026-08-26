// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- fixtures ----------------------------------------------------------------

// fakeWeekProfile satisfies WeekProfileDescriptor for use in tests.
type fakeWeekProfile struct {
	uniqueID       string
	profiles       []string
	currentProfile string
	callbacks      []func()
}

func (f *fakeWeekProfile) UniqueID() string            { return f.uniqueID }
func (f *fakeWeekProfile) AvailableProfiles() []string { return f.profiles }
func (f *fakeWeekProfile) CurrentProfile() string      { return f.currentProfile }
func (f *fakeWeekProfile) OnChange(fn func()) func() {
	f.callbacks = append(f.callbacks, fn)
	idx := len(f.callbacks) - 1
	return func() {
		if idx < len(f.callbacks) {
			f.callbacks[idx] = nil
		}
	}
}

func newFakeWP(profiles []string, current string) *fakeWeekProfile {
	return &fakeWeekProfile{
		uniqueID:       "ccu-01:VCU123456:1:WEEKPROFILE",
		profiles:       profiles,
		currentProfile: current,
	}
}

func hmipProfiles() []string { return []string{"P1", "P2", "P3", "P4", "P5", "P6"} }
func rfProfiles() []string   { return []string{"P1", "P2", "P3"} }

func newWPBuilder() *DefaultDiscoveryBuilder {
	return NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01")
}

func newWPEvent(wp WeekProfileDescriptor) WeekProfileEvent {
	return WeekProfileEvent{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "VCU123456",
		ChannelNo:     1,
		DeviceName:    "Wandthermostat",
		Model:         "HmIP-eTRV-2",
		WP:            wp,
	}
}

// --- topic builder tests -----------------------------------------------------

func TestTopicBuilderWeekProfileState(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.WeekProfileState("ccu-01", "HmIP-RF", "VCU123456", 1)
	want := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/state"
	if got != want {
		t.Fatalf("WeekProfileState: got %q want %q", got, want)
	}
}

func TestTopicBuilderWeekProfileCommand(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")
	got := tb.WeekProfileCommand("ccu-01", "HmIP-RF", "VCU123456", 1)
	want := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/set"
	if got != want {
		t.Fatalf("WeekProfileCommand: got %q want %q", got, want)
	}
}

// --- discovery payload tests -------------------------------------------------

// TestWeekProfileDiscoveryShape verifies the full select payload shape for an
// HmIP device (P1..P6).
func TestWeekProfileDiscoveryShape(t *testing.T) {
	t.Parallel()
	d := newWPBuilder()
	wp := newFakeWP(hmipProfiles(), "P3")
	item := d.BuildWeekProfileDiscovery("ccu-01", newWPEvent(wp))
	if !item.OK {
		t.Fatal("expected OK=true for climate channel")
	}
	if item.Component != string(HAComponentSelect) {
		t.Fatalf("component: got %q want %q", item.Component, HAComponentSelect)
	}

	var doc map[string]any
	if err := json.Unmarshal(item.Payload, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// options list matches available profiles.
	opts, _ := doc["options"].([]any)
	if len(opts) != 6 {
		t.Fatalf("options length: got %d want 6; %v", len(opts), opts)
	}
	for i, wantKey := range hmipProfiles() {
		if opts[i] != wantKey {
			t.Fatalf("options[%d]: got %q want %q", i, opts[i], wantKey)
		}
	}

	// state topic.
	wantState := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/state"
	if doc["state_topic"] != wantState {
		t.Fatalf("state_topic: got %q want %q", doc["state_topic"], wantState)
	}

	// command topic.
	wantCmd := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/set"
	if doc["command_topic"] != wantCmd {
		t.Fatalf("command_topic: got %q want %q", doc["command_topic"], wantCmd)
	}

	// name.
	if doc["name"] != "Week profile" {
		t.Fatalf("name: got %q want %q", doc["name"], "Week profile")
	}

	// availability carries two entries (bridge/status + device).
	avail, _ := doc["availability"].([]any)
	if len(avail) != 2 {
		t.Fatalf("availability entries: got %d want 2", len(avail))
	}
}

// TestWeekProfileDiscoveryDeviceBlock verifies that the `device` block in the
// week-profile discovery matches what other entities produce for the same
// device address.
func TestWeekProfileDiscoveryDeviceBlock(t *testing.T) {
	t.Parallel()
	d := newWPBuilder()
	wp := newFakeWP(hmipProfiles(), "P1")
	item := d.BuildWeekProfileDiscovery("ccu-01", newWPEvent(wp))
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	var doc map[string]any
	if err := json.Unmarshal(item.Payload, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// device block.
	dev, ok := doc["device"].(map[string]any)
	if !ok {
		t.Fatal("missing device block")
	}
	// identifiers must contain the address.
	ids, _ := dev["identifiers"].([]any)
	if len(ids) == 0 {
		t.Fatalf("device.identifiers empty: %v", dev["identifiers"])
	}
	firstID, _ := ids[0].(string)
	if !strings.Contains(firstID, "vcu123456") {
		t.Fatalf("device.identifiers[0] does not contain address: %q", firstID)
	}
	// model.
	if dev["model"] != "HmIP-eTRV-2" {
		t.Fatalf("device.model: got %q want HmIP-eTRV-2", dev["model"])
	}
	// name.
	if dev["name"] != "Wandthermostat" {
		t.Fatalf("device.name: got %q want Wandthermostat", dev["name"])
	}
	// Compare scalar fields to what deviceDescriptor produces for the same address.
	referenceEv := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "VCU123456",
		DeviceName:    "Wandthermostat",
		Model:         "HmIP-eTRV-2",
	}
	refDev := deviceDescriptor(referenceEv, "", false)
	for k, refV := range refDev {
		// "identifiers" is []string in the reference but []any after JSON
		// round-trip — skip it here; the per-item check above covers it.
		if k == "identifiers" {
			continue
		}
		if dv := dev[k]; dv != refV {
			t.Fatalf("device.%s: wp=%v ref=%v", k, dv, refV)
		}
	}
}

// TestWeekProfileDiscoveryRFProfiles verifies the options list for an RF
// device that exposes only P1..P3.
func TestWeekProfileDiscoveryRFProfiles(t *testing.T) {
	t.Parallel()
	d := newWPBuilder()
	wp := newFakeWP(rfProfiles(), "P2")
	item := d.BuildWeekProfileDiscovery("ccu-01", newWPEvent(wp))
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	var doc map[string]any
	if err := json.Unmarshal(item.Payload, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	opts, _ := doc["options"].([]any)
	if len(opts) != 3 {
		t.Fatalf("options length: got %d want 3", len(opts))
	}
}

// TestWeekProfileDiscoveryNonClimateSuppressed verifies that a nil / empty
// AvailableProfiles (non-climate channel) produces OK=false.
func TestWeekProfileDiscoveryNonClimateSuppressed(t *testing.T) {
	t.Parallel()
	d := newWPBuilder()
	wp := newFakeWP(nil, "")
	item := d.BuildWeekProfileDiscovery("ccu-01", newWPEvent(wp))
	if item.OK {
		t.Fatal("expected OK=false for nil profiles (non-climate)")
	}
}

// TestWeekProfileDiscoveryNilWP verifies that a nil WP produces OK=false.
func TestWeekProfileDiscoveryNilWP(t *testing.T) {
	t.Parallel()
	d := newWPBuilder()
	ev := WeekProfileEvent{Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "VCU123456", ChannelNo: 1, WP: nil}
	item := d.BuildWeekProfileDiscovery("ccu-01", ev)
	if item.OK {
		t.Fatal("expected OK=false for nil WP")
	}
}

// --- bridge publish tests ----------------------------------------------------

// TestPublishWeekProfileStateCarriesProfile verifies that P3 is published to
// the correct topic with retain=true.
func TestPublishWeekProfileStateCarriesProfile(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	err := b.PublishWeekProfileState(context.Background(), "ccu-01", "HmIP-RF", "VCU123456", 1, "P3")
	if err != nil {
		t.Fatalf("PublishWeekProfileState: %v", err)
	}
	wantTopic := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/state"
	var found *publishRecord
	for i := range pub.sent {
		if pub.sent[i].topic == wantTopic {
			found = &pub.sent[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("state publish not found; topics: %v", collectTopics(pub))
	}
	if found.payload != "P3" {
		t.Fatalf("payload: got %q want %q", found.payload, "P3")
	}
	if !found.retain {
		t.Fatal("expected retain=true on state topic")
	}
}

// TestPublishWeekProfileStateEmptyProfile verifies that an empty CurrentProfile
// publishes an empty payload (not "P0" or similar).
func TestPublishWeekProfileStateEmptyProfile(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	err := b.PublishWeekProfileState(context.Background(), "ccu-01", "HmIP-RF", "VCU123456", 1, "")
	if err != nil {
		t.Fatalf("PublishWeekProfileState: %v", err)
	}
	wantTopic := "openccu-loom/ccu-01/HmIP-RF/VCU123456/1/week_profile/state"
	var found *publishRecord
	for i := range pub.sent {
		if pub.sent[i].topic == wantTopic {
			found = &pub.sent[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("state publish not found; topics: %v", collectTopics(pub))
	}
	if found.payload != "" {
		t.Fatalf("empty profile: got payload %q want empty", found.payload)
	}
}

// TestPublishWeekProfileDiscoveryPublishesSelectRetained verifies that the
// bridge emits a retained discovery message on the correct discovery topic.
func TestPublishWeekProfileDiscoveryPublishesSelectRetained(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	wp := newFakeWP(hmipProfiles(), "P2")
	ev := newWPEvent(wp)
	err := b.PublishWeekProfileDiscovery(context.Background(), "ccu-01", ev)
	if err != nil {
		t.Fatalf("PublishWeekProfileDiscovery: %v", err)
	}
	// Discovery topic must start with "homeassistant/select/".
	var found *publishRecord
	for i := range pub.sent {
		if strings.HasPrefix(pub.sent[i].topic, "homeassistant/select/") {
			found = &pub.sent[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no select discovery published; topics: %v", collectTopics(pub))
	}
	if !found.retain {
		t.Fatal("discovery message must be retained")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(found.payload), &doc); err != nil {
		t.Fatalf("unmarshal discovery: %v", err)
	}
	if doc["name"] != "Week profile" {
		t.Fatalf("name: got %q", doc["name"])
	}
}

// TestPublishWeekProfileDiscoveryDeduplicates verifies that a second identical
// call does not result in a second broker publish (dedup via declared cache).
func TestPublishWeekProfileDiscoveryDeduplicates(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	wp := newFakeWP(hmipProfiles(), "P1")
	ev := newWPEvent(wp)
	_ = b.PublishWeekProfileDiscovery(context.Background(), "ccu-01", ev)
	_ = b.PublishWeekProfileDiscovery(context.Background(), "ccu-01", ev)
	count := 0
	for _, r := range pub.sent {
		if strings.HasPrefix(r.topic, "homeassistant/select/") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("select discovery publishes: got %d want 1", count)
	}
}

// TestPublishWeekProfileDiscoveryNoopWhenDisabled verifies that the bridge
// silently skips the publish when HA discovery is disabled.
func TestPublishWeekProfileDiscoveryNoopWhenDisabled(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryEnabled = false })
	wp := newFakeWP(hmipProfiles(), "P1")
	ev := newWPEvent(wp)
	err := b.PublishWeekProfileDiscovery(context.Background(), "ccu-01", ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range pub.sent {
		if strings.HasPrefix(r.topic, "homeassistant/") {
			t.Fatalf("unexpected discovery publish while discovery disabled: %q", r.topic)
		}
	}
}

// --- helpers -----------------------------------------------------------------

func collectTopics(pub *mockPublisher) []string {
	pub.mu.Lock()
	defer pub.mu.Unlock()
	topics := make([]string, len(pub.sent))
	for i, r := range pub.sent {
		topics[i] = r.topic
	}
	return topics
}
