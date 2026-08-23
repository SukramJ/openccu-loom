// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestOutputFailureScopesPanelAvailabilityToItsOwnZone covers K1: a
// siren enrolled only in one zone must not remove Home Assistant's
// disarm control from every other zone (or the whole house) at exactly
// the moment an alarm needs it.
//
// Two armable zones share nothing but the master aggregate. Zone A's
// only output points at a central that was never registered, so
// resolving its device fails and the output driver reports a failure.
// Before the fix, onPanelHealthEvent read that failure as a fleet-wide
// signal and flipped every panel — including zone B's, which owns no
// failing output at all.
func TestOutputFailureScopesPanelAvailabilityToItsOwnZone(t *testing.T) {
	t.Parallel()

	const zoneA = "og"
	const zoneB = "eg"

	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-panel-output-scoping.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stores := NewStores(db)
	ctx := context.Background()
	zoneCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, z := range []struct{ id, name string }{{zoneA, "Obergeschoss"}, {zoneB, "Erdgeschoss"}} {
		if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
			ID: z.id, Name: z.name, Slug: z.id, ConfigJSON: string(zoneCfg),
			CreatedAtMS: now, UpdatedAtMS: now,
		}); err != nil {
			t.Fatalf("seed zone %s: %v", z.id, err)
		}
	}
	// zoneA's only output; zoneB has none. The unresolvable central makes
	// the output driver's fire command fail without needing a device fake.
	if err := stores.Outputs.Upsert(ctx, sqlitestore.AlarmOutputRow{
		ID: "siren1", ZoneID: zoneA, Name: "Sirene",
		Class:          hmenum.AlarmOutputClassAcousticSiren,
		CentralName:    "ccu-missing",
		ChannelAddress: "0001D3C99ABCDE:3",
	}); err != nil {
		t.Fatalf("seed output: %v", err)
	}

	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: central.NewRegistry(),
		Stores:   stores,
		Clock:    clock.NewFake(time.UnixMilli(now)),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("service start: %v", err)
	}
	t.Cleanup(func() {
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			_ = svc.Stop(context.Background())
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("alarm service stop did not return: the engine lock is still held")
		}
	})

	if err := svc.Engine().PanicTrigger(ctx, zoneA, false, "tester", "test"); err != nil {
		t.Fatalf("PanicTrigger: %v", err)
	}

	// The output failure is asynchronous relative to the fire call; poll
	// for zone A's panel to reflect it before asserting on the others.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !panelAvailable(t, svc, zoneA) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("zone A's panel never went unavailable after its only output failed to fire")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !panelAvailable(t, svc, zoneB) {
		t.Fatalf("zone B's panel went unavailable because zone A's output — which zone B does not "+
			"own — failed to fire; got panels=%+v", svc.Panels())
	}
	master := findMasterPanel(t, svc)
	if !master.Available {
		t.Fatalf("the master panel went unavailable because a single zone's output failed; "+
			"got master=%+v", master)
	}
}

func panelAvailable(t *testing.T, svc *Service, zoneID string) bool {
	t.Helper()
	for _, p := range svc.Panels() {
		if p.ZoneID == zoneID {
			return p.Available
		}
	}
	t.Fatalf("no panel for zone %q; got %+v", zoneID, svc.Panels())
	return false
}

func findMasterPanel(t *testing.T, svc *Service) alarmpanel.Panel {
	t.Helper()
	for _, p := range svc.Panels() {
		if p.Master {
			return p
		}
	}
	t.Fatalf("no master panel found; got %+v", svc.Panels())
	return alarmpanel.Panel{}
}
