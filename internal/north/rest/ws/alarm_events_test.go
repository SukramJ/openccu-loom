// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newAlarmPanelSubscriberFixture wires a fresh Hub and a started
// AlarmPanelSubscriber over a fresh bus, mirroring the
// NewHubEventsSubscriber setup in hub_events_test.go. The subscriber
// fans a single shared bus onto the hub — unlike HubEventsSubscriber
// there is no per-central registry to build.
func newAlarmPanelSubscriberFixture(t *testing.T) (*Hub, *events.Bus) {
	t.Helper()
	h := NewHub()
	bus := events.NewBus()
	sub := NewAlarmPanelSubscriber(bus, h)
	sub.Start()
	t.Cleanup(sub.Stop)
	return h, bus
}

// alarmTopicFilter matches every hub event published on the single
// flat alarm.panel topic.
func alarmTopicFilter(topic string) bool { return topic == alarmPanelTopic }

// TestAlarmPanelSubscriberStateChanged verifies an
// AlarmStateChangedEvent fans out as alarm.state_changed with every
// payload field carried through.
func TestAlarmPanelSubscriberStateChanged(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmStateChangedEvent{
		Base:       hmevent.NewBase(),
		ZoneID:     "eg",
		ZoneName:   "Erdgeschoss",
		From:       hmenum.AlarmZoneStateDisarmed,
		To:         hmenum.AlarmZoneStateArmed,
		Mode:       hmenum.AlarmModeFull,
		ChangedBy:  "operator",
		Source:     "rest",
		IncidentID: 0,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmStateChanged {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmStateChanged)
	}
	p, ok := ev.Payload.(AlarmStateChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmStateChangedPayload", ev.Payload)
	}
	if p.ZoneID != "eg" || p.ZoneName != "Erdgeschoss" {
		t.Fatalf("zone fields = %+v", p)
	}
	if p.OldState != "disarmed" || p.NewState != "armed" || p.Mode != "full" {
		t.Fatalf("transition fields = %+v", p)
	}
	if p.ChangedBy != "operator" || p.Source != "rest" {
		t.Fatalf("attribution fields = %+v", p)
	}
}

// TestAlarmPanelSubscriberCountdown verifies an AlarmCountdownEvent
// fans out as alarm.countdown with both the millisecond source values
// and their derived whole-second display fields.
func TestAlarmPanelSubscriberCountdown(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmCountdownEvent{
		Base:        hmevent.NewBase(),
		ZoneID:      "eg",
		Kind:        "exit_delay",
		RemainingMS: 12_000,
		TotalMS:     30_000,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmCountdown {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmCountdown)
	}
	p, ok := ev.Payload.(AlarmCountdownPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmCountdownPayload", ev.Payload)
	}
	if p.ZoneID != "eg" || p.Kind != "exit_delay" {
		t.Fatalf("zone/kind fields = %+v", p)
	}
	if p.RemainingMS != 12_000 || p.TotalMS != 30_000 {
		t.Fatalf("ms fields = %+v", p)
	}
	if p.RemainingS != 12 || p.TotalS != 30 {
		t.Fatalf("derived second fields = %+v", p)
	}
}

// TestAlarmPanelSubscriberReadinessChanged verifies an
// AlarmReadinessChangedEvent fans out as alarm.readiness_changed with
// the per-mode readiness map converted onto the REST-shaped DTO.
func TestAlarmPanelSubscriberReadinessChanged(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmReadinessChangedEvent{
		Base:   hmevent.NewBase(),
		ZoneID: "eg",
		Readiness: map[hmenum.AlarmMode]hmevent.AlarmModeReadiness{
			hmenum.AlarmModeFull: {Ready: false, Blockers: []string{"window"}},
		},
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmReadinessChanged {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmReadinessChanged)
	}
	p, ok := ev.Payload.(AlarmReadinessChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmReadinessChangedPayload", ev.Payload)
	}
	if p.ZoneID != "eg" {
		t.Fatalf("zone_id = %q, want eg", p.ZoneID)
	}
	full, ok := p.Readiness["full"]
	if !ok {
		t.Fatalf("readiness map missing full mode: %+v", p.Readiness)
	}
	if full.Ready || len(full.Blockers) != 1 || full.Blockers[0] != "window" {
		t.Fatalf("full mode readiness = %+v", full)
	}
}

// TestAlarmPanelSubscriberTriggered verifies an AlarmTriggeredEvent
// fans out as alarm.triggered with every payload field carried
// through.
func TestAlarmPanelSubscriberTriggered(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBase(),
		ZoneID:     "eg",
		ZoneName:   "Erdgeschoss",
		IncidentID: 42,
		SensorID:   "window",
		SensorName: "Kitchen window",
		Cause:      "sensor",
		Mode:       hmenum.AlarmModeFull,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmTriggered {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmTriggered)
	}
	p, ok := ev.Payload.(AlarmTriggeredPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmTriggeredPayload", ev.Payload)
	}
	if p.ZoneID != "eg" || p.IncidentID != 42 {
		t.Fatalf("zone/incident fields = %+v", p)
	}
	if p.SensorID != "window" || p.SensorName != "Kitchen window" || p.Cause != "sensor" {
		t.Fatalf("cause fields = %+v", p)
	}
	if p.Mode != "full" {
		t.Fatalf("mode = %q, want full", p.Mode)
	}
}

// TestAlarmPanelSubscriberNotification verifies an
// AlarmNotificationEvent fans out as alarm.notification with every
// payload field carried through.
func TestAlarmPanelSubscriberNotification(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmNotificationEvent{
		Base:       hmevent.NewBase(),
		ZoneID:     "eg",
		ZoneName:   "Erdgeschoss",
		OutputID:   "notify1",
		OutputName: "Doorbell",
		IncidentID: 9,
		Mode:       hmenum.AlarmModeFull,
		MQTT:       true,
		Webhook:    true,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmNotification {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmNotification)
	}
	p, ok := ev.Payload.(AlarmNotificationPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmNotificationPayload", ev.Payload)
	}
	if p.ZoneID != "eg" || p.ZoneName != "Erdgeschoss" {
		t.Fatalf("zone fields = %+v", p)
	}
	if p.OutputID != "notify1" || p.OutputName != "Doorbell" {
		t.Fatalf("output fields = %+v", p)
	}
	if p.IncidentID != 9 {
		t.Fatalf("incident_id = %d, want 9", p.IncidentID)
	}
	if p.Mode != "full" {
		t.Fatalf("mode = %q, want full", p.Mode)
	}
}

// TestAlarmPanelSubscriberJournalAppended verifies an
// AlarmJournalAppendedEvent fans out as alarm.journal_appended
// carrying the entry head, not the details document.
func TestAlarmPanelSubscriberJournalAppended(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmJournalAppendedEvent{
		Base:       hmevent.NewBase(),
		EntryID:    7,
		ZoneID:     "eg",
		Class:      hmenum.AlarmJournalClassArm,
		Event:      "armed",
		Actor:      "operator",
		IncidentID: 0,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmJournalAppended {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmJournalAppended)
	}
	p, ok := ev.Payload.(AlarmJournalAppendedPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmJournalAppendedPayload", ev.Payload)
	}
	if p.EntryID != 7 || p.ZoneID != "eg" {
		t.Fatalf("entry/zone fields = %+v", p)
	}
	if p.Class != string(hmenum.AlarmJournalClassArm) || p.Event != "armed" || p.Actor != "operator" {
		t.Fatalf("class/event/actor fields = %+v", p)
	}
}

// TestAlarmPanelSubscriberWalkTestProgress verifies an
// AlarmWalkTestEvent fans out as alarm.walktest_progress with the
// session progress counters.
func TestAlarmPanelSubscriberWalkTestProgress(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmWalkTestEvent{
		Base:       hmevent.NewBase(),
		ZoneID:     "eg",
		SensorID:   "window",
		SensorName: "Kitchen window",
		Seen:       2,
		Total:      5,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmWalkTestProgress {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmWalkTestProgress)
	}
	p, ok := ev.Payload.(AlarmWalkTestProgressPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmWalkTestProgressPayload", ev.Payload)
	}
	if p.ZoneID != "eg" || p.SensorID != "window" || p.SensorName != "Kitchen window" {
		t.Fatalf("sensor fields = %+v", p)
	}
	if p.Seen != 2 || p.Total != 5 {
		t.Fatalf("progress fields = %+v", p)
	}
}

// TestAlarmPanelSubscriberHealthChanged verifies an
// AlarmHealthChangedEvent fans out as alarm.health_changed. The
// payload is service-global (no zone_id) even though the broadcast
// still rides the shared alarm.panel topic.
func TestAlarmPanelSubscriberHealthChanged(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmHealthChangedEvent{
		Base:    hmevent.NewBase(),
		Healthy: false,
		Note:    "output driver watchdog timeout",
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmHealthChanged {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmHealthChanged)
	}
	p, ok := ev.Payload.(AlarmHealthChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmHealthChangedPayload", ev.Payload)
	}
	if p.Healthy {
		t.Fatalf("healthy = true, want false")
	}
	if p.Note != "output driver watchdog timeout" {
		t.Fatalf("note = %q", p.Note)
	}
}

// TestAlarmPanelSubscriberPanelChanged verifies an
// AlarmPanelChangedEvent fans out as alarm.panel_changed carrying the
// entity's effective code-policy flags through to the broadcast
// payload, so a live policy edit (notes/concepts/alarm-concept.md §11) propagates
// to WebSocket clients the same way it does to REST and MQTT.
func TestAlarmPanelSubscriberPanelChanged(t *testing.T) {
	t.Parallel()
	h, bus := newAlarmPanelSubscriberFixture(t)

	events.Publish(bus, hmevent.AlarmPanelChangedEvent{
		Base:               hmevent.NewBase(),
		UniqueID:           "openccu-loom_alarm_eg",
		ZoneID:             "eg",
		Name:               "Erdgeschoss",
		State:              "armed_away",
		Available:          true,
		CodeArmRequired:    true,
		CodeDisarmRequired: true,
	})

	ev := pollHub(t, h, alarmTopicFilter)
	if ev.Type != broadcastAlarmPanelChanged {
		t.Fatalf("type = %q, want %q", ev.Type, broadcastAlarmPanelChanged)
	}
	p, ok := ev.Payload.(AlarmPanelChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want AlarmPanelChangedPayload", ev.Payload)
	}
	if p.UniqueID != "openccu-loom_alarm_eg" || p.ZoneID != "eg" {
		t.Fatalf("identity fields = %+v", p)
	}
	if !p.CodeArmRequired || !p.CodeDisarmRequired {
		t.Fatalf("code policy fields = %+v, want both true", p)
	}
	if p.Removed {
		t.Fatalf("removed = true, want false for a live update")
	}
}

// TestAlarmPanelSubscriberNilSafe mirrors
// TestHubEventsSubscriberNilSafe: Start/Stop on a subscriber with no
// bus and no hub must not panic — the daemon leaves the subscriber
// unwired when the alarm service is disabled.
func TestAlarmPanelSubscriberNilSafe(t *testing.T) {
	s := NewAlarmPanelSubscriber(nil, nil)
	s.Start()
	s.Stop()
}
