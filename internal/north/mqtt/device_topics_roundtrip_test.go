// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDevicePlaneTopicsRoundTrip asserts that every topic a per-device
// discovery payload declares is a topic the device plane actually
// writes to (state / json-attributes / latest-version) or subscribes
// on (command), for every variant this test can drive through its
// real builder without constructing a full southbound device/channel
// model.
//
// Covered, each through its real Build*/topic-helper pair:
//
//   - discovery.go's per-parameter [DefaultDiscoveryBuilder.Build] —
//     a writable switch (state+command+json_attributes) and a
//     read-only sensor (state+json_attributes).
//   - discovery_press_button.go's [DefaultDiscoveryBuilder.BuildPressButton]
//     (command only).
//   - discovery_aggregate.go's [DefaultDiscoveryBuilder.BuildChannelEvent]
//     (the channel-level keypress event, state only) and
//     [DefaultDiscoveryBuilder.aggregateChannel] driven generically
//     through a minimal fake [payload.HADiscoveryPayloadBuilder] +
//     [payload.Slotted] source — this exercises the SHARED
//     custom-DP topic plumbing every domain (climate/cover/lock/
//     light/valve/siren) routes through, not any one domain's own
//     payload shape (see "NOT covered" below).
//   - discovery_schedule.go's schedule entity + schedule-switch
//     builders.
//   - discovery_week_profile.go's week-profile select builder.
//   - discovery_combined.go's projection-driven combined builder
//     (number / sensor / select).
//   - discovery_update.go's per-device update entity, driven through
//     a minimal fake mirroring [internal/model/device.Update.HADiscoveryPayload]'s
//     shape (state/latest-version/json-attributes share one topic; no
//     command topic — see the fix note below).
//
// This test's first run over the update-entity fixture found a real
// defect, not a fixture bug: [internal/model/device.Update.HADiscoveryPayload]
// used to declare `command_topic` + `payload_install: "INSTALL"`, but
// no [CommandSubscriber] subscription — bucket-aware, schedule,
// week-profile, combined, custom-DP service-method, hub, alarm, or
// add-on-update — matches `<base>/<central>/<iface>/<addr>/update/set`.
// Clicking "Install" on a device's HA update card silently did
// nothing. Fixed by making the entity read-only (no command_topic /
// payload_install), matching the CCU-level update entity
// ([DefaultDiscoveryBuilder.BuildHubUpdateDiscovery]), which was
// already deliberately command-less for the same reason (see
// notes/parity/by_design.md): an unconfirmed MQTT payload triggering a
// firmware flash is unsafe, and the REST path
// (`POST /devices/{addr}/firmware/update`) already carries the
// operator-confirm guard.
//
// NOT covered, and why:
//
//   - Each custom-DP domain's OWN [payload.HADiscoveryPayloadBuilder]
//     body (internal/model/custom/{climate,cover,lock,light,valve,siren}/payload.go)
//     — driving these faithfully needs each domain's real Source
//     type (HVAC modes, tilt support, siren tones, …), which is a
//     payload-SHAPE concern already exercised by
//     discovery_payload_test.go / discovery_ha_schema_test.go. This
//     test's generic aggregateChannel case proves the shared
//     state/command topic derivation every one of those domains
//     shares is sound; it does not re-verify each domain's field set.
//   - discovery_notify.go's [DefaultDiscoveryBuilder.BuildTextDisplayNotify]
//     — a command-only entity (like the press button) whose sole
//     declared field is `command_topic`, gated on a
//     textDisplaySource fake. Its risk profile — a command topic
//     nobody subscribes to — is already demonstrated by the press-
//     button and per-parameter switch coverage above; adding a
//     third command-only fixture would not exercise a new code path.
//
// The comparison is one-directional: declared-but-unpublished fails;
// published-but-undeclared (the nested availability topics) is
// reported via t.Logf only.
func TestDevicePlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	const (
		base = "gh"
		// A name the topic escaping actually rewrites. With a bare ASCII
		// fixture a hand-assembled segment is indistinguishable from the
		// shared builder's output, so the guard cannot see the drift it
		// exists to catch.
		central = "CCU Küche"
		iface   = "HmIP-RF"
		addr    = "ABCD1234"
	)
	topics := NewTopicBuilder(base)
	d := NewDefaultDiscoveryBuilder(topics, central)

	declared := map[string]bool{}

	// --- Main per-parameter path: switch (state+command+json_attrs) ---
	_, _, _, buf, ok := d.Build(Event{
		Central: central, Interface: iface, DeviceAddress: addr, ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Writable: true,
	})
	collectDeviceDeclaredTopics(t, declared, "per-parameter switch", buf, ok)

	// --- Main per-parameter path: read-only sensor (state+json_attrs, no command) ---
	_, _, _, buf, ok = d.Build(Event{
		Central: central, Interface: iface, DeviceAddress: addr, ChannelNo: 1,
		Parameter: "ACTUAL_TEMPERATURE", Category: hmenum.DataPointCategorySensor,
	})
	collectDeviceDeclaredTopics(t, declared, "per-parameter sensor", buf, ok)

	// --- Press button (command only) ---
	pbItem := d.BuildPressButton(Event{
		Central: central, Interface: iface, DeviceAddress: addr, ChannelNo: 1,
		Parameter: "PRESS_SHORT", Category: hmenum.DataPointCategoryButton, Usage: hmenum.DataPointUsageDataPoint,
	})
	collectDeviceDeclaredItem(t, declared, pbItem)

	// --- Channel-level keypress event (state only) ---
	_, _, _, buf, ok = d.BuildChannelEvent(Event{
		Central: central, Interface: iface, DeviceAddress: addr, ChannelNo: 2,
		Channel: fakePressChannel{},
	})
	collectDeviceDeclaredTopics(t, declared, "channel press event", buf, ok)

	// --- Generic custom-DP aggregate (shared slot-topic plumbing) ---
	aggComp, _, _, buf, ok := d.aggregateChannel(Event{
		Central: central, Interface: iface, DeviceAddress: addr, ChannelNo: 3,
		Source: fakeCustomDPSource{kind: "switch"},
	})
	if aggComp == "" {
		t.Fatal("aggregateChannel fixture did not classify — fixture bug")
	}
	collectDeviceDeclaredTopics(t, declared, "custom-DP aggregate", buf, ok)

	// --- Schedule entity + schedule switch ---
	collectDeviceDeclaredItem(t, declared, d.BuildScheduleEntityDiscovery(central, ScheduleEntityEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 4,
	}))
	collectDeviceDeclaredItem(t, declared, d.BuildScheduleSwitchDiscovery(central, ScheduleSwitchEvent{
		Interface: iface, DeviceAddress: addr, ScheduleChannelNo: 4, Key: "1_1", Label: "Zeitplan Kanal 18",
	}))

	// --- Week profile --- (fakeWeekProfile/newFakeWP is shared with
	// discovery_week_profile_test.go)
	collectDeviceDeclaredItem(t, declared, d.BuildWeekProfileDiscovery(central, WeekProfileEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 1,
		WP: newFakeWP([]string{"P1", "P2"}, "P1"),
	}))

	// --- Combined data points ---
	// One per projected shape: the number the timer maps onto, the sensor
	// the level/colour pairs map onto, and the select a mode maps onto.
	collectDeviceDeclaredItem(t, declared, d.BuildCombinedDiscovery(central, CombinedEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 3, Kind: "duration",
		Component: "number",
		Body:      map[string]any{"name": "Zeitdauer", "command_topic": topics.CombinedCommand(central, iface, addr, 3, "duration")},
	}))
	collectDeviceDeclaredItem(t, declared, d.BuildCombinedDiscovery(central, CombinedEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 3, Kind: "hs_color",
		Component: "sensor",
		Body:      map[string]any{"name": "Farbe", "value_template": "{{ value_json.hue }}"},
	}))
	collectDeviceDeclaredItem(t, declared, d.BuildCombinedDiscovery(central, CombinedEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 3, Kind: "door_mode",
		Component: "select",
		Body: map[string]any{
			"name":          "Tormodus",
			"command_topic": topics.CombinedCommand(central, iface, addr, 3, "door_mode"),
			"options":       []string{"CLOSED", "VENTILATION_POSITION", "OPEN"},
		},
	}))

	// --- Firmware update entity ---
	collectDeviceDeclaredItem(t, declared, d.BuildUpdateDiscovery(central, UpdateEvent{
		Interface: iface, DeviceAddress: addr, Update: fakeUpdateSource{},
	}))

	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
	}

	obs := runDevicePlane(t, base, central, iface, addr)
	planeRoundTrip(t, "device plane", declared, obs.publishedTopics(), obs.subscribedFilters(), nil)
}

// runDevicePlane drives every real [Bridge] publish call site the
// fixtures above declare an entity for, plus the real
// [CommandSubscriber], against a recording broker and returns
// everything carried.
//
// Each state write goes through the same [Bridge] method the
// production publish call sites use (eventbridge.go's
// publishSlotState/publishCustomDPState via PublishSlotState/
// PublishSlotConfig/PublishCustomDPState, PublishChannelEventState,
// PublishScheduleEntityState/Attrs, PublishScheduleSwitchState,
// PublishWeekProfileState, PublishCombinedTimerState/
// PublishCombinedSensorState, PublishUpdateState) — the topic is
// never re-derived by hand. The command half is registered by
// starting the real [CommandSubscriber]; a declared command topic
// counts as carried only when one of its real wildcard subscriptions
// matches it — see [topicMatchesFilter].
func runDevicePlane(t *testing.T, base, central, iface, addr string) *observedPlane {
	t.Helper()
	ctx := context.Background()
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: central,
		RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)

	// --- Main per-parameter path: switch (state+command+json_attrs) ---
	switchSlot := payload.TopicSlot{Address: addr, Channel: 1, Bucket: payload.BucketValues, Parameter: "STATE"}
	if err := bridge.PublishSlotState(ctx, central, iface, switchSlot, payload.PerDPState{Value: true, Available: true}); err != nil {
		t.Fatalf("publish switch state: %v", err)
	}
	if err := bridge.PublishSlotConfig(ctx, central, iface, switchSlot, map[string]any{"type": "boolean"}); err != nil {
		t.Fatalf("publish switch config: %v", err)
	}

	// --- Main per-parameter path: read-only sensor (state+json_attrs, no command) ---
	sensorSlot := payload.TopicSlot{Address: addr, Channel: 1, Bucket: payload.BucketValues, Parameter: "ACTUAL_TEMPERATURE"}
	if err := bridge.PublishSlotState(ctx, central, iface, sensorSlot, payload.PerDPState{Value: 21.5, Available: true}); err != nil {
		t.Fatalf("publish sensor state: %v", err)
	}
	if err := bridge.PublishSlotConfig(ctx, central, iface, sensorSlot, map[string]any{"unit": "°C"}); err != nil {
		t.Fatalf("publish sensor config: %v", err)
	}

	// --- Channel-level keypress event (state only) ---
	if err := bridge.PublishChannelEventState(ctx, central, iface, addr, 2, "", "press_short"); err != nil {
		t.Fatalf("publish channel event state: %v", err)
	}

	// --- Generic custom-DP aggregate (shared slot-topic plumbing) ---
	customSlot := payload.TopicSlot{Address: addr, Channel: 3, Bucket: payload.BucketCustom, Parameter: "switch"}
	if err := bridge.PublishCustomDPState(ctx, central, iface, customSlot, map[string]any{"switch": "on"}); err != nil {
		t.Fatalf("publish custom-DP state: %v", err)
	}

	// --- Schedule entity + schedule switch ---
	if err := bridge.PublishScheduleEntityState(ctx, central, iface, addr, 4, 2); err != nil {
		t.Fatalf("publish schedule entity state: %v", err)
	}
	if err := bridge.PublishScheduleEntityAttrs(ctx, central, iface, addr, 4, map[string]any{"schedule_enabled": true}); err != nil {
		t.Fatalf("publish schedule entity attrs: %v", err)
	}
	if err := bridge.PublishScheduleSwitchState(ctx, central, iface, addr, 4, "1_1", true); err != nil {
		t.Fatalf("publish schedule switch state: %v", err)
	}

	// --- Week profile ---
	if err := bridge.PublishWeekProfileState(ctx, central, iface, addr, 1, "P1"); err != nil {
		t.Fatalf("publish week profile state: %v", err)
	}

	// --- Combined data points ---
	if err := bridge.PublishCombinedState(ctx, central, iface, addr, 3, "duration", "30"); err != nil {
		t.Fatalf("publish combined timer state: %v", err)
	}
	if err := bridge.PublishCombinedState(ctx, central, iface, addr, 3, "hs_color", `{"hue":120}`); err != nil {
		t.Fatalf("publish combined sensor state: %v", err)
	}
	if err := bridge.PublishCombinedState(ctx, central, iface, addr, 3, "door_mode", "VENTILATION_POSITION"); err != nil {
		t.Fatalf("publish combined door-mode state: %v", err)
	}

	// --- Firmware update entity ---
	if err := bridge.PublishUpdateState(ctx, central, iface, addr, map[string]any{
		"firmware": "1.0", "latest_firmware": "1.1", "in_progress": false,
	}); err != nil {
		t.Fatalf("publish update state: %v", err)
	}

	// The command half is observed rather than mirrored: the real
	// subscriber registers its own wildcards, and a declared command
	// topic counts as heard only when one of them matches it.
	cs := NewCommandSubscriber(obs, NewTopicBuilder(base), nil, slog.Default())
	if err := cs.Start(ctx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cs.Close)

	obs.settle(t)
	return obs
}

// collectDeviceDeclaredTopics is the (component, nodeID, objectID,
// payload, ok) 5-tuple variant of [collectDeviceDeclaredItem], used by
// the builders that don't return a [DiscoveryItem].
func collectDeviceDeclaredTopics(t *testing.T, out map[string]bool, label string, buf []byte, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("%s: builder returned ok=false for a valid fixture", label)
		return
	}
	collectDeviceDeclaredBody(t, out, label, buf)
}

// collectDeviceDeclaredItem extracts declared topics from a
// [DiscoveryItem]-returning builder.
func collectDeviceDeclaredItem(t *testing.T, out map[string]bool, item DiscoveryItem) {
	t.Helper()
	if !item.OK {
		t.Errorf("builder returned OK=false for a valid fixture (component=%q, objectID=%q)", item.Component, item.ObjectID)
		return
	}
	collectDeviceDeclaredBody(t, out, item.Component+"/"+item.ObjectID, item.Payload)
}

func collectDeviceDeclaredBody(t *testing.T, out map[string]bool, label string, buf []byte) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("%s: discovery payload is not JSON: %v", label, err)
	}
	for _, field := range []string{"state_topic", "command_topic", "json_attributes_topic", "latest_version_topic"} {
		if v, ok := body[field].(string); ok && v != "" {
			out[v] = true
		}
	}
}

// fakePressChannel is a minimal [ChannelInspector] exposing exactly one
// press parameter, enough to drive [DefaultDiscoveryBuilder.BuildChannelEvent].
type fakePressChannel struct{}

func (fakePressChannel) HasParameter(name string) bool { return name == "PRESS_SHORT" }

// fakeCustomDPSource is a minimal [payload.Source] (the interface
// [Event.Source] requires) that also implements
// [payload.HADiscoveryPayloadBuilder] + [payload.Slotted], used to
// drive [DefaultDiscoveryBuilder.aggregateChannel] generically without
// any one custom-DP domain's real model type. The HADiscoveryPayload
// method mirrors the shape every domain's own implementation follows
// (internal/model/custom/switch/payload.go and its siblings): pull the
// state/command topics from the context, hand back a component + body.
// The remaining [payload.Source] methods are unused by
// aggregateChannel and return zero values.
type fakeCustomDPSource struct{ kind string }

func (f fakeCustomDPSource) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	return "switch", map[string]any{
		"state_topic":   ctx.CustomDPStateTopic(),
		"command_topic": ctx.ServiceMethodCommandTopic("set"),
	}
}

func (f fakeCustomDPSource) TopicSlot() payload.TopicSlot {
	return payload.TopicSlot{Bucket: payload.BucketCustom, Parameter: f.kind}
}

func (fakeCustomDPSource) Info() payload.InfoPayload     { return nil }
func (fakeCustomDPSource) Config() payload.ConfigPayload { return nil }
func (fakeCustomDPSource) State() payload.StatePayload   { return nil }
func (fakeCustomDPSource) ServiceMethodNames() []string  { return nil }
func (fakeCustomDPSource) Invoke(context.Context, string, map[string]any, hmenum.CommandPriority) error {
	return nil
}

// fakeUpdateSource mirrors [internal/model/device.Update.HADiscoveryPayload]'s
// topic shape without pulling in the full device model: state and
// latest-version share one topic, and — like the real, fixed
// production type — there is deliberately no command topic (see the
// doc comment on [TestDevicePlaneTopicsRoundTrip]).
type fakeUpdateSource struct{}

func (fakeUpdateSource) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	stateTopic := ctx.CustomDPStateTopic()
	return "update", map[string]any{
		"state_topic":          stateTopic,
		"latest_version_topic": stateTopic,
	}
}
