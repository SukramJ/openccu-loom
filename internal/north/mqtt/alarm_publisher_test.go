// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// alarmPublisherFixtureStart is the fixture wall-clock origin, kept
// after the engine's clock-plausibility epoch (see
// internal/north/rest/handlers/alarm_fixture_test.go's identical
// convention) so persisted state behaves the way it would in
// production.
var alarmPublisherFixtureStart = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

// alarmPublisherFixtureCentral is the fixed central name fixture
// sensors resolve under. No central is ever registered, so sensor
// reads always come back "unknown" — irrelevant here, the tests drive
// state through direct engine calls.
const alarmPublisherFixtureCentral = "c1"

// alarmPublisherFixture wires a real [alarm.Service] (engine + output
// manager + bus) over a migrated temporary SQLite database — the same
// component shape production assembles, minus any registered central
// — plus a real [Bridge]/[Wiring] pair over a [mockPublisher] so
// publishes can be asserted directly. It is deliberately not a fake:
// [AlarmMQTTPublisher] depends on the concrete *alarm.Service and
// *Bridge types, so exercising the real engine's state machine is both
// the simplest and the most faithful way to drive it.
type alarmPublisherFixture struct {
	t    *testing.T
	svc  *alarm.Service
	eng  *engine.Engine
	pub  *AlarmMQTTPublisher
	mp   *mockPublisher
	base string
}

// newAlarmPublisherFixture builds and starts an empty alarm service (no
// zones) plus a bound [AlarmMQTTPublisher] that has NOT been started
// yet — tests seed zones first, then call start() so the publisher's
// initial reconcile observes the pre-seeded zones, mirroring
// [AlarmMQTTPublisher.Start]'s documented "catch zones that already
// exist" behavior.
func newAlarmPublisherFixture(t *testing.T) *alarmPublisherFixture {
	t.Helper()
	svc := newAlarmFixtureService(t)
	bridge, mp := newTestBridge(t)
	wiring := NewWiring(bridge, slog.Default())
	pub := NewAlarmMQTTPublisher(svc, wiring, slog.Default())

	return &alarmPublisherFixture{t: t, svc: svc, eng: svc.Engine(), pub: pub, mp: mp, base: "openccu-loom"}
}

// newAlarmFixtureService builds and starts a real [alarm.Service]
// (engine + output manager + bus) over a migrated temporary SQLite
// database — the same component shape production assembles, minus any
// registered central.
func newAlarmFixtureService(t *testing.T) *alarm.Service {
	t.Helper()
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm.db")))
	if err != nil {
		t.Fatalf("open alarm fixture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{Enabled: true},
		Registry: central.NewRegistry(),
		Stores:   alarm.NewStores(db),
		Clock:    clock.NewFake(alarmPublisherFixtureStart),
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("alarm.NewService: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("svc.Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	return svc
}

// seedZone persists an zone row (full-mode, zero delays so Arm
// completes synchronously) and reloads the service.
func (f *alarmPublisherFixture) seedZone(id, name string, cfg engine.ZoneConfig) {
	f.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal zone config: %v", err)
	}
	now := time.Now().UnixMilli()
	stores := f.svc.Stores()
	if err := stores.Zones.Upsert(context.Background(), sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed zone %s: %v", id, err)
	}
	if err := f.svc.Reload(context.Background()); err != nil {
		f.t.Fatalf("reload after seeding zone %s: %v", id, err)
	}
}

// seedSensor persists an instant (no entry delay) sensor under zoneID
// and reloads.
func (f *alarmPublisherFixture) seedSensor(id, zoneID, name string, modes []hmenum.AlarmMode) {
	f.t.Helper()
	cfg := engine.SensorConfig{Modes: modes}
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal sensor config: %v", err)
	}
	now := time.Now().UnixMilli()
	stores := f.svc.Stores()
	if err := stores.Sensors.Upsert(context.Background(), sqlitestore.AlarmSensorRow{
		ID: id, ZoneID: zoneID, CentralName: alarmPublisherFixtureCentral, InterfaceID: "HmIP-RF",
		ChannelAddress: id + ":1", Parameter: "STATE", SensorType: hmenum.AlarmSensorTypeDoor,
		Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed sensor %s: %v", id, err)
	}
	if err := f.svc.Reload(context.Background()); err != nil {
		f.t.Fatalf("reload after seeding sensor %s: %v", id, err)
	}
}

// seedPINCode persists an enabled or disabled pin-kind alarm-code row
// directly through the store — there is no facade "create" path in
// this package, so this mirrors the shape
// internal/alarm/codes/codes_test.go's pinRow helper produces. Every
// verb permission is granted: the code-policy tests care only about
// whether an applicable pin code exists, never which verb it
// authorizes. A nil zones list applies to every zone, matching the
// store's own "[]" catch-all convention (internal/alarm/codes/facade.go's
// parseZones).
func (f *alarmPublisherFixture) seedPINCode(id, name, pin string, enabled bool, zones []string) {
	f.t.Helper()
	hash, err := codes.HashPIN(pin)
	if err != nil {
		f.t.Fatalf("HashPIN: %v", err)
	}
	zonesJSON := "[]"
	if len(zones) > 0 {
		b, err := json.Marshal(zones)
		if err != nil {
			f.t.Fatalf("marshal zones: %v", err)
		}
		zonesJSON = string(b)
	}
	now := time.Now().UnixMilli()
	row := sqlitestore.AlarmCodeRow{
		ID: id, Name: name, Kind: string(codes.KindPIN), Hash: hash,
		PermsJSON: `{"arm":true,"disarm":true,"silence":true}`,
		ZonesJSON: zonesJSON, BindingJSON: "{}",
		Enabled: enabled, CreatedAtMS: now, UpdatedAtMS: now,
	}
	if err := f.svc.Stores().Codes.Upsert(context.Background(), row); err != nil {
		f.t.Fatalf("seed pin code %s: %v", id, err)
	}
}

// removeZone deletes an zone row and reloads.
func (f *alarmPublisherFixture) removeZone(id string) {
	f.t.Helper()
	if err := f.svc.Stores().Zones.Delete(context.Background(), id); err != nil {
		f.t.Fatalf("delete zone %s: %v", id, err)
	}
	if err := f.svc.Reload(context.Background()); err != nil {
		f.t.Fatalf("reload after removing zone %s: %v", id, err)
	}
}

// start starts the publisher and stops it on test cleanup, marking
// every published panel offline.
func (f *alarmPublisherFixture) start() {
	f.t.Helper()
	f.pub.Start()
	f.t.Cleanup(f.pub.Stop)
}

// zeroDelayFullMode is a single-mode zone configuration with no exit
// or entry delay, so Arm/trigger transitions complete synchronously —
// the tests need no fake-clock advancement to observe the resulting
// state.
func zeroDelayFullMode() engine.ZoneConfig {
	return engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {},
		},
	}
}

// --- publication lookup helpers ---

// findPublish returns the most recent recorded publish to topic, or
// (zero, false) when none exists yet.
func (f *alarmPublisherFixture) findPublish(topic string) (publishRecord, bool) {
	f.t.Helper()
	f.mp.mu.Lock()
	defer f.mp.mu.Unlock()
	var found publishRecord
	ok := false
	for _, rec := range f.mp.sent {
		if rec.topic == topic {
			found = rec
			ok = true
		}
	}
	return found, ok
}

// waitForPublish polls until a publish to topic satisfies want, or
// fails the test after a bounded timeout. The publisher's reconcile
// worker runs on its own goroutine, so assertions after a bus event
// cannot rely on synchronous delivery.
func (f *alarmPublisherFixture) waitForPublish(topic string, want func(publishRecord) bool) publishRecord {
	f.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last publishRecord
	var lastOK bool
	for time.Now().Before(deadline) {
		if rec, ok := f.findPublish(topic); ok {
			last, lastOK = rec, true
			if want(rec) {
				return rec
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if lastOK {
		f.t.Fatalf("publish to %s never matched; last seen payload=%q retain=%v", topic, last.payload, last.retain)
	} else {
		f.t.Fatalf("no publish to %s observed within timeout", topic)
	}
	return publishRecord{}
}

// --- tests ---

// TestAlarmMQTTPublisher_RetainedDiscoveryAndStateOnStart covers the
// pre-existing-zone case: an zone configured before the publisher
// starts still gets its retained discovery config, state, and
// availability published once Start runs its catch-up reconcile.
func TestAlarmMQTTPublisher_RetainedDiscoveryAndStateOnStart(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	discTopic := "homeassistant/alarm_control_panel/alarm/eg/config"
	rec := f.waitForPublish(discTopic, func(publishRecord) bool { return true })
	if !rec.retain {
		t.Errorf("discovery publish for eg must be retained")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rec.payload), &body); err != nil {
		t.Fatalf("unmarshal discovery payload: %v", err)
	}
	if body["name"] != "Erdgeschoss" {
		t.Errorf("discovery name = %v, want Erdgeschoss", body["name"])
	}

	stateTopic := f.base + "/alarm/eg/state"
	stRec := f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })
	if !stRec.retain {
		t.Errorf("state publish for eg must be retained")
	}

	availTopic := f.base + "/alarm/eg/availability"
	avRec := f.waitForPublish(availTopic, func(r publishRecord) bool { return r.payload == "online" })
	if !avRec.retain {
		t.Errorf("availability publish for eg must be retained")
	}
}

// TestAlarmMQTTPublisher_StateTokenUpdatesOnStateChanged drives a real
// arm transition through the engine and confirms the retained state
// topic republishes with the new HA token.
func TestAlarmMQTTPublisher_StateTokenUpdatesOnStateChanged(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	stateTopic := f.base + "/alarm/eg/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	if _, err := f.eng.Arm(context.Background(), "eg", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("Arm: %v", err)
	}

	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateArmedAway })
}

// TestAlarmMQTTPublisher_AvailabilityFlipsOnHealthChanged publishes an
// AlarmHealthChangedEvent directly on the service bus (the same event
// [alarm.Service] emits on a real health transition) and confirms every
// known panel's availability topic flips offline, then back online.
func TestAlarmMQTTPublisher_AvailabilityFlipsOnHealthChanged(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	availTopic := f.base + "/alarm/eg/availability"
	f.waitForPublish(availTopic, func(r publishRecord) bool { return r.payload == "online" })

	events.Publish(f.svc.Bus(), hmevent.AlarmHealthChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()), Healthy: false, Note: "test degradation",
	})
	f.waitForPublish(availTopic, func(r publishRecord) bool { return r.payload == "offline" })

	events.Publish(f.svc.Bus(), hmevent.AlarmHealthChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()), Healthy: true, Note: "",
	})
	f.waitForPublish(availTopic, func(r publishRecord) bool { return r.payload == "online" })
}

// TestAlarmMQTTPublisher_RetractsRemovedZone covers zone deletion: once
// an zone drops out of the engine's snapshot, the publisher must clear
// (empty, retained) its discovery/state/availability topics instead of
// leaving stale retained messages on the broker.
//
// The zone under test is armed before removal rather than left
// disarmed: engine.Reload (docs: "removed zones are disarmed ...
// before they vanish") forces an armed zone through a disarm
// transition first, and that transition is what actually publishes an
// AlarmStateChangedEvent the publisher's reconcile worker reacts to.
// Removing an already-disarmed zone publishes no bus event at all —
// [Engine.refreshReadiness] only republishes when a surviving zone's
// own readiness verdict changes, which an unrelated zone's removal
// never does — so a reconcile is not guaranteed to run promptly for
// that case. That gap sits in the reconcile-trigger wiring, not in the
// diff/retract logic this test exercises.
func TestAlarmMQTTPublisher_RetractsRemovedZone(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.seedZone("og", "Obergeschoss", zeroDelayFullMode())
	f.start()

	discTopic := "homeassistant/alarm_control_panel/alarm/og/config"
	stateTopic := f.base + "/alarm/og/state"
	availTopic := f.base + "/alarm/og/availability"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	if _, err := f.eng.Arm(context.Background(), "og", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("Arm og: %v", err)
	}
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateArmedAway })

	f.removeZone("og")

	f.waitForPublish(discTopic, func(r publishRecord) bool { return r.payload == "" })
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == "" })
	f.waitForPublish(availTopic, func(r publishRecord) bool { return r.payload == "" })
}

// TestAlarmMQTTPublisher_RetractsDeletedDisarmedZone pins the
// lifecycle fix from the slice review: deleting a DISARMED zone (the
// only deletion the management API permits) fires no state transition
// — only the panel entity event. Without the publisher's panel-event
// subscription the retained discovery, state, and availability of the
// deleted zone (and a stale master panel) would ghost in the broker
// forever.
func TestAlarmMQTTPublisher_RetractsDeletedDisarmedZone(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.seedZone("og", "Obergeschoss", zeroDelayFullMode())
	f.start()

	stateTopic := f.base + "/alarm/og/state"
	masterState := f.base + "/alarm/master/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })
	f.waitForPublish(masterState, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	// Delete while disarmed — no arm, no state event.
	f.removeZone("og")

	f.waitForPublish("homeassistant/alarm_control_panel/alarm/og/config", func(r publishRecord) bool { return r.payload == "" })
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == "" })
	f.waitForPublish(f.base+"/alarm/og/availability", func(r publishRecord) bool { return r.payload == "" })
	// The master panel retracts with the zone count back below two.
	f.waitForPublish("homeassistant/alarm_control_panel/alarm/master/config", func(r publishRecord) bool { return r.payload == "" })
}

// TestAlarmMQTTPublisher_BrokerConnectReseeds pins the reconnect fix:
// a broker restart wipes the retained store; OnBrokerConnect must
// republish discovery, state, and availability for every panel even
// when no alarm event ever fires again.
func TestAlarmMQTTPublisher_BrokerConnectReseeds(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()
	stateTopic := f.base + "/alarm/eg/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	f.mp.mu.Lock()
	before := len(f.mp.sent)
	f.mp.mu.Unlock()
	f.pub.OnBrokerConnect()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mp.mu.Lock()
		after := len(f.mp.sent)
		f.mp.mu.Unlock()
		if after > before {
			// The re-seed republished the retained plane.
			f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("OnBrokerConnect produced no republish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAlarmMQTTPublisher_DiscoveryReflipsOnCodesChanged pins the
// review-fix regression: an zone seeded with the RequireDisarm
// default (nil, "required once a code exists") and no pin code yet
// must advertise code_disarm_required=false, and adding an enabled
// pin code must flip that flag to true once the alarm service
// notifies AlarmCodesChangedEvent — without any other alarm activity
// (arm/disarm/trigger) ever occurring. Asserting on a publish-count
// increase before re-checking the topic mirrors
// TestAlarmMQTTPublisher_BrokerConnectReseeds: the worker goroutine
// republishes asynchronously, so a fixed record index would flake.
func TestAlarmMQTTPublisher_DiscoveryReflipsOnCodesChanged(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	discTopic := "homeassistant/alarm_control_panel/alarm/eg/config"
	f.waitForPublish(discTopic, func(r publishRecord) bool {
		var body map[string]any
		if err := json.Unmarshal([]byte(r.payload), &body); err != nil {
			return false
		}
		return body["code_disarm_required"] == false
	})

	f.mp.mu.Lock()
	before := len(f.mp.sent)
	f.mp.mu.Unlock()

	f.seedPINCode("c1", "Markus", "1234", true, nil)
	f.svc.NotifyCodesChanged()

	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mp.mu.Lock()
		after := len(f.mp.sent)
		f.mp.mu.Unlock()
		if after > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("NotifyCodesChanged produced no republish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.waitForPublish(discTopic, func(r publishRecord) bool {
		var body map[string]any
		if err := json.Unmarshal([]byte(r.payload), &body); err != nil {
			return false
		}
		return body["code_disarm_required"] == true && body["code"] == alarmRemoteCode
	})
}

// TestAlarmMQTTPublisher_MasterAggregationAcrossTwoZones covers the
// aggregate master panel: it appears only once a second zone exists,
// tracks the union of both zones' state via [Masteralarmpanel.StateToken],
// and disappears again once the zone count drops back below two.
func TestAlarmMQTTPublisher_MasterAggregationAcrossTwoZones(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	masterState := f.base + "/alarm/master/state"
	masterDisc := "homeassistant/alarm_control_panel/alarm/master/config"

	// A single zone never gets a master panel.
	f.waitForPublish(f.base+"/alarm/eg/state", func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })
	time.Sleep(20 * time.Millisecond)
	if _, ok := f.findPublish(masterState); ok {
		t.Fatalf("master panel published with only one zone configured")
	}

	f.seedZone("og", "Obergeschoss", zeroDelayFullMode())
	// Both zones disarmed -> the uniform token collapses to disarmed.
	f.waitForPublish(masterState, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })
	discRec := f.waitForPublish(masterDisc, func(r publishRecord) bool { return r.retain })
	var discBody map[string]any
	if err := json.Unmarshal([]byte(discRec.payload), &discBody); err != nil {
		t.Fatalf("unmarshal master discovery: %v", err)
	}
	if discBody["name"] != "Alarm system" {
		t.Errorf("master discovery name = %v, want localized fallback %q", discBody["name"], "Alarm system")
	}

	// Arm one zone only -> mixed set -> away (notes/concepts/alarm-concept.md §13.3).
	if _, err := f.eng.Arm(context.Background(), "eg", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("Arm eg: %v", err)
	}
	f.waitForPublish(masterState, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateArmedAway })

	// Arm the second zone the same way -> uniform set -> exact token.
	if _, err := f.eng.Arm(context.Background(), "og", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("Arm og: %v", err)
	}
	f.waitForPublish(masterState, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateArmedAway })

	// Dropping back to one zone retracts the master panel.
	f.removeZone("og")
	f.waitForPublish(masterState, func(r publishRecord) bool { return r.payload == "" })
	f.waitForPublish(masterDisc, func(r publishRecord) bool { return r.payload == "" })
}

// TestAlarmMQTTPublisher_EventTopicJSONOnTriggered drives a real
// instant sensor activation against an armed zone and asserts the
// non-retained event-topic JSON body for the resulting TRIGGER event.
func TestAlarmMQTTPublisher_EventTopicJSONOnTriggered(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.seedSensor("door1", "eg", "Front Door", []hmenum.AlarmMode{hmenum.AlarmModeFull})
	f.start()

	stateTopic := f.base + "/alarm/eg/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	if _, err := f.eng.Arm(context.Background(), "eg", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateArmedAway })

	f.eng.HandleSensorEvent(context.Background(), "door1", true)
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateTriggered })

	eventTopic := f.base + "/alarm/eg/event"
	rec := f.waitForPublish(eventTopic, func(r publishRecord) bool {
		var pay alarmEventPayload
		if err := json.Unmarshal([]byte(r.payload), &pay); err != nil {
			return false
		}
		return pay.Type == alarmEventTypeTrigger
	})
	if rec.retain {
		t.Errorf("event-topic publish must not be retained")
	}
	var pay alarmEventPayload
	if err := json.Unmarshal([]byte(rec.payload), &pay); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if pay.ZoneID != "eg" {
		t.Errorf("event zone_id = %q, want eg", pay.ZoneID)
	}
	if pay.ZoneName != "Erdgeschoss" {
		t.Errorf("event zone_name = %q, want Erdgeschoss", pay.ZoneName)
	}
	if pay.Mode != string(hmenum.AlarmModeFull) {
		t.Errorf("event mode = %q, want full", pay.Mode)
	}
	if len(pay.OpenSensors) != 1 || pay.OpenSensors[0] != "Front Door" {
		t.Errorf("event open_sensors = %v, want [Front Door]", pay.OpenSensors)
	}
}

// TestAlarmMQTTPublisher_NotificationRespectsMQTTFlag covers
// onNotification: an AlarmNotificationEvent with MQTT=false must never
// enqueue an event-topic publish, while the identical event with
// MQTT=true enqueues a NOTIFICATION body carrying the output's name.
func TestAlarmMQTTPublisher_NotificationRespectsMQTTFlag(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	stateTopic := f.base + "/alarm/eg/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	eventTopic := f.base + "/alarm/eg/event"
	notification := func(mqtt bool) hmevent.AlarmNotificationEvent {
		return hmevent.AlarmNotificationEvent{
			Base: hmevent.NewBaseAt(time.Now()), ZoneID: "eg", ZoneName: "Erdgeschoss",
			OutputID: "notify1", OutputName: "Doorbell", IncidentID: 1, Mode: hmenum.AlarmModeFull,
			MQTT: mqtt, Webhook: true,
		}
	}

	// Both events go onto the bus in order, and the bus delivers serially:
	// once the MQTT=true one has reached the event topic, the MQTT=false one
	// has had its turn too. Counting publishes on the event topic — rather
	// than every publish inside a fixed sleep window — keeps the assertion
	// immune to whatever else the start-up sequence is still flushing, which
	// is what made this test fail on a loaded CI runner.
	events.Publish(f.svc.Bus(), notification(false))
	events.Publish(f.svc.Bus(), notification(true))

	rec := f.waitForPublish(eventTopic, func(r publishRecord) bool {
		var pay alarmEventPayload
		if err := json.Unmarshal([]byte(r.payload), &pay); err != nil {
			return false
		}
		return pay.Type == alarmEventTypeNotification
	})
	if rec.retain {
		t.Errorf("notification event-topic publish must not be retained")
	}
	f.mp.mu.Lock()
	onEventTopic := 0
	for _, r := range f.mp.sent {
		if r.topic == eventTopic {
			onEventTopic++
		}
	}
	f.mp.mu.Unlock()
	if onEventTopic != 1 {
		t.Fatalf("%d publishes on the event topic, want exactly 1 — the MQTT=false notification must "+
			"publish nothing", onEventTopic)
	}
	var pay alarmEventPayload
	if err := json.Unmarshal([]byte(rec.payload), &pay); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if pay.Output != "Doorbell" {
		t.Errorf("event output = %q, want Doorbell", pay.Output)
	}
	if pay.Mode != string(hmenum.AlarmModeFull) {
		t.Errorf("event mode = %q, want full", pay.Mode)
	}
}

// TestAlarmMQTTPublisher_NotificationOutputFallsBackToID covers the
// unnamed-output case: an enrolled notification output with no display
// name publishes its ID in the event body's output field instead.
func TestAlarmMQTTPublisher_NotificationOutputFallsBackToID(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("eg", "Erdgeschoss", zeroDelayFullMode())
	f.start()

	stateTopic := f.base + "/alarm/eg/state"
	f.waitForPublish(stateTopic, func(r publishRecord) bool { return r.payload == alarmpanel.HAAlarmStateDisarmed })

	events.Publish(f.svc.Bus(), hmevent.AlarmNotificationEvent{
		Base: hmevent.NewBaseAt(time.Now()), ZoneID: "eg", ZoneName: "Erdgeschoss",
		OutputID: "notify2", OutputName: "", IncidentID: 2, Mode: hmenum.AlarmModeFull,
		MQTT: true, Webhook: false,
	})

	eventTopic := f.base + "/alarm/eg/event"
	rec := f.waitForPublish(eventTopic, func(r publishRecord) bool {
		var pay alarmEventPayload
		if err := json.Unmarshal([]byte(r.payload), &pay); err != nil {
			return false
		}
		return pay.Type == alarmEventTypeNotification
	})
	var pay alarmEventPayload
	if err := json.Unmarshal([]byte(rec.payload), &pay); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	if pay.Output != "notify2" {
		t.Errorf("event output = %q, want notify2 (ID fallback for an unnamed output)", pay.Output)
	}
}

// TestAlarmPlaneDeclaresSoItsOrphanedConfigsAreSwept asserts the effect,
// not the call: a zone deleted while the daemon was down leaves a
// retained discovery config that only the orphan sweep can reach —
// `retractPanel` cannot, because `knownZones` starts empty on every
// boot.
//
// The sweep skips a daemon-level plane until that plane declares, and
// the alarm plane never did. Its entry in `daemonLevelNodeIDs` was
// therefore dead: every stale `homeassistant/*/alarm/*/config` survived
// forever and Home Assistant re-created a permanently unavailable panel
// on every restart.
func TestAlarmPlaneDeclaresSoItsOrphanedConfigsAreSwept(t *testing.T) {
	t.Parallel()
	const (
		staleTopic = "homeassistant/alarm_control_panel/alarm/z-42/config"
		liveTopic  = "homeassistant/alarm_control_panel/alarm/eg/config"
	)
	svc := newAlarmFixtureService(t)

	// The broker still holds the config of a zone this build no longer
	// knows, plus the one the live zone is about to re-declare.
	mc := &mockRetainClient{retained: []retainedMsg{
		{topic: staleTopic, payload: []byte(`{"name":"Gone"}`)},
		{topic: liveTopic, payload: []byte(`{"name":"Erdgeschoss"}`)},
	}}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, mc).WithSubscriber(mc)
	pub := NewAlarmMQTTPublisher(svc, NewWiring(bridge, slog.Default()), slog.Default())

	// Seed the surviving zone, then let the publisher complete a
	// reconcile against the loaded engine.
	b, err := json.Marshal(zeroDelayFullMode())
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := svc.Stores().Zones.Upsert(context.Background(), sqlitestore.AlarmZoneRow{
		ID: "eg", Name: "Erdgeschoss", ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	pub.Start()
	t.Cleanup(pub.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for !bridge.planeDeclared(alarmDiscoveryNodeID) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !bridge.planeDeclared(alarmDiscoveryNodeID) {
		t.Fatal("the alarm plane never declared, so the orphan sweep skips it forever")
	}

	n, err := bridge.RunDiscoveryOrphanCleanupOnce(context.Background(), "", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	evicted := map[string]bool{}
	for _, topic := range mc.evicted() {
		evicted[topic] = true
	}
	if !evicted[staleTopic] {
		t.Errorf("stale alarm config %q survived the sweep (evicted=%d, topics=%v)", staleTopic, n, mc.evicted())
	}
	if evicted[liveTopic] {
		t.Errorf("the sweep evicted the live zone's config %q", liveTopic)
	}
}

// TestAlarmPlaneDoesNotDeclareBeforeTheEngineLoaded pins the gate on the
// declaration: Start signals an eager reconcile, and on that pass the
// engine may not have read its zone set yet. Declaring an empty plane
// would let the sweep classify every live panel as an orphan and wipe
// the alarm surface — the failure the gate exists to prevent.
func TestAlarmPlaneDoesNotDeclareBeforeTheEngineLoaded(t *testing.T) {
	t.Parallel()
	svc := newAlarmFixtureService(t)
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("svc.Stop: %v", err)
	}
	if svc.Engine().ZonesLoaded() {
		t.Fatal("a stopped engine reports its zone set as loaded")
	}

	bridge, _ := newTestBridge(t)
	pub := NewAlarmMQTTPublisher(svc, NewWiring(bridge, slog.Default()), slog.Default())
	pub.Start()
	t.Cleanup(pub.Stop)

	// Give the eager reconcile a chance to run before asserting.
	time.Sleep(50 * time.Millisecond)
	if bridge.planeDeclared(alarmDiscoveryNodeID) {
		t.Fatal("the alarm plane declared before the engine loaded its zones; the sweep would wipe the live plane")
	}
}

// discoveryRefusingPublisher accepts every publish except the retained HA
// discovery configs, which it refuses the way a broker with an open
// circuit breaker or a denying ACL would.
type discoveryRefusingPublisher struct {
	mu    sync.Mutex
	other []string
}

var errDiscoveryRefused = errors.New("broker refused discovery config")

func (p *discoveryRefusingPublisher) Publish(_ context.Context, topic string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	if strings.HasPrefix(topic, "homeassistant/") {
		return errDiscoveryRefused
	}
	p.mu.Lock()
	p.other = append(p.other, topic)
	p.mu.Unlock()
	return nil
}

func (p *discoveryRefusingPublisher) sawStateTopic() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.other {
		if strings.HasSuffix(t, "/alarm/eg/state") {
			return true
		}
	}
	return false
}

// TestAlarmPlaneDoesNotDeclareWhenADiscoveryPublishFailed pins the second
// half of the declaration gate. The bridge remembers only the configs the
// broker accepted, so a refused publish leaves a live panel outside the
// declared set. Declaring the plane anyway arms the orphan sweep against
// that panel's retained config, and the sweep clears it — the alarm entity
// disappears from the consumer until a later event republishes it.
func TestAlarmPlaneDoesNotDeclareWhenADiscoveryPublishFailed(t *testing.T) {
	t.Parallel()
	svc := newAlarmFixtureService(t)

	pubClient := &discoveryRefusingPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, pubClient)

	cfgJSON, err := json.Marshal(zeroDelayFullMode())
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := svc.Stores().Zones.Upsert(context.Background(), sqlitestore.AlarmZoneRow{
		ID: "eg", Name: "Erdgeschoss", ConfigJSON: string(cfgJSON), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	pub := NewAlarmMQTTPublisher(svc, NewWiring(bridge, slog.Default()), slog.Default())
	pub.Start()
	t.Cleanup(pub.Stop)

	// Wait for the reconcile pass to reach its state publish — the step
	// that follows the refused discovery configs — so the assertion below
	// measures the completed pass, not a race with it.
	deadline := time.Now().Add(2 * time.Second)
	for !pubClient.sawStateTopic() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !pubClient.sawStateTopic() {
		t.Fatal("the reconcile pass never published the zone state")
	}
	if bridge.planeDeclared(alarmDiscoveryNodeID) {
		t.Fatal("the alarm plane declared although the broker refused its discovery configs; " +
			"the orphan sweep would clear the live panel")
	}
}

// TestAlarmPlaneGatesOnlyTheTopicDiscoveryDoesNotDeclare pins which half
// of the alarm plane the raw switch may silence.
//
// The zone's discovery payload names its state and availability topics, so
// those must publish whatever `north.mqtt.raw_enabled` says: gated, Home
// Assistant keeps the entity the config created and never receives a value
// for it, which is worse than either extreme. The event topic appears in no
// discovery payload — it is raw-plane traffic and the switch owns it.
func TestAlarmPlaneGatesOnlyTheTopicDiscoveryDoesNotDeclare(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.RawEnabled = false })
	ctx := context.Background()

	if err := b.PublishAlarmState(ctx, "openccu-loom/alarm/eg/state", "armed_home"); err != nil {
		t.Fatalf("PublishAlarmState: %v", err)
	}
	if err := b.PublishAlarmAvailability(ctx, "openccu-loom/alarm/eg/availability", true); err != nil {
		t.Fatalf("PublishAlarmAvailability: %v", err)
	}
	if err := b.RetractAlarmTopic(ctx, "openccu-loom/alarm/eg/state"); err != nil {
		t.Fatalf("RetractAlarmTopic: %v", err)
	}
	if len(pub.sent) != 3 {
		t.Fatalf("raw_enabled=false: the discovery-declared alarm topics must still publish, got %d writes: %+v", len(pub.sent), pub.sent)
	}

	if err := b.PublishAlarmEvent(ctx, "openccu-loom/alarm/eg/event", []byte(`{}`)); err != nil {
		t.Fatalf("PublishAlarmEvent: %v", err)
	}
	if len(pub.sent) != 3 {
		t.Fatalf("raw_enabled=false: the event topic is raw-plane traffic and must stay silent, got %d writes: %+v", len(pub.sent), pub.sent)
	}

	b2, pub2 := newTestBridge(t)
	if err := b2.PublishAlarmEvent(ctx, "openccu-loom/alarm/eg/event", []byte(`{}`)); err != nil {
		t.Fatalf("PublishAlarmEvent: %v", err)
	}
	if len(pub2.sent) != 1 {
		t.Fatalf("raw_enabled=true: the event topic must reach the broker, got %d writes", len(pub2.sent))
	}
}
