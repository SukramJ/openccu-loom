// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
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
//   - discovery_combined.go's combined-timer and combined-sensor
//     builders.
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
// docs/parity/by_design.md): an unconfirmed MQTT payload triggering a
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
		base    = "gh"
		central = "ccu"
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

	// --- Combined timer + combined sensor ---
	collectDeviceDeclaredItem(t, declared, d.BuildCombinedTimerDiscovery(central, CombinedTimerEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 3, Kind: "duration", Label: "Zeitdauer",
	}))
	collectDeviceDeclaredItem(t, declared, d.BuildCombinedSensorDiscovery(central, CombinedSensorEvent{
		Interface: iface, DeviceAddress: addr, ChannelNo: 3, Kind: "hs_color", Label: "Farbe",
		ValueTemplate: "{{ value_json.hue }}",
	}))

	// --- Firmware update entity ---
	collectDeviceDeclaredItem(t, declared, d.BuildUpdateDiscovery(central, UpdateEvent{
		Interface: iface, DeviceAddress: addr, Update: fakeUpdateSource{},
	}))

	if len(declared) == 0 {
		t.Fatal("no topics declared; the walk found no discovery payloads and would pass vacuously")
	}

	published := devicePublishedTopics(base, central, iface, addr)
	if len(published) == 0 {
		t.Fatal("no topics published/subscribed; the walk found nothing and would pass vacuously")
	}

	for topic := range declared {
		if !published[topic] {
			t.Errorf("declared but never published/subscribed: %q — a consumer creates this entity "+
				"and it either stays unavailable forever (state) or its commands vanish silently (command)", topic)
		}
	}
	for topic := range published {
		if !declared[topic] {
			t.Logf("published but not declared: %q (no entity is created for it)", topic)
		}
	}
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

// devicePublishedTopics is the set of topics the device plane actually
// writes to or subscribes on for the fixture built above.
//
// State/json-attributes/latest-version entries are derived from the
// same [TopicBuilder] method the real publish call site uses
// (eventbridge.go's publishSlotState/publishCustomDPState via
// bridge.go's PublishSlotState/PublishSlotConfig/PublishCustomDPState,
// PublishChannelEventState, PublishScheduleEntityState/Attrs,
// PublishScheduleSwitchState, PublishWeekProfileState,
// PublishCombinedTimerState/PublishCombinedSensorState,
// PublishUpdateState) — never by re-deriving the topic string by hand.
// The custom-DP service-method entry is the one command topic derived
// the same way: [CommandSubscriber.handleServiceMethod] subscribes the
// exact wildcard shape [TopicBuilder.CustomDPServiceMethod] produces,
// so both discovery's declaration and this "published" anchor calling
// the same [TopicBuilder] method mirrors the two independent call
// sites converging on one shared helper — same pattern as
// [TestSecurityPlaneTopicsRoundTrip]'s reuse of [securityAvailabilityTopic].
//
// Every other command-only entry (bucket-aware per-parameter, press
// button, schedule switch, week profile, combined-DP) is NOT derived
// by calling the matching [TopicBuilder] method again — for these,
// unlike the custom-DP service method above, nothing outside discovery
// ever calls that method; [CommandSubscriber.Start] only matches a
// hand-written wildcard literal, so reusing the builder method would
// only prove discovery agrees with itself. [devWildcardCommand]
// reproduces each wildcard's literal shape independently instead, so a
// drift in either side is what makes the comparison fail.
func devicePublishedTopics(base, central, iface, addr string) map[string]bool {
	const valuesBucket = "values"
	tb := NewTopicBuilder(base)
	return map[string]bool{
		tb.ParameterState(central, iface, addr, 1, valuesBucket, "STATE"):                     true,
		devWildcardCommand(base, central, iface, addr, 1, valuesBucket, "STATE", "set"):       true,
		tb.ParameterConfig(central, iface, addr, 1, valuesBucket, "STATE"):                    true,
		tb.ParameterState(central, iface, addr, 1, valuesBucket, "ACTUAL_TEMPERATURE"):        true,
		tb.ParameterConfig(central, iface, addr, 1, valuesBucket, "ACTUAL_TEMPERATURE"):       true,
		devWildcardCommand(base, central, iface, addr, 1, valuesBucket, "PRESS_SHORT", "set"): true,
		tb.ChannelEvent(central, iface, addr, 2):                                              true,
		tb.SlotState(central, iface, payload.TopicSlot{
			Address: addr, Channel: 3, Bucket: payload.BucketCustom, Parameter: "switch",
		}): true,
		tb.CustomDPServiceMethod(central, iface, payload.TopicSlot{
			Address: addr, Channel: 3, Bucket: payload.BucketCustom, Parameter: "switch",
		}, "set"): true,
		tb.ScheduleEntityState(central, iface, addr, 4):                                  true,
		tb.ScheduleEntityAttrs(central, iface, addr, 4):                                  true,
		tb.ScheduleSwitchState(central, iface, addr, 4, "1_1"):                           true,
		devWildcardCommand(base, central, iface, addr, 4, "schedule", "1_1", "set"):      true,
		tb.WeekProfileState(central, iface, addr, 1):                                     true,
		devWildcardCommand(base, central, iface, addr, 1, "week_profile", "set"):         true,
		tb.CombinedState(central, iface, addr, 3, "duration"):                            true,
		devWildcardCommand(base, central, iface, addr, 3, "combined", "duration", "set"): true,
		tb.CombinedState(central, iface, addr, 3, "hs_color"):                            true,
		tb.DeviceUpdateState(central, iface, addr):                                       true,
	}
}

// devWildcardCommand reproduces one of [CommandSubscriber.Start]'s
// literal command-topic wildcard registrations
// (`<base>/+/+/+/+/+/+/set`, `<base>/+/+/+/+/schedule/+/set`,
// `<base>/+/+/+/+/week_profile/set`, `<base>/+/+/+/+/combined/+/set`)
// with every wildcard segment after `<addr>` substituted by segments,
// and every `+` before it substituted by central/iface/addr. Built as
// a plain string — see the doc comment on [devicePublishedTopics] for
// why this must NOT call a [TopicBuilder] method instead.
func devWildcardCommand(base, central, iface, addr string, channel int, segments ...string) string {
	parts := append([]string{base, central, iface, addr, strconv.Itoa(channel)}, segments...)
	return strings.Join(parts, "/")
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
