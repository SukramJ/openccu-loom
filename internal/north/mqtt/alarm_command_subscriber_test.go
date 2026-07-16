// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	mu             sync.Mutex
	armCalls       []fakeAlarmArmCall
	disarmCalls    []fakeAlarmCodeCall
	silenceCalls   []fakeAlarmCodeCall
	panicCalls     []string
	masterArmCalls []hmenum.AlarmMode
	masterDisarm   int
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

// newAlarmCommandSubscriber builds a CommandSubscriber wired only with
// sink as its AlarmSink — the CommandSink dependency stays nil since
// none of these tests exercise the datapoint/sysvar/program plane.
func newAlarmCommandSubscriber(t *testing.T, sink AlarmSink) *CommandSubscriber {
	t.Helper()
	sub := NewCommandSubscriber(NewNoopClient(), NewTopicBuilder("gh"), nil, nil).WithAlarmSink(sink)
	t.Cleanup(sub.Close)
	return sub
}

// --- TRIGGER -> panic ---

// TestHandleAlarmCommand_Trigger_RoutesToPanic is the HA TRIGGER
// affordance (docs/alarm-concept.md §7): a bare "TRIGGER" payload on an
// area's command topic must route to the sink's loud panic path, not
// any of the arm/disarm/silence verbs.
func TestHandleAlarmCommand_Trigger_RoutesToPanic(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("TRIGGER"), false)
	sub.WaitIdle()

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

	sub.handleAlarmCommand("gh/alarm/master/set", []byte("TRIGGER"), false)
	sub.WaitIdle()

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for master TRIGGER", got.panicCalls)
	}
}

// --- arm / disarm / silence, bare-string and JSON-envelope payloads ---

func TestHandleAlarmCommand_ArmWithCode_ParsesJSONEnvelope(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte(`{"action":"ARM_AWAY","code":"1234"}`), false)
	sub.WaitIdle()

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

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("DISARM"), false)
	sub.WaitIdle()

	got := sink.snapshot()
	if len(got.disarmCalls) != 1 || got.disarmCalls[0] != (fakeAlarmCodeCall{area: "eg", code: ""}) {
		t.Fatalf("disarmCalls = %+v, want [{eg }]", got.disarmCalls)
	}
}

func TestHandleAlarmCommand_Silence(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte(`{"action":"SILENCE","code":"9999"}`), false)
	sub.WaitIdle()

	got := sink.snapshot()
	if len(got.silenceCalls) != 1 || got.silenceCalls[0] != (fakeAlarmCodeCall{area: "eg", code: "9999"}) {
		t.Fatalf("silenceCalls = %+v, want [{eg 9999}]", got.silenceCalls)
	}
}

func TestHandleAlarmCommand_MasterSilence_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/master/set", []byte("SILENCE"), false)
	sub.WaitIdle()

	if got := sink.snapshot(); len(got.silenceCalls) != 0 {
		t.Fatalf("silenceCalls = %+v, want none for master SILENCE", got.silenceCalls)
	}
}

// --- master aggregate verbs ---

func TestHandleAlarmCommand_MasterArmAndMasterDisarm(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/master/set", []byte("ARM_NIGHT"), false)
	sub.handleAlarmCommand("gh/alarm/master/set", []byte("DISARM"), false)
	sub.WaitIdle()

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

func TestHandleAlarmCommand_RetainedMessage_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("TRIGGER"), true)
	sub.WaitIdle()

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for a retained message", got.panicCalls)
	}
}

func TestHandleAlarmCommand_UnknownAction_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("BOGUS_ACTION"), false)
	sub.WaitIdle()

	got := sink.snapshot()
	if len(got.armCalls)+len(got.disarmCalls)+len(got.silenceCalls)+len(got.panicCalls) != 0 {
		t.Fatalf("unknown action dispatched a call: %+v", got)
	}
}

func TestHandleAlarmCommand_MalformedTopic_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/set", []byte("TRIGGER"), false) // missing the area segment
	sub.WaitIdle()

	if got := sink.snapshot(); len(got.panicCalls) != 0 {
		t.Fatalf("panicCalls = %v, want none for a malformed topic", got.panicCalls)
	}
}

func TestHandleAlarmCommand_NilSink_DroppedWithoutPanic(t *testing.T) {
	t.Parallel()
	sub := NewCommandSubscriber(NewNoopClient(), NewTopicBuilder("gh"), nil, nil) // no WithAlarmSink
	t.Cleanup(sub.Close)

	// Must not panic on a nil alarmSink.
	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("TRIGGER"), false)
	sub.WaitIdle()
}

func TestHandleAlarmCommand_EmptyPayload_Dropped(t *testing.T) {
	t.Parallel()
	sink := &fakeAlarmSink{}
	sub := newAlarmCommandSubscriber(t, sink)

	sub.handleAlarmCommand("gh/alarm/eg/set", []byte("  "), false)
	sub.WaitIdle()

	got := sink.snapshot()
	if len(got.armCalls)+len(got.disarmCalls)+len(got.silenceCalls)+len(got.panicCalls) != 0 {
		t.Fatalf("empty payload dispatched a call: %+v", got)
	}
}
