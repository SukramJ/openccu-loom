// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
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
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		h.t.Fatalf("central.New(%s): %v", name, err)
	}
	writer := &fakeSysvarWriter{}
	creator := &fakeSysvarCreator{}
	unit.Hub.SetSysvarValueWriter(writer).SetSysvarCreator(creator)
	if err := h.reg.Register(unit); err != nil {
		h.t.Fatalf("register central %s: %v", name, err)
	}
	return writer, creator
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

// start starts the service and registers cleanup, mirroring
// intents_test.go's start().
func (h *sysvarHarness) start() {
	h.t.Helper()
	if err := h.svc.Start(h.ctx); err != nil {
		h.t.Fatalf("service start: %v", err)
	}
	h.t.Cleanup(func() { _ = h.svc.Stop(context.Background()) })
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

	calls := writer.callsSnapshot()
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

	calls := writer.callsSnapshot()
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
