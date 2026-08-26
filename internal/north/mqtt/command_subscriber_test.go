// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type fakeSink struct {
	setValues    atomic.Int32
	setSysvars   atomic.Int32
	triggers     atomic.Int32
	masterValues atomic.Int32
	lastVal      struct {
		centralName, iface, chanAddr string
		param                        string
		value                        any
	}
	lastMaster struct {
		centralName, iface, chanAddr string
		param                        string
		value                        any
	}
	lastSysvar struct {
		centralName, name string
		value             any
	}
	programEnables    atomic.Int32
	lastProgram       struct{ centralName, id string }
	lastProgramEnable struct {
		centralName, id string
		enabled         bool
	}
}

func (f *fakeSink) SetValue(_ context.Context, centralName, iface, chanAddr string,
	param hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	f.setValues.Add(1)
	f.lastVal.centralName = centralName
	f.lastVal.iface = iface
	f.lastVal.chanAddr = chanAddr
	f.lastVal.param = string(param)
	f.lastVal.value = v
	return nil
}

func (f *fakeSink) SetMasterValue(_ context.Context, centralName, iface, chanAddr string,
	param hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	f.masterValues.Add(1)
	f.lastMaster.centralName = centralName
	f.lastMaster.iface = iface
	f.lastMaster.chanAddr = chanAddr
	f.lastMaster.param = string(param)
	f.lastMaster.value = v
	return nil
}

func (f *fakeSink) SetSysvar(_ context.Context, centralName, name string, v any) error {
	f.setSysvars.Add(1)
	f.lastSysvar.centralName = centralName
	f.lastSysvar.name = name
	f.lastSysvar.value = v
	return nil
}

func (f *fakeSink) TriggerProgram(_ context.Context, centralName, id string) error {
	f.triggers.Add(1)
	f.lastProgram.centralName = centralName
	f.lastProgram.id = id
	return nil
}

func (f *fakeSink) SetProgramEnabled(_ context.Context, centralName, id string, enabled bool) error {
	f.programEnables.Add(1)
	f.lastProgramEnable.centralName = centralName
	f.lastProgramEnable.id = id
	f.lastProgramEnable.enabled = enabled
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
	sub.dispatcher.flush()
	if sink.setValues.Load() != 1 {
		t.Fatalf("calls=%d", sink.setValues.Load())
	}
	if sink.lastVal.centralName != "ccu-01" || sink.lastVal.chanAddr != "0001ABCD:1" ||
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
	sub.dispatcher.flush()
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
	// "true" is the payload_press the HA-Discovery button declares.
	noop.DeliverInbound("openccu-loom/+/hub/programs/+/trigger",
		"openccu-loom/ccu-01/hub/programs/Morning/trigger", []byte("true"))
	sub.dispatcher.flush()
	if sink.triggers.Load() != 1 || sink.lastProgram.id != "Morning" {
		t.Fatalf("program: %+v", sink.lastProgram)
	}
}

// An empty trigger payload is not a command: it is the shape of a
// retained-topic eviction (retain-cleanup publishes zero bytes), and
// the broker forwards that eviction to the daemon's own trigger
// subscription as a live message. Executing a CCU program because a
// topic was cleaned must never happen.
func TestCommandSubscriberProgramEmptyPayloadDropped(t *testing.T) {
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	_ = sub.Start(context.Background())
	for _, payload := range [][]byte{nil, []byte(""), []byte("  ")} {
		noop.DeliverInbound("openccu-loom/+/hub/programs/+/trigger",
			"openccu-loom/ccu-01/hub/programs/Morning/trigger", payload)
	}
	sub.dispatcher.flush()
	if n := sink.triggers.Load(); n != 0 {
		t.Fatalf("empty payloads must be dropped, got %d trigger call(s)", n)
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

func (f *fakeCDPSink) InvokeCustomDP(_ context.Context, centralName, device, name, op string,
	params map[string]any, prio hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.lastCentral = centralName
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
	centralName, _, device string, channel int,
	method string, params map[string]any, prio hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.lastCentral = centralName
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
	sub.dispatcher.flush()
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
	sub.dispatcher.flush()
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
	sub.dispatcher.flush()
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
		centralName, iface, addr string
		channel                  int
		profile                  string
	}
}

func (f *fakeWPSink) SetActiveProfile(_ context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	profileKey string, _ hmenum.CommandPriority,
) error {
	f.calls.Add(1)
	f.last.centralName = centralName
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
	sub.dispatcher.flush()
	if wpSink.calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", wpSink.calls.Load())
	}
	last := wpSink.last
	if last.centralName != "ccu-01" {
		t.Errorf("central: got %q want %q", last.centralName, "ccu-01")
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

// --- InstallMode button tests ---

type fakeInstallModeSink struct {
	calls atomic.Int32
	last  struct {
		centralName, iface string
		seconds            int
	}
}

func (f *fakeInstallModeSink) ActivateInstallMode(_ context.Context, centralName, interfaceID string, seconds int) error {
	f.calls.Add(1)
	f.last.centralName = centralName
	f.last.iface = interfaceID
	f.last.seconds = seconds
	return nil
}

func TestCommandSubscriberInstallModePressTopic(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	imSink := &fakeInstallModeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithInstallModeSink(imSink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ok := noop.DeliverInbound("openccu-loom/+/hub/install_mode/+/set",
		"openccu-loom/ccu-01/hub/install_mode/HmIP-RF/set", []byte("PRESS"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	sub.dispatcher.flush()
	if imSink.calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", imSink.calls.Load())
	}
	if imSink.last.centralName != "ccu-01" || imSink.last.iface != "HmIP-RF" {
		t.Fatalf("last=%+v", imSink.last)
	}
	// PRESS maps to the default duration (seconds=0 → sink default).
	if imSink.last.seconds != 0 {
		t.Fatalf("seconds=%d, want 0 (default)", imSink.last.seconds)
	}
}

func TestCommandSubscriberInstallModeNumericDuration(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	imSink := &fakeInstallModeSink{}
	sub := NewCommandSubscriber(noop, topics, &fakeSink{}, nil).WithInstallModeSink(imSink)
	_ = sub.Start(context.Background())
	noop.DeliverInbound("openccu-loom/+/hub/install_mode/+/set",
		"openccu-loom/ccu-01/hub/install_mode/BidCos-RF/set", []byte("120"))
	sub.dispatcher.flush()
	if imSink.calls.Load() != 1 || imSink.last.seconds != 120 || imSink.last.iface != "BidCos-RF" {
		t.Fatalf("last=%+v", imSink.last)
	}
}

func TestCommandSubscriberInstallModeMissingSink(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sub := NewCommandSubscriber(noop, topics, &fakeSink{}, nil) // no install-mode sink
	_ = sub.Start(context.Background())
	// Must not panic when the sink is unwired.
	noop.DeliverInbound("openccu-loom/+/hub/install_mode/+/set",
		"openccu-loom/ccu-01/hub/install_mode/HmIP-RF/set", []byte("PRESS"))
}

func TestCommandSubscriberInstallModeRetainedDrop(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	imSink := &fakeInstallModeSink{}
	sub := NewCommandSubscriber(noop, topics, &fakeSink{}, nil).WithInstallModeSink(imSink)
	_ = sub.Start(context.Background())
	sub.handleInstallMode("openccu-loom/ccu-01/hub/install_mode/HmIP-RF/set", []byte("PRESS"), true)
	if imSink.calls.Load() != 0 {
		t.Fatalf("retained press must be dropped; calls=%d", imSink.calls.Load())
	}
}

// --- MASTER bucket routing tests ---

// TestCommandSubscriberMasterBucketRoutes verifies that a message on the
// 8-segment canonical topic with bucket=master is delivered to
// SetMasterValue (not SetValue) with the correct field extraction.
func TestCommandSubscriberMasterBucketRoutes(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ok := noop.DeliverInbound("openccu-loom/+/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/master/SHORT_ON_TIME/set", []byte("0.5"))
	if !ok {
		t.Fatal("subscription did not match")
	}
	sub.dispatcher.flush()
	if sink.masterValues.Load() != 1 {
		t.Fatalf("SetMasterValue calls=%d, want 1", sink.masterValues.Load())
	}
	if sink.setValues.Load() != 0 {
		t.Fatalf("SetValue must not be called for master bucket; got %d calls", sink.setValues.Load())
	}
	m := sink.lastMaster
	if m.centralName != "ccu-01" {
		t.Errorf("central: got %q want %q", m.centralName, "ccu-01")
	}
	if m.iface != "HmIP-RF" {
		t.Errorf("iface: got %q want %q", m.iface, "HmIP-RF")
	}
	if m.chanAddr != "0001ABCD:1" {
		t.Errorf("chanAddr: got %q want %q", m.chanAddr, "0001ABCD:1")
	}
	if m.param != "SHORT_ON_TIME" {
		t.Errorf("param: got %q want %q", m.param, "SHORT_ON_TIME")
	}
	if m.value != 0.5 {
		t.Errorf("value: got %v want 0.5", m.value)
	}
}

// TestCommandSubscriberCalculatedBucketDropped verifies that a message on
// the 8-segment topic with bucket=calculated is silently dropped (calculated
// parameters are read-only).
func TestCommandSubscriberCalculatedBucketDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInbound("openccu-loom/+/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/calculated/SOME_PARAM/set", []byte("true"))
	if sink.masterValues.Load() != 0 {
		t.Fatalf("SetMasterValue must not be called for calculated bucket; got %d calls", sink.masterValues.Load())
	}
	if sink.setValues.Load() != 0 {
		t.Fatalf("SetValue must not be called for calculated bucket; got %d calls", sink.setValues.Load())
	}
}

// TestCommandSubscriberValuesBucketStillRoutes confirms that the existing
// values-bucket path is unaffected by the master-bucket routing addition.
func TestCommandSubscriberValuesBucketStillRoutes(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInbound("openccu-loom/+/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/values/STATE/set", []byte("true"))
	sub.dispatcher.flush()
	if sink.setValues.Load() != 1 {
		t.Fatalf("SetValue calls=%d, want 1", sink.setValues.Load())
	}
	if sink.masterValues.Load() != 0 {
		t.Fatalf("SetMasterValue must not be called for values bucket; got %d calls", sink.masterValues.Load())
	}
}

// qosRecordingSubscriber wraps NoopClient's storage but also records the
// QoS every Subscribe call registered at — NoopClient itself discards it.
type qosRecordingSubscriber struct {
	*NoopClient
	qosByFilter map[string]QoS
}

func newQoSRecordingSubscriber() *qosRecordingSubscriber {
	return &qosRecordingSubscriber{NoopClient: NewNoopClient(), qosByFilter: map[string]QoS{}}
}

func (s *qosRecordingSubscriber) Subscribe(ctx context.Context, filter string, qos QoS, handler MessageHandler, opts ...SubscribeOption) (SubscribeResult, error) {
	s.qosByFilter[filter] = qos
	return s.NoopClient.Subscribe(ctx, filter, qos, handler, opts...)
}

// TestCommandSubscriberWiresConfiguredQoS reproduces the M2 bug: every
// inbound command Subscribe call hardcoded QoS1, so QoSProfile.Commands was
// dead configuration. It sets a non-default QoS via WithQoS and asserts
// every registered subscription (data-point, legacy, sysvar, program,
// install-mode, CDP invoke, service-method, week-profile, combined-DP,
// schedule-switch) uses it instead of the QoS1 default.
func TestCommandSubscriberWiresConfiguredQoS(t *testing.T) {
	t.Parallel()
	sub := newQoSRecordingSubscriber()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	const wantQoS = QoS0
	cs := NewCommandSubscriber(sub, topics, sink, nil).WithQoS(wantQoS)
	if err := cs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(sub.qosByFilter) == 0 {
		t.Fatal("no subscriptions were recorded")
	}
	for filter, qos := range sub.qosByFilter {
		if qos != wantQoS {
			t.Fatalf("filter %q subscribed at QoS %d, want %d", filter, qos, wantQoS)
		}
	}
}

// TestCommandSubscriberDefaultQoSIsQoS1 locks in the backward-compatible
// default so existing deployments that never call WithQoS keep the
// historical at-least-once behavior.
func TestCommandSubscriberDefaultQoSIsQoS1(t *testing.T) {
	t.Parallel()
	sub := newQoSRecordingSubscriber()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cs := NewCommandSubscriber(sub, topics, sink, nil)
	if err := cs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	for filter, qos := range sub.qosByFilter {
		if qos != QoS1 {
			t.Fatalf("filter %q subscribed at QoS %d, want default QoS1", filter, qos)
		}
	}
}

// TestCommandSubscriberProgramEnableTopic covers the activation control,
// which is separate from the trigger: a deactivated program refuses to
// run, so turning it back on cannot go through the same topic that runs it.
func TestCommandSubscriberProgramEnableTopic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    bool
		applied bool
	}{
		{"true activates", "true", true, true},
		{"ON activates", "ON", true, true},
		{"false deactivates", "false", false, true},
		{"0 deactivates", "0", false, true},
		// A typo must not silently deactivate a program.
		{"gibberish is rejected", "maybe", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noop := NewNoopClient()
			sink := &fakeSink{}
			sub := NewCommandSubscriber(noop, NewTopicBuilder("openccu-loom"), sink, nil)
			_ = sub.Start(context.Background())
			noop.DeliverInbound("openccu-loom/+/hub/programs/+/set",
				"openccu-loom/ccu-01/hub/programs/1234/set", []byte(tc.payload))
			sub.dispatcher.flush()

			if !tc.applied {
				if sink.programEnables.Load() != 0 {
					t.Fatalf("payload %q was applied, want rejection", tc.payload)
				}
				return
			}
			if sink.programEnables.Load() != 1 {
				t.Fatalf("payload %q was not applied", tc.payload)
			}
			if sink.lastProgramEnable.id != "1234" || sink.lastProgramEnable.centralName != "ccu-01" {
				t.Errorf("routed to %+v", sink.lastProgramEnable)
			}
			if sink.lastProgramEnable.enabled != tc.want {
				t.Errorf("enabled = %v, want %v", sink.lastProgramEnable.enabled, tc.want)
			}
		})
	}
}

// fakeCentralNames is a [CentralNameLister] over a fixed name set.
type fakeCentralNames struct{ names []string }

func (f fakeCentralNames) Names() []string { return f.names }

// TestCommandSubscriberResolvesEscapedCentralSegment pins the inverse of
// the topic escaping every publisher applies.
//
// A central named `Wohn Zimmer` is advertised — by our own discovery
// payloads — under the topic segment `Wohn_Zimmer`. Handing that segment
// to the sinks verbatim missed every exact-key lookup they perform
// (`Registry.Get`, the ValueWriter's per-central backend map), so every
// MQTT write for that CCU was dropped while its state topics kept
// updating.
func TestCommandSubscriberResolvesEscapedCentralSegment(t *testing.T) {
	t.Parallel()
	const (
		base       = "openccu-loom"
		configured = "Wohn Zimmer"
		segment    = "Wohn_Zimmer"
	)
	if got := naming.TopicSafe(configured); got != segment {
		t.Fatalf("fixture no longer exercises the escaping: TopicSafe(%q) = %q", configured, got)
	}

	newSub := func() (*NoopClient, *fakeSink, *CommandSubscriber) {
		noop := NewNoopClient()
		sink := &fakeSink{}
		sub := NewCommandSubscriber(noop, NewTopicBuilder(base), sink, nil).
			WithCentralNames(fakeCentralNames{names: []string{configured, "ccu-01"}})
		if err := sub.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		return noop, sink, sub
	}

	t.Run("data point", func(t *testing.T) {
		t.Parallel()
		noop, sink, sub := newSub()
		if !noop.DeliverInbound(base+"/+/+/+/+/+/+/set",
			base+"/"+segment+"/HmIP-RF/0001ABCD/4/values/STATE/set", []byte("true")) {
			t.Fatal("subscription did not match the declared command topic")
		}
		sub.dispatcher.flush()
		if sink.setValues.Load() != 1 {
			t.Fatalf("calls=%d, want 1", sink.setValues.Load())
		}
		if sink.lastVal.centralName != configured {
			t.Errorf("central = %q, want %q — the write never reaches a backend", sink.lastVal.centralName, configured)
		}
	})

	t.Run("sysvar", func(t *testing.T) {
		t.Parallel()
		noop, sink, sub := newSub()
		noop.DeliverInbound(base+"/+/hub/sysvars/+/set",
			base+"/"+segment+"/hub/sysvars/Anwesenheit/set", []byte("true"))
		sub.dispatcher.flush()
		if sink.lastSysvar.centralName != configured {
			t.Errorf("central = %q, want %q", sink.lastSysvar.centralName, configured)
		}
	})

	t.Run("program trigger", func(t *testing.T) {
		t.Parallel()
		noop, sink, sub := newSub()
		noop.DeliverInbound(base+"/+/hub/programs/+/trigger",
			base+"/"+segment+"/hub/programs/1234/trigger", []byte("PRESS"))
		sub.dispatcher.flush()
		if sink.lastProgram.centralName != configured {
			t.Errorf("central = %q, want %q", sink.lastProgram.centralName, configured)
		}
	})

	t.Run("unescaped names still route verbatim", func(t *testing.T) {
		t.Parallel()
		noop, sink, sub := newSub()
		noop.DeliverInbound(base+"/+/+/+/+/+/+/set",
			base+"/ccu-01/HmIP-RF/0001ABCD/4/values/STATE/set", []byte("true"))
		sub.dispatcher.flush()
		if sink.lastVal.centralName != "ccu-01" {
			t.Errorf("central = %q, want %q", sink.lastVal.centralName, "ccu-01")
		}
	})
}

// TestCommandSubscriberRefusesAmbiguousCentralSegment: two configured
// centrals whose names escape to the same topic segment cannot be told
// apart from the wire, so the command is dropped rather than routed to
// an arbitrary one of them.
func TestCommandSubscriberRefusesAmbiguousCentralSegment(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"
	noop := NewNoopClient()
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, NewTopicBuilder(base), sink, nil).
		WithCentralNames(fakeCentralNames{names: []string{"Wohn Zimmer", "Wohn+Zimmer"}})
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInbound(base+"/+/+/+/+/+/+/set",
		base+"/Wohn_Zimmer/HmIP-RF/0001ABCD/4/values/STATE/set", []byte("true"))
	sub.dispatcher.flush()
	if sink.setValues.Load() != 0 {
		t.Fatalf("an ambiguous central segment was routed to a CCU anyway (calls=%d)", sink.setValues.Load())
	}
}

// TestWeekProfileCommandDoesNotAlsoIssueADataPointWrite pins that a topic
// owned by a dedicated subscription is handled by that subscription alone.
//
// The week-profile command topic has seven levels, which is exactly the shape
// of the legacy bucket-less data-point filter, and a broker delivers a message
// to EVERY matching subscription. Delivering through one named filter at a
// time — what the other tests in this file do — cannot see that: the topic has
// to go through the client so both handlers run, the way production does.
//
// Without the guard, picking a heating profile in Home Assistant switched the
// profile AND issued a CCU write for a parameter named `week_profile`, which
// no channel has.
func TestWeekProfileCommandDoesNotAlsoIssueADataPointWrite(t *testing.T) {
	client := newEchoClient()
	sink := &fakeSink{}
	wpSink := &fakeWPSink{}
	sub := NewCommandSubscriber(client, NewTopicBuilder("openccu-loom"), sink, nil).WithWeekProfileSink(wpSink)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	const topic = "openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set"
	// Vacuity guard: the overlap only exists while both filters are active.
	matching := 0
	for _, f := range client.Filters() {
		if mqttFilterMatches(f, topic) {
			matching++
		}
	}
	if matching < 2 {
		t.Fatalf("filters matching %q = %d, want at least 2 — the overlap this test guards is gone", topic, matching)
	}

	if err := client.Publish(context.Background(), topic, []byte("P2"), QoS1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	sub.WaitIdle()

	if got := wpSink.calls.Load(); got != 1 {
		t.Fatalf("week-profile writes = %d, want 1", got)
	}
	if got := sink.setValues.Load(); got != 0 {
		t.Fatalf("CCU data-point writes = %d, want 0 (a bogus `week_profile` parameter reached the CCU)", got)
	}
}

// failingNthSubscriber refuses exactly one Subscribe call — the one at
// index `failAt` — and accepts every other. It lets a test drive
// [CommandSubscriber.Start] once per registered filter and observe how
// that single rejection is reported.
type failingNthSubscriber struct {
	*NoopClient
	failAt  int
	seen    int
	filters []string
}

var errSubscribeRefused = errors.New("broker refused subscribe")

func (s *failingNthSubscriber) Subscribe(ctx context.Context, filter string, qos QoS, handler MessageHandler, opts ...SubscribeOption) (SubscribeResult, error) {
	idx := s.seen
	s.seen++
	s.filters = append(s.filters, filter)
	if idx == s.failAt {
		return SubscribeResult{}, errSubscribeRefused
	}
	return s.NoopClient.Subscribe(ctx, filter, qos, handler, opts...)
}

// TestCommandSubscriberReportsEverySubscribeFailure pins the two things an
// operator needs when a broker ACL denies one command filter: the
// subscribe_failures counter moves, and the returned error names the
// filter instead of surfacing the broker's bare message. The
// program-activation subscription used to do neither, so an ACL problem on
// that one topic left the metric at zero and the log without a subject.
func TestCommandSubscriberReportsEverySubscribeFailure(t *testing.T) {
	t.Parallel()

	// Discover how many Subscribe calls Start makes.
	probe := &failingNthSubscriber{NoopClient: NewNoopClient(), failAt: -1}
	if err := NewCommandSubscriber(probe, NewTopicBuilder("openccu-loom"), &fakeSink{}, nil).
		Start(context.Background()); err != nil {
		t.Fatalf("probe start: %v", err)
	}
	total := probe.seen
	if total == 0 {
		t.Fatal("Start registered no subscriptions")
	}

	for i := range total {
		sub := &failingNthSubscriber{NoopClient: NewNoopClient(), failAt: i}
		reg := metrics.NewRegistry()
		col := metrics.NewMqttCollector(reg)
		cs := NewCommandSubscriber(sub, NewTopicBuilder("openccu-loom"), &fakeSink{}, nil).
			WithCollector(col)
		err := cs.Start(context.Background())
		filter := sub.filters[i]
		if err == nil {
			t.Fatalf("filter %q: Start returned nil despite a refused subscribe", filter)
		}
		if !errors.Is(err, errSubscribeRefused) {
			t.Fatalf("filter %q: error does not wrap the broker error: %v", filter, err)
		}
		if err.Error() == errSubscribeRefused.Error() {
			t.Fatalf("filter %q: error is unwrapped, so the log names no subscription: %v", filter, err)
		}
		if got := col.SubscribeFailures.Value(); got != 1 {
			t.Fatalf("filter %q: subscribe_failures = %d, want 1", filter, got)
		}
	}
}
