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

// connectivityTestStart keeps the fake clock past the engine's
// clock-plausibility epoch, as the other alarm harnesses do.
var connectivityTestStart = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

// TestOnConnectivityDegradesTheSensorsOfTheLostInterface pins the alarm
// service against the identifier space the connectivity event actually
// speaks.
//
// The probe reports the CCU's own interface name ("BidCos-RF") while
// every routing key in this package is keyed by the wire id an ingested
// data point carries ("<central>-BidCos-RF"). Matching one against the
// other found nothing: no sensor was ever degraded when a radio went
// away, and the all-interfaces-down comparison never became true, so
// the zone's central-loss policy never ran either. The DP-level UNREACH
// path still worked, which is what kept the gap invisible.
//
// The event is published on the real central bus rather than handed to
// the handler, so the subscription is part of what is under test.
func TestOnConnectivityDegradesTheSensorsOfTheLostInterface(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu"
		zoneID      = "eg"
	)

	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-connectivity.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}

	ctx := context.Background()
	stores := NewStores(db)
	zoneCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := connectivityTestStart.UnixMilli()
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: zoneID, Name: "Erdgeschoss", Slug: zoneID, ConfigJSON: string(zoneCfg),
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	sensorCfg, err := json.Marshal(engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	// Enrolled the way the enrollment surface builds a row: the wire id
	// off the data point's key, never the CCU's bare interface name.
	for _, s := range []struct {
		id, iface, channel string
	}{
		{"door", central.WireInterfaceID(centralName, hmenum.InterfaceHmIPRF), "0001D3C99ABCDE:1"},
		{"gate", central.WireInterfaceID(centralName, hmenum.InterfaceBidCosRF), "LEQ0123456:1"},
	} {
		if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
			ID: s.id, ZoneID: zoneID, CentralName: centralName, InterfaceID: s.iface,
			ChannelAddress: s.channel, Parameter: string(hmenum.ParameterState),
			SensorType: hmenum.AlarmSensorTypeWindow, Name: s.id, ConfigJSON: string(sensorCfg),
			CreatedAtMS: now, UpdatedAtMS: now,
		}); err != nil {
			t.Fatalf("seed sensor %s: %v", s.id, err)
		}
	}

	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: reg,
		Stores:   stores,
		Clock:    clock.NewFake(connectivityTestStart),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("service start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	if _, err := svc.Engine().Arm(ctx, zoneID, engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	// One radio goes away. Only the sensors behind it degrade.
	events.Publish(unit.EventBus, hmevent.ConnectivityChangedEvent{
		Base: hmevent.NewBase(), CentralName: centralName,
		InterfaceID: string(hmenum.InterfaceBidCosRF), Reachable: false,
	})
	waitForJournalEvent(t, svc, "sensor_unavailable_while_armed",
		"a lost interface must degrade the sensors enrolled behind it")

	// The second radio follows: every enrolled interface of the central
	// is now down, which is the central-loss escalation.
	events.Publish(unit.EventBus, hmevent.ConnectivityChangedEvent{
		Base: hmevent.NewBase(), CentralName: centralName,
		InterfaceID: string(hmenum.InterfaceHmIPRF), Reachable: false,
	})
	waitForJournalEvent(t, svc, "central_lost_while_armed",
		"losing every enrolled interface must run the zone's central-loss policy")
}

// waitForJournalEvent polls the alarm journal until event appears, or
// fails with why. Bus dispatch is asynchronous, so the assertion cannot
// read the journal once and be done.
func waitForJournalEvent(t *testing.T, svc *Service, event, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		entries, err := svc.Stores().Journal.Query(context.Background(),
			sqlitestore.AlarmJournalFilter{IncludeHidden: true})
		if err != nil {
			t.Fatalf("query journal: %v", err)
		}
		seen = seen[:0]
		for i := range entries {
			if entries[i].Event == event {
				return
			}
			seen = append(seen, entries[i].Event)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %q journal entry: %s; got %v", event, why, seen)
}
