// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// scheduleTestStart is an arbitrary fixed wall-clock origin for the
// schedule tests: 2026-07-14 12:00 UTC is a Tuesday.
var scheduleTestStart = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

// manualScheduler is a deterministic TimerScheduler: callbacks run
// inline on the test goroutine when run() is called, in deadline
// order. Combined with clock.Fake this gives fully deterministic
// timer assertions — the same pattern used by the engine package's
// harness_test.go and the outputs package's harness_test.go.
type manualScheduler struct {
	clk *clock.Fake

	mu     sync.Mutex
	nextID int
	timers map[int]*manualTimer
}

type manualTimer struct {
	deadline time.Time
	fn       func()
}

func newManualScheduler(clk *clock.Fake) *manualScheduler {
	return &manualScheduler{clk: clk, timers: map[int]*manualTimer{}}
}

func (s *manualScheduler) Schedule(d time.Duration, fn func()) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	s.timers[id] = &manualTimer{deadline: s.clk.Now().Add(d), fn: fn}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.timers, id)
	}
}

// run fires every timer due at the current fake time, inline and in
// deadline order, until none remain due.
func (s *manualScheduler) run() {
	for {
		s.mu.Lock()
		now := s.clk.Now()
		var dueID int
		var due *manualTimer
		for id, t := range s.timers {
			if t.deadline.After(now) {
				continue
			}
			if due == nil || t.deadline.Before(due.deadline) || (t.deadline.Equal(due.deadline) && id < dueID) {
				dueID, due = id, t
			}
		}
		if due == nil {
			s.mu.Unlock()
			return
		}
		delete(s.timers, dueID)
		s.mu.Unlock()
		due.fn()
	}
}

// pendingCount reports the number of live (uncancelled, unfired) timers.
func (s *manualScheduler) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// fakeZoneStore implements engine.ZoneStore over an in-memory row set.
type fakeZoneStore struct {
	rows []sqlitestore.AlarmZoneRow
	err  error
}

func (f *fakeZoneStore) GetAll(context.Context) ([]sqlitestore.AlarmZoneRow, error) {
	return f.rows, f.err
}

// fakeScheduleEngine implements scheduleEngine over an in-memory
// snapshot map, with a controllable Arm outcome and a call recorder.
type fakeScheduleEngine struct {
	mu       sync.Mutex
	zones    map[string]engine.ZoneSnapshot
	armErr   error
	armCalls []engine.ArmRequest
}

func (f *fakeScheduleEngine) Zone(id string) (engine.ZoneSnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.zones[id]
	return snap, ok
}

func (f *fakeScheduleEngine) Arm(_ context.Context, _ string, req engine.ArmRequest) (engine.ArmResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armCalls = append(f.armCalls, req)
	if f.armErr != nil {
		return engine.ArmResult{}, f.armErr
	}
	return engine.ArmResult{State: hmenum.AlarmZoneStateArmed}, nil
}

// fakeJournalRecorder implements engine.Journal, recording every entry.
type fakeJournalRecorder struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
}

func (f *fakeJournalRecorder) Append(_ context.Context, e engine.JournalEntry) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return int64(len(f.entries)), nil
}

func (f *fakeJournalRecorder) snapshot() []engine.JournalEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]engine.JournalEntry, len(f.entries))
	copy(out, f.entries)
	return out
}

// publishRecorder records every event handed to Publish.
type publishRecorder struct {
	mu     sync.Mutex
	events []hmevent.Event
}

func (p *publishRecorder) publish(e hmevent.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func (p *publishRecorder) snapshot() []hmevent.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]hmevent.Event, len(p.events))
	copy(out, p.events)
	return out
}

// --- fire() unit tests ---

func TestScheduleFireSkipsWhenAlreadyInMode(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	pub := &publishRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeFull},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Engine: eng, Journal: journal, Publish: pub.publish, Clock: clk,
	})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	if len(journal.snapshot()) != 0 {
		t.Fatalf("expected no journal entry when already in mode, got %d", len(journal.snapshot()))
	}
	if len(pub.snapshot()) != 0 {
		t.Fatalf("expected no published event when already in mode, got %d", len(pub.snapshot()))
	}
	if len(eng.armCalls) != 0 {
		t.Fatalf("expected no Arm call when already in mode, got %d", len(eng.armCalls))
	}
}

func TestScheduleFireReminderWhenAutoArmOff(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	pub := &publishRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Engine: eng, Journal: journal, Publish: pub.publish, Clock: clk,
	})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: false},
	})

	entries := journal.snapshot()
	if len(entries) != 1 || entries[0].Event != "arm_reminder" || entries[0].Class != hmenum.AlarmJournalClassArm {
		t.Fatalf("expected one arm_reminder/Arm-class journal entry, got %+v", entries)
	}
	if entries[0].ZoneID != "a1" || entries[0].Source != alarmSourceSchedule {
		t.Fatalf("journal entry zone/source mismatch: %+v", entries[0])
	}

	events := pub.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected one published event, got %d", len(events))
	}
	rem, ok := events[0].(hmevent.AlarmReminderEvent)
	if !ok {
		t.Fatalf("expected AlarmReminderEvent, got %T", events[0])
	}
	if rem.ZoneID != "a1" || rem.ZoneName != "House" || rem.Mode != hmenum.AlarmModeFull {
		t.Fatalf("unexpected reminder payload: %+v", rem)
	}
	if len(eng.armCalls) != 0 {
		t.Fatalf("reminder-only schedule must never call Arm, got %d calls", len(eng.armCalls))
	}
}

func TestScheduleFireAutoArmSuccess(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	pub := &publishRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Engine: eng, Journal: journal, Publish: pub.publish, Clock: clk,
	})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	if len(eng.armCalls) != 1 {
		t.Fatalf("expected exactly one Arm call, got %d", len(eng.armCalls))
	}
	req := eng.armCalls[0]
	if req.Mode != hmenum.AlarmModeFull || req.Force || req.Source != alarmSourceSchedule {
		t.Fatalf("unexpected ArmRequest: %+v (want Mode=full Force=false Source=%q)", req, alarmSourceSchedule)
	}
	if len(journal.snapshot()) != 0 {
		t.Fatalf("expected no fault journal on a successful arm, got %+v", journal.snapshot())
	}
	if len(pub.snapshot()) != 0 {
		t.Fatalf("expected no published event on a successful arm, got %+v", pub.snapshot())
	}
}

func TestScheduleFireAutoArmNotReadyNotifiesHookWhenWired(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	eng := &fakeScheduleEngine{
		zones: map[string]engine.ZoneSnapshot{"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed}},
		armErr: &engine.NotReadyError{
			Blockers: []string{"sensor-1", "sensor-2"},
			Details: []hmevent.AlarmBlockerDetail{
				{SensorID: "sensor-1", Name: "Front door", Reason: hmevent.AlarmBlockerReasonOpen, Blocking: true},
				{SensorID: "sensor-2", Name: "Terrace", Reason: hmevent.AlarmBlockerReasonUnreachable, Blocking: true},
			},
		},
	}
	var hookCalls int
	var gotBlockers []hmevent.AlarmBlockerDetail
	r := newScheduleRunner(scheduleRunnerDeps{
		Engine: eng, Journal: journal, Clock: clk,
		ArmFailure: func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail) {
			hookCalls++
			gotBlockers = blockers
			if zoneID != "a1" || zoneName != "House" || mode != hmenum.AlarmModeFull {
				t.Errorf("unexpected hook args: zone=%s name=%s mode=%s", zoneID, zoneName, mode)
			}
		},
	})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	if hookCalls != 1 {
		t.Fatalf("expected the ArmFailure hook to fire exactly once, got %d", hookCalls)
	}
	if len(gotBlockers) != 2 {
		t.Fatalf("expected the blockers to be forwarded, got %v", gotBlockers)
	}
	// The hook must carry the reason, not just the opaque row ID —
	// that is the point of forwarding details instead of Blockers.
	if gotBlockers[0].Reason != hmevent.AlarmBlockerReasonOpen ||
		gotBlockers[1].Reason != hmevent.AlarmBlockerReasonUnreachable {
		t.Errorf("blocker reasons not forwarded: %+v", gotBlockers)
	}
	if gotBlockers[0].Name != "Front door" {
		t.Errorf("blocker name not forwarded: %+v", gotBlockers[0])
	}
	entries := journal.snapshot()
	if len(entries) != 1 || entries[0].Event != "failed_to_arm" || entries[0].Class != hmenum.AlarmJournalClassFault {
		t.Fatalf("expected one failed_to_arm/Fault journal entry, got %+v", entries)
	}
}

func TestScheduleFireAutoArmNotReadyJournalOnlyWithoutHook(t *testing.T) {
	// No ArmFailure hook wired — the shape of a daemon configured
	// without MQTT. The failure must still be fail-visible via the
	// journal alone.
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	eng := &fakeScheduleEngine{
		zones:  map[string]engine.ZoneSnapshot{"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed}},
		armErr: &engine.NotReadyError{Blockers: []string{"sensor-1"}},
	}
	r := newScheduleRunner(scheduleRunnerDeps{Engine: eng, Journal: journal, Clock: clk})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	entries := journal.snapshot()
	if len(entries) != 1 || entries[0].Event != "failed_to_arm" {
		t.Fatalf("expected one failed_to_arm journal entry, got %+v", entries)
	}
}

func TestScheduleFireAutoArmOtherErrorJournalsGeneric(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	var hookCalls int
	eng := &fakeScheduleEngine{
		zones:  map[string]engine.ZoneSnapshot{"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed}},
		armErr: engine.ErrUnknownMode,
	}
	r := newScheduleRunner(scheduleRunnerDeps{
		Engine: eng, Journal: journal, Clock: clk,
		ArmFailure: func(string, string, hmenum.AlarmMode, []hmevent.AlarmBlockerDetail) { hookCalls++ },
	})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "a1", zoneName: "House",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	entries := journal.snapshot()
	if len(entries) != 1 || entries[0].Event != "schedule_arm_failed" || entries[0].Class != hmenum.AlarmJournalClassFault {
		t.Fatalf("expected one schedule_arm_failed/Fault journal entry, got %+v", entries)
	}
	if hookCalls != 0 {
		t.Fatalf("a non-NotReadyError must not trigger the FAILED_TO_ARM hook (no blockers to report), got %d calls", hookCalls)
	}
}

func TestScheduleFireUnknownZoneIsNoop(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	journal := &fakeJournalRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{}}
	r := newScheduleRunner(scheduleRunnerDeps{Engine: eng, Journal: journal, Clock: clk})

	r.fire(context.Background(), scheduleEntry{
		zoneID: "gone", zoneName: "Gone",
		sched: engine.AlarmSchedule{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
	})

	if len(journal.snapshot()) != 0 || len(eng.armCalls) != 0 {
		t.Fatalf("expected a no-op for a removed zone, got journal=%+v armCalls=%d", journal.snapshot(), len(eng.armCalls))
	}
}

// --- chain lifecycle tests ---

func zoneRow(t *testing.T, id, name string, cfg engine.ZoneConfig) sqlitestore.AlarmZoneRow {
	t.Helper()
	raw, err := marshalZoneConfig(cfg)
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	return sqlitestore.AlarmZoneRow{ID: id, Name: name, ConfigJSON: raw}
}

func TestScheduleStartBuildsOneChainPerScheduleEntry(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	sched := newManualScheduler(clk)
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: true},
			{Time: "07:00", Mode: hmenum.AlarmModeDisarmed},
		}}),
		zoneRow(t, "a2", "Garage", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "23:00", Mode: hmenum.AlarmModePerimeter},
		}}),
	}}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
		"a2": {ID: "a2", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Zones: store, Engine: eng, Clock: clk, Scheduler: sched,
	})

	r.start(context.Background())

	if got := sched.pendingCount(); got != 3 {
		t.Fatalf("expected 3 live chains (one per schedule entry), got %d", got)
	}
}

func TestScheduleChainFiresAtTheRightTimeAndRechains(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart) // Tuesday 12:00 UTC
	sched := newManualScheduler(clk)
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "22:00", Mode: hmenum.AlarmModeFull, AutoArm: false},
		}}),
	}}
	journal := &fakeJournalRecorder{}
	pub := &publishRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Zones: store, Engine: eng, Journal: journal, Publish: pub.publish,
		Clock: clk, Scheduler: sched,
	})
	r.start(context.Background())

	// Advance short of the fire time: nothing fires yet.
	clk.Advance(9*time.Hour + 59*time.Minute)
	sched.run()
	if len(journal.snapshot()) != 0 {
		t.Fatalf("expected no fire before 22:00, got %+v", journal.snapshot())
	}

	// Cross 22:00 today: the reminder fires exactly once.
	clk.Advance(time.Minute)
	sched.run()
	if len(journal.snapshot()) != 1 {
		t.Fatalf("expected exactly one fire at 22:00, got %+v", journal.snapshot())
	}

	// The chain must have re-armed itself for the next occurrence
	// (tomorrow 22:00), not left the zone unscheduled.
	if got := sched.pendingCount(); got != 1 {
		t.Fatalf("expected the chain to re-schedule itself after firing, got %d pending", got)
	}

	// Advancing a full day fires it again.
	clk.Advance(24 * time.Hour)
	sched.run()
	if len(journal.snapshot()) != 2 {
		t.Fatalf("expected a second fire the next day, got %+v", journal.snapshot())
	}
}

func TestScheduleStopCancelsAllChains(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	sched := newManualScheduler(clk)
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "22:00", Mode: hmenum.AlarmModeFull},
		}}),
	}}
	journal := &fakeJournalRecorder{}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Zones: store, Engine: eng, Journal: journal, Clock: clk, Scheduler: sched,
	})
	r.start(context.Background())
	r.stop()

	if got := sched.pendingCount(); got != 0 {
		t.Fatalf("expected stop() to cancel every chain, got %d pending", got)
	}

	clk.Advance(24 * time.Hour)
	sched.run()
	if len(journal.snapshot()) != 0 {
		t.Fatalf("expected no fire after stop(), got %+v", journal.snapshot())
	}
}

func TestScheduleStartRecomputesChainsOnReload(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	sched := newManualScheduler(clk)
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "22:00", Mode: hmenum.AlarmModeFull},
		}}),
	}}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Zones: store, Engine: eng, Clock: clk, Scheduler: sched,
	})
	r.start(context.Background())
	if got := sched.pendingCount(); got != 1 {
		t.Fatalf("expected 1 chain after the first start, got %d", got)
	}

	// Simulate a config write dropping the schedule entirely (e.g. the
	// operator removed it), then Reload recomputing the chains.
	store.rows = []sqlitestore.AlarmZoneRow{zoneRow(t, "a1", "House", engine.ZoneConfig{})}
	r.start(context.Background())

	if got := sched.pendingCount(); got != 0 {
		t.Fatalf("expected the stale chain to be cancelled on reload, got %d pending", got)
	}
}

func TestScheduleChainSkipsInvalidTimeWithoutCrashing(t *testing.T) {
	clk := clock.NewFake(scheduleTestStart)
	sched := newManualScheduler(clk)
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "not-a-time", Mode: hmenum.AlarmModeFull},
			{Time: "22:00", Mode: hmenum.AlarmModeFull},
		}}),
	}}
	eng := &fakeScheduleEngine{zones: map[string]engine.ZoneSnapshot{
		"a1": {ID: "a1", Mode: hmenum.AlarmModeDisarmed},
	}}
	r := newScheduleRunner(scheduleRunnerDeps{
		Zones: store, Engine: eng, Clock: clk, Scheduler: sched,
	})

	r.start(context.Background())

	if got := sched.pendingCount(); got != 1 {
		t.Fatalf("expected the malformed entry to be skipped and the valid one chained, got %d pending", got)
	}
}

func TestScheduleLoadEntriesSkipsMalformedZoneConfig(t *testing.T) {
	store := &fakeZoneStore{rows: []sqlitestore.AlarmZoneRow{
		{ID: "bad", Name: "Bad", ConfigJSON: "{not json"},
		zoneRow(t, "a1", "House", engine.ZoneConfig{Schedules: []engine.AlarmSchedule{
			{Time: "22:00", Mode: hmenum.AlarmModeFull},
		}}),
	}}
	r := newScheduleRunner(scheduleRunnerDeps{Zones: store, Clock: clock.NewFake(scheduleTestStart)})

	entries, err := r.loadEntries(context.Background())
	if err != nil {
		t.Fatalf("loadEntries returned an error: %v", err)
	}
	if len(entries) != 1 || entries[0].zoneID != "a1" {
		t.Fatalf("expected only the well-formed zone's schedule, got %+v", entries)
	}
}

func TestScheduleLoadEntriesPropagatesStoreError(t *testing.T) {
	store := &fakeZoneStore{err: errors.New("db unavailable")}
	r := newScheduleRunner(scheduleRunnerDeps{Zones: store, Clock: clock.NewFake(scheduleTestStart)})

	if _, err := r.loadEntries(context.Background()); err == nil {
		t.Fatal("expected loadEntries to propagate the store error")
	}
}

// --- pure helper tests ---

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in         string
		wantHour   int
		wantMinute int
		wantErr    bool
	}{
		{"08:05", 8, 5, false},
		{"00:00", 0, 0, false},
		{"23:59", 23, 59, false},
		{"", 0, 0, true},
		{"9", 0, 0, true},
		{"24:00", 0, 0, true},
		{"10:60", 0, 0, true},
		{"ab:cd", 0, 0, true},
		{"-1:00", 0, 0, true},
	}
	for _, tc := range cases {
		h, m, err := parseHHMM(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHHMM(%q): expected an error, got hour=%d minute=%d", tc.in, h, m)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHHMM(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if h != tc.wantHour || m != tc.wantMinute {
			t.Errorf("parseHHMM(%q) = (%d, %d), want (%d, %d)", tc.in, h, m, tc.wantHour, tc.wantMinute)
		}
	}
}

func TestNextFire(t *testing.T) {
	// scheduleTestStart is Tuesday 2026-07-14 12:00 UTC.
	tuesdayNoon := scheduleTestStart

	t.Run("later today, no day filter", func(t *testing.T) {
		got := nextFire(tuesdayNoon, 18, 0, nil)
		want := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("already past today rolls to tomorrow", func(t *testing.T) {
		got := nextFire(tuesdayNoon, 8, 0, nil)
		want := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("exact current minute rolls to tomorrow (strictly after now)", func(t *testing.T) {
		got := nextFire(tuesdayNoon, 12, 0, nil)
		want := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("weekday filter selects the next matching day", func(t *testing.T) {
		// Tuesday noon, restricted to Sunday (0): next Sunday is 5 days out.
		got := nextFire(tuesdayNoon, 9, 0, []int{0})
		want := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if got.Weekday() != time.Sunday {
			t.Errorf("resolved weekday = %v, want Sunday", got.Weekday())
		}
	})

	t.Run("weekday filter includes today when time has not passed", func(t *testing.T) {
		// Tuesday noon, restricted to Tuesday (2), time later today.
		got := nextFire(tuesdayNoon, 18, 0, []int{int(time.Tuesday)})
		want := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// marshalZoneConfig encodes cfg into the alarm_zones.config_json wire
// format, for test fixtures.
func marshalZoneConfig(cfg engine.ZoneConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
