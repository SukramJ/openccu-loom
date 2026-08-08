// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// AlarmCollector holds the daemon-global alarm-engine Prometheus
// counters.
//
// Counters:
//   - Triggered         — areas entering the `triggered` state
//     ([hmevent.AlarmTriggeredEvent]).
//   - StateChanges      — every arm-state-machine transition
//     ([hmevent.AlarmStateChangedEvent]).
//   - JournalEntries    — entries appended to the alarm journal
//     ([hmevent.AlarmJournalAppendedEvent]).
//   - HealthTransitions — alarm-subsystem health verdict flips
//     ([hmevent.AlarmHealthChangedEvent]).
//
// Unlike [MqttCollector], the alarm system is a single daemon-level
// service (notes/concepts/alarm-concept.md §14: areas span centrals, not the
// other way around), so metric names carry no per-central segment.
type AlarmCollector struct {
	Triggered         *Counter
	StateChanges      *Counter
	JournalEntries    *Counter
	HealthTransitions *Counter
}

// NewAlarmCollector registers the four "alarm_" counters in reg and
// subscribes them to bus — the alarm service's own event bus, obtained
// via alarm.Service.Bus() — so each counter increments as the
// corresponding event type is published. The returned stop func
// unsubscribes all four handlers; callers fold it into daemon
// teardown. bus is expected to outlive the collector; stop is safe to
// call at most once effectively (repeated calls are a no-op, mirroring
// [events.Subscribe]'s unsubscribe closures).
func NewAlarmCollector(reg *Registry, bus *events.Bus) (collector *AlarmCollector, stop func()) {
	c := &AlarmCollector{
		Triggered:         reg.Counter("alarm_triggered_total", "Total alarm areas that entered the triggered state."),
		StateChanges:      reg.Counter("alarm_state_changes_total", "Total alarm-area arm-state-machine transitions."),
		JournalEntries:    reg.Counter("alarm_journal_entries_total", "Total entries appended to the alarm journal."),
		HealthTransitions: reg.Counter("alarm_health_transitions_total", "Total alarm-subsystem health verdict transitions."),
	}

	unsubTriggered := events.Subscribe(bus, func(hmevent.AlarmTriggeredEvent) {
		c.Triggered.Inc()
	})
	unsubState := events.Subscribe(bus, func(hmevent.AlarmStateChangedEvent) {
		c.StateChanges.Inc()
	})
	unsubJournal := events.Subscribe(bus, func(hmevent.AlarmJournalAppendedEvent) {
		c.JournalEntries.Inc()
	})
	unsubHealth := events.Subscribe(bus, func(hmevent.AlarmHealthChangedEvent) {
		c.HealthTransitions.Inc()
	})

	return c, func() {
		unsubTriggered()
		unsubState()
		unsubJournal()
		unsubHealth()
	}
}
