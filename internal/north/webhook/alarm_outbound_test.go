// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file covers the notes/concepts/alarm-concept.md §13.4 forwarding gap:
// Outbound.SetAlarmBus wires the daemon-level alarm event bus so state
// changes, triggers, journal entries, health transitions, reminders and
// the silent duress fan-out reach the webhook endpoint under their own
// EventType strings, riding the existing allow-list. A fake bus (no
// registry, no real central) is enough — subscribeAlarm only needs
// *events.Bus, not a live central.

// alarmOutboundFixture builds an Outbound with an empty registry (no
// per-central subscriptions) wired to a standalone alarm bus via
// SetAlarmBus, started against a fake transport.
func alarmOutboundFixture(t *testing.T, ft *fakeTransport) (*Outbound, *events.Bus) {
	t.Helper()
	reg := central.NewRegistry()
	bus := events.NewBus()
	cfg := config.NorthWebhook{Enabled: true, URL: "http://hook.test"}
	o := NewOutbound(
		reg, cfg, nil,
		WithHTTPClient(&http.Client{Transport: ft}),
		WithBackoff(instantBackoff()),
		WithClock(fixedClock),
	)
	o.SetAlarmBus(bus)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })
	return o, bus
}

// alarmEnvelope decodes one recorded POST body into the envelope shape
// plus its nested alarm detail, for field-by-field assertions.
func alarmEnvelope(t *testing.T, r recorded) (ev envelope, payload map[string]any) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(r.body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw=%s)", err, r.body)
	}
	var detail map[string]any
	if len(env.Alarm) > 0 {
		if err := json.Unmarshal(env.Alarm, &detail); err != nil {
			t.Fatalf("unmarshal alarm detail: %v (raw=%s)", err, env.Alarm)
		}
	}
	return env, detail
}

// TestOutboundForwardsAlarmStateChanged covers the baseline alarm-plane
// shape: no `central` field (zones are daemon-level), the event type
// header matches the hmevent EventType string, and the nested `alarm`
// detail carries the zone/state transition.
func TestOutboundForwardsAlarmStateChanged(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmStateChangedEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss",
		From: hmenum.AlarmZoneStateDisarmed, To: hmenum.AlarmZoneStateArmed,
		Mode: hmenum.AlarmModeFull, ChangedBy: "op1", Source: "rest-operator",
	})
	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmStateChanged) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeAlarmStateChanged)
	}
	env, detail := alarmEnvelope(t, r)
	if env.Central != "" {
		t.Errorf("central = %q, want empty (zones are daemon-level)", env.Central)
	}
	if env.Event != string(hmevent.EventTypeAlarmStateChanged) {
		t.Errorf("event = %q, want %q", env.Event, hmevent.EventTypeAlarmStateChanged)
	}
	if detail["zone_id"] != "eg" || detail["from_state"] != "disarmed" || detail["to_state"] != "armed" {
		t.Errorf("alarm detail = %+v, want zone_id=eg from_state=disarmed to_state=armed", detail)
	}
	if detail["changed_by"] != "op1" || detail["source"] != "rest-operator" {
		t.Errorf("alarm detail = %+v, want changed_by=op1 source=rest-operator", detail)
	}
}

// TestOutboundForwardsAlarmTriggered covers the trigger-plane shape,
// including the sensor cause fields.
func TestOutboundForwardsAlarmTriggered(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmTriggeredEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss",
		IncidentID: 42, SensorID: "window", SensorName: "Window", Cause: "sensor", Mode: hmenum.AlarmModeFull,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmTriggered) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeAlarmTriggered)
	}
	_, detail := alarmEnvelope(t, r)
	if detail["sensor_id"] != "window" || detail["cause"] != "sensor" {
		t.Errorf("alarm detail = %+v, want sensor_id=window cause=sensor", detail)
	}
	if v, ok := detail["incident_id"].(float64); !ok || int64(v) != 42 {
		t.Errorf("incident_id = %v, want 42", detail["incident_id"])
	}
}

// TestOutboundForwardsAlarmDuress is the security-relevant case §11
// calls out explicitly as a webhook duress sink: the silent fan-out
// must still reach the webhook endpoint (unlike the WS surface, which
// never broadcasts it) and never carry the code's secret — only the
// resolved identity and the verb it accompanied.
func TestOutboundForwardsAlarmDuress(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmDuressEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss",
		Verb: "disarm", By: "Under Duress", Source: "mqtt", IncidentID: 7,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmDuress) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeAlarmDuress)
	}
	_, detail := alarmEnvelope(t, r)
	if detail["verb"] != "disarm" || detail["changed_by"] != "Under Duress" || detail["source"] != "mqtt" {
		t.Errorf("alarm detail = %+v, want verb=disarm changed_by=%q source=mqtt", detail, "Under Duress")
	}
	for _, secretKey := range []string{"code", "pin", "hash"} {
		if _, has := detail[secretKey]; has {
			t.Errorf("duress payload leaks a %q field: %+v", secretKey, detail)
		}
	}
}

// TestOutboundForwardsAlarmReminder covers the §15 row-19 schedule
// reminder plane.
func TestOutboundForwardsAlarmReminder(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmReminderEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss", Mode: hmenum.AlarmModeFull,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmReminder) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeAlarmReminder)
	}
	_, detail := alarmEnvelope(t, r)
	if detail["zone_id"] != "eg" || detail["mode"] != "full" {
		t.Errorf("alarm detail = %+v, want zone_id=eg mode=full", detail)
	}
}

// TestOutboundForwardsAlarmNotification covers the notification plane
// (notes/concepts/alarm-concept.md §7): an enrolled notification output firing
// with Webhook=true forwards under alarm_panel.notification, carrying
// the output identity in the nested detail.
func TestOutboundForwardsAlarmNotification(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmNotificationEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss",
		OutputID: "notify1", OutputName: "Doorbell", IncidentID: 9, Mode: hmenum.AlarmModeFull,
		MQTT: true, Webhook: true,
	})
	waitForCount(t, ft, 1, 2*time.Second)

	r := ft.get(0)
	if got := r.header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmNotification) {
		t.Errorf("X-OpenCCU-Event = %q, want %q", got, hmevent.EventTypeAlarmNotification)
	}
	_, detail := alarmEnvelope(t, r)
	if detail["output_id"] != "notify1" || detail["output_name"] != "Doorbell" {
		t.Errorf("alarm detail = %+v, want output_id=notify1 output_name=Doorbell", detail)
	}
	if detail["zone_id"] != "eg" || detail["mode"] != "full" {
		t.Errorf("alarm detail = %+v, want zone_id=eg mode=full", detail)
	}
	if v, ok := detail["incident_id"].(float64); !ok || int64(v) != 9 {
		t.Errorf("incident_id = %v, want 9", detail["incident_id"])
	}
}

// TestOutboundSkipsAlarmNotificationWhenWebhookDisabled covers the
// per-output plane opt-out: Webhook=false must never enqueue a
// delivery, even though the output fired (and the MQTT plane is
// enabled for the same event).
func TestOutboundSkipsAlarmNotificationWhenWebhookDisabled(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, bus := alarmOutboundFixture(t, ft)

	events.Publish(bus, hmevent.AlarmNotificationEvent{
		Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg", ZoneName: "Erdgeschoss",
		OutputID: "notify1", OutputName: "Doorbell", IncidentID: 9, Mode: hmenum.AlarmModeFull,
		MQTT: true, Webhook: false,
	})

	time.Sleep(50 * time.Millisecond)
	if ft.count() != 0 {
		t.Fatalf("POST count = %d, want 0 (Webhook=false must not enqueue a delivery)", ft.count())
	}
}

// TestOutboundAlarmEventTypeFilterAppliesToAlarmPlane asserts the alarm
// plane rides the same event-type allow-list as the datapoint/system
// planes: an operator who allow-lists only alarm_panel.triggered never
// receives a state-changed delivery.
func TestOutboundAlarmEventTypeFilterAppliesToAlarmPlane(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	reg := central.NewRegistry()
	bus := events.NewBus()
	cfg := config.NorthWebhook{
		Enabled: true, URL: "http://hook.test",
		Events: []string{string(hmevent.EventTypeAlarmTriggered)},
	}
	o := NewOutbound(reg, cfg, nil, WithHTTPClient(&http.Client{Transport: ft}), WithBackoff(instantBackoff()), WithClock(fixedClock))
	o.SetAlarmBus(bus)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Stop(context.Background()) })

	events.Publish(bus, hmevent.AlarmStateChangedEvent{Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg"})
	events.Publish(bus, hmevent.AlarmTriggeredEvent{Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg"})
	waitForCount(t, ft, 1, 2*time.Second)

	// Give a filtered-out delivery a moment it could have arrived in.
	time.Sleep(50 * time.Millisecond)
	if ft.count() != 1 {
		t.Fatalf("POST count = %d, want exactly 1 (state_changed must be filtered out)", ft.count())
	}
	if got := ft.get(0).header.Get("X-OpenCCU-Event"); got != string(hmevent.EventTypeAlarmTriggered) {
		t.Errorf("delivered event = %q, want %q", got, hmevent.EventTypeAlarmTriggered)
	}
}

// TestOutboundStopUnsubscribesAlarmBus asserts Stop tears down the
// alarm-plane subscription too, not just the per-central ones — a
// published event after Stop must never enqueue a delivery.
func TestOutboundStopUnsubscribesAlarmBus(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	o, bus := alarmOutboundFixture(t, ft)

	if err := o.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events.Publish(bus, hmevent.AlarmStateChangedEvent{Base: hmevent.NewBaseAt(fixedNow), ZoneID: "eg"})

	time.Sleep(50 * time.Millisecond)
	if ft.count() != 0 {
		t.Fatalf("POST count after Stop = %d, want 0", ft.count())
	}
}
