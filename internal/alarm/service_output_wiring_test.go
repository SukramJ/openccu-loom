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
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// outputWiringStart keeps the fake clock past the engine's
// clock-plausibility epoch, as the other alarm harnesses do.
var outputWiringStart = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

// newOutputWiringService builds a real alarm.Service on a fresh
// temp-file SQLite database over an empty central registry, seeds one
// armable zone, and returns the started service.
//
// Everything below goes through NewService and the engine's public
// verbs. Constructing the output manager and handing it a sink of the
// test's own would prove the collaboration can happen; only the real
// composition root proves the daemon makes it happen.
func newOutputWiringService(t *testing.T, zoneID, zoneName string, rows ...sqlitestore.AlarmOutputRow) *Service {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-output-wiring.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stores := NewStores(db)
	ctx := context.Background()
	cfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := outputWiringStart.UnixMilli()
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: zoneID, Name: zoneName, Slug: zoneID, ConfigJSON: string(cfg),
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	for i := range rows {
		if err := stores.Outputs.Upsert(ctx, rows[i]); err != nil {
			t.Fatalf("seed output %s: %v", rows[i].ID, err)
		}
	}

	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: central.NewRegistry(),
		Stores:   stores,
		Clock:    clock.NewFake(outputWiringStart),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("service start: %v", err)
	}
	// Stop takes the engine lock. A defect that leaves the lock held
	// would turn every failure in this file into a hung package run
	// instead of a named one, so the teardown is bounded too.
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
	return svc
}

// runWithin runs fn on its own goroutine and fails the test when it has
// not returned within d. A blocked engine verb would otherwise hang the
// whole package run instead of naming the defect.
func runWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", what, d)
	}
}

// TestNotificationOutputReachesTheBusWithoutBlockingTheEngine pins the
// notification fan-out against the OutputPort contract: the driver's
// sink runs with the engine lock held, so it must resolve everything it
// publishes from what the cycle carries and never call an engine verb.
//
// A sink that reads the zone name or the incident sources back off the
// engine self-deadlocks on the first notification output an operator
// enrols: the trigger path holds the lock, the sink asks for it again,
// and the engine never returns — no state event, no further sensor
// handling, and every later verb (Disarm and Silence included) blocks
// forever with the siren already sounding.
//
// The Disarm at the end is the second half of the assertion: it proves
// the lock was actually released rather than merely that the event was
// published before the block.
func TestNotificationOutputReachesTheBusWithoutBlockingTheEngine(t *testing.T) {
	t.Parallel()

	const zoneID = "eg"
	svc := newOutputWiringService(t, zoneID, "Erdgeschoss", sqlitestore.AlarmOutputRow{
		ID: "notify1", ZoneID: zoneID, Name: "Messenger",
		Class: hmenum.AlarmOutputClassNotification,
	})

	got := make(chan hmevent.AlarmNotificationEvent, 4)
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.AlarmNotificationEvent) {
		select {
		case got <- e:
		default:
		}
	})
	t.Cleanup(unsub)

	ctx := context.Background()
	runWithin(t, 5*time.Second, "PanicTrigger", func() {
		if err := svc.Engine().PanicTrigger(ctx, zoneID, false, "tester", "test"); err != nil {
			t.Errorf("PanicTrigger: %v", err)
		}
	})

	select {
	case e := <-got:
		if e.OutputID != "notify1" {
			t.Fatalf("notification output id = %q, want notify1", e.OutputID)
		}
		if e.ZoneName != "Erdgeschoss" {
			t.Fatalf("notification zone name = %q, want Erdgeschoss — the cycle must carry the zone "+
				"identity, because the sink cannot ask the engine for it", e.ZoneName)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no AlarmNotificationEvent reached the alarm bus")
	}

	runWithin(t, 5*time.Second, "Disarm", func() {
		if err := svc.Engine().Disarm(ctx, zoneID, "tester", "test"); err != nil {
			t.Errorf("Disarm: %v", err)
		}
	})
}

// TestFailedOutputCommandPublishesAlarmHealthOnTheBus pins the health
// fan-out of the output driver through the real composition root.
//
// The service builds its health callback to fan out twice — to the
// daemon health tracker and onto the alarm bus — because the alarm bus
// is what the MQTT panels, the WS/SPA health surface, the webhook and
// the Security domain read. The output manager is the only component
// that produces driver health transitions, so handing it the raw inner
// callback instead leaves every live surface reporting a healthy alarm
// system while a siren failed to fire or refused to stop.
func TestFailedOutputCommandPublishesAlarmHealthOnTheBus(t *testing.T) {
	t.Parallel()

	const zoneID = "og"
	// The central is not registered, so the resolver cannot find the
	// siren's channel and the fire command fails — the driver-level
	// failure this test needs, without a device fake.
	svc := newOutputWiringService(t, zoneID, "Obergeschoss", sqlitestore.AlarmOutputRow{
		ID: "siren1", ZoneID: zoneID, Name: "Sirene",
		Class: hmenum.AlarmOutputClassAcousticSiren,
		// The health event has to reach the bus even before the
		// composition root wires an inner tracker.
		CentralName: "ccu-missing", ChannelAddress: "0001D3C99ABCDE:3",
	})

	got := make(chan hmevent.AlarmHealthChangedEvent, 4)
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.AlarmHealthChangedEvent) {
		select {
		case got <- e:
		default:
		}
	})
	t.Cleanup(unsub)

	if err := svc.Engine().PanicTrigger(context.Background(), zoneID, false, "tester", "test"); err != nil {
		t.Fatalf("PanicTrigger: %v", err)
	}

	select {
	case e := <-got:
		if e.Healthy {
			t.Fatalf("health event reports healthy=%v, want false after a failed output command", e.Healthy)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no AlarmHealthChangedEvent reached the alarm bus after a failed output command")
	}
}
