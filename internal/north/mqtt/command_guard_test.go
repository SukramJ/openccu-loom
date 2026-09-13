// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strings"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// The guards in this file each refuse something. Finding 8 was that none of
// them had a test: every one survived a mutation — `if false && …`, a deleted
// early return, an inverted default arm — with `./internal/...` and the
// contract suite green. A refusal nothing exercises is indistinguishable from
// a refusal that was never written, which is why each test below names the
// mutation it fails under.

// TestStartRefusesAnAttributedRouteSet covers command_subscriber.go's
// `router.Attributed()` refusal.
//
// Mutation it fails under: `if false && router.Attributed()`.
//
// What the guard protects: an overlapping filter pair makes the shared router
// subscribe with MQTT 5.0 Subscription Identifiers, attribution is v5-only,
// and the router never retries an attributed route unattributed. So on an
// overlapping set `north.mqtt.protocol_version: "3.1.1"` stops being a
// dialect choice and becomes a refused Start — the whole command plane down
// at boot, with the state plane unaffected and looking healthy. Refusing at
// registration turns that into a failure at the one moment someone is looking
// at the diff that caused it.
//
// The overlapping pair is installed through the routes override rather than
// by editing the real set, because the real set must stay disjoint — that is
// the property TestCommandFiltersArePairwiseDisjoint pins.
func TestStartRefusesAnAttributedRouteSet(t *testing.T) {
	t.Parallel()

	sub := NewCommandSubscriber(newFilterRecorder(), NewTopicBuilder("gh"), &fakeSink{}, nil)
	sub.routesOverride = func(base string) []commandRoute {
		noop := func(context.Context, hapublisher.Command) {}
		return []commandRoute{
			{base + "/+/+/set", noop},
			{base + "/+/alarm/set", noop},
		}
	}
	t.Cleanup(sub.Close)

	err := sub.Start(context.Background())
	if err == nil {
		t.Fatal("Start accepted an overlapping filter pair — the router subscribes it with MQTT " +
			"5.0 subscription identifiers, which makes north.mqtt.protocol_version: \"3.1.1\" a " +
			"boot failure of the whole command plane")
	}
	if !strings.Contains(err.Error(), "pairwise disjoint") {
		t.Fatalf("Start failed for some other reason than the attributed-mode refusal: %v", err)
	}
}

// TestStartIsRefusedTwice covers the double-`Start` refusal.
//
// Mutation it fails under: `if false && c.currentRouter() != nil`.
//
// What the guard protects: a second Start builds a second router over the
// same client and both stay live — the first one's subscriptions keep their
// handlers and its worker pool keeps its goroutines, while only the handle
// this subscriber holds is replaced. Nothing could ever stop the first one,
// and every inbound command would run twice.
func TestStartIsRefusedTwice(t *testing.T) {
	t.Parallel()

	sub := NewCommandSubscriber(newFilterRecorder(), NewTopicBuilder("gh"), &fakeSink{}, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	t.Cleanup(sub.Close)

	if err := sub.Start(context.Background()); err == nil {
		t.Fatal("a second Start was accepted — it builds a second router over the same client " +
			"and leaves the first one live with no handle to stop it, so every inbound command " +
			"runs twice for the rest of the process")
	}
}

// commandGuardFixture is a started subscriber with every optional sink these
// guards need, plus the client the messages arrive on.
type commandGuardFixture struct {
	client *NoopClient
	sub    *CommandSubscriber
	sched  *fakeScheduleSwitchSink
	comb   *fakeCombinedDPSink
	addon  *fakeAddonUpdateSink
}

func newCommandGuardFixture(t *testing.T) *commandGuardFixture {
	t.Helper()
	f := &commandGuardFixture{
		client: NewNoopClient(),
		sched:  &fakeScheduleSwitchSink{},
		comb:   &fakeCombinedDPSink{},
		addon:  &fakeAddonUpdateSink{},
	}
	f.sub = NewCommandSubscriber(f.client, NewTopicBuilder("gh"), &fakeSink{}, nil).
		WithScheduleSwitchSink(f.sched).
		WithCombinedDPSink(f.comb).
		WithAddonUpdateSink(f.addon)
	if err := f.sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(f.sub.Close)
	return f
}

// deliver pushes one live broker message onto the named subscription and
// blocks until the worker that picked it up is done. The WaitIdle is not
// optional: handlers run on the router's worker pool, so an assertion on a
// sink right after the delivery would otherwise be a race.
func (f *commandGuardFixture) deliver(t *testing.T, filter, topic, payload string) {
	t.Helper()
	if !f.client.DeliverInbound(filter, topic, []byte(payload)) {
		t.Fatalf("no subscriber registered for %q", filter)
	}
	f.sub.WaitIdle()
}

// The two data-point routes every shape below arrives on. Written out because
// these tests drive the plane the way a broker does — one message on one
// subscription — rather than by calling a handler the router parses the topic
// for.
const (
	dpBucketFilter = "gh/+/+/+/+/+/+/set"
	addonFilter    = "gh/system/addon_update/set"
)

// TestScheduleSwitchRejectsAnUnrecognisedPayload covers
// handleScheduleSwitch's payload switch.
//
// Mutation it fails under: replacing the `default:` arm's warn-and-return
// with `enabled = true`.
//
// This is the sharpest of the seven. The handler's job is to enable or
// disable a device's schedule, and its `default` arm is the only thing
// standing between an unrecognised payload and a silent ENABLE. A typo, a
// JSON-quoted `"true"`, an HA template that rendered to `unknown` — each
// would turn a schedule on, on the device, with a debug line at most.
func TestScheduleSwitchRejectsAnUnrecognisedPayload(t *testing.T) {
	t.Parallel()
	f := newCommandGuardFixture(t)

	const topic = "gh/ccu-01/HmIP-RF/0001ABCD/1/schedule/WEEK_PROGRAM_CHANNEL_LOCKS/set"
	for _, payload := range []string{"maybe", `"true"`, "unknown", "", "2", "enable"} {
		f.deliver(t, dpBucketFilter, topic, payload)
	}
	if n := f.sched.count(); n != 0 {
		t.Fatalf("%d unrecognised schedule payloads reached the sink, want 0 — any of them "+
			"silently ENABLES the schedule on the device", n)
	}

	// And the recognised tokens must still get through, or the guard would be
	// refusing the whole shape rather than the payloads outside it.
	for _, tc := range []struct {
		payload string
		want    bool
	}{{"true", true}, {" ON ", true}, {"1", true}, {"False", false}, {"off", false}, {"0", false}} {
		f.deliver(t, dpBucketFilter, topic, tc.payload)
	}
	if n := f.sched.count(); n != 6 {
		t.Fatalf("%d recognised schedule payloads reached the sink, want 6", n)
	}
	f.sched.mu.Lock()
	defer f.sched.mu.Unlock()
	for i, want := range []bool{true, true, true, false, false, false} {
		if f.sched.calls[i].enabled != want {
			t.Errorf("call %d: enabled = %v, want %v", i, f.sched.calls[i].enabled, want)
		}
	}
}

// TestCombinedDPRejectsAnEmptyPayload covers handleCombinedDP's empty-payload
// guard.
//
// Mutation it fails under: deleting the `raw == ""` early return.
//
// The payload is forwarded verbatim to the combined-DP sink, which is where
// it becomes a value on a device. An empty string is not a value: it is what
// a RETRACTION looks like on the wire, and the broker delivers a retraction
// to a command filter exactly like a command.
func TestCombinedDPRejectsAnEmptyPayload(t *testing.T) {
	t.Parallel()
	f := newCommandGuardFixture(t)

	const topic = "gh/ccu-01/HmIP-RF/0001ABCD/1/combined/level/set"
	for _, payload := range []string{"", "   ", "\t\n"} {
		f.deliver(t, dpBucketFilter, topic, payload)
	}
	if n := f.comb.count(); n != 0 {
		t.Fatalf("%d empty combined-DP payloads reached the sink, want 0 — an empty payload is "+
			"a retraction, and the sink would write it to the device as a value", n)
	}
	f.deliver(t, dpBucketFilter, topic, "30")
	if n := f.comb.count(); n != 1 {
		t.Fatalf("a real combined-DP payload reached the sink %d times, want 1", n)
	}
}

// TestAddonUpdateRejectsAnEmptyPayload covers handleAddonUpdateCommand's
// empty-payload guard.
//
// Mutation it fails under: deleting the `TrimSpace(...) == ""` early return.
//
// The handler accepts any non-empty payload so a hand-built tool need not
// match HA's `payload_install` token exactly, which leaves the empty payload
// as the only thing the guard can key on — and the empty payload is what the
// broker delivers when the retained `…/set` topic is cleared. Without the
// guard, retracting that topic starts a CCU add-on install.
func TestAddonUpdateRejectsAnEmptyPayload(t *testing.T) {
	t.Parallel()
	f := newCommandGuardFixture(t)

	for _, payload := range []string{"", "  ", "\n"} {
		f.deliver(t, addonFilter, addonFilter, payload)
	}
	if n := f.addon.count(); n != 0 {
		t.Fatalf("%d empty add-on update payloads triggered an install, want 0 — clearing the "+
			"retained command topic would start a CCU add-on install", n)
	}
	f.deliver(t, addonFilter, addonFilter, "INSTALL")
	if n := f.addon.count(); n != 1 {
		t.Fatalf("INSTALL triggered %d installs, want 1", n)
	}
}
