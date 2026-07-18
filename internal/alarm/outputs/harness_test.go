// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// testStart is the harness wall-clock origin.
var testStart = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

// testCentral is the fixed central name every fixture row and fake
// device resolves under.
const testCentral = "ccu-test"

// healthCall records one HealthFunc invocation.
type healthCall struct {
	Healthy bool
	Note    string
}

// harness bundles a Manager under test with deterministic time and
// recording fakes for every dependency.
type harness struct {
	t   *testing.T
	ctx context.Context

	clk      *clock.Fake
	sched    *manualScheduler
	resolver *fakeResolver
	ledger   *fakeLedger
	journal  *fakeJournal
	rows     *fakeRows
	seq      *seqCounter

	healthMu    sync.Mutex
	healthCalls []healthCall

	notifyMu    sync.Mutex
	notifyCalls []notifyCall

	allRows []sqlitestore.AlarmOutputRow
	mgr     *Manager
}

// notifyCall records one Config.Notify invocation.
type notifyCall struct {
	row      sqlitestore.AlarmOutputRow
	cfg      OutputConfig
	incident sqlitestore.AlarmIncident
}

// newHarness builds an empty harness: no rows, no devices, no
// Manager yet. Call seedOutputs (directly or via seedStandardArea)
// to populate it, which also constructs the Manager on first use.
func newHarness(t *testing.T) *harness {
	t.Helper()
	seq := &seqCounter{}
	clk := clock.NewFake(testStart)
	return &harness{
		t:        t,
		ctx:      context.Background(),
		clk:      clk,
		sched:    newManualScheduler(clk),
		resolver: newFakeResolver(),
		ledger:   newFakeLedger(seq),
		journal:  &fakeJournal{},
		rows:     &fakeRows{},
		seq:      seq,
	}
}

// build constructs the Manager with the standard test bounds
// (180 s default siren duration, 300 s per-incident acoustic budget,
// 60 s stop-verify window), letting cfgOverride adjust the Config
// before construction. Safe to call before any rows are seeded.
func (h *harness) build(cfgOverride func(*Config)) {
	h.t.Helper()
	cfg := Config{
		Clock:                  h.clk,
		Scheduler:              h.sched,
		Resolver:               h.resolver,
		Ledger:                 h.ledger,
		Journal:                h.journal,
		Rows:                   h.rows,
		Health:                 h.recordHealth,
		DefaultSirenDuration:   180 * time.Second,
		MaxAcousticPerIncident: 300 * time.Second,
		StopVerifyWindow:       60 * time.Second,
	}
	if cfgOverride != nil {
		cfgOverride(&cfg)
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		h.t.Fatalf("NewManager: %v", err)
	}
	h.mgr = mgr
	if err := h.mgr.Reload(h.ctx); err != nil {
		h.t.Fatalf("Reload: %v", err)
	}
}

// registerDevice creates and resolves the fake device matching row's
// class under (CentralName, ChannelAddress). A Chirp output also gets
// a sound-device fake registered, since the config decides at Chirp
// time whether the tone or the MP3 path is used.
func (h *harness) registerDevice(row sqlitestore.AlarmOutputRow) {
	switch row.Class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		h.resolver.addSiren(row.CentralName, row.ChannelAddress, newFakeSirenDevice(h.seq))
		if row.Class == hmenum.AlarmOutputClassChirp {
			h.resolver.addSound(row.CentralName, row.ChannelAddress, newFakeSoundDevice(h.seq))
		}
	case hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight:
		h.resolver.addActuator(row.CentralName, row.ChannelAddress, newFakeActuator(h.seq))
	case hmenum.AlarmOutputClassSmokeSounder:
		h.resolver.addSmoke(row.CentralName, row.ChannelAddress, newFakeSmokeDevice(h.seq))
	case hmenum.AlarmOutputClassNotification, hmenum.AlarmOutputClassSysvarMirror:
		// No device path — these classes never resolve through
		// DeviceResolver.
	}
}

// seedOutputs appends rows to the row source, registers a fake device
// per row, and (re)builds/reloads the Manager.
func (h *harness) seedOutputs(rows ...sqlitestore.AlarmOutputRow) {
	h.t.Helper()
	for i := range rows {
		h.registerDevice(rows[i])
	}
	h.allRows = append(h.allRows, rows...)
	h.rows.set(h.allRows)
	if h.mgr == nil {
		h.build(nil)
		return
	}
	if err := h.mgr.Reload(h.ctx); err != nil {
		h.t.Fatalf("Reload: %v", err)
	}
}

// outputRow builds one alarm_outputs row under area "eg", resolving
// under testCentral and channel "<id>:1".
func outputRow(id string, class hmenum.AlarmOutputClass, cfg OutputConfig) sqlitestore.AlarmOutputRow {
	b, err := json.Marshal(cfg)
	if err != nil {
		// Programmer error: OutputConfig always marshals.
		panic(err)
	}
	return sqlitestore.AlarmOutputRow{
		ID:             id,
		AreaID:         "eg",
		Class:          class,
		CentralName:    testCentral,
		ChannelAddress: id + ":1",
		Name:           id,
		ConfigJSON:     string(b),
	}
}

// seedStandardArea seeds the suite's standard fixture: area "eg" with
// one output per class plus an outdoor siren and a mode-restricted
// light, as described in the output-driver test brief.
func (h *harness) seedStandardArea() {
	h.t.Helper()
	h.seedOutputs(
		outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120, AcousticTone: "FREQ_HIGH"}),
		outputRow("sirO", hmenum.AlarmOutputClassOpticalSiren, OutputConfig{}),
		outputRow("plug", hmenum.AlarmOutputClassSwitchedSiren, OutputConfig{DurationSeconds: 60}),
		outputRow("smoke", hmenum.AlarmOutputClassSmokeSounder, OutputConfig{}),
		outputRow("light", hmenum.AlarmOutputClassAlarmLight, OutputConfig{}),
		outputRow("chirp", hmenum.AlarmOutputClassChirp, OutputConfig{ChirpArmTone: "EXTERNALLY_ARMED", ChirpTickTone: "EVENT"}),
		outputRow("sirOut", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 90, Outdoor: true}),
		outputRow("modeOnly", hmenum.AlarmOutputClassAlarmLight, OutputConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}}),
	)
}

func (h *harness) recordHealth(healthy bool, note string) {
	h.healthMu.Lock()
	defer h.healthMu.Unlock()
	h.healthCalls = append(h.healthCalls, healthCall{Healthy: healthy, Note: note})
}

// recordNotify is a Config.Notify implementation that records every
// call for later assertion; wire it via h.build's cfgOverride.
func (h *harness) recordNotify(row sqlitestore.AlarmOutputRow, cfg OutputConfig, incident sqlitestore.AlarmIncident) {
	h.notifyMu.Lock()
	defer h.notifyMu.Unlock()
	h.notifyCalls = append(h.notifyCalls, notifyCall{row: row, cfg: cfg, incident: incident})
}

// notifyCallsSnapshot returns a defensive copy of every recorded
// Notify call so far.
func (h *harness) notifyCallsSnapshot() []notifyCall {
	h.notifyMu.Lock()
	defer h.notifyMu.Unlock()
	return append([]notifyCall(nil), h.notifyCalls...)
}

func (h *harness) healthCallCount() int {
	h.healthMu.Lock()
	defer h.healthMu.Unlock()
	return len(h.healthCalls)
}

func (h *harness) lastHealth(t *testing.T) healthCall {
	t.Helper()
	h.healthMu.Lock()
	defer h.healthMu.Unlock()
	if len(h.healthCalls) == 0 {
		t.Fatal("expected at least one health callback")
	}
	return h.healthCalls[len(h.healthCalls)-1]
}

// advance moves the fake clock and runs every due timer callback
// inline, in deadline order.
func (h *harness) advance(d time.Duration) {
	h.clk.Advance(d)
	h.sched.run()
}

// siren resolves the fake siren device registered for output id.
func (h *harness) siren(id string) *fakeSirenDevice {
	h.t.Helper()
	dev, err := h.resolver.Siren(testCentral, id+":1")
	if err != nil {
		h.t.Fatalf("siren %s: %v", id, err)
	}
	fd, ok := dev.(*fakeSirenDevice)
	if !ok {
		h.t.Fatalf("siren %s: not a fake siren device", id)
	}
	return fd
}

// actuator resolves the fake actuator device registered for output id.
func (h *harness) actuator(id string) *fakeActuator {
	h.t.Helper()
	dev, err := h.resolver.Actuator(testCentral, id+":1")
	if err != nil {
		h.t.Fatalf("actuator %s: %v", id, err)
	}
	fd, ok := dev.(*fakeActuator)
	if !ok {
		h.t.Fatalf("actuator %s: not a fake actuator", id)
	}
	return fd
}

// smoke resolves the fake smoke-sounder device registered for output id.
func (h *harness) smoke(id string) *fakeSmokeDevice {
	h.t.Helper()
	dev, err := h.resolver.SmokeSounder(testCentral, id+":1")
	if err != nil {
		h.t.Fatalf("smoke %s: %v", id, err)
	}
	fd, ok := dev.(*fakeSmokeDevice)
	if !ok {
		h.t.Fatalf("smoke %s: not a fake smoke device", id)
	}
	return fd
}

// sound resolves the fake sound device registered for output id.
func (h *harness) sound(id string) *fakeSoundDevice {
	h.t.Helper()
	dev, err := h.resolver.Sound(testCentral, id+":1")
	if err != nil {
		h.t.Fatalf("sound %s: %v", id, err)
	}
	fd, ok := dev.(*fakeSoundDevice)
	if !ok {
		h.t.Fatalf("sound %s: not a fake sound device", id)
	}
	return fd
}

// newIncident builds a minimal incident for area "eg".
func newIncident(id int64, mode hmenum.AlarmMode) sqlitestore.AlarmIncident {
	return sqlitestore.AlarmIncident{ID: id, AreaID: "eg", Mode: mode}
}

// ptrFloat64 returns a pointer to v (dimmer-level / volume fields are
// *float64 so a nil selects the device's own default).
func ptrFloat64(v float64) *float64 { return &v }

// noPolicy is the zero-value OutputPolicy: no silence, no outdoor
// exclusion, smoke sounders off, no chirps enabled.
var noPolicy = engine.OutputPolicy{}
