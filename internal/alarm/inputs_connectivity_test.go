// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// connectivityTestStart keeps the fake clock past the engine's
// clock-plausibility epoch, as the other alarm harnesses do.
var connectivityTestStart = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

const (
	connectivityTestCentral = "ccu"
	connectivityTestZone    = "eg"
)

// TestOnConnectivityDegradesTheSensorsOfTheLostInterface pins the alarm
// service against the identifier space the connectivity event actually
// speaks.
//
// ConnectivityChangedEvent.InterfaceID already carries the wire id
// ("<central>-BidCos-RF") that observeProbeLatency stamps before the
// reconciler publishes, and every routing key in this package is keyed
// by that same wire id. Re-wrapping it a second time doubled the id
// ("<central>-<central>-BidCos-RF"), so no sensor was ever degraded
// when a radio went away, and the all-interfaces-down comparison never
// became true, so the zone's central-loss policy never ran either. The
// DP-level UNREACH path still worked, which is what kept the gap
// invisible.
//
// The event is published on the real central bus rather than handed to
// the handler, so the subscription is part of what is under test.
func TestOnConnectivityDegradesTheSensorsOfTheLostInterface(t *testing.T) {
	t.Parallel()

	svc, unit := armedConnectivityService(t)

	// One radio goes away. Only the sensors behind it degrade.
	events.Publish(unit.EventBus, hmevent.ConnectivityChangedEvent{
		Base: hmevent.NewBase(), CentralName: connectivityTestCentral,
		InterfaceID: central.WireInterfaceID(connectivityTestCentral, hmenum.InterfaceBidCosRF), Reachable: false,
	})
	waitForJournalEvent(t, svc, "sensor_unavailable_while_armed",
		"a lost interface must degrade the sensors enrolled behind it")

	// The second radio follows: every enrolled interface of the central
	// is now down, which is the central-loss escalation.
	events.Publish(unit.EventBus, hmevent.ConnectivityChangedEvent{
		Base: hmevent.NewBase(), CentralName: connectivityTestCentral,
		InterfaceID: central.WireInterfaceID(connectivityTestCentral, hmenum.InterfaceHmIPRF), Reachable: false,
	})
	waitForJournalEvent(t, svc, "central_lost_while_armed",
		"losing every enrolled interface must run the zone's central-loss policy")
}

// TestVanishedInterfaceEscalatesThroughTheReconciler crosses the seam
// between the producer of the connectivity event and this domain.
//
// The CCU's interface list carries a lost radio by dropping it from the
// answer, and the reconciler is what turns that absence into a
// ConnectivityChangedEvent — carrying the wire id observeProbeLatency
// stamped onto the reachability answer, which this domain uses
// directly. Publishing the event by hand — as the test above does —
// proves this domain reacts, never that a radio which genuinely
// disappears reaches it. The escalation is the reason it matters: an
// armed zone whose interfaces are all gone must run its central-loss
// policy rather than keep trusting the last known values.
func TestVanishedInterfaceEscalatesThroughTheReconciler(t *testing.T) {
	t.Parallel()

	svc, unit := armedConnectivityService(t)

	var probed []coordinators.InterfaceReachability
	reconciler := &coordinators.Reconciler{
		CentralName:  connectivityTestCentral,
		Bus:          unit.EventBus,
		Connectivity: hub.NewConnectivity(),
		Connect: coordinators.ProbeFunc(func(_ context.Context) ([]coordinators.InterfaceReachability, error) {
			return probed, nil
		}),
	}
	// VirtualDevices carries no enrolled sensor and never leaves the
	// answer: an empty answer is the CCU-is-away case the reconciler
	// deliberately reads as "no information". observeProbeLatency
	// stamps every InterfaceReachability with the wire id before the
	// reconciler ever publishes, so this helper mirrors that.
	serves := func(ids ...string) []coordinators.InterfaceReachability {
		out := make([]coordinators.InterfaceReachability, 0, len(ids)+1)
		for _, id := range ids {
			out = append(out, coordinators.InterfaceReachability{
				InterfaceID: central.WireInterfaceID(connectivityTestCentral, hmenum.Interface(id)), Reachable: true,
			})
		}
		return append(out, coordinators.InterfaceReachability{
			InterfaceID: central.WireInterfaceID(connectivityTestCentral, hmenum.InterfaceVirtualDevices), Reachable: true,
		})
	}
	reconcile := func(ids ...string) {
		t.Helper()
		probed = serves(ids...)
		if err := reconciler.Reconcile(context.Background()); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}

	reconcile(string(hmenum.InterfaceBidCosRF), string(hmenum.InterfaceHmIPRF))
	reconcile(string(hmenum.InterfaceHmIPRF))
	waitForJournalEvent(t, svc, "sensor_unavailable_while_armed",
		"an interface that drops out of the CCU's interface list must degrade its sensors")

	reconcile()
	waitForJournalEvent(t, svc, "central_lost_while_armed",
		"losing every enrolled interface from the list must run the zone's central-loss policy")
}

// TestPartialInterfaceRecoveryKeepsTheStillDownRadioDegraded pins what
// a half-recovered central reports.
//
// The all-interfaces-down boundary is the only edge the zone's
// central-loss policy runs on, and the engine's central-scoped restore
// marks every sensor of the central available again — the whole
// central, not the interface that actually came back. So when one of
// two lost radios returns, the sensors behind the other one flip to
// available while that radio is still gone, and nothing corrects them:
// the reconciler publishes only on drift, and a radio that stayed away
// has not drifted. The zone then reports ready to arm, raises no
// unreachable blocker and journals no fault, for as long as the daemon
// runs.
func TestPartialInterfaceRecoveryKeepsTheStillDownRadioDegraded(t *testing.T) {
	t.Parallel()

	svc, unit := armedConnectivityService(t)
	report := func(iface hmenum.Interface, reachable bool) {
		events.Publish(unit.EventBus, hmevent.ConnectivityChangedEvent{
			Base: hmevent.NewBase(), CentralName: connectivityTestCentral,
			InterfaceID: central.WireInterfaceID(connectivityTestCentral, iface), Reachable: reachable,
		})
	}

	report(hmenum.InterfaceHmIPRF, false)
	report(hmenum.InterfaceBidCosRF, false)
	waitForJournalEvent(t, svc, "central_lost_while_armed",
		"losing every enrolled interface must run the zone's central-loss policy")

	// Only the BidCos radio comes back; the HmIP one stays away.
	report(hmenum.InterfaceBidCosRF, true)

	waitForBlockers(t, svc, connectivityTestZone, hmenum.AlarmModeFull, []string{"door"},
		"a sensor on a radio that never came back must keep blocking the arm")
}

// TestNotReadyDemotionKeepsTheZoneOutOfTheCentralLossPolicy pins the
// boundary between a CCU that is away and a radio that is gone.
//
// While a central is not operational its reconcile pass cannot probe
// anything, so the not-ready emitter demotes every interface it last
// saw reachable — that keeps the retained MQTT/REST view honest during a
// CCU outage. Those events say nothing about the radios themselves,
// though, and feeding them to this domain turns every CCU reboot into
// the zone's central-loss escalation: a fault in each armed zone's
// journal on the default policy, and a sounding siren plus an incident
// on `trigger`.
//
// The run goes through the real Reconciler and the real service
// subscription: what is under test is that the escalation does not
// happen, so anything short of the production producer would prove
// nothing.
func TestNotReadyDemotionKeepsTheZoneOutOfTheCentralLossPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy hmenum.AlarmCentralLossPolicy
	}{
		{name: "alert", policy: hmenum.AlarmCentralLossAlert},
		{name: "trigger", policy: hmenum.AlarmCentralLossTrigger},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, unit := armedConnectivityServiceWithLoss(t, tc.policy)

			conn := hub.NewConnectivity()
			for _, iface := range []hmenum.Interface{hmenum.InterfaceHmIPRF, hmenum.InterfaceBidCosRF} {
				conn.OnStateWithInterface(central.WireInterfaceID(connectivityTestCentral, iface), iface, true)
			}
			reconciler := &coordinators.Reconciler{
				CentralName:  connectivityTestCentral,
				Bus:          unit.EventBus,
				Connectivity: conn,
			}

			// The demotion the CCU outage produces, from the production
			// emitter the reconcile job runs while the central is FAILED.
			if err := reconciler.EmitNotReady(context.Background()); err != nil {
				t.Fatalf("emit not ready: %v", err)
			}

			// The diagnostic half must still happen: the aggregate every
			// north-bound surface reads now reports the interfaces as down.
			for _, entry := range conn.List() {
				if entry.Reachable {
					t.Fatalf("interface %s still reachable after the not-ready demotion", entry.InterfaceID)
				}
			}

			expectNoJournalEvent(t, svc, "central_lost_while_armed",
				"a CCU that is merely away must not run the zone's central-loss policy")
			expectNoJournalEvent(t, svc, "sensor_unavailable_while_armed",
				"a CCU that is merely away must not degrade the sensors behind its radios")
			snap, ok := svc.Engine().Zone(connectivityTestZone)
			if !ok {
				t.Fatalf("unknown zone %q", connectivityTestZone)
			}
			if snap.State != hmenum.AlarmZoneStateArmed {
				t.Fatalf("zone state = %q, want %q: a CCU reboot must not trigger the zone",
					snap.State, hmenum.AlarmZoneStateArmed)
			}
			if snap.IncidentID != 0 {
				t.Fatalf("incident %d opened by a CCU reboot", snap.IncidentID)
			}
		})
	}
}

// expectNoJournalEvent fails when event shows up in the journal within a
// short settle window. Bus dispatch and the journal write are both
// asynchronous with respect to the publisher, so the absence has to be
// given time to become present before it counts.
func expectNoJournalEvent(t *testing.T, svc *Service, event, why string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		entries, err := svc.Stores().Journal.Query(context.Background(),
			sqlitestore.AlarmJournalFilter{IncludeHidden: true})
		if err != nil {
			t.Fatalf("query journal: %v", err)
		}
		for i := range entries {
			if entries[i].Event == event {
				t.Fatalf("journal entry %q written: %s", event, why)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForBlockers polls the zone's readiness until its blockers for
// mode equal want, or fails with why. The engine is fed from bus
// handlers, so the verdict cannot be read once and be done.
func waitForBlockers(t *testing.T, svc *Service, zoneID string, mode hmenum.AlarmMode, want []string, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		snap, ok := svc.Engine().Zone(zoneID)
		if !ok {
			t.Fatalf("unknown zone %q", zoneID)
		}
		got = snap.Readiness[mode].Blockers
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("readiness blockers = %v, want %v: %s", got, want, why)
}

// armedConnectivityService brings up an alarm service with two sensors
// on two radios of one central, arms the zone, and returns the service
// plus the central whose bus the connectivity events ride.
func armedConnectivityService(t *testing.T) (*Service, *central.Unit) {
	t.Helper()
	return armedConnectivityServiceWithLoss(t, hmenum.AlarmCentralLossAlert)
}

// armedConnectivityServiceWithLoss is [armedConnectivityService] with an
// explicit central-loss policy: the escalation the policy selects is the
// observable the connectivity tests assert on.
func armedConnectivityServiceWithLoss(t *testing.T, loss hmenum.AlarmCentralLossPolicy) (*Service, *central.Unit) {
	t.Helper()

	const (
		centralName = connectivityTestCentral
		zoneID      = connectivityTestZone
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
		Modes:       map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
		CentralLoss: loss,
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
	return svc, unit
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
