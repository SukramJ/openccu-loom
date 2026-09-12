// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// availabilityWrite is one write on an availability topic — a flip or a
// retraction — that step 6 routed onto the shared availability publisher.
type availabilityWrite struct {
	name  string
	topic string
	// empty says the write is a retraction, so the recorded payload has to
	// be zero bytes rather than a token.
	empty bool
	// call takes a context for the reason [movedStatePublish.call] does: a
	// closure whose first parameter is a *testing.T reads as a test helper.
	call func(ctx context.Context, b *Bridge) error
}

// TestEveryAvailabilityWriteIsAtLeastOnce is finding F5, asserted on the wire
// in both directions.
//
// Before this step the three availability *publishes* pinned QoS 1 by stated
// policy while every availability *retraction* — and the program-role publish
// — used the state QoS, which is 0. The two halves are now one stated level
// on one publisher, so they cannot drift apart again, and the level is read
// back off the transport call rather than trusted.
//
// Falsifiability: set [hapublisher.AvailabilityConfig.QoS] in
// [newAvailabilityPublisher] to `hapublisher.QoSAtMostOnce` and every row
// fails; route [Bridge.RetractAlarmAvailability] back through
// `b.client.Publish(..., b.cfg.QoS.State, true)` and the alarm retraction row
// alone fails, which is the pre-step state of exactly that write.
func TestEveryAvailabilityWriteIsAtLeastOnce(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	if b.cfg.QoS.State == QoS1 {
		t.Fatalf("fixture: state QoS is already 1, the test cannot show the divergence a retraction used to have")
	}

	base, central, iface, addr := b.cfg.Base, b.cfg.CentralName, "HmIP-RF", "0001ABCD"
	deviceTopic := b.topics.DeviceAvailability(central, iface, addr)
	roleTopic := base + "/" + central + "/hub/programs/12459/execute_available"
	alarmTopic := alarmAvailabilityTopic(base, "erdgeschoss")
	securityTopic := securityAvailabilityTopic(base)
	prog := roleProgram{id: "12459"}

	writes := []availabilityWrite{
		{
			name:  "device flip",
			topic: deviceTopic,
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishAvailability(ctx, central, iface, addr, true)
			},
		},
		{
			name:  "device retraction",
			topic: deviceTopic,
			empty: true,
			call: func(ctx context.Context, b *Bridge) error {
				b.RetractRawStateForDevice(ctx, central, iface, addr)
				return nil
			},
		},
		{
			name:  "program role flip",
			topic: roleTopic,
			call: func(ctx context.Context, b *Bridge) error {
				roles := b.ProgramRoles(central, prog)
				if len(roles) != 1 {
					return fmt.Errorf("fixture: %d roles, want 1", len(roles))
				}
				return b.PublishRoleAvailability(ctx, &roles[0], true)
			},
		},
		{
			name:  "program role retraction",
			topic: roleTopic,
			empty: true,
			call: func(ctx context.Context, b *Bridge) error {
				return b.RetractProgramTopics(ctx, central, prog)
			},
		},
		{
			name:  "alarm zone flip",
			topic: alarmTopic,
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishAlarmAvailability(ctx, alarmTopic, true)
			},
		},
		{
			name:  "alarm zone retraction",
			topic: alarmTopic,
			empty: true,
			call: func(ctx context.Context, b *Bridge) error {
				return b.RetractAlarmAvailability(ctx, alarmTopic)
			},
		},
		{
			name:  "security plane flip",
			topic: securityTopic,
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishSecurityAvailability(ctx, securityTopic, true)
			},
		},
	}

	seen := 0
	for _, w := range writes {
		mp.reset()
		if err := w.call(t.Context(), b); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		found := false
		for _, rec := range mp.recorded() {
			if rec.topic != w.topic {
				continue
			}
			found = true
			seen++
			if rec.qos != QoS1 {
				t.Errorf("%s: %s written at QoS %v, want QoS 1", w.name, rec.topic, rec.qos)
			}
			if !rec.retain {
				t.Errorf("%s: %s written non-retained, want retained", w.name, rec.topic)
			}
			switch {
			case w.empty && rec.payload != "":
				t.Errorf("%s: retraction carried %q, want zero bytes", w.name, rec.payload)
			case !w.empty && rec.payload != "online":
				t.Errorf("%s: flip carried %q, want %q", w.name, rec.payload, "online")
			}
		}
		if !found {
			t.Errorf("%s: nothing was written to %s", w.name, w.topic)
		}
	}
	if seen != len(writes) {
		t.Fatalf("observed %d availability writes, want %d — a row matched nothing and the sweep is partly vacuous",
			seen, len(writes))
	}
}

// TestAvailabilityLayoutAgreesWithTheBridgeStatusTopic is the guard the
// library cannot make on this plane.
//
// [hapublisher.New] compares [hapublisher.Config.StatusTopic] against its
// layout and panics at the composition root on a disagreement, which is what
// #797 wired the discovery runtime's layout up for.
// [hapublisher.AvailabilityConfig] carries no status topic, so its
// constructor has nothing to compare — and this is the one string every
// entity's `availability` list references, where under the default
// `availability_mode: all` a single typo greys out the whole fleet with
// nothing on the wire naming the cause. So the comparison is made here, over
// all three producers of it: the availability layout, the topic builder the
// bridge publishes `online`/`offline` through, and the Last Will the
// composition root configures.
//
// Falsifiability: have [availabilityLayout.Bridge] return
// `l.topics.Base + "/bridge/state"` and every arm fails.
func TestAvailabilityLayoutAgreesWithTheBridgeStatusTopic(t *testing.T) {
	t.Parallel()

	b, _ := newTestBridge(t)
	got, err := b.availabilityBridgeTopic()
	if err != nil {
		t.Fatalf("availabilityBridgeTopic: %v", err)
	}
	if want := b.topics.BridgeStatus(); got != want {
		t.Errorf("availability layout bridge topic = %q, want %q", got, want)
	}
	will, err := b.LastWill()
	if err != nil {
		t.Fatalf("LastWill: %v", err)
	}
	if got != will.Topic {
		t.Errorf("availability layout bridge topic = %q, but the configured will clears %q", got, will.Topic)
	}
}

// TestDeviceAvailabilityIsIndexedAndRetractedByTheSameDerivation is finding
// F6 for the device plane.
//
// Device availability topics were in no index at all: only the security
// plane's marker was recorded (#796) and the alarm plane's (#797), so a
// device's marker could be retracted only by reconstructing its name in a
// second place, and no sweep path could reach it. The shared publisher's map
// is simultaneously the transition gate, the topic listing, the republish
// worklist and the ownership set of its own sweep — so publishing through it
// is what puts the topic somewhere a sweep can walk, and
// [Bridge.deviceAvailabilityTopic] is what makes the retraction name the
// string the publish actually wrote.
//
// Falsifiability: have [availabilityLayout.Availability] append a segment and
// both the identity arm and the index arm fail — the publish would land on
// one topic and every other derivation would name another, which is the
// defect the layout exists to prevent.
func TestDeviceAvailabilityIsIndexedAndRetractedByTheSameDerivation(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	central, iface, addr := b.cfg.CentralName, "HmIP-RF", "0001ABCD"

	topic, err := b.deviceAvailabilityTopic(central, iface, addr)
	if err != nil {
		t.Fatalf("deviceAvailabilityTopic: %v", err)
	}
	if want := b.topics.DeviceAvailability(central, iface, addr); topic != want {
		t.Fatalf("availability layout renders %q, the declaring side names %q", topic, want)
	}

	if err := b.PublishAvailability(t.Context(), central, iface, addr, true); err != nil {
		t.Fatalf("PublishAvailability: %v", err)
	}
	if !slices.Contains(b.avail.Topics(), topic) {
		t.Fatalf("%s did not enter the availability index; index=%v", topic, b.avail.Topics())
	}
	if online, known := b.avail.Online(topic); !known || !online {
		t.Errorf("Online(%s) = (%v, %v), want (true, true)", topic, online, known)
	}

	mp.reset()
	b.RetractRawStateForDevice(t.Context(), central, iface, addr)
	if slices.Contains(b.avail.Topics(), topic) {
		t.Errorf("%s stayed in the availability index after the device was removed", topic)
	}
	cleared := false
	for _, rec := range mp.recorded() {
		if rec.topic == topic && rec.payload == "" && rec.retain {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("no retraction reached %s; writes=%v", topic, mp.recorded())
	}
}

// TestAvailabilityFlipsAreGatedAndResetReopensThem pins the gate the device
// plane gains and the reconnect hazard that comes with it.
//
// The domain layer already gates the device edge through its own
// `availabilityCache`, which it has to: that gate also drives the bus
// lifecycle announcement, and it has to work with no MQTT wired at all. This
// is the publish-side gate underneath it, and it needs the same reconnect
// treatment for the same reason — a broker back without a persistent
// retained store holds none of the markers it remembers.
//
// Falsifiability: drop the `b.avail.Reset()` arm from
// [Bridge.ResetRuntimeGates] and the third arm fails.
func TestAvailabilityFlipsAreGatedAndResetReopensThem(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	flip := func() int {
		mp.reset()
		if err := b.PublishAvailability(t.Context(), b.cfg.CentralName, "HmIP-RF", "0001ABCD", true); err != nil {
			t.Fatalf("PublishAvailability: %v", err)
		}
		return len(mp.recorded())
	}
	if got := flip(); got != 1 {
		t.Fatalf("first flip produced %d messages, want 1", got)
	}
	if got := flip(); got != 0 {
		t.Errorf("second, identical flip produced %d messages, want 0 — the transition gate did not fire", got)
	}
	b.ResetRuntimeGates()
	if got := flip(); got != 1 {
		t.Errorf("flip after ResetRuntimeGates produced %d messages, want 1 — the reset did not reopen the gate", got)
	}
}

// roleProgram is a [pload.MQTTRoleAddressable] whose one declared control
// owns an availability topic, which is the shape [Bridge.PublishRoleAvailability]
// exists for.
type roleProgram struct{ id string }

func (p roleProgram) MQTTTopics(base, central string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{
		State: strings.Join([]string{base, central, "hub", "programs", p.id, "state"}, "/"),
		Set:   strings.Join([]string{base, central, "hub", "programs", p.id, "set"}, "/"),
	}
}

func (p roleProgram) MQTTRoles(base, central string) []pload.MQTTRole {
	prefix := strings.Join([]string{base, central, "hub", "programs", p.id}, "/")
	return []pload.MQTTRole{{
		Topics: pload.MQTTTopicSet{
			Trigger:      prefix + "/trigger",
			Availability: prefix + "/execute_available",
		},
	}}
}
