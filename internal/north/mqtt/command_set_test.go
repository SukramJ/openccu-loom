// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// lockedBuffer is a log sink the router's workers can write to concurrently.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// failingAlarmSink refuses every disarm, so the failure path logs.
type failingAlarmSink struct{ fakeAlarmSink }

func (f *failingAlarmSink) Disarm(context.Context, string, string) error {
	return errors.New("wrong code")
}

// TestRedactAlarmCode pins the rendering every alarm command log line goes
// through: the JSON command form, the `{"val": {…}}` wrapper and a payload
// that opens like JSON but does not parse — which could carry a code the
// parser cannot locate, so it is not echoed at all.
func TestRedactAlarmCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{`{"action":"DISARM","code":"1234"}`, `{"action":"DISARM","code":"***"}`},
		{`{"val":{"action":"ARM_AWAY","code":"0815"}}`, `{"val":{"action":"ARM_AWAY","code":"***"}}`},
		{`{"action":"DISARM","code":1234}`, `{"action":"DISARM","code":"***"}`},
		{`DISARM`, `DISARM`},
		{`{"action":"DISARM","code":"1234"`, `<redacted: malformed JSON>`},
	} {
		got := redactAlarmCode([]byte(tc.in))
		if got != tc.want {
			t.Errorf("redactAlarmCode(%s) = %s, want %s", tc.in, got, tc.want)
		}
		if strings.Contains(got, "1234") || strings.Contains(got, "0815") {
			t.Errorf("redactAlarmCode(%s) leaks the code: %s", tc.in, got)
		}
	}
}

// TestAlarmCommandLogsNeverCarryTheCode drives the redaction through the
// real route: a rejected (malformed) and a failed alarm command are both
// logged at warn with their topic and payload (spec §3.3), and neither log
// line contains the disarm code.
//
// Falsifiability: register the alarm route with [CommandSubscriber.normalized]
// instead of the redacting variant, or log `cmd.Payload` in
// [CommandSubscriber.alarmAttrs], and the code appears in the log.
func TestAlarmCommandLogsNeverCarryTheCode(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	noop := NewNoopClient()
	sub := NewCommandSubscriber(noop, NewTopicBuilder("gh"), &fakeSink{}, logger).
		WithAlarmSink(&failingAlarmSink{})
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	topic := alarmCommandTopic("gh", "eg")

	noop.DeliverInbound(alarmCommandFilter, topic, []byte(`{"action":"DISARM","code":"1234"`))
	noop.DeliverInbound(alarmCommandFilter, topic, []byte(`{"action":"DISARM","code":"1234"}`))
	sub.WaitIdle()

	out := logs.String()
	if strings.Contains(out, "1234") {
		t.Fatalf("an alarm command log line carries the code:\n%s", out)
	}
	for _, want := range []string{"mqtt.command.rejected", "mqtt.command.alarm.disarm", topic, `\"code\":\"***\"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

// TestSetPayloadNormalisation pins spec §5.3 on the data-point route: a
// plain value and `{"val": …}` are the same write, an empty payload and
// `{"val": null}` are no write at all, and malformed JSON is refused and
// logged at warn with its topic and payload.
func TestSetPayloadNormalisation(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	noop := NewNoopClient()
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, NewTopicBuilder("gh"), sink, logger)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	const filter = "gh/set/+/+/+/+/+/+"
	topic := NewTopicBuilder("gh").DataPointCommand("ccu", "ccu-HmIP-RF", "0001ABCD", 1, "LEVEL")

	for _, body := range []string{"", "  ", `{"val":null}`, `{"val":`} {
		noop.DeliverInbound(filter, topic, []byte(body))
	}
	sub.WaitIdle()
	if n := sink.setValues.Load(); n != 0 {
		t.Fatalf("an empty, null or malformed payload reached the CCU %d times", n)
	}
	if out := logs.String(); !strings.Contains(out, "mqtt.command.rejected") || !strings.Contains(out, topic) {
		t.Errorf("the malformed payload was not logged at warn with its topic:\n%s", out)
	}

	noop.DeliverInbound(filter, topic, []byte(`{"val": 0.5}`))
	sub.WaitIdle()
	if n := sink.setValues.Load(); n != 1 {
		t.Fatalf(`{"val": 0.5} wrote %d times, want 1`, n)
	}
	if sink.lastVal.value != 0.5 {
		t.Errorf(`{"val": 0.5} wrote %v (%T), want the plain value 0.5`, sink.lastVal.value, sink.lastVal.value)
	}
	noop.DeliverInbound(filter, topic, []byte(`0.5`))
	sub.WaitIdle()
	if n := sink.setValues.Load(); n != 2 || sink.lastVal.value != 0.5 {
		t.Errorf("the plain form wrote %v after %d writes, want 0.5 after 2", sink.lastVal.value, n)
	}
}

// TestProgramActiveAcceptsTheSpecBooleanSpellings pins §5.3's boolean
// conversions on an item that has a boolean `set`.
func TestProgramActiveAcceptsTheSpecBooleanSpellings(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, NewTopicBuilder("gh"), sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	for _, body := range []string{"ON", "yes", "1", "true", `{"val": true}`} {
		noop.DeliverInbound("gh/set/+/hub/programs/+/active", "gh/set/ccu/hub/programs/12/active", []byte(body))
	}
	sub.WaitIdle()
	if n := sink.programEnables.Load(); n != 5 {
		t.Errorf("%d of 5 boolean spellings reached the CCU", n)
	}
}

// TestMaintenanceRoutesRideTheCommandPlane pins mqtt-smarthome 2.0 §7 as
// this daemon serves it, through [CommandSubscriber.WithMaintenance] and the
// bridge's instance publisher:
//
//   - `maintenance/set/loglevel` sets the level through the configured
//     setter, `{"val": …}` included;
//   - `maintenance/set/restart` fires on an EMPTY payload — the one
//     exception to "empty payloads are ignored", because she publishes it
//     empty — but only while the supervisor predicate answers true;
//   - a retained restart never fires, or every reconnect would restart.
//
// Falsifiability: register the maintenance routes through
// [CommandSubscriber.normalized] and the empty restart is dropped; drop the
// Supervised gate and the unsupervised restart fires.
func TestMaintenanceRoutesRideTheCommandPlane(t *testing.T) {
	t.Parallel()
	for _, supervised := range []bool{true, false} {
		var level atomic.Int64
		level.Store(int64(slog.LevelInfo))
		var restarts atomic.Int32
		restarted := make(chan struct{}, 4)
		noop := NewNoopClient()
		bridge := NewBridge(BridgeConfig{
			Base: "gh", CentralName: "ccu", RawEnabled: true,
			Maintenance: MaintenanceConfig{
				SetLogLevel: func(l slog.Level) { level.Store(int64(l)) },
				Supervised:  func() bool { return supervised },
				Shutdown: func() {
					restarts.Add(1)
					restarted <- struct{}{}
				},
			},
		}, noop)
		sub := NewCommandSubscriber(noop, bridge.Topics(), &fakeSink{}, nil).
			WithMaintenance(bridge.Instance())
		if err := sub.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		const filter = "gh/maintenance/set/#"
		if !noop.DeliverInbound(filter, "gh/maintenance/set/loglevel", []byte(`{"val":"debug"}`)) {
			t.Fatal("the maintenance routes are not subscribed")
		}
		noop.DeliverInboundRetained(filter, "gh/maintenance/set/restart", []byte(""))
		noop.DeliverInbound(filter, "gh/maintenance/set/restart", []byte(""))
		sub.WaitIdle()
		if supervised {
			<-restarted
		}
		sub.Close()

		if got := slog.Level(level.Load()); got != slog.LevelDebug {
			t.Errorf("supervised=%v: log level = %v, want debug", supervised, got)
		}
		want := int32(0)
		if supervised {
			want = 1
		}
		if got := restarts.Load(); got != want {
			t.Errorf("supervised=%v: restart fired %d times, want %d (the retained one never, the empty one only when supervised)",
				supervised, got, want)
		}
	}
}

// TestMaintenanceDisabledRoutesNothing pins `north.mqtt.maintenance.enabled:
// false`: no maintenance route is subscribed at all.
func TestMaintenanceDisabledRoutesNothing(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	bridge := NewBridge(BridgeConfig{
		Base: "gh", RawEnabled: true,
		Maintenance: MaintenanceConfig{Disabled: true, SetLogLevel: func(slog.Level) {}},
	}, noop)
	sub := NewCommandSubscriber(noop, bridge.Topics(), &fakeSink{}, nil).WithMaintenance(bridge.Instance())
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(sub.Close)
	if noop.DeliverInbound("gh/maintenance/set/#", "gh/maintenance/set/loglevel", []byte("debug")) {
		t.Fatal("a maintenance route is subscribed although maintenance is disabled")
	}
}
