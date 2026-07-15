// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration — MQTT alarm_control_panel round-trip
// (docs/alarm-concept.md §13.3, docs/mqtt-topic-schema.md).
//
// Exercises the daemon-level alarm MQTT plane against a real Mosquitto
// broker end to end:
//
//	broker --ARM_AWAY/DISARM--> CommandSubscriber --> AlarmSink -->
//	alarm.Engine --> AlarmMQTTPublisher --retained state--> broker
//
// The alarm.Service is built the same way as
// TestAlarmFullChainWindowOpenTriggersSirenThenSilence in
// alarm_engine_e2e_test.go (newAlarmHarness: a temp-file SQLite DB, the
// in-process godevccu-backed central) and seeded with one area carrying
// an instant sensor (no exit or entry delay, so a command-plane arm
// resolves synchronously) rather than a full siren/window fixture — this
// test proves the MQTT wiring, not the engine's trigger chain, which is
// alarm_engine_e2e_test.go's job. A publish on `<base>/alarm/<area>/set`
// must reach the engine and the resulting state change must republish on
// the retained `<base>/alarm/<area>/state` topic.
//
// Gated exactly like the sibling real-broker tests: startMosquitto skips
// automatically when neither a Docker daemon nor a native `mosquitto`
// binary is reachable.
package integration

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmMqttBase is the MQTT base topic the alarm round-trip rig uses.
const alarmMqttBase = "gh"

// alarmMqttSource tags every alarm verb this test issues through the
// command plane, mirroring the daemon's alarmSourceMQTT constant
// (cmd/openccu-loom/daemon_north.go).
const alarmMqttSource = "mqtt"

// testAlarmSink adapts the alarm engine onto [mqtt.AlarmSink] the same
// way the daemon's composition root does. It is a small test-local
// mirror of cmd/openccu-loom/daemon_north.go's alarmMQTTSink — that type
// lives in package main and cannot be imported from an external test
// package.
type testAlarmSink struct {
	ah *alarmHarness
}

func (s testAlarmSink) Arm(ctx context.Context, areaID string, mode hmenum.AlarmMode) error {
	_, err := s.ah.svc.Engine().Arm(ctx, areaID, engine.ArmRequest{Mode: mode, Source: alarmMqttSource})
	return err
}

func (s testAlarmSink) Disarm(ctx context.Context, areaID string) error {
	return s.ah.svc.Engine().Disarm(ctx, areaID, "", alarmMqttSource)
}

func (s testAlarmSink) Silence(ctx context.Context, areaID string) error {
	return s.ah.svc.Engine().Silence(ctx, areaID, "", alarmMqttSource)
}

func (s testAlarmSink) MasterArm(ctx context.Context, mode hmenum.AlarmMode) error {
	var lastErr error
	for _, a := range s.ah.svc.Engine().Areas() {
		if _, err := s.ah.svc.Engine().Arm(ctx, a.ID, engine.ArmRequest{Mode: mode, Source: alarmMqttSource}); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (s testAlarmSink) MasterDisarm(ctx context.Context) error {
	var lastErr error
	for _, a := range s.ah.svc.Engine().Areas() {
		if err := s.ah.svc.Engine().Disarm(ctx, a.ID, "", alarmMqttSource); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Compile-time proof the test sink satisfies the production contract.
var _ mqtt.AlarmSink = testAlarmSink{}

// alarmMqttRig bundles the alarm engine harness with the real broker
// plumbing: a CommandSubscriber wired with the alarm sink for the
// inbound `…/set` plane, and an AlarmMQTTPublisher for the retained
// state republish. states captures every broker-delivered payload on
// the alarm topic tree, keyed by topic.
type alarmMqttRig struct {
	ah     *alarmHarness
	areaID string
	cmdPub *mqtt.TCPClient
	cmdSub *mqtt.CommandSubscriber
	pub    *mqtt.AlarmMQTTPublisher

	mu     sync.Mutex
	states map[string]string
}

// captureState records the latest payload seen on an alarm-plane topic.
func (r *alarmMqttRig) captureState(topic string, payload []byte, _ bool) {
	r.mu.Lock()
	r.states[topic] = string(payload)
	r.mu.Unlock()
}

// waitStateTopic polls the capture log until topic carries exactly want
// or the timeout elapses. Returns the last observed payload and whether
// it matched.
func (r *alarmMqttRig) waitStateTopic(topic, want string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		got, ok := r.states[topic]
		r.mu.Unlock()
		if ok && got == want {
			return got, true
		}
		if time.Now().After(deadline) {
			return got, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// publishAlarmCommand publishes a non-retained command on
// `<base>/alarm/<area>/set`, the shape [CommandSubscriber.Start]
// registers for the daemon-level alarm plane.
func (r *alarmMqttRig) publishAlarmCommand(t *testing.T, areaID, action string) {
	t.Helper()
	topic := alarmMqttBase + "/alarm/" + areaID + "/set"
	if err := r.cmdPub.Publish(context.Background(), topic, []byte(action), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish alarm command %s to %s: %v", action, topic, err)
	}
}

// setupAlarmMqttRig boots the alarm engine (newAlarmHarness: temp SQLite
// DB, in-process godevccu-backed central) seeded with one area and one
// instant sensor, then wires a real CommandSubscriber + AlarmSink and a
// real AlarmMQTTPublisher against a Mosquitto broker. Skips (via
// startMosquitto) when no broker is available.
func setupAlarmMqttRig(t *testing.T) *alarmMqttRig {
	t.Helper()
	broker := startMosquitto(t) // skips the whole test when unavailable

	ah := newAlarmHarness(t)
	const areaID = "area-mqtt"
	// An instant sensor: zero exit/entry delay so a command-plane arm
	// resolves synchronously into the armed state and a would-be trigger
	// would not wait out an entry delay either. Bound to the harness'
	// SWDO STATE key (never opened here) purely so the sensor row
	// resolves to a real channel, matching the newAlarmHarness pattern.
	ah.seedArea(areaID, "Erdgeschoss", engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {ExitDelaySeconds: 0, EntryDelaySeconds: 0, TriggerSeconds: 10},
		},
	})
	ah.seedSensor("sensor-instant", areaID, ah.swdoStateKey(), hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes:         []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseEntryDelay: false,
	})
	ah.start()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	topics := mqtt.NewTopicBuilder(alarmMqttBase)

	// A daemon-lifetime context for the always-on subscriber/publisher;
	// the per-op connect uses its own short-lived context.
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	t.Cleanup(lifeCancel)

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer connectCancel()

	rig := &alarmMqttRig{ah: ah, areaID: areaID, states: make(map[string]string)}

	// --- state subscriber: capture the retained alarm-plane republish ---
	stateSub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "alarm-state-sub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := stateSub.Connect(connectCtx); err != nil {
		t.Fatalf("state subscriber connect: %v", err)
	}
	t.Cleanup(func() { _ = stateSub.Disconnect(context.Background()) })
	if _, err := stateSub.Subscribe(connectCtx, alarmMqttBase+"/alarm/#", mqtt.QoS1, mqtt.LegacyHandler(rig.captureState)); err != nil {
		t.Fatalf("state subscribe: %v", err)
	}

	// --- AlarmMQTTPublisher: the retained-state republish path ---
	bridgePub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "alarm-bridge-pub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := bridgePub.Connect(connectCtx); err != nil {
		t.Fatalf("bridge publisher connect: %v", err)
	}
	t.Cleanup(func() { _ = bridgePub.Disconnect(context.Background()) })
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: alarmMqttBase, CentralName: ah.centralName(), HADiscoveryEnabled: true,
	}, bridgePub)
	wiring := mqtt.NewWiring(bridge, logger)

	rig.pub = mqtt.NewAlarmMQTTPublisher(ah.svc, wiring, logger)
	rig.pub.Start()
	t.Cleanup(rig.pub.Stop)

	// --- command subscriber: the real inbound `…/alarm/<area>/set` plane ---
	cmdSubClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "alarm-cmd-sub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := cmdSubClient.Connect(connectCtx); err != nil {
		t.Fatalf("command subscriber connect: %v", err)
	}
	t.Cleanup(func() { _ = cmdSubClient.Disconnect(context.Background()) })
	cmdSub := mqtt.NewCommandSubscriber(cmdSubClient, topics, nil, logger).
		WithAlarmSink(testAlarmSink{ah: ah}).
		WithLifecycleContext(lifeCtx)
	if err := cmdSub.Start(lifeCtx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cmdSub.Close)
	rig.cmdSub = cmdSub

	// --- command publisher ---
	cmdPub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "alarm-cmd-pub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := cmdPub.Connect(connectCtx); err != nil {
		t.Fatalf("command publisher connect: %v", err)
	}
	t.Cleanup(func() { _ = cmdPub.Disconnect(context.Background()) })
	rig.cmdPub = cmdPub

	// Let every SUBACK land, and the initial reconcile (readiness event
	// fired on Service.Start) publish its retained disarmed baseline,
	// before the test publishes a command.
	time.Sleep(400 * time.Millisecond)
	return rig
}

// TestAlarmMqttArmDisarmRoundtrip proves the full daemon-level alarm MQTT
// loop against a real broker: publishing ARM_AWAY on
// `<base>/alarm/<area>/set` reaches the real CommandSubscriber, flows
// through the AlarmSink into the engine, and the resulting armed/full
// state republishes as `armed_away` on the retained
// `<base>/alarm/<area>/state` topic (docs/alarm-concept.md §13.3 state
// mapping); DISARM reverses both sides.
func TestAlarmMqttArmDisarmRoundtrip(t *testing.T) {
	rig := setupAlarmMqttRig(t)
	areaID := rig.areaID
	stateTopic := alarmMqttBase + "/alarm/" + areaID + "/state"

	rig.publishAlarmCommand(t, areaID, alarmpanel.HAAlarmCommandArmAway)

	armed := rig.ah.waitAreaState(areaID, hmenum.AlarmAreaStateArmed, 5*time.Second)
	if armed != hmenum.AlarmAreaStateArmed {
		t.Fatalf("after ARM_AWAY: engine state = %q, want armed", armed)
	}
	snap, ok := rig.ah.svc.Engine().Area(areaID)
	if !ok || snap.Mode != hmenum.AlarmModeFull {
		t.Fatalf("after ARM_AWAY: engine mode = %q ok=%v, want full", snap.Mode, ok)
	}

	if got, ok := rig.waitStateTopic(stateTopic, alarmpanel.HAAlarmStateArmedAway, 5*time.Second); !ok {
		t.Fatalf("retained state topic %s never carried %q; last payload = %q", stateTopic, alarmpanel.HAAlarmStateArmedAway, got)
	}

	rig.publishAlarmCommand(t, areaID, alarmpanel.HAAlarmCommandDisarm)

	disarmed := rig.ah.waitAreaState(areaID, hmenum.AlarmAreaStateDisarmed, 5*time.Second)
	if disarmed != hmenum.AlarmAreaStateDisarmed {
		t.Fatalf("after DISARM: engine state = %q, want disarmed", disarmed)
	}

	if got, ok := rig.waitStateTopic(stateTopic, alarmpanel.HAAlarmStateDisarmed, 5*time.Second); !ok {
		t.Fatalf("retained state topic %s never carried %q; last payload = %q", stateTopic, alarmpanel.HAAlarmStateDisarmed, got)
	}
}
