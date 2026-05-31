// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type fakeSink struct {
	setValues  atomic.Int32
	setSysvars atomic.Int32
	triggers   atomic.Int32
	lastVal    struct {
		central, iface, chanAddr string
		param                    string
		value                    any
	}
	lastSysvar struct {
		central, name string
		value         any
	}
	lastProgram struct{ central, id string }
}

func (f *fakeSink) SetValue(_ context.Context, central, iface, chanAddr string,
	param hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	f.setValues.Add(1)
	f.lastVal.central = central
	f.lastVal.iface = iface
	f.lastVal.chanAddr = chanAddr
	f.lastVal.param = string(param)
	f.lastVal.value = v
	return nil
}

func (f *fakeSink) SetSysvar(_ context.Context, central, name string, v any) error {
	f.setSysvars.Add(1)
	f.lastSysvar.central = central
	f.lastSysvar.name = name
	f.lastSysvar.value = v
	return nil
}

func (f *fakeSink) TriggerProgram(_ context.Context, central, id string) error {
	f.triggers.Add(1)
	f.lastProgram.central = central
	f.lastProgram.id = id
	return nil
}

func TestCommandSubscriberDataPointTopic(t *testing.T) {
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	if sink.setValues.Load() != 1 {
		t.Fatalf("calls=%d", sink.setValues.Load())
	}
	if sink.lastVal.central != "ccu-01" || sink.lastVal.chanAddr != "0001ABCD:1" ||
		sink.lastVal.param != "STATE" || sink.lastVal.value != true {
		t.Fatalf("last=%+v", sink.lastVal)
	}
}

func TestCommandSubscriberSysvarTopic(t *testing.T) {
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())
	noop.DeliverInbound("openccu-loom/+/hub/sysvars/+/set",
		"openccu-loom/ccu-01/hub/sysvars/PartyMode/set", []byte("false"))
	if sink.setSysvars.Load() != 1 || sink.lastSysvar.name != "PartyMode" || sink.lastSysvar.value != false {
		t.Fatalf("sysvar call: %+v", sink.lastSysvar)
	}
}

func TestCommandSubscriberProgramTopic(t *testing.T) {
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())
	noop.DeliverInbound("openccu-loom/+/hub/programs/+/trigger",
		"openccu-loom/ccu-01/hub/programs/Morning/trigger", nil)
	if sink.triggers.Load() != 1 || sink.lastProgram.id != "Morning" {
		t.Fatalf("program: %+v", sink.lastProgram)
	}
}

// --- CDP Invocation tests ---

type fakeCDPSink struct {
	calls                                     atomic.Int32
	lastErr                                   error // when non-nil, InvokeCustomDP returns this
	lastCentral, lastDevice, lastName, lastOp string
	lastParams                                map[string]any
	lastPrio                                  hmenum.CommandPriority
}

func (f *fakeCDPSink) InvokeCustomDP(_ context.Context, central, device, name, op string,
	params map[string]any, prio hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.lastCentral = central
	f.lastDevice = device
	f.lastName = name
	f.lastOp = op
	f.lastParams = params
	f.lastPrio = prio
	return f.lastErr
}

// InvokeChannelService satisfies the CDPInvocationSink interface for
// the ADR 0009 service-method dispatch tests. Records the invocation
// in the same fields as InvokeCustomDP for assertion convenience —
// `name` is reused for the channel suffix to keep the fake compact.
func (f *fakeCDPSink) InvokeChannelService(_ context.Context,
	central, _, device string, channel int,
	method string, params map[string]any, prio hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.lastCentral = central
	f.lastDevice = device
	f.lastName = fmt.Sprintf("%s:%d", device, channel)
	f.lastOp = method
	f.lastParams = params
	f.lastPrio = prio
	return f.lastErr
}

func TestCommandSubscriberCDPInvoke(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	payload, _ := json.Marshal(CDPInvokePayload{
		Params:   map[string]any{"brightness": 0.8},
		Priority: "high",
	})
	ok := noop.DeliverInbound("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/0001ABCD/cdps/light_dp/turn_on/invoke", payload)
	if !ok {
		t.Fatal("subscription did not match")
	}
	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 CDP call, got %d", cdpSink.calls.Load())
	}
	if cdpSink.lastCentral != "ccu-01" {
		t.Errorf("central: got %q want %q", cdpSink.lastCentral, "ccu-01")
	}
	if cdpSink.lastDevice != "0001ABCD" {
		t.Errorf("device: got %q want %q", cdpSink.lastDevice, "0001ABCD")
	}
	if cdpSink.lastName != "light_dp" {
		t.Errorf("name: got %q want %q", cdpSink.lastName, "light_dp")
	}
	if cdpSink.lastOp != "turn_on" {
		t.Errorf("operation: got %q want %q", cdpSink.lastOp, "turn_on")
	}
	if cdpSink.lastPrio != hmenum.CommandPriorityHigh {
		t.Errorf("priority: got %v want High", cdpSink.lastPrio)
	}
	if v, ok := cdpSink.lastParams["brightness"]; !ok || v != 0.8 {
		t.Errorf("params: got %v", cdpSink.lastParams)
	}
}

func TestCommandSubscriberCDPInvokePriorities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want hmenum.CommandPriority
	}{
		{"critical", hmenum.CommandPriorityCritical},
		{"low", hmenum.CommandPriorityLow},
		{"high", hmenum.CommandPriorityHigh},
		{"", hmenum.CommandPriorityHigh},
		{"unknown", hmenum.CommandPriorityHigh},
	}
	for _, c := range cases {
		got := parseMQTTPriority(c.in)
		if got != c.want {
			t.Errorf("parseMQTTPriority(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCommandSubscriberCDPInvokeBadPayload(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Deliver malformed JSON — should not crash and should not call the sink.
	noop.DeliverInbound("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/0001ABCD/cdps/light_dp/turn_on/invoke",
		[]byte("{not valid json"))
	if cdpSink.calls.Load() != 0 {
		t.Fatalf("expected 0 calls on bad payload, got %d", cdpSink.calls.Load())
	}
}

func TestCommandSubscriberCDPInvokeNilSink(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	// No CDP sink wired.
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())

	// Should not panic even without a CDP sink.
	noop.DeliverInbound("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/0001ABCD/cdps/light_dp/turn_on/invoke",
		[]byte(`{}`))
}

func TestCommandSubscriberCDPInvokeEmptyPayload(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Empty payload → default priority + nil params → should still dispatch.
	noop.DeliverInbound("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/0001ABCD/cdps/light_dp/turn_off/invoke",
		[]byte(""))
	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 call on empty payload, got %d", cdpSink.calls.Load())
	}
	if cdpSink.lastPrio != hmenum.CommandPriorityHigh {
		t.Errorf("expected default High priority, got %v", cdpSink.lastPrio)
	}
}

func TestCommandSubscriberCDPInvokeSinkError(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdpSink := &fakeCDPSink{lastErr: errors.New("device not found")}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdpSink)
	_ = sub.Start(context.Background())

	// Sink error must be logged but not propagate (subscriber is fire-and-forget).
	noop.DeliverInbound("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/UNKNOWN/cdps/x/turn_on/invoke",
		[]byte(`{}`))
	// Should not panic or block; error is swallowed.
	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 call even on error, got %d", cdpSink.calls.Load())
	}
}

func TestParseMQTTPriority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want hmenum.CommandPriority
	}{
		{"critical", hmenum.CommandPriorityCritical},
		{"CRITICAL", hmenum.CommandPriorityCritical},
		{"Critical", hmenum.CommandPriorityCritical},
		{"low", hmenum.CommandPriorityLow},
		{"LOW", hmenum.CommandPriorityLow},
		{"high", hmenum.CommandPriorityHigh},
		{"HIGH", hmenum.CommandPriorityHigh},
		{"", hmenum.CommandPriorityHigh},
		{"  ", hmenum.CommandPriorityHigh},
		{"medium", hmenum.CommandPriorityHigh}, // unknown → High
	}
	for _, c := range cases {
		got := parseMQTTPriority(c.in)
		if got != c.want {
			t.Errorf("parseMQTTPriority(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseCommandPayloadFlavours(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"", nil},
		{"true", true},
		{"false", false},
		{"ON", true},
		{"off", false},
		{"42", int64(42)},
		{"-3", int64(-3)},
		{"3.25", 3.25},
		{`"foo"`, "foo"},
		{"unparsable", "unparsable"},
	}
	for _, c := range cases {
		got := parseCommandPayload([]byte(c.in))
		if got != c.want {
			t.Fatalf("parse %q = %v (%T), want %v (%T)", c.in, got, got, c.want, c.want)
		}
	}
}

// --- WeekProfile command handler tests ---

type fakeWPSink struct {
	calls atomic.Int32
	last  struct {
		central, iface, addr string
		channel              int
		profile              string
	}
}

func (f *fakeWPSink) SetActiveProfile(_ context.Context,
	central, interfaceID, deviceAddress string, channel int,
	profileKey string, _ hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.last.central = central
	f.last.iface = interfaceID
	f.last.addr = deviceAddress
	f.last.channel = channel
	f.last.profile = profileKey
	return nil
}

func TestCommandSubscriberWeekProfileTopic(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	wpSink := &fakeWPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithWeekProfileSink(wpSink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/week_profile/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set", []byte("P3"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	if wpSink.calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", wpSink.calls.Load())
	}
	last := wpSink.last
	if last.central != "ccu-01" {
		t.Errorf("central: got %q want %q", last.central, "ccu-01")
	}
	if last.iface != "HmIP-RF" {
		t.Errorf("iface: got %q want %q", last.iface, "HmIP-RF")
	}
	if last.addr != "0001ABCD" {
		t.Errorf("addr: got %q want %q", last.addr, "0001ABCD")
	}
	if last.channel != 1 {
		t.Errorf("channel: got %d want 1", last.channel)
	}
	if last.profile != "P3" {
		t.Errorf("profile: got %q want %q", last.profile, "P3")
	}
}

func TestCommandSubscriberWeekProfileMissingSink(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	// No WeekProfileSink wired — nil-sink path must not panic.
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Should not panic; subscription still matches the wildcard.
	noop.DeliverInbound("openccu-loom/+/+/+/+/week_profile/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set", []byte("P3"))
}

func TestCommandSubscriberWeekProfileEmptyPayload(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	wpSink := &fakeWPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithWeekProfileSink(wpSink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInbound("openccu-loom/+/+/+/+/week_profile/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set", []byte{})
	if wpSink.calls.Load() != 0 {
		t.Fatalf("calls=%d, want 0 (empty payload must be dropped)", wpSink.calls.Load())
	}
}
