// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// armFailureClockStart is where the test's synctest bubble clock is
// moved to; it is well past the engine's clock-plausibility epoch, as
// the other alarm harnesses keep theirs.
var armFailureClockStart = time.Date(2026, 7, 16, 21, 59, 0, 0, time.UTC)

// armFailureScheduleTime is the schedule's fire time of day, one minute
// after armFailureClockStart so a 90 s sleep crosses it. The schedule
// runner reads time.Now in the process-local zone, so the time of day is
// rendered in that zone: the test stays correct on a host in any zone.
func armFailureScheduleTime() string {
	return armFailureClockStart.Add(time.Minute).In(time.Local).Format("15:04")
}

// TestWireSystemStatusSubscribersPublishesAScheduledArmFailure pins the
// unattended auto-arm failure onto the MQTT alarm plane through the real
// composition root.
//
// The engine refuses an arm whose zone still has an open contact. For a
// command that refusal travels back as an error the caller renders; for
// the schedule chain there is no caller — the arm happens at 22:00 with
// nobody watching, so the refusal only exists on the surfaces the daemon
// pushes it to. wireSystemStatusSubscribers never wired the service's
// arm-failure hook, so the nightly auto-arm that did not happen reached
// exactly one place: a journal row nobody reads at 22:00. The house
// stayed unarmed and every live surface kept saying "disarmed" without
// ever saying "and here is why it is not armed".
//
// The assertion is the publish, not the setter call: a test that hands
// the publisher to the service itself proves the two can work together
// and says nothing about whether a running daemon connects them. Only
// the wiring under test is production's — the bubble clock makes
// the schedule chain reachable in a test without waiting for it, and the
// sensor is opened through the same engine entry point the
// device-event path uses.
func TestWireSystemStatusSubscribersPublishesAScheduledArmFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const (
			zoneID   = "zone-schedule-arm"
			sensorID = "sensor-front-door"
			mqttBase = "openccu-loom"
		)
		ctx := context.Background()

		db := openMigratedTestDB(t, "alarm_arm_failure.db")
		stores := alarm.NewStores(db)
		seedZoneWithNightlyAutoArm(ctx, t, stores, zoneID, sensorID)

		time.Sleep(time.Until(armFailureClockStart))
		svc, err := alarm.NewService(alarm.Deps{
			Settings: alarm.Settings{Enabled: true},
			Registry: central.NewRegistry(),
			Stores:   stores,
			Logger:   discardTestLogger(),
		})
		if err != nil {
			t.Fatalf("alarm.NewService: %v", err)
		}

		client := mqtt.NewNoopClient()
		wiring := mqtt.NewWiring(mqtt.NewBridge(mqtt.BridgeConfig{
			Base: mqttBase, CentralName: "ccu-test", RawEnabled: true,
		}, client), discardTestLogger())

		_, teardown := wireSystemStatusSubscribers(
			central.NewRegistry(), ws.NewHub(), wiring, nil,
			svc, newAlarmMQTTSink(svc), nil, "", "", discardTestLogger(),
		)
		t.Cleanup(teardown)

		// Production order: the subscribers are wired before the alarm
		// service starts, and the schedule chain only exists after Start.
		if err := svc.Start(ctx); err != nil {
			t.Fatalf("alarm service Start: %v", err)
		}
		t.Cleanup(func() { _ = svc.Stop(ctx) })

		// Somebody left the front door open. This is the engine entry point
		// the device-event path calls when a contact reports open.
		svc.Engine().HandleSensorEvent(ctx, sensorID, true)

		// 22:00 arrives and the chain fires.
		time.Sleep(90 * time.Second)

		want := mqttBase + "/status/alarm/" + zoneID + "/event"
		pay := waitForAlarmEvent(t, client, want)
		if pay.Type != "FAILED_TO_ARM" {
			t.Fatalf("event type = %q, want FAILED_TO_ARM", pay.Type)
		}
		if pay.HM.Mode != string(hmenum.AlarmModeFull) {
			t.Errorf("event mode = %q, want %q", pay.HM.Mode, hmenum.AlarmModeFull)
		}
		if len(pay.HM.OpenSensors) != 1 || pay.HM.OpenSensors[0] != "Front door" {
			t.Errorf("open sensors = %v, want the blocking sensor's display name", pay.HM.OpenSensors)
		}
	})
}

// alarmEventBody is the subset of the alarm event topic's JSON this pin
// asserts on. It is decoded rather than string-matched so a field rename
// on the publisher side fails here instead of passing on a substring.
// alarmEventBody is the alarm event's status object (ADR 0083): the event
// type in `val`, the rest of the event under `hm`.
type alarmEventBody struct {
	Type string `json:"val"`
	HM   struct {
		ZoneID      string   `json:"zone_id"`
		Mode        string   `json:"mode"`
		OpenSensors []string `json:"open_sensors"`
	} `json:"hm"`
}

// waitForAlarmEvent polls the recorded publications for topic until one
// arrives. The publisher hands every event to a worker goroutine, so the
// publish is never done by the time the triggering call returns.
func waitForAlarmEvent(t *testing.T, client *mqtt.NoopClient, topic string) alarmEventBody {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range client.Published() {
			if p.Topic != topic {
				continue
			}
			var body alarmEventBody
			if err := json.Unmarshal(p.Payload, &body); err != nil {
				t.Fatalf("decode alarm event on %s: %v", topic, err)
			}
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	var seen []string
	for _, p := range client.Published() {
		if strings.Contains(p.Topic, "/alarm/") {
			seen = append(seen, p.Topic)
		}
	}
	t.Fatalf("no alarm event reached %s within the deadline; the scheduled auto-arm was refused and "+
		"nothing north-bound said so. Alarm topics published: %v", topic, seen)
	return alarmEventBody{}
}

// seedZoneWithNightlyAutoArm persists the operator configuration this
// pin needs: one zone that auto-arms into full protection every night,
// with one contact sensor enrolled in that mode. A sensor reporting open
// blocks the arm under the default blocker policy.
func seedZoneWithNightlyAutoArm(
	ctx context.Context, t *testing.T, stores *alarm.Stores, zoneID, sensorID string,
) {
	t.Helper()

	zoneCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {},
		},
		Schedules: []engine.AlarmSchedule{
			{Time: armFailureScheduleTime(), Mode: hmenum.AlarmModeFull, AutoArm: true},
		},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: zoneID, Name: "House", Slug: "house", ConfigJSON: string(zoneCfg),
	}); err != nil {
		t.Fatalf("upsert zone: %v", err)
	}

	sensorCfg, err := json.Marshal(engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID:             sensorID,
		ZoneID:         zoneID,
		CentralName:    "ccu-test",
		InterfaceID:    "ccu-test-HmIP-RF",
		ChannelAddress: "0001D3C99ABCDE:1",
		Parameter:      string(hmenum.ParameterState),
		SensorType:     hmenum.AlarmSensorTypeDoor,
		Name:           "Front door",
		ConfigJSON:     string(sensorCfg),
	}); err != nil {
		t.Fatalf("upsert sensor: %v", err)
	}
}
