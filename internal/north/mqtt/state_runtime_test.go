// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// movedStatePublish is one publish this migration routed off the bare client
// and onto the shared state publisher, together with the topic it lands on.
type movedStatePublish struct {
	name  string
	topic string
	// call takes a context rather than the *testing.T it would be natural
	// to pass: a closure whose first parameter is a *testing.T reads as a
	// test helper to the linter, and these are table rows, not helpers.
	call func(ctx context.Context, b *Bridge) error
}

// movedStatePlane enumerates every retained publish step 5 moved.
//
// It is a table rather than a loop over the package because the point of the
// list is that it is deliberate: the per-datapoint plane is NOT here, and a
// publish appearing in this table is a statement that its bytes and its
// delivery guarantee were checked.
func movedStatePlane(base, central string) []movedStatePublish {
	hub := base + "/" + central + "/hub"
	return []movedStatePublish{
		{
			name:  "bridge health",
			topic: base + "/bridge/health",
			call: func(ctx context.Context, b *Bridge) error {
				return b.AnnounceOnline(ctx)
			},
		},
		{
			name:  "sysvar",
			topic: hub + "/sysvars/party_mode/state",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishSysvar(ctx, central, testSysvar{name: "party_mode"}, 21.5)
			},
		},
		{
			name:  "program state",
			topic: hub + "/programs/12459/state",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishProgram(ctx, central, testProgram{id: "12459"}, true)
			},
		},
		{
			name:  "install mode",
			topic: hub + "/install_mode/HmIP-RF",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishInstallMode(ctx, central, "HmIP-RF", 60)
			},
		},
		{
			name:  "hub system health score",
			topic: base + "/" + central + "/system/health_score",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishHubSystemHealthScore(ctx, central, 97)
			},
		},
		{
			name:  "hub connection latency",
			topic: base + "/" + central + "/system/latency",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishHubConnectionLatency(ctx, central, 12.5)
			},
		},
		{
			name:  "hub last event age",
			topic: base + "/" + central + "/system/last_event_age",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishHubLastEventAge(ctx, central, 4)
			},
		},
		{
			name:  "hub firmware update",
			topic: hub + "/update",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishHubUpdate(ctx, central, "3.79.6", "3.81.5", false)
			},
		},
		{
			name:  "addon update state",
			topic: base + "/system/addon_update/state",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishAddonUpdateState(ctx, "1.2.3", "1.2.4", false)
			},
		},
		{
			name:  "alarm zone state",
			topic: base + "/alarm/erdgeschoss/state",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishAlarmState(ctx, base+"/alarm/erdgeschoss/state", "disarmed")
			},
		},
		{
			name:  "security aggregate state",
			topic: base + "/security/state",
			call: func(ctx context.Context, b *Bridge) error {
				return b.PublishSecurityState(ctx, base+"/security/state", []byte(`{"state":"ok"}`))
			},
		},
	}
}

// TestEveryMovedStatePublishIsAtMostOnce reads the QoS back off the transport
// call for every publish step 5 moved onto the shared state publisher.
//
// This is the one thing no golden file can see. [hapublisher.StateConfig.QoS]
// reads its zero value as *unset* and resolves it to QoS 1, while this
// daemon's state plane has always been QoS 0 ([DefaultQoS]) — so handing the
// configured level over as a plain byte, or forgetting the field, would raise
// the delivery guarantee of the whole plane on the first boot after the
// migration with not one payload byte different anywhere.
//
// Falsifiability: set [StateConfig.QoS] in [newStatePublisher] to
// [hapublisher.QoSUnset] (or delete the field) and every row here fails with
// "published at QoS 1".
func TestEveryMovedStatePublishIsAtMostOnce(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	// The fixture guard: QoS 0 is what makes the divergence observable at
	// all, exactly as TestAvailabilityIsAlwaysQoS1 states it.
	if b.cfg.QoS.State != QoS0 {
		t.Fatalf("fixture: state QoS is %v, want QoS 0 — the test cannot show the divergence", b.cfg.QoS.State)
	}

	moved := movedStatePlane(b.cfg.Base, b.cfg.CentralName)
	seen := 0
	for _, mv := range moved {
		mp.reset()
		if err := mv.call(t.Context(), b); err != nil {
			t.Fatalf("%s: %v", mv.name, err)
		}
		found := false
		for _, rec := range mp.recorded() {
			if rec.topic != mv.topic {
				continue
			}
			found = true
			seen++
			if rec.qos != QoS0 {
				t.Errorf("%s: %s published at QoS %v, want QoS 0", mv.name, rec.topic, rec.qos)
			}
			if !rec.retain {
				t.Errorf("%s: %s published non-retained, want retained", mv.name, rec.topic)
			}
		}
		if !found {
			t.Errorf("%s: nothing was published to %s", mv.name, mv.topic)
		}
	}
	if seen != len(moved) {
		t.Fatalf("observed %d moved state publishes, want %d — a row matched nothing and the sweep is partly vacuous",
			seen, len(moved))
	}
}

// TestMovedStateRetractionsAreAtMostOnceAndEmpty pins the other half of the
// wire contract: a retraction on the moved plane is an empty retained payload
// at the same QoS as a publish.
//
// It goes through [hapublisher.StatePublisher.Evict] rather than a
// zero-length publish because the publish path refuses empty bytes with
// [hapublisher.ErrEmptyStatePayload] — a deletion has to be asked for on
// purpose. That refusal is asserted here too, so a later caller that reaches
// for `Publish(topic, nil)` finds out in a test rather than by deleting an
// entity.
//
// Falsifiability: route [Bridge.RetractSysvarState] back through
// `b.state.Publish(ctx, topic, nil)` and the refusal arm turns the retraction
// into an error; point it at `b.client.Publish(..., QoS1, true)` and the QoS
// arm fails.
func TestMovedStateRetractionsAreAtMostOnceAndEmpty(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	sv := testSysvar{name: "party_mode"}
	topic := b.cfg.Base + "/" + b.cfg.CentralName + "/hub/sysvars/party_mode/state"

	if err := b.PublishSysvar(t.Context(), b.cfg.CentralName, sv, 21.5); err != nil {
		t.Fatalf("PublishSysvar: %v", err)
	}
	if !rawTopicTracked(b, topic) {
		t.Fatalf("%s did not enter the retained-topic index", topic)
	}

	mp.reset()
	if err := b.RetractSysvarState(t.Context(), b.cfg.CentralName, sv); err != nil {
		t.Fatalf("RetractSysvarState: %v", err)
	}
	recs := mp.recorded()
	if len(recs) != 1 {
		t.Fatalf("retraction produced %d publishes, want 1", len(recs))
	}
	if recs[0].topic != topic || recs[0].payload != "" || !recs[0].retain {
		t.Errorf("retraction = %+v, want empty retained payload on %s", recs[0], topic)
	}
	if recs[0].qos != QoS0 {
		t.Errorf("retraction published at QoS %v, want QoS 0 — publish and retract must not disagree", recs[0].qos)
	}
	if rawTopicTracked(b, topic) {
		t.Errorf("%s stayed in the retained-topic index after the retraction", topic)
	}

	// And the reason it is Evict and not Publish.
	if _, err := b.state.Publish(t.Context(), topic, nil); !errors.Is(err, hapublisher.ErrEmptyStatePayload) {
		t.Errorf("Publish(nil) error = %v, want ErrEmptyStatePayload", err)
	}
}

// TestMovedStatePlaneDedupsAndResetOpensTheGate is the win the move buys and
// the hazard that comes with it, in one test.
//
// The planes moved here carry no per-emission field, so re-publishing an
// unchanged value is byte-identical and the shared gate swallows it — which
// is the whole reason the per-datapoint plane, whose `modified_at` changes on
// every emission, stayed behind. The hazard is a broker that came back
// without its retained store: the gate would then suppress writes for bytes
// nothing holds, so [Bridge.ResetRuntimeGates] has to open it on every
// reconnect.
//
// Falsifiability: empty the body of [Bridge.ResetRuntimeGates] and the third
// arm fails with "the reset did not reopen the gate"; route
// [Bridge.PublishSysvar] back through `b.client.Publish` and the second arm
// fails with two publishes instead of one.
func TestMovedStatePlaneDedupsAndResetOpensTheGate(t *testing.T) {
	t.Parallel()

	b, mp := newTestBridge(t)
	sv := testSysvar{name: "party_mode"}
	publish := func() int {
		mp.reset()
		if err := b.PublishSysvar(t.Context(), b.cfg.CentralName, sv, 21.5); err != nil {
			t.Fatalf("PublishSysvar: %v", err)
		}
		return len(mp.recorded())
	}

	if got := publish(); got != 1 {
		t.Fatalf("first publish produced %d messages, want 1", got)
	}
	if got := publish(); got != 0 {
		t.Errorf("second, identical publish produced %d messages, want 0 — the dedup gate did not fire", got)
	}
	b.ResetRuntimeGates()
	if got := publish(); got != 1 {
		t.Errorf("publish after ResetRuntimeGates produced %d messages, want 1 — the reset did not reopen the gate", got)
	}
}

// TestRenderValueMatchesTheSharedRenderer checks, rather than assumes, that
// [hapublisher.RenderRawValue] renders this daemon's values the way this
// daemon has always rendered them.
//
// A sibling migration found the shared renderer writing a Go bool as
// `"true"`/`"false"` where that bridge published `"1"`/`"0"` — a payload
// change wearing a refactor's clothes. Here the two agree on every scalar,
// and they disagree on exactly two shapes, both of which this daemon keeps:
// a nil value is [ErrNilValue] rather than the shared sentinel because
// callers switch on it, and a []byte stays JSON-encoded.
//
// Falsifiability: delete the `case []byte` arm from [renderValue] and the
// byte-slice row fails with a raw `abc` instead of a quoted base64 string;
// delete the `case nil` arm and the nil row fails on the sentinel.
func TestRenderValueMatchesTheSharedRenderer(t *testing.T) {
	t.Parallel()

	agree := []any{
		true, false,
		"", "on", "21.5",
		int(0), int(-7), int32(42), int64(-9007199254740993),
		float32(0.1), float32(-273.15),
		0.0, 21.5, 0.0000001, -1.5e-7, 1234567.891,
		[]string{"a", "b"},
		map[string]any{"k": 1.0},
	}
	for _, v := range agree {
		mine, myErr := renderValue(v)
		theirs, theirErr := hapublisher.RenderRawValue(v)
		if myErr != nil || theirErr != nil {
			t.Errorf("%#v: renderValue err=%v, RenderRawValue err=%v", v, myErr, theirErr)
			continue
		}
		if !bytes.Equal(mine, theirs) {
			t.Errorf("%#v: renderValue = %q, RenderRawValue = %q", v, mine, theirs)
		}
	}

	// Divergence 1 — nil keeps this daemon's own sentinel.
	if _, err := renderValue(nil); !errors.Is(err, ErrNilValue) {
		t.Errorf("renderValue(nil) error = %v, want ErrNilValue", err)
	}

	// Divergence 2 — a []byte stays JSON-encoded, which is a quoted base64
	// string, not the shared renderer's raw passthrough.
	got, err := renderValue([]byte("abc"))
	if err != nil {
		t.Fatalf("renderValue([]byte): %v", err)
	}
	if string(got) != `"YWJj"` {
		t.Errorf("renderValue([]byte(\"abc\")) = %q, want %q", got, `"YWJj"`)
	}
	shared, err := hapublisher.RenderRawValue([]byte("abc"))
	if err != nil {
		t.Fatalf("RenderRawValue([]byte): %v", err)
	}
	if bytes.Equal(shared, got) {
		t.Error("the []byte divergence has gone away in the shared renderer — drop the local arm and this note")
	}
}

// TestRuntimeQoSStatesEveryLevel pins the translation itself, including the
// one that matters: this daemon's QoS 0 must become
// [hapublisher.QoSAtMostOnce] and never [hapublisher.QoSUnset], because the
// shared constructors read unset as QoS 1.
//
// Falsifiability: return [hapublisher.QoSUnset] for QoS0 and the first row
// fails.
func TestRuntimeQoSStatesEveryLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   QoS
		want hapublisher.QoS
		wire byte
	}{
		{QoS0, hapublisher.QoSAtMostOnce, 0},
		{QoS1, hapublisher.QoSAtLeastOnce, 1},
		{QoS2, hapublisher.QoSExactlyOnce, 2},
	}
	for _, tc := range cases {
		got := runtimeQoS(tc.in)
		if got != tc.want {
			t.Errorf("runtimeQoS(%v) = %v, want %v", tc.in, got, tc.want)
		}
		if got == hapublisher.QoSUnset {
			t.Errorf("runtimeQoS(%v) is QoSUnset, which the shared publisher reads as QoS 1", tc.in)
		}
		wire, ok := got.Wire()
		if !ok || wire != tc.wire {
			t.Errorf("runtimeQoS(%v).Wire() = (%d, %v), want (%d, true)", tc.in, wire, ok, tc.wire)
		}
	}
}

// --- fixtures -------------------------------------------------------------

// rawTopicTracked reports whether topic sits in the bridge's retained-topic
// index, under the lock the index is written with.
func rawTopicTracked(b *Bridge, topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.rawTopics[topic]
	return ok
}

// testSysvar is the minimal [pload.MQTTAddressable] the hub-plane publishers
// resolve their topics through, so the test drives the production topic
// derivation rather than a literal.
type testSysvar struct{ name string }

func (s testSysvar) MQTTTopics(base, central string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{
		State: strings.Join([]string{base, central, "hub", "sysvars", s.name, "state"}, "/"),
		Set:   strings.Join([]string{base, central, "hub", "sysvars", s.name, "set"}, "/"),
	}
}

// testProgram is the program-plane counterpart of [testSysvar].
type testProgram struct{ id string }

func (p testProgram) MQTTTopics(base, central string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{
		State: strings.Join([]string{base, central, "hub", "programs", p.id, "state"}, "/"),
		Set:   strings.Join([]string{base, central, "hub", "programs", p.id, "set"}, "/"),
	}
}
