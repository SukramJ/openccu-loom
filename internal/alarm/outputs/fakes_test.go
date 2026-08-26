// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seqCounter hands out a strictly increasing sequence number shared
// between the ledger fake and the device fakes, so tests can assert
// call ORDER (e.g. "the ledger write happened before the device
// write") without relying on wall-clock timestamps.
type seqCounter struct{ n atomic.Int64 }

func (s *seqCounter) next() int64 { return s.n.Add(1) }

// priorityCall records one no-argument stop-style call (TurnOff,
// Stop) alongside the priority it was issued at and its position in
// the shared call sequence.
type priorityCall struct {
	Priority hmenum.CommandPriority
	Seq      int64
}

// sirenTurnOnCall records one SirenDevice.TurnOn invocation.
type sirenTurnOnCall struct {
	Cfg      sirencdp.OnConfig
	Priority hmenum.CommandPriority
	Seq      int64
}

// fakeSirenDevice is the test double for SirenDevice. Acoustic and
// optical feedback are independently settable so tests can simulate
// the CCU's asynchronous state read-back without coupling it to the
// TurnOn/TurnOff call itself.
type fakeSirenDevice struct {
	mu  sync.Mutex
	seq *seqCounter

	turnOnCalls  []sirenTurnOnCall
	turnOffCalls []priorityCall
	turnOnErr    error
	turnOffErr   error

	acousticActive, acousticObserved bool
	acousticSelection                string
	opticalActive, opticalObserved   bool
	opticalSelection                 string

	tones  []string
	lights []string
}

func newFakeSirenDevice(seq *seqCounter) *fakeSirenDevice {
	return &fakeSirenDevice{seq: seq}
}

func (d *fakeSirenDevice) TurnOn(_ context.Context, cfg sirencdp.OnConfig, priority hmenum.CommandPriority) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOnCalls = append(d.turnOnCalls, sirenTurnOnCall{Cfg: cfg, Priority: priority, Seq: d.seq.next()})
	return d.turnOnErr
}

func (d *fakeSirenDevice) TurnOff(_ context.Context, priority hmenum.CommandPriority) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOffCalls = append(d.turnOffCalls, priorityCall{Priority: priority, Seq: d.seq.next()})
	return d.turnOffErr
}

func (d *fakeSirenDevice) AcousticState() (active bool, selection string, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.acousticActive, d.acousticSelection, d.acousticObserved
}

func (d *fakeSirenDevice) OpticalState() (active bool, selection string, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opticalActive, d.opticalSelection, d.opticalObserved
}

func (d *fakeSirenDevice) AvailableTones() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tones
}

func (d *fakeSirenDevice) AvailableLights() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lights
}

// setAcoustic overrides the simulated acoustic-channel read-back.
func (d *fakeSirenDevice) setAcoustic(active, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.acousticActive, d.acousticObserved = active, observed
}

// setOptical overrides the simulated optical-channel read-back.
func (d *fakeSirenDevice) setOptical(active, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opticalActive, d.opticalObserved = active, observed
}

// setValueLists declares the device's acoustic/optical selection value
// lists. Their head is the disable entry the CCU exposes first, which
// is what an optical-only activation pins the acoustic half to — a test
// that asserts what reaches the acoustic half has to declare them.
func (d *fakeSirenDevice) setValueLists(tones, lights []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tones, d.lights = tones, lights
}

func (d *fakeSirenDevice) setTurnOnErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOnErr = err
}

func (d *fakeSirenDevice) turnOnCallsSnapshot() []sirenTurnOnCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]sirenTurnOnCall(nil), d.turnOnCalls...)
}

func (d *fakeSirenDevice) turnOnCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.turnOnCalls)
}

func (d *fakeSirenDevice) turnOffCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.turnOffCalls)
}

func (d *fakeSirenDevice) turnOffCallsSnapshot() []priorityCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]priorityCall(nil), d.turnOffCalls...)
}

// fakeSmokeDevice is the test double for SmokeSounderDevice.
type fakeSmokeDevice struct {
	mu  sync.Mutex
	seq *seqCounter

	turnOnCalls  []priorityCall
	turnOffCalls []priorityCall
	turnOnErr    error
	turnOffErr   error

	isActive, observed, isIntrusion bool
}

func newFakeSmokeDevice(seq *seqCounter) *fakeSmokeDevice {
	return &fakeSmokeDevice{seq: seq}
}

func (d *fakeSmokeDevice) TurnOn(_ context.Context, priority hmenum.CommandPriority) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOnCalls = append(d.turnOnCalls, priorityCall{Priority: priority, Seq: d.seq.next()})
	return d.turnOnErr
}

func (d *fakeSmokeDevice) TurnOff(_ context.Context, priority hmenum.CommandPriority) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOffCalls = append(d.turnOffCalls, priorityCall{Priority: priority, Seq: d.seq.next()})
	return d.turnOffErr
}

func (d *fakeSmokeDevice) IsIntrusion() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isIntrusion
}

func (d *fakeSmokeDevice) IsActive() (active, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isActive, d.observed
}

func (d *fakeSmokeDevice) setActive(active, observed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isActive, d.observed = active, observed
}

func (d *fakeSmokeDevice) setIntrusion(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isIntrusion = v
}

func (d *fakeSmokeDevice) setTurnOnErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnOnErr = err
}

func (d *fakeSmokeDevice) turnOnCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.turnOnCalls)
}

func (d *fakeSmokeDevice) turnOffCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.turnOffCalls)
}

// actuatorBoundedCall records one ActuatorDevice.TurnOnBounded call.
type actuatorBoundedCall struct {
	D        time.Duration
	Level    *float64
	Priority hmenum.CommandPriority
	Seq      int64
}

// actuatorSteadyCall records one ActuatorDevice.TurnOnSteady call.
type actuatorSteadyCall struct {
	Level    *float64
	Priority hmenum.CommandPriority
	Seq      int64
}

// fakeActuator is the test double for ActuatorDevice.
type fakeActuator struct {
	mu  sync.Mutex
	seq *seqCounter

	boundedCalls []actuatorBoundedCall
	steadyCalls  []actuatorSteadyCall
	turnOffCalls []priorityCall
	boundedErr   error
	steadyErr    error
	turnOffErr   error

	on, observed bool
}

func newFakeActuator(seq *seqCounter) *fakeActuator {
	return &fakeActuator{seq: seq}
}

func (a *fakeActuator) TurnOnBounded(_ context.Context, d time.Duration, level *float64, priority hmenum.CommandPriority) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.boundedCalls = append(a.boundedCalls, actuatorBoundedCall{D: d, Level: level, Priority: priority, Seq: a.seq.next()})
	return a.boundedErr
}

func (a *fakeActuator) TurnOnSteady(_ context.Context, level *float64, priority hmenum.CommandPriority) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steadyCalls = append(a.steadyCalls, actuatorSteadyCall{Level: level, Priority: priority, Seq: a.seq.next()})
	return a.steadyErr
}

func (a *fakeActuator) TurnOff(_ context.Context, priority hmenum.CommandPriority) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.turnOffCalls = append(a.turnOffCalls, priorityCall{Priority: priority, Seq: a.seq.next()})
	return a.turnOffErr
}

func (a *fakeActuator) IsOn() (on, observed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.on, a.observed
}

func (a *fakeActuator) setOn(on, observed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.on, a.observed = on, observed
}

func (a *fakeActuator) boundedCallsSnapshot() []actuatorBoundedCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]actuatorBoundedCall(nil), a.boundedCalls...)
}

func (a *fakeActuator) boundedCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.boundedCalls)
}

func (a *fakeActuator) steadyCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.steadyCalls)
}

func (a *fakeActuator) turnOffCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.turnOffCalls)
}

// soundPlayCall records one SoundDevice.PlayChirp call.
type soundPlayCall struct {
	Index    int
	Volume   float64
	Priority hmenum.CommandPriority
	Seq      int64
}

// fakeSoundDevice is the test double for SoundDevice.
type fakeSoundDevice struct {
	mu  sync.Mutex
	seq *seqCounter

	playCalls []soundPlayCall
	stopCalls []priorityCall
	playErr   error
	stopErr   error
}

func newFakeSoundDevice(seq *seqCounter) *fakeSoundDevice {
	return &fakeSoundDevice{seq: seq}
}

func (s *fakeSoundDevice) PlayChirp(_ context.Context, soundfileIndex int, volume float64, priority hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playCalls = append(s.playCalls, soundPlayCall{Index: soundfileIndex, Volume: volume, Priority: priority, Seq: s.seq.next()})
	return s.playErr
}

func (s *fakeSoundDevice) Stop(_ context.Context, priority hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalls = append(s.stopCalls, priorityCall{Priority: priority, Seq: s.seq.next()})
	return s.stopErr
}

func (s *fakeSoundDevice) playCallsSnapshot() []soundPlayCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]soundPlayCall(nil), s.playCalls...)
}

// fakeResolver implements DeviceResolver over maps keyed by
// "centralName|channelAddress". A lookup miss returns an error, the
// same fault shape a real registry-backed resolver reports for an
// unenrolled or mismatched-class channel.
type fakeResolver struct {
	mu        sync.Mutex
	sirens    map[string]SirenDevice
	smokes    map[string]SmokeSounderDevice
	actuators map[string]ActuatorDevice
	sounds    map[string]SoundDevice
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		sirens:    map[string]SirenDevice{},
		smokes:    map[string]SmokeSounderDevice{},
		actuators: map[string]ActuatorDevice{},
		sounds:    map[string]SoundDevice{},
	}
}

func resolverKey(centralName, channelAddress string) string {
	return centralName + "|" + channelAddress
}

func (r *fakeResolver) addSiren(centralName, channelAddress string, dev SirenDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sirens[resolverKey(centralName, channelAddress)] = dev
}

func (r *fakeResolver) addSmoke(centralName, channelAddress string, dev SmokeSounderDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.smokes[resolverKey(centralName, channelAddress)] = dev
}

func (r *fakeResolver) addActuator(centralName, channelAddress string, dev ActuatorDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actuators[resolverKey(centralName, channelAddress)] = dev
}

func (r *fakeResolver) addSound(centralName, channelAddress string, dev SoundDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sounds[resolverKey(centralName, channelAddress)] = dev
}

func (r *fakeResolver) Siren(centralName, channelAddress string) (SirenDevice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dev, ok := r.sirens[resolverKey(centralName, channelAddress)]
	if !ok {
		return nil, fmt.Errorf("fakeResolver: no siren for %s/%s", centralName, channelAddress)
	}
	return dev, nil
}

func (r *fakeResolver) SmokeSounder(centralName, channelAddress string) (SmokeSounderDevice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dev, ok := r.smokes[resolverKey(centralName, channelAddress)]
	if !ok {
		return nil, fmt.Errorf("fakeResolver: no smoke sounder for %s/%s", centralName, channelAddress)
	}
	return dev, nil
}

func (r *fakeResolver) Actuator(centralName, channelAddress string) (ActuatorDevice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dev, ok := r.actuators[resolverKey(centralName, channelAddress)]
	if !ok {
		return nil, fmt.Errorf("fakeResolver: no actuator for %s/%s", centralName, channelAddress)
	}
	return dev, nil
}

func (r *fakeResolver) Sound(centralName, channelAddress string) (SoundDevice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dev, ok := r.sounds[resolverKey(centralName, channelAddress)]
	if !ok {
		return nil, fmt.Errorf("fakeResolver: no sound device for %s/%s", centralName, channelAddress)
	}
	return dev, nil
}

// ledgerAddCall records one IncidentLedger.AddAcousticMS invocation.
type ledgerAddCall struct {
	IncidentID int64
	DeltaMS    int64
	Seq        int64
}

// fakeLedger is the in-memory IncidentLedger test double. Get answers
// from a per-incident override map so tests can seed "the incident
// already has N ms accounted" without needing a real accumulation
// pass; AddAcousticMS still accumulates into totals for tests that
// want to read it back.
type fakeLedger struct {
	mu     sync.Mutex
	seq    *seqCounter
	calls  []ledgerAddCall
	totals map[int64]int64
	get    map[int64]sqlitestore.AlarmIncident
	addErr error
}

func newFakeLedger(seq *seqCounter) *fakeLedger {
	return &fakeLedger{
		seq:    seq,
		totals: map[int64]int64{},
		get:    map[int64]sqlitestore.AlarmIncident{},
	}
}

func (l *fakeLedger) AddAcousticMS(_ context.Context, id, deltaMS int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.addErr != nil {
		return l.addErr
	}
	l.calls = append(l.calls, ledgerAddCall{IncidentID: id, DeltaMS: deltaMS, Seq: l.seq.next()})
	l.totals[id] += deltaMS
	return nil
}

func (l *fakeLedger) Get(_ context.Context, id int64) (sqlitestore.AlarmIncident, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	inc, ok := l.get[id]
	return inc, ok, nil
}

// seedGet makes the next Get(id) report acousticMS as the incident's
// accumulated acoustic budget consumption.
func (l *fakeLedger) seedGet(id, acousticMS int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.get[id] = sqlitestore.AlarmIncident{ID: id, AcousticMS: acousticMS}
}

func (l *fakeLedger) setAddErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addErr = err
}

func (l *fakeLedger) callsFor(id int64) []ledgerAddCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []ledgerAddCall
	for _, c := range l.calls {
		if c.IncidentID == id {
			out = append(out, c)
		}
	}
	return out
}

// callWithDelta returns the first recorded call whose accounted delta
// equals deltaMS — a convenient way to pick out one output's
// contribution when several outputs share an incident's ledger calls.
func (l *fakeLedger) callWithDelta(deltaMS int64) (ledgerAddCall, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.calls {
		if c.DeltaMS == deltaMS {
			return c, true
		}
	}
	return ledgerAddCall{}, false
}

// fakeJournal records engine.JournalEntry values appended by the
// manager under test.
type fakeJournal struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
}

func (j *fakeJournal) Append(_ context.Context, e engine.JournalEntry) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return int64(len(j.entries)), nil
}

func (j *fakeJournal) entriesFor(event string) []engine.JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []engine.JournalEntry
	for _, e := range j.entries {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

// hasForOutput reports whether an entry of the given event carries
// output_id == outputID in its Details.
func (j *fakeJournal) hasForOutput(event, outputID string) bool {
	for _, e := range j.entriesFor(event) {
		if id, _ := e.Details["output_id"].(string); id == outputID {
			return true
		}
	}
	return false
}

// fakeRows implements OutputRowSource over a settable slice.
type fakeRows struct {
	mu   sync.Mutex
	rows []sqlitestore.AlarmOutputRow
	err  error
}

func (r *fakeRows) GetAll(_ context.Context) ([]sqlitestore.AlarmOutputRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return append([]sqlitestore.AlarmOutputRow(nil), r.rows...), nil
}

func (r *fakeRows) set(rows []sqlitestore.AlarmOutputRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = rows
}

// manualTimer is one pending manualScheduler callback.
type manualTimer struct {
	deadline time.Time
	fn       func()
}

// manualScheduler is a deterministic engine.TimerScheduler: callbacks
// run inline, in deadline order, only when run() is called. Paired
// with clock.Fake this gives fully deterministic timer assertions —
// mirrors the manualScheduler pattern in
// internal/alarm/engine/harness_test.go.
type manualScheduler struct {
	clk *clock.Fake

	mu     sync.Mutex
	nextID int
	timers map[int]*manualTimer
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
