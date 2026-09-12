// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeAlarmSink is an in-memory AlarmSink double recording every call so
// tests can assert exactly which verb the raw `<base>/alarm/<area>/set`
// command plane dispatched, with which area/mode/code.
type fakeAlarmSink struct {
	mu                sync.Mutex
	armCalls          []fakeAlarmArmCall
	disarmCalls       []fakeAlarmCodeCall
	silenceCalls      []fakeAlarmCodeCall
	panicCalls        []string
	masterArmCalls    []hmenum.AlarmMode
	masterDisarm      int
	resetMotionCalls  []string
	masterResetMotion int
}

type fakeAlarmArmCall struct {
	area string
	mode hmenum.AlarmMode
	code string
}

type fakeAlarmCodeCall struct{ area, code string }

func (f *fakeAlarmSink) Arm(_ context.Context, areaID string, mode hmenum.AlarmMode, code string) error {
	f.mu.Lock()
	f.armCalls = append(f.armCalls, fakeAlarmArmCall{areaID, mode, code})
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) Disarm(_ context.Context, areaID, code string) error {
	f.mu.Lock()
	f.disarmCalls = append(f.disarmCalls, fakeAlarmCodeCall{areaID, code})
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) Silence(_ context.Context, areaID, code string) error {
	f.mu.Lock()
	f.silenceCalls = append(f.silenceCalls, fakeAlarmCodeCall{areaID, code})
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) Panic(_ context.Context, areaID string) error {
	f.mu.Lock()
	f.panicCalls = append(f.panicCalls, areaID)
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) MasterArm(_ context.Context, mode hmenum.AlarmMode) error {
	f.mu.Lock()
	f.masterArmCalls = append(f.masterArmCalls, mode)
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) MasterDisarm(context.Context) error {
	f.mu.Lock()
	f.masterDisarm++
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) ResetMotion(_ context.Context, zoneID string) error {
	f.mu.Lock()
	f.resetMotionCalls = append(f.resetMotionCalls, zoneID)
	f.mu.Unlock()
	return nil
}

func (f *fakeAlarmSink) MasterResetMotion(context.Context) error {
	f.mu.Lock()
	f.masterResetMotion++
	f.mu.Unlock()
	return nil
}

// alarmSinkSnapshot is a lock-free copy of fakeAlarmSink's recorded
// calls, safe to pass around and print in test failure messages.
type alarmSinkSnapshot struct {
	armCalls       []fakeAlarmArmCall
	disarmCalls    []fakeAlarmCodeCall
	silenceCalls   []fakeAlarmCodeCall
	panicCalls     []string
	masterArmCalls []hmenum.AlarmMode
	masterDisarm   int
}

func (f *fakeAlarmSink) snapshot() alarmSinkSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return alarmSinkSnapshot{
		armCalls: append([]fakeAlarmArmCall(nil), f.armCalls...), disarmCalls: append([]fakeAlarmCodeCall(nil), f.disarmCalls...),
		silenceCalls: append([]fakeAlarmCodeCall(nil), f.silenceCalls...), panicCalls: append([]string(nil), f.panicCalls...),
		masterArmCalls: append([]hmenum.AlarmMode(nil), f.masterArmCalls...), masterDisarm: f.masterDisarm,
	}
}

// alarmCommandFilter is the route the daemon-level alarm plane registers.
// Written out because these tests drive the plane the way a broker does —
// one message on one subscription — rather than by calling the handler,
// which is no longer reachable from outside the router that parses the topic
// for it.
const alarmCommandFilter = "gh/alarm/+/set"

// alarmFixture is a started CommandSubscriber wired only with sink as its
// AlarmSink — the CommandSink dependency stays nil since none of these tests
// exercise the datapoint/sysvar/program plane — plus the client the messages
// arrive on.
type alarmFixture struct {
	client *NoopClient
	sub    *CommandSubscriber
}

// newAlarmCommandSubscriber builds and starts the fixture.
func newAlarmCommandSubscriber(t *testing.T, sink AlarmSink) *alarmFixture {
	t.Helper()
	client := NewNoopClient()
	sub := NewCommandSubscriber(client, NewTopicBuilder("gh"), nil, nil).WithAlarmSink(sink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	return &alarmFixture{client: client, sub: sub}
}

// deliver pushes one live broker message onto the alarm subscription and
// blocks until the worker that picked it up is done.
//
// The WaitIdle is not optional and not a tidiness measure: handlers run on
// the router's worker pool, never on the goroutine that delivered the
// message, so an assertion on the sink immediately after this call would be
// a race. That is the handler-goroutine contract this plane moved onto.
func (f *alarmFixture) deliver(t *testing.T, topic, payload string) {
	t.Helper()
	f.deliverAs(t, topic, payload, false)
}

// deliverRetained is the retained-replay form.
func (f *alarmFixture) deliverRetained(t *testing.T, topic, payload string) {
	t.Helper()
	f.deliverAs(t, topic, payload, true)
}

func (f *alarmFixture) deliverAs(t *testing.T, topic, payload string, retained bool) {
	t.Helper()
	deliver := f.client.DeliverInbound
	if retained {
		deliver = f.client.DeliverInboundRetained
	}
	if !deliver(alarmCommandFilter, topic, []byte(payload)) {
		t.Fatalf("no subscriber registered for %q — the alarm plane went silent", alarmCommandFilter)
	}
	f.sub.WaitIdle()
}

// --- TRIGGER -> panic ---

// TestHandleAlarmCommand_Trigger_RoutesToPanic is the HA TRIGGER
// affordance (notes/concepts/alarm-concept.md §7): a bare "TRIGGER" payload on an
// area's command topic must route to the sink's loud panic path, not
// any of the arm/disarm/silence verbs.
func TestHandleAlarmCommand_Trigger_RoutesToPanic(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", "TRIGGER")

	got := sink.snapshot()
	if len(got.panicCalls) != 1 || got.panicCalls[0] != "eg" {
		t.Fatalf("panicCalls = %v, want [eg]", got.panicCalls)
	}
	if len(got.armCalls) != 0 || len(got.disarmCalls) != 0 || len(got.silenceCalls) != 0 {
		t.Fatalf("TRIGGER must not touch any other verb: %+v", got)
	}
}

// TestHandleAlarmCommand_MasterTrigger_Dropped asserts TRIGGER has no
// aggregate form: the reserved "master" area segment drops it silently
// rather than firing every area's panic path from one command.
func TestHandleAlarmCommand_MasterTrigger_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/master/set", "TRIGGER")

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for master TRIGGER", got.panicCalls)
	}
}

// --- arm / disarm / silence, bare-string and JSON-envelope payloads ---

func TestHandleAlarmCommand_ArmWithCode_ParsesJSONEnvelope(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", `{"action":"ARM_AWAY","code":"1234"}`)

	got := sink.snapshot()
	if len(got.armCalls) != 1 {
		t.Fatalf("armCalls = %+v, want exactly one", got.armCalls)
	}
	if got.armCalls[0] != (fakeAlarmArmCall{area: "eg", mode: hmenum.AlarmModeFull, code: "1234"}) {
		t.Errorf("armCalls[0] = %+v, want {eg full 1234}", got.armCalls[0])
	}
}

func TestHandleAlarmCommand_Disarm_BareStringPayloadCarriesNoCode(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", "DISARM")

	got := sink.snapshot()
	if len(got.disarmCalls) != 1 || got.disarmCalls[0] != (fakeAlarmCodeCall{area: "eg", code: ""}) {
		t.Fatalf("disarmCalls = %+v, want [{eg }]", got.disarmCalls)
	}
}

func TestHandleAlarmCommand_Silence(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", `{"action":"SILENCE","code":"9999"}`)

	got := sink.snapshot()
	if len(got.silenceCalls) != 1 || got.silenceCalls[0] != (fakeAlarmCodeCall{area: "eg", code: "9999"}) {
		t.Fatalf("silenceCalls = %+v, want [{eg 9999}]", got.silenceCalls)
	}
}

func TestHandleAlarmCommand_MasterSilence_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/master/set", "SILENCE")

	if got := sink.snapshot(); len(got.silenceCalls) != 0 {
		t.Fatalf("silenceCalls = %+v, want none for master SILENCE", got.silenceCalls)
	}
}

// --- master aggregate verbs ---

func TestHandleAlarmCommand_MasterArmAndMasterDisarm(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/master/set", "ARM_NIGHT")
	sub.deliver(t, "gh/alarm/master/set", "DISARM")

	got := sink.snapshot()
	if len(got.masterArmCalls) != 1 || got.masterArmCalls[0] != hmenum.AlarmModeNight {
		t.Fatalf("masterArmCalls = %v, want [night]", got.masterArmCalls)
	}
	if got.masterDisarm != 1 {
		t.Fatalf("masterDisarm = %d, want 1", got.masterDisarm)
	}
	// The master form never touches the per-area verbs.
	if len(got.armCalls) != 0 || len(got.disarmCalls) != 0 {
		t.Fatalf("master verbs leaked into per-area calls: %+v", got)
	}
}

// --- guard rails ---

// TestHandleAlarmCommand_RetainedMessage_Dropped pins that a retained
// `/set` replay never reaches the alarm engine.
//
// The mechanism moved with the router adoption and the guarantee did not:
// every handler used to open with its own `if retained { debug; return }`,
// and the drop is now [hapublisher.CommandConfig.DeliverRetained] staying
// off — one decision for the whole plane instead of thirteen copies of it.
// What it keeps out is unchanged: Home Assistant never publishes a command
// topic retained, so a retained ARM on an alarm topic is somebody's
// `mosquitto_pub -r` left behind, and the broker replays it on every single
// (re)subscribe.
func TestHandleAlarmCommand_RetainedMessage_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliverRetained(t, "gh/alarm/eg/set", "TRIGGER")

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for a retained message", got.panicCalls)
	}
}

func TestHandleAlarmCommand_UnknownAction_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", "BOGUS_ACTION")

	got := sink.snapshot()
	if len(got.armCalls)+len(got.disarmCalls)+len(got.silenceCalls)+len(got.panicCalls) != 0 {
		t.Fatalf("unknown action dispatched a call: %+v", got)
	}
}

// TestHandleAlarmCommand_MalformedTopic_Dropped pins that a topic the alarm
// route does not claim reaches no verb.
//
// It used to exercise a shape re-check inside the handler — `len(parts) != 3
// || parts[0] != "alarm"` — which is gone, because the route IS the shape
// check now: the router resolves the topic against its filters before a
// handler exists, and a topic matching none of them goes to
// [hapublisher.CommandConfig.OnUnroutable] instead. The delivery is
// therefore forced past the subscription on purpose, the way a shared
// broker's cross-talk would arrive, and the assertion is the same one that
// mattered: no alarm verb ran.
func TestHandleAlarmCommand_MalformedTopic_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/set", "TRIGGER") // missing the area segment

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for a malformed topic", got.panicCalls)
	}
}

func TestHandleAlarmCommand_NilSink_DroppedWithoutPanic(t *testing.T) {
	t.Parallel()
	client := NewNoopClient()
	sub := NewCommandSubscriber(client, NewTopicBuilder("gh"), nil, nil) // no WithAlarmSink
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)

	// Must not panic on a nil alarmSink.
	f := &alarmFixture{client: client, sub: sub}
	f.deliver(t, "gh/alarm/eg/set", "TRIGGER")
}

func TestHandleAlarmCommand_EmptyPayload_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.deliver(t, "gh/alarm/eg/set", "  ")

	got := sink.snapshot()
	if len(got.armCalls)+len(got.disarmCalls)+len(got.silenceCalls)+len(got.panicCalls) != 0 {
		t.Fatalf("empty payload dispatched a call: %+v", got)
	}
}
