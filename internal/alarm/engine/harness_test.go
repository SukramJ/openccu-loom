// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// testStart is the harness wall-clock origin — after the engine's
// plausibility epoch so restores trust the fake clock by default.
var testStart = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

// manualScheduler is a deterministic TimerScheduler: callbacks run
// inline on the test goroutine when run() is called, in deadline
// order. Combined with clock.Fake this gives fully deterministic
// timer assertions.
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
// deadline order, until none remain due (callbacks may schedule new
// timers that are themselves already due).
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

// fireCall records one OutputPort.FireCycle invocation.
type fireCall struct {
	ZoneID   string
	Incident sqlitestore.AlarmIncident
	Opts     engine.FireOptions
}

// stopCall records one OutputPort.StopAll invocation.
type stopCall struct {
	ZoneID     string
	IncidentID int64
}

// chirpCall records one OutputPort.Chirp invocation.
type chirpCall struct {
	ZoneID string
	Req    engine.ChirpRequest
}

// fakeOutputs records output-port calls and optionally fails them.
type fakeOutputs struct {
	mu      sync.Mutex
	fires   []fireCall
	stops   []stopCall
	chirps  []chirpCall
	fireErr error
	stopErr error
}

func (f *fakeOutputs) FireCycle(_ context.Context, zoneID string, inc sqlitestore.AlarmIncident, opts engine.FireOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fires = append(f.fires, fireCall{ZoneID: zoneID, Incident: inc, Opts: opts})
	return f.fireErr
}

func (f *fakeOutputs) StopAll(_ context.Context, zoneID string, incidentID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, stopCall{ZoneID: zoneID, IncidentID: incidentID})
	return f.stopErr
}

func (f *fakeOutputs) Chirp(_ context.Context, zoneID string, req engine.ChirpRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chirps = append(f.chirps, chirpCall{ZoneID: zoneID, Req: req})
	return nil
}

func (f *fakeOutputs) fireCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.fires)
}

func (f *fakeOutputs) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stops)
}

func (f *fakeOutputs) lastFire(t *testing.T) fireCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fires) == 0 {
		t.Fatal("expected at least one FireCycle call")
	}
	return f.fires[len(f.fires)-1]
}

// fakeSink records published events.
type fakeSink struct {
	mu     sync.Mutex
	events []hmevent.Event
}

func (s *fakeSink) Publish(e hmevent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *fakeSink) stateChanges() []hmevent.AlarmStateChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []hmevent.AlarmStateChangedEvent
	for _, e := range s.events {
		if sc, ok := e.(hmevent.AlarmStateChangedEvent); ok {
			out = append(out, sc)
		}
	}
	return out
}

func (s *fakeSink) triggered() []hmevent.AlarmTriggeredEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []hmevent.AlarmTriggeredEvent
	for _, e := range s.events {
		if tr, ok := e.(hmevent.AlarmTriggeredEvent); ok {
			out = append(out, tr)
		}
	}
	return out
}

// fakeJournal records engine journal entries; err fails Append.
type fakeJournal struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
	err     error
}

func (j *fakeJournal) Append(_ context.Context, e engine.JournalEntry) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return int64(len(j.entries)), j.err
}

func (j *fakeJournal) events() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, 0, len(j.entries))
	for _, e := range j.entries {
		out = append(out, e.Event)
	}
	return out
}

func (j *fakeJournal) has(event string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range j.entries {
		if e.Event == event {
			return true
		}
	}
	return false
}

// fakeReader answers restore-time fresh-value reads from a map keyed
// by sensor ID.
type fakeReader struct {
	mu     sync.Mutex
	active map[string]bool
}

func (r *fakeReader) CurrentActive(_ context.Context, s sqlitestore.AlarmSensorRow) (active, known bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.active[s.ID]
	return v, ok
}

func (r *fakeReader) set(sensorID string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		r.active = map[string]bool{}
	}
	r.active[sensorID] = active
}

// harness bundles a real SQLite store set with fake ports and a
// deterministic clock/scheduler pair around one engine instance.
type harness struct {
	t   *testing.T
	ctx context.Context
	db  *sql.DB

	clk     *clock.Fake
	sched   *manualScheduler
	outputs *fakeOutputs
	sink    *fakeSink
	journal *fakeJournal
	reader  *fakeReader

	zones     *sqlitestore.AlarmZoneStore
	sensors   *sqlitestore.AlarmSensorStore
	states    *sqlitestore.AlarmStateStore
	incidents *sqlitestore.AlarmIncidentStore
	runtime   *sqlitestore.AlarmRuntimeStore

	eng *engine.Engine
}

// openMu serialises store Open calls across parallel tests (the goose
// bootstrap writes library-global state).
var openMu sync.Mutex

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-engine.db"))
	openMu.Lock()
	db, err := sqlitestore.Open(context.Background(), dsn)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	h := &harness{
		t:         t,
		ctx:       context.Background(),
		db:        db,
		zones:     sqlitestore.NewAlarmZoneStore(db),
		sensors:   sqlitestore.NewAlarmSensorStore(db),
		states:    sqlitestore.NewAlarmStateStore(db),
		incidents: sqlitestore.NewAlarmIncidentStore(db),
		runtime:   sqlitestore.NewAlarmRuntimeStore(db),
	}
	h.freshPorts(testStart)
	return h
}

// freshPorts replaces clock, scheduler, and all recording fakes —
// used at construction and before every simulated restart so call
// records start clean.
func (h *harness) freshPorts(now time.Time) {
	h.clk = clock.NewFake(now)
	h.sched = newManualScheduler(h.clk)
	h.outputs = &fakeOutputs{}
	h.sink = &fakeSink{}
	h.journal = &fakeJournal{}
	if h.reader == nil {
		h.reader = &fakeReader{}
	}
}

// build constructs a new engine on the harness's current ports.
func (h *harness) build() {
	h.t.Helper()
	eng, err := engine.New(engine.Deps{
		Clock:        h.clk,
		Scheduler:    h.sched,
		Zones:        h.zones,
		Sensors:      h.sensors,
		State:        h.states,
		Incidents:    h.incidents,
		Runtime:      h.runtime,
		Outputs:      h.outputs,
		Sink:         h.sink,
		Journal:      h.journal,
		SensorReader: h.reader,
	})
	if err != nil {
		h.t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
}

// start builds and starts the engine.
func (h *harness) start() {
	h.t.Helper()
	h.build()
	if err := h.eng.Start(h.ctx); err != nil {
		h.t.Fatalf("engine.Start: %v", err)
	}
}

// advance moves the fake clock and runs every due timer callback
// inline.
func (h *harness) advance(d time.Duration) {
	h.clk.Advance(d)
	h.sched.run()
}

// restart simulates a daemon restart: stop the engine, optionally
// shift the wall clock by downtime (negative values simulate a
// backwards clock jump), and start a fresh engine with fresh
// recording fakes on the same database.
func (h *harness) restart(downtime time.Duration) {
	h.t.Helper()
	h.eng.Stop(h.ctx)
	h.freshPorts(h.clk.Now().Add(downtime))
	h.start()
}

// restartAt is restart with an absolute new wall-clock time (for
// implausible-clock scenarios).
func (h *harness) restartAt(now time.Time) {
	h.t.Helper()
	h.eng.Stop(h.ctx)
	h.freshPorts(now)
	h.start()
}

// mustSnapshot returns the zone snapshot or fails.
func (h *harness) mustSnapshot(zoneID string) engine.ZoneSnapshot {
	h.t.Helper()
	snap, ok := h.eng.Zone(zoneID)
	if !ok {
		h.t.Fatalf("unknown zone %q", zoneID)
	}
	return snap
}

// wantState asserts the zone's state-machine position.
func (h *harness) wantState(zoneID string, want hmenum.AlarmZoneState) {
	h.t.Helper()
	if got := h.mustSnapshot(zoneID).State; got != want {
		h.t.Fatalf("zone %s: state = %s, want %s", zoneID, got, want)
	}
}

// seedZone persists an zone row.
func (h *harness) seedZone(id, name string, cfg engine.ZoneConfig) {
	h.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal zone config: %v", err)
	}
	now := testStart.UnixMilli()
	if err := h.zones.Upsert(h.ctx, sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed zone: %v", err)
	}
}

// seedSensor persists a sensor row.
func (h *harness) seedSensor(id, zoneID string, typ hmenum.AlarmSensorType, cfg engine.SensorConfig) {
	h.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal sensor config: %v", err)
	}
	now := testStart.UnixMilli()
	if err := h.sensors.Upsert(h.ctx, sqlitestore.AlarmSensorRow{
		ID: id, ZoneID: zoneID, CentralName: "ccu-test", InterfaceID: "HmIP-RF",
		ChannelAddress: id + ":1", Parameter: "STATE", SensorType: typ,
		Name: id, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed sensor: %v", err)
	}
}

// openIncident loads the open incident of an zone directly from the
// store.
func (h *harness) openIncident(zoneID string) (sqlitestore.AlarmIncident, bool) {
	h.t.Helper()
	inc, ok, err := h.incidents.GetOpenByZone(h.ctx, zoneID)
	if err != nil {
		h.t.Fatalf("get open incident: %v", err)
	}
	return inc, ok
}

// defaultZoneConfig is the standard two-mode test zone: full with all
// delays, perimeter immediate.
func defaultZoneConfig() engine.ZoneConfig {
	return engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {
				ExitDelaySeconds:  30,
				EntryDelaySeconds: 15,
				TriggerSeconds:    60,
			},
			hmenum.AlarmModePerimeter: {
				TriggerSeconds: 60,
			},
		},
	}
}

// seedStandardZone seeds zone "eg" with a delayed door, an instant
// window, and a motion sensor that participates only in full.
func (h *harness) seedStandardZone() {
	h.t.Helper()
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes:         []hmenum.AlarmMode{hmenum.AlarmModePerimeter, hmenum.AlarmModeFull},
		UseExitDelay:  true,
		UseEntryDelay: true,
	})
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModePerimeter, hmenum.AlarmModeFull},
	})
	h.seedSensor("motion", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes:        []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseExitDelay: true,
	})
}

// armFull arms zone "eg" into full and completes the exit delay.
func (h *harness) armFull() {
	h.t.Helper()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		h.t.Fatalf("arm: %v", err)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

// sortedStrings returns a sorted copy (assertion helper).
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// jsonUnmarshal decodes a JSON string (assertion helper).
func jsonUnmarshal(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }
