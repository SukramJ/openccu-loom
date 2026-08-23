// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type fakeCombinedDPSink struct {
	mu    sync.Mutex
	calls []fakeCombinedCall
}

type fakeCombinedCall struct {
	central, iface, addr, kind string
	channel                    int
	raw                        string
}

func (f *fakeCombinedDPSink) SetCombinedValue(_ context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	kind, raw string, _ hmenum.CommandPriority,
) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCombinedCall{centralName, interfaceID, deviceAddress, kind, channel, raw})
	f.mu.Unlock()
	return nil
}

func (f *fakeCombinedDPSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeScheduleSwitchSink struct {
	mu    sync.Mutex
	calls []fakeScheduleCall
}

type fakeScheduleCall struct {
	central, iface, addr, key string
	channel                   int
	enabled                   bool
}

func (f *fakeScheduleSwitchSink) SetScheduleSwitch(_ context.Context,
	centralName, interfaceID, deviceAddress string, channel int,
	key string, enabled bool, _ hmenum.CommandPriority,
) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeScheduleCall{centralName, interfaceID, deviceAddress, key, channel, enabled})
	f.mu.Unlock()
	return nil
}

func (f *fakeScheduleSwitchSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeAddonUpdateSink struct {
	mu       sync.Mutex
	installs int
}

func (f *fakeAddonUpdateSink) TriggerInstall(context.Context) error {
	f.mu.Lock()
	f.installs++
	f.mu.Unlock()
	return nil
}

func (f *fakeAddonUpdateSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installs
}

// commandPlaneSinks bundles one double per inbound command shape so a
// case can assert on the sink its topic is supposed to reach.
type commandPlaneSinks struct {
	values *fakeSink
	cdp    *fakeCDPSink
	wp     *fakeWPSink
	cmb    *fakeCombinedDPSink
	sched  *fakeScheduleSwitchSink
	im     *fakeInstallModeSink
	alarm  *fakeAlarmSink
	addon  *fakeAddonUpdateSink
}

// startCommandPlane wires a fully-sinked subscriber on `base` and starts
// it, so every case runs against a fresh set of doubles.
func startCommandPlane(t *testing.T, base string) (*NoopClient, *CommandSubscriber, *commandPlaneSinks) {
	t.Helper()
	sinks := &commandPlaneSinks{
		values: &fakeSink{},
		cdp:    &fakeCDPSink{},
		wp:     &fakeWPSink{},
		cmb:    &fakeCombinedDPSink{},
		sched:  &fakeScheduleSwitchSink{},
		im:     &fakeInstallModeSink{},
		alarm:  &fakeAlarmSink{},
		addon:  &fakeAddonUpdateSink{},
	}
	noop := NewNoopClient()
	sub := NewCommandSubscriber(noop, NewTopicBuilder(base), sinks.values, nil).
		WithCDPSink(sinks.cdp).
		WithWeekProfileSink(sinks.wp).
		WithCombinedDPSink(sinks.cmb).
		WithScheduleSwitchSink(sinks.sched).
		WithInstallModeSink(sinks.im).
		WithAlarmSink(sinks.alarm).
		WithAddonUpdateSink(sinks.addon)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	return noop, sub, sinks
}

// TestCommandSubscriberParsesEveryShapeRelativeToTheTopicBase drives one
// message per inbound command shape under a single-segment and a
// two-segment `topic_base`.
//
// A base with an internal slash is accepted config, and the broker
// delivers on the wildcard filters either way — but the handlers used to
// length-check absolute segment positions, so every extra base segment
// shifted the whole plane by one and every command was dropped with a
// warning while the state plane kept updating normally.
func TestCommandSubscriberParsesEveryShapeRelativeToTheTopicBase(t *testing.T) {
	t.Parallel()
	const (
		central = "ccu-01"
		iface   = "HmIP-RF"
		addr    = "0001ABCD"
	)
	cdpBody, err := json.Marshal(CDPInvokePayload{Params: map[string]any{"brightness": 0.8}})
	if err != nil {
		t.Fatalf("marshal cdp payload: %v", err)
	}
	customSlot := payload.TopicSlot{
		Address: addr, Channel: 1,
		Bucket: payload.BucketCustom, Parameter: "climate",
	}

	cases := []struct {
		name string
		// filter is the subscription filter suffix; the base is prefixed
		// by the runner so the case is base-agnostic.
		filter string
		topic  func(b *TopicBuilder) string
		body   []byte
		want   func(t *testing.T, s *commandPlaneSinks)
	}{
		{
			name:   "datapoint values bucket",
			filter: "/+/+/+/+/+/+/set",
			topic: func(b *TopicBuilder) string {
				return b.ParameterCommand(central, iface, addr, 1, string(payload.BucketValues), "STATE")
			},
			body: []byte("true"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.setValues.Load(); n != 1 {
					t.Fatalf("SetValue calls=%d, want 1", n)
				}
				if s.values.lastVal.centralName != central || s.values.lastVal.chanAddr != addr+":1" ||
					s.values.lastVal.param != "STATE" || s.values.lastVal.value != true {
					t.Fatalf("last=%+v", s.values.lastVal)
				}
			},
		},
		{
			name:   "datapoint master bucket",
			filter: "/+/+/+/+/+/+/set",
			topic: func(b *TopicBuilder) string {
				return b.ParameterCommand(central, iface, addr, 1, string(payload.BucketMaster), "TEMPERATURE_MINIMUM")
			},
			body: []byte("17.5"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.masterValues.Load(); n != 1 {
					t.Fatalf("SetMasterValue calls=%d, want 1", n)
				}
				if s.values.lastMaster.param != "TEMPERATURE_MINIMUM" {
					t.Fatalf("last=%+v", s.values.lastMaster)
				}
			},
		},
		{
			name:   "datapoint legacy bucket-less shape",
			filter: "/+/+/+/+/+/set",
			topic: func(b *TopicBuilder) string {
				return b.Base + "/" + central + "/" + iface + "/" + addr + "/1/STATE/set"
			},
			body: []byte("false"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.setValues.Load(); n != 1 {
					t.Fatalf("SetValue calls=%d, want 1", n)
				}
				if s.values.lastVal.value != false {
					t.Fatalf("last=%+v", s.values.lastVal)
				}
			},
		},
		{
			name:   "sysvar",
			filter: "/+/hub/sysvars/+/set",
			topic: func(b *TopicBuilder) string {
				return naming.MQTTHubSysvarCommand(b.Base, central, "Sunset")
			},
			body: []byte("42"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.setSysvars.Load(); n != 1 {
					t.Fatalf("SetSysvar calls=%d, want 1", n)
				}
				if s.values.lastSysvar.centralName != central || s.values.lastSysvar.name != "Sunset" {
					t.Fatalf("last=%+v", s.values.lastSysvar)
				}
			},
		},
		{
			name:   "program trigger",
			filter: "/+/hub/programs/+/trigger",
			topic: func(b *TopicBuilder) string {
				return naming.MQTTHubProgramTrigger(b.Base, central, "4711")
			},
			body: []byte("PRESS"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.triggers.Load(); n != 1 {
					t.Fatalf("TriggerProgram calls=%d, want 1", n)
				}
				if s.values.lastProgram.id != "4711" {
					t.Fatalf("last=%+v", s.values.lastProgram)
				}
			},
		},
		{
			name:   "program enable",
			filter: "/+/hub/programs/+/set",
			topic: func(b *TopicBuilder) string {
				return naming.MQTTHubProgramSet(b.Base, central, "4711")
			},
			body: []byte("false"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.values.programEnables.Load(); n != 1 {
					t.Fatalf("SetProgramEnabled calls=%d, want 1", n)
				}
				if s.values.lastProgramEnable.id != "4711" || s.values.lastProgramEnable.enabled {
					t.Fatalf("last=%+v", s.values.lastProgramEnable)
				}
			},
		},
		{
			name:   "install mode",
			filter: "/+/hub/install_mode/+/set",
			topic: func(b *TopicBuilder) string {
				return naming.MQTTHubInstallModeCommand(b.Base, central, iface)
			},
			body: []byte("PRESS"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.im.calls.Load(); n != 1 {
					t.Fatalf("ActivateInstallMode calls=%d, want 1", n)
				}
				if s.im.last.iface != iface {
					t.Fatalf("last=%+v", s.im.last)
				}
			},
		},
		{
			name:   "custom-DP invoke",
			filter: "/+/devices/+/cdps/+/+/invoke",
			topic: func(b *TopicBuilder) string {
				return b.CustomDPInvoke(central, addr, "light_dp", "turn_on")
			},
			body: cdpBody,
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.cdp.calls.Load(); n != 1 {
					t.Fatalf("InvokeCustomDP calls=%d, want 1", n)
				}
				if s.cdp.lastName != "light_dp" || s.cdp.lastOp != "turn_on" {
					t.Fatalf("name=%q op=%q", s.cdp.lastName, s.cdp.lastOp)
				}
			},
		},
		{
			name:   "custom-DP service method",
			filter: "/+/+/+/+/custom/+/set/+",
			topic: func(b *TopicBuilder) string {
				return b.CustomDPServiceMethod(central, iface, customSlot, "set_temperature")
			},
			body: []byte("21.5"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.cdp.calls.Load(); n != 1 {
					t.Fatalf("InvokeChannelService calls=%d, want 1", n)
				}
				if s.cdp.lastOp != "set_temperature" {
					t.Fatalf("method=%q", s.cdp.lastOp)
				}
			},
		},
		{
			name:   "week profile",
			filter: "/+/+/+/+/week_profile/set",
			topic: func(b *TopicBuilder) string {
				return b.WeekProfileCommand(central, iface, addr, 1)
			},
			body: []byte("P3"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.wp.calls.Load(); n != 1 {
					t.Fatalf("SetActiveProfile calls=%d, want 1", n)
				}
				if s.wp.last.profile != "P3" || s.wp.last.channel != 1 {
					t.Fatalf("last=%+v", s.wp.last)
				}
			},
		},
		{
			name:   "combined data point",
			filter: "/+/+/+/+/combined/+/set",
			topic: func(b *TopicBuilder) string {
				return b.CombinedCommand(central, iface, addr, 1, "duration")
			},
			body: []byte("30"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.cmb.count(); n != 1 {
					t.Fatalf("SetCombinedTimerSeconds calls=%d, want 1", n)
				}
				if got := s.cmb.calls[0]; got.kind != "duration" || got.raw != "30" {
					t.Fatalf("call=%+v", got)
				}
			},
		},
		{
			name:   "schedule switch",
			filter: "/+/+/+/+/schedule/+/set",
			topic: func(b *TopicBuilder) string {
				return b.ScheduleSwitchCommand(central, iface, addr, 1, "1_1")
			},
			body: []byte("true"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.sched.count(); n != 1 {
					t.Fatalf("SetScheduleSwitch calls=%d, want 1", n)
				}
				if got := s.sched.calls[0]; got.key != "1_1" || !got.enabled {
					t.Fatalf("call=%+v", got)
				}
			},
		},
		{
			name:   "alarm command",
			filter: "/alarm/+/set",
			topic: func(b *TopicBuilder) string {
				return alarmCommandTopic(b.Base, "night")
			},
			body: []byte(`{"action":"DISARM","code":"1234"}`),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				s.alarm.mu.Lock()
				defer s.alarm.mu.Unlock()
				if len(s.alarm.disarmCalls) != 1 {
					t.Fatalf("Disarm calls=%d, want 1", len(s.alarm.disarmCalls))
				}
				if got := s.alarm.disarmCalls[0]; got.area != "night" || got.code != "1234" {
					t.Fatalf("call=%+v", got)
				}
			},
		},
		{
			name:   "add-on update install",
			filter: "/system/addon_update/set",
			topic:  func(b *TopicBuilder) string { return b.AddonUpdateCommand() },
			body:   []byte("INSTALL"),
			want: func(t *testing.T, s *commandPlaneSinks) {
				t.Helper()
				if n := s.addon.count(); n != 1 {
					t.Fatalf("TriggerInstall calls=%d, want 1", n)
				}
			},
		},
	}

	for _, base := range []string{"openccu-loom", "home/loom"} {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					noop, sub, sinks := startCommandPlane(t, base)
					topics := NewTopicBuilder(base)
					topic := tc.topic(topics)
					if !noop.DeliverInbound(topics.Base+tc.filter, topic, tc.body) {
						t.Fatalf("no subscription for filter %q", topics.Base+tc.filter)
					}
					sub.WaitIdle()
					tc.want(t, sinks)
				})
			}
		})
	}
}
