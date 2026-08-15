// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// This file drives the sysvar mirror (sysvar.go) through a real
// alarm.Service — a real engine, a real journal, real SQLite stores —
// with a real *central.Unit registered so export/onInbound exercise
// the exact HubCoordinator plumbing production wires; only the
// south-bound sysvar write/create hooks are faked, mirroring
// intents_test.go's convention of driving the router's unexported
// entry points directly through the harness.

// sysvarTestStart is the harness wall-clock origin, kept after the
// engine's clock-plausibility epoch (intents_test.go's convention).
var sysvarTestStart = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

// fakeSysvarWrite records one SetSysvar call.
type fakeSysvarWrite struct {
	name  string
	value any
}

// fakeSysvarWriter is a test double for coordinators.SysvarValueWriter.
type fakeSysvarWriter struct {
	mu    sync.Mutex
	calls []fakeSysvarWrite
}

func (f *fakeSysvarWriter) SetSysvar(_ context.Context, name string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeSysvarWrite{name: name, value: value})
	return nil
}

func (f *fakeSysvarWriter) callsSnapshot() []fakeSysvarWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSysvarWrite(nil), f.calls...)
}

// fakeSysvarCreator is a test double for coordinators.SysvarCreator.
// Only CreateSysvarEnum matters to the mirror (the enum-ensure path);
// the other two methods exist to satisfy the interface.
type fakeSysvarCreator struct {
	mu         sync.Mutex
	enumCalled bool
}

func (*fakeSysvarCreator) CreateSysvarBool(context.Context, string, bool) (map[string]any, error) {
	return nil, nil
}

func (f *fakeSysvarCreator) CreateSysvarEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	f.mu.Lock()
	f.enumCalled = true
	f.mu.Unlock()
	return map[string]any{}, nil
}

func (*fakeSysvarCreator) CreateSysvarFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, nil
}

func (f *fakeSysvarCreator) wasEnumCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enumCalled
}

// blockingSysvarWriter stands in for a CCU that is slow, rebooting or
// gone: every SetSysvar parks until release is closed.
type blockingSysvarWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSysvarWriter) SetSysvar(_ context.Context, _ string, _ any) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

// flakySysvarCreator fails the first failures CreateSysvarEnum calls,
// reproducing the "sysvar creator not wired" answer the hub gives while
// the south-bound bring-up is still gated.
type flakySysvarCreator struct {
	mu       sync.Mutex
	calls    int
	failures int
}

func (*flakySysvarCreator) CreateSysvarBool(context.Context, string, bool) (map[string]any, error) {
	return nil, nil
}

func (f *flakySysvarCreator) CreateSysvarEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("sysvar creator not wired")
	}
	return map[string]any{}, nil
}

func (*flakySysvarCreator) CreateSysvarFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, nil
}

func (f *flakySysvarCreator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// sysvarHarness bundles a real SQLite-backed alarm.Service around a
// central.Registry that can carry real *central.Unit entries with a
// faked south-bound sysvar hook, so mirrorTarget.export/onInbound run
// against the same HubCoordinator plumbing production wires.
type sysvarHarness struct {
	t   *testing.T
	ctx context.Context
	clk *clock.Fake
	reg *central.Registry
	svc *Service
}

func newSysvarHarness(t *testing.T) *sysvarHarness {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-sysvar.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	reg := central.NewRegistry()
	clk := clock.NewFake(sysvarTestStart)
	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: reg,
		Stores:   NewStores(db),
		Clock:    clk,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &sysvarHarness{t: t, ctx: context.Background(), clk: clk, reg: reg, svc: svc}
}

// wireCentral registers a real *central.Unit under name with a faked
// south-bound sysvar writer/creator, so export()/onInbound() resolve
// a live Hub through the registry exactly as production does.
func (h *sysvarHarness) wireCentral(name string) (*fakeSysvarWriter, *fakeSysvarCreator) {
	h.t.Helper()
	writer := &fakeSysvarWriter{}
	creator := &fakeSysvarCreator{}
	h.wireCentralWith(name, writer, creator)
	return writer, creator
}

// wireCentralWith is wireCentral with caller-supplied south-bound
// hooks, for the cases that need a failing or a blocking CCU.
func (h *sysvarHarness) wireCentralWith(name string, w coordinators.SysvarValueWriter, c coordinators.SysvarCreator) {
	h.t.Helper()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		h.t.Fatalf("central.New(%s): %v", name, err)
	}
	unit.Hub.SetSysvarValueWriter(w).SetSysvarCreator(c)
	if err := h.reg.Register(unit); err != nil {
		h.t.Fatalf("register central %s: %v", name, err)
	}
}

// seedZone persists a minimal armable zone: mode "full" with no exit
// delay, so Arm completes synchronously, mirroring intents_test.go's
// seedZone.
func (h *sysvarHarness) seedZone(id, name string) {
	h.t.Helper()
	cfg := engine.ZoneConfig{Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal zone config: %v", err)
	}
	now := h.clk.Now().UnixMilli()
	if err := h.svc.Stores().Zones.Upsert(h.ctx, sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed zone: %v", err)
	}
}

// seedOutput persists a sysvar_mirror output row under zoneID,
// resolving under centralName, with cfg as its parsed configuration.
func (h *sysvarHarness) seedOutput(id, zoneID, centralName string, cfg mirrorConfig) {
	h.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal mirror config: %v", err)
	}
	now := h.clk.Now().UnixMilli()
	if err := h.svc.Stores().Outputs.Upsert(h.ctx, sqlitestore.AlarmOutputRow{
		ID: id, ZoneID: zoneID, Class: hmenum.AlarmOutputClassSysvarMirror,
		CentralName: centralName, Name: id, ConfigJSON: string(b),
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed output %s: %v", id, err)
	}
}

// seedSensor persists one enrolled window sensor of zoneID so the
// ordinary refused-arm case (an open contact) can be produced through
// the real engine.
func (h *sysvarHarness) seedSensor(id, zoneID, centralName string) {
	h.t.Helper()
	cfg, err := json.Marshal(engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if err != nil {
		h.t.Fatalf("marshal sensor config: %v", err)
	}
	now := h.clk.Now().UnixMilli()
	if err := h.svc.Stores().Sensors.Upsert(h.ctx, sqlitestore.AlarmSensorRow{
		ID: id, ZoneID: zoneID, CentralName: centralName,
		InterfaceID:    central.WireInterfaceID(centralName, hmenum.InterfaceHmIPRF),
		ChannelAddress: "0001D3C99ABCDE:1", Parameter: string(hmenum.ParameterState),
		SensorType: hmenum.AlarmSensorTypeWindow, Name: id, ConfigJSON: string(cfg),
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed sensor %s: %v", id, err)
	}
}

// start starts the service and registers cleanup, mirroring
// intents_test.go's start().
func (h *sysvarHarness) start() {
	h.t.Helper()
	if err := h.svc.Start(h.ctx); err != nil {
		h.t.Fatalf("service start: %v", err)
	}
	h.t.Cleanup(func() { _ = h.svc.Stop(context.Background()) })
}

// waitCalls polls w until it has recorded at least n SetSysvar calls
// and returns them. Exports run on the mirror's own worker goroutine —
// deliberately, so the engine lock is never held across a CCU round
// trip — so reading the fake in the statement after the trigger races
// the write.
func (h *sysvarHarness) waitCalls(w *fakeSysvarWriter, n int) []fakeSysvarWrite {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		calls := w.callsSnapshot()
		if len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("SetSysvar calls = %+v, want at least %d", calls, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// zoneState reads the engine's current state for id, failing if
// unknown.
func (h *sysvarHarness) zoneState(id string) hmenum.AlarmZoneState {
	h.t.Helper()
	snap, ok := h.svc.Engine().Zone(id)
	if !ok {
		h.t.Fatalf("unknown zone %q", id)
	}
	return snap.State
}

// --- export: existing-mode (bool ALARM variable) target ---

// TestSysvarMirrorExisting_ExportsTrueWhenTriggered verifies an
// existing-mode target (sysvar_existing=true) writes a plain bool
// true while the zone is triggered, and never calls CreateSysvarEnum
// — the variable is operator-owned and never created/retyped by the
// mirror.
func TestSysvarMirrorExisting_ExportsTrueWhenTriggered(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	writer, creator := h.wireCentral("ccu1")
	h.seedZone("eg", "Erdgeschoss")
	h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmState", SysvarExisting: true})
	h.start()

	h.svc.sysvarMirror.onStateChanged(hmevent.AlarmStateChangedEvent{
		ZoneID: "eg", To: hmenum.AlarmZoneStateTriggered, Mode: hmenum.AlarmModeFull,
	})

	calls := h.waitCalls(writer, 1)
	if len(calls) != 1 {
		t.Fatalf("SetSysvar calls = %+v, want exactly 1", calls)
	}
	if calls[0].name != "AlarmState" {
		t.Fatalf("sysvar name = %q, want AlarmState", calls[0].name)
	}
	v, ok := calls[0].value.(bool)
	if !ok || !v {
		t.Fatalf("sysvar value = %v (%T), want bool true", calls[0].value, calls[0].value)
	}
	if creator.wasEnumCalled() {
		t.Error("CreateSysvarEnum must never be called for an existing-mode target")
	}
}

// TestSysvarMirrorExisting_ExportsFalseOnModeChange verifies the same
// existing-mode target writes a plain bool false for any non-triggered
// transition (e.g. an arm to a protection mode), and still never calls
// CreateSysvarEnum.
func TestSysvarMirrorExisting_ExportsFalseOnModeChange(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	writer, creator := h.wireCentral("ccu1")
	h.seedZone("eg", "Erdgeschoss")
	h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmState", SysvarExisting: true})
	h.start()

	h.svc.sysvarMirror.onStateChanged(hmevent.AlarmStateChangedEvent{
		ZoneID: "eg", To: hmenum.AlarmZoneStateArmed, Mode: hmenum.AlarmModeFull,
	})

	calls := h.waitCalls(writer, 1)
	if len(calls) != 1 {
		t.Fatalf("SetSysvar calls = %+v, want exactly 1", calls)
	}
	v, ok := calls[0].value.(bool)
	if !ok || v {
		t.Fatalf("sysvar value = %v (%T), want bool false", calls[0].value, calls[0].value)
	}
	if creator.wasEnumCalled() {
		t.Error("CreateSysvarEnum must never be called for an existing-mode target")
	}
}

// --- onInbound: existing-mode targets never route an intent ---

// TestSysvarMirrorOnInbound_ExistingModeTargetProducesNoIntentButManagedTargetDoes
// verifies that an inbound write to an existing-mode (bool) mirror
// target is never turned into an arm intent — a bool carries no mode,
// so mirrorTargets excludes it from onInbound's match set entirely —
// while the same inbound value on an ordinary managed mirror target
// still arms its zone, confirming the exclusion is scoped to the
// existing-mode target and does not regress the managed path.
func TestSysvarMirrorOnInbound_ExistingModeTargetProducesNoIntentButManagedTargetDoes(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	h.wireCentral("ccu1")
	h.seedZone("existing", "Existing Zone")
	h.seedZone("managed", "Managed Zone")
	h.seedOutput("mirrorExisting", "existing", "ccu1", mirrorConfig{SysvarName: "ExistingVar", SysvarExisting: true})
	h.seedOutput("mirrorManaged", "managed", "ccu1", mirrorConfig{SysvarName: "ManagedVar"})
	h.start()

	// Index 2 is AlarmModeFull (sysvarIndexByMode). On the
	// existing-mode target this must be a no-op.
	h.svc.sysvarMirror.onInbound("ccu1", hmevent.SysvarChangedEvent{Name: "ExistingVar", NewValue: hmtypes.IntValue(2)})
	if got := h.zoneState("existing"); got != hmenum.AlarmZoneStateDisarmed {
		t.Fatalf("existing zone state = %s, want disarmed (existing-mode target must never route an intent)", got)
	}

	// The identical index on the managed target still arms its zone.
	h.svc.sysvarMirror.onInbound("ccu1", hmevent.SysvarChangedEvent{Name: "ManagedVar", NewValue: hmtypes.IntValue(2)})
	if got := h.zoneState("managed"); got != hmenum.AlarmZoneStateArmed {
		t.Fatalf("managed zone state = %s, want armed (managed sysvar target must still route an intent)", got)
	}
}

// --- onInbound: an intent the engine does not carry out ---

// TestSysvarMirrorOnInbound_IntentTheEngineDoesNotCarryOutReExportsTheRealState
// pins the invariant the refused-disarm branch already states in prose:
// the mirror cannot lie.
//
// Whatever the third party wrote is sitting in the CCU variable by the
// time the intent reaches us. Every exit that leaves the zone where it
// was therefore has to push the real state back, or the variable keeps
// asserting a protection level the engine refused — for CCU programs,
// the WebUI and every third-party reader, until some unrelated
// transition happens to correct it.
func TestSysvarMirrorOnInbound_IntentTheEngineDoesNotCarryOutReExportsTheRealState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		inbound  int
		wantZone hmenum.AlarmZoneState
	}{
		// Index 2 is AlarmModeFull: the arm is refused because a window
		// is open, the everyday reason an arm fails.
		{name: "refused arm", inbound: 2, wantZone: hmenum.AlarmZoneStateDisarmed},
		// Index 6 is "Alarm", an export-only state, and index 5 is a
		// mode this zone does not configure: neither is an intent the
		// engine can carry out.
		{name: "export-only alarm index", inbound: 6, wantZone: hmenum.AlarmZoneStateDisarmed},
		{name: "mode the zone does not configure", inbound: 5, wantZone: hmenum.AlarmZoneStateDisarmed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newSysvarHarness(t)
			writer, _ := h.wireCentral("ccu1")
			h.seedZone("eg", "Erdgeschoss")
			h.seedSensor("window", "eg", "ccu1")
			h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmMode"})
			h.start()
			h.svc.Engine().HandleSensorEvent(h.ctx, "window", true)

			h.svc.sysvarMirror.onInbound("ccu1", hmevent.SysvarChangedEvent{
				Name: "AlarmMode", NewValue: hmtypes.IntValue(tc.inbound),
			})

			if got := h.zoneState("eg"); got != tc.wantZone {
				t.Fatalf("zone state = %s, want %s", got, tc.wantZone)
			}
			calls := h.waitCalls(writer, 1)
			last := calls[len(calls)-1]
			if last.name != "AlarmMode" {
				t.Fatalf("re-export wrote %q, want AlarmMode", last.name)
			}
			if idx, ok := last.value.(int); !ok || idx != 0 {
				t.Fatalf("re-exported index = %v (%T), want 0 (disarmed)", last.value, last.value)
			}
		})
	}
}

// TestSysvarMirrorOnInbound_RefusedArmIsJournalled keeps the operator's
// record of a rejected intent: the fault row is what explains, after
// the fact, why the CCU variable snapped back.
func TestSysvarMirrorOnInbound_RefusedArmIsJournalled(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	h.wireCentral("ccu1")
	h.seedZone("eg", "Erdgeschoss")
	h.seedSensor("window", "eg", "ccu1")
	h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmMode"})
	h.start()
	h.svc.Engine().HandleSensorEvent(h.ctx, "window", true)

	h.svc.sysvarMirror.onInbound("ccu1", hmevent.SysvarChangedEvent{Name: "AlarmMode", NewValue: hmtypes.IntValue(2)})

	entries, err := h.svc.Stores().Journal.Query(h.ctx, sqlitestore.AlarmJournalFilter{IncludeHidden: true})
	if err != nil {
		t.Fatalf("query journal: %v", err)
	}
	for i := range entries {
		if entries[i].Event == "sysvar_arm_failed" {
			return
		}
	}
	t.Fatalf("no sysvar_arm_failed journal entry after a refused arm; got %+v", entries)
}

// --- export: the engine keeps running while the CCU does not ---

// TestSysvarMirrorExport_LeavesAlarmVerbsAnswerableWhileTheCCUHangs
// drives the mirror through the real sink — engine verb → publishState
// → Service.publish → mirror — with a CCU write that never returns.
//
// The sink runs with the engine lock held, so exporting inline put a
// CCU round trip inside that lock: every alarm verb, Disarm and Silence
// included, then queued behind an unreachable CCU, which is precisely
// when the state machine has to answer.
func TestSysvarMirrorExport_LeavesAlarmVerbsAnswerableWhileTheCCUHangs(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	writer := &blockingSysvarWriter{entered: make(chan struct{}), release: make(chan struct{})}
	h.wireCentralWith("ccu1", writer, &fakeSysvarCreator{})
	h.seedZone("eg", "Erdgeschoss")
	h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmMode"})
	h.start()
	t.Cleanup(func() { close(writer.release) })

	armed := make(chan error, 1)
	go func() {
		_, err := h.svc.Engine().Arm(context.Background(), "eg",
			engine.ArmRequest{Mode: hmenum.AlarmModeFull, Source: "test"})
		armed <- err
	}()
	select {
	case err := <-armed:
		if err != nil {
			t.Fatalf("arm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Arm never returned: the export ran on the engine's goroutine, under the engine lock")
	}

	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the armed transition never reached the mirror export")
	}

	disarmed := make(chan error, 1)
	go func() { disarmed <- h.svc.Engine().Disarm(context.Background(), "eg", "", "test") }()
	select {
	case err := <-disarmed:
		if err != nil {
			t.Fatalf("disarm while an export is on the wire: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Disarm blocked behind the stuck sysvar export — the engine lock is held across the CCU write")
	}
}

// --- export: a failed enum create must not disable the mirror ---

// TestSysvarMirrorExport_RetriesEnumCreateAfterAFailure pins that a
// failed CreateSysvarEnum is retried on the next export.
//
// The first export after boot regularly races the south-bound bring-up
// and is answered with "sysvar creator not wired". Latching the ensure
// flag on that failure disabled enum creation for the rest of the
// process: every later write addressed a variable that had never been
// created, so a configured mirror stayed dead until a restart.
func TestSysvarMirrorExport_RetriesEnumCreateAfterAFailure(t *testing.T) {
	t.Parallel()
	h := newSysvarHarness(t)
	writer := &fakeSysvarWriter{}
	creator := &flakySysvarCreator{failures: 1}
	h.wireCentralWith("ccu1", writer, creator)
	h.seedZone("eg", "Erdgeschoss")
	h.seedOutput("mirror1", "eg", "ccu1", mirrorConfig{SysvarName: "AlarmMode"})
	h.start()

	h.svc.sysvarMirror.onStateChanged(hmevent.AlarmStateChangedEvent{
		ZoneID: "eg", To: hmenum.AlarmZoneStateArmed, Mode: hmenum.AlarmModeFull,
	})
	h.waitCalls(writer, 1)

	h.svc.sysvarMirror.onStateChanged(hmevent.AlarmStateChangedEvent{
		ZoneID: "eg", To: hmenum.AlarmZoneStateDisarmed, Mode: hmenum.AlarmModeDisarmed,
	})
	h.waitCalls(writer, 2)

	if got := creator.callCount(); got != 2 {
		t.Fatalf("CreateSysvarEnum calls = %d, want 2 (the failed create must be retried, not latched)", got)
	}

	// And once it succeeds the ensure stops repeating.
	h.svc.sysvarMirror.onStateChanged(hmevent.AlarmStateChangedEvent{
		ZoneID: "eg", To: hmenum.AlarmZoneStateArmed, Mode: hmenum.AlarmModeFull,
	})
	h.waitCalls(writer, 3)
	if got := creator.callCount(); got != 2 {
		t.Fatalf("CreateSysvarEnum calls = %d after a successful create, want 2", got)
	}
}
