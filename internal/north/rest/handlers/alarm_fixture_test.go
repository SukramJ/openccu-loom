// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	alarmjournal "github.com/SukramJ/openccu-loom/internal/alarm/journal"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmFixtureStart is the fixture wall-clock origin, kept after the
// engine's clock-plausibility epoch (engine/timers.go) so persisted
// state and restores behave the way they would in production.
var alarmFixtureStart = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

// alarmFixtureCentral is the fixed central name every fixture sensor
// and output row resolves under.
const alarmFixtureCentral = "c1"

// alarmPanelFixture wires a real engine, a real output-driver layer,
// and real SQLite-backed stores on a migrated temporary database — the
// same components alarm.Service assembles in production, minus the
// central-registry subscriptions the REST surface never touches. It
// implements the AlarmPanel facade the alarm handlers drive, mirroring
// the runtime component-instantiation pattern of
// tests/contract/alarm_siren_safety_test.go's newAlarmEngineFixture.
type alarmPanelFixture struct {
	t        *testing.T
	stores   *alarm.Stores
	eng      *engine.Engine
	mgr      *outputs.Manager
	resolver *fakeAlarmDeviceResolver
	clk      *clock.Fake
}

var _ AlarmPanel = (*alarmPanelFixture)(nil)

func (f *alarmPanelFixture) Engine() *engine.Engine    { return f.eng }
func (f *alarmPanelFixture) Manager() *outputs.Manager { return f.mgr }
func (f *alarmPanelFixture) Stores() *alarm.Stores     { return f.stores }

func (f *alarmPanelFixture) Panels() []alarmpanel.Panel { return nil }

func (f *alarmPanelFixture) OutputCandidates(hmenum.AlarmOutputClass) []alarm.OutputCandidate {
	return nil
}

// OutputTargetEligible reports unknown targets: the fixture has no
// central registry, so soft validation must always pass.
func (f *alarmPanelFixture) OutputTargetEligible(string, string, hmenum.AlarmOutputClass) (eligible, known bool) {
	return true, false
}

func (f *alarmPanelFixture) RemoteKeyCandidates() []alarm.RemoteKeyCandidate { return nil }

// Reload mirrors the driver + engine half of alarm.Service.Reload; the
// REST surface never touches the sensor-event routing indexes that
// belong to the daemon-level service's central subscriptions.
func (f *alarmPanelFixture) Reload(ctx context.Context) error {
	if err := f.mgr.Reload(ctx); err != nil {
		return err
	}
	return f.eng.Reload(ctx)
}

// newAlarmPanelFixture builds an empty, started fixture on a fresh
// temporary database: no areas, no sensors, no outputs. Tests seed
// through the seed* helpers (direct store writes + reload) or by
// driving the CRUD handlers under test.
func newAlarmPanelFixture(t *testing.T) *alarmPanelFixture {
	t.Helper()
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm.db")))
	if err != nil {
		t.Fatalf("open alarm fixture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stores := alarm.NewStores(db)
	clk := clock.NewFake(alarmFixtureStart)
	resolver := newFakeAlarmDeviceResolver()
	jrn := alarmjournal.New(stores.Journal, clk, nil, nil)

	mgr, err := outputs.NewManager(outputs.Config{
		Clock:    clk,
		Resolver: resolver,
		Ledger:   stores.Incidents,
		Journal:  jrn,
		Rows:     stores.Outputs,
	})
	if err != nil {
		t.Fatalf("outputs.NewManager: %v", err)
	}
	if err := mgr.Reload(ctx); err != nil {
		t.Fatalf("manager.Reload: %v", err)
	}

	eng, err := engine.New(engine.Deps{
		Clock:     clk,
		Areas:     stores.Areas,
		Sensors:   stores.Sensors,
		State:     stores.State,
		Incidents: stores.Incidents,
		Runtime:   stores.Runtime,
		Outputs:   mgr,
		Journal:   jrn,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	return &alarmPanelFixture{t: t, stores: stores, eng: eng, mgr: mgr, resolver: resolver, clk: clk}
}

// seedArea persists an area row and reloads the engine so it takes
// effect immediately.
func (f *alarmPanelFixture) seedArea(id, name string, cfg engine.AreaConfig) {
	f.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal area config: %v", err)
	}
	now := f.clk.Now().UnixMilli()
	if err := f.stores.Areas.Upsert(context.Background(), sqlitestore.AlarmAreaRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed area %s: %v", id, err)
	}
	f.mustReload()
}

// seedSensor persists a sensor row under areaID and reloads.
func (f *alarmPanelFixture) seedSensor(id, areaID string, typ hmenum.AlarmSensorType, cfg engine.SensorConfig) {
	f.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal sensor config: %v", err)
	}
	now := f.clk.Now().UnixMilli()
	if err := f.stores.Sensors.Upsert(context.Background(), sqlitestore.AlarmSensorRow{
		ID: id, AreaID: areaID, CentralName: alarmFixtureCentral, InterfaceID: "HmIP-RF",
		ChannelAddress: id + ":1", Parameter: "STATE", SensorType: typ,
		Name: id, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed sensor %s: %v", id, err)
	}
	f.mustReload()
}

// seedOutput persists an output row under areaID, registers a fake
// device for classes that resolve one, and reloads the manager.
func (f *alarmPanelFixture) seedOutput(id, areaID string, class hmenum.AlarmOutputClass, cfg outputs.OutputConfig) {
	f.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		f.t.Fatalf("marshal output config: %v", err)
	}
	now := f.clk.Now().UnixMilli()
	channel := id + ":1"
	if err := f.stores.Outputs.Upsert(context.Background(), sqlitestore.AlarmOutputRow{
		ID: id, AreaID: areaID, Class: class, CentralName: alarmFixtureCentral,
		ChannelAddress: channel, Name: id, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		f.t.Fatalf("seed output %s: %v", id, err)
	}
	switch class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		f.resolver.addSiren(alarmFixtureCentral, channel, &fakeAlarmSiren{})
	case hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight:
		f.resolver.addActuator(alarmFixtureCentral, channel, &fakeAlarmActuator{})
	default:
		// smoke sounders / notifications / sysvar mirrors need no
		// resolvable device in these fixtures.
	}
	f.mustReload()
}

func (f *alarmPanelFixture) mustReload() {
	f.t.Helper()
	if err := f.Reload(context.Background()); err != nil {
		f.t.Fatalf("reload: %v", err)
	}
}

// fullModeAreaConfig builds a single-mode ("full") area configuration
// with the given delays — the standard shape most alarm handler tests
// arm against.
func fullModeAreaConfig(exitDelayS, entryDelayS, triggerS int) engine.AreaConfig {
	return engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {
				ExitDelaySeconds:  exitDelayS,
				EntryDelaySeconds: entryDelayS,
				TriggerSeconds:    triggerS,
			},
		},
	}
}

// alarmOutputConfigFixture returns a minimal valid output configuration
// (the zero value: every mode, engine-default duration/tone).
func alarmOutputConfigFixture() outputs.OutputConfig {
	return outputs.OutputConfig{}
}

// --- fake device resolver ---

// fakeAlarmDeviceResolver implements outputs.DeviceResolver over maps
// keyed by "central|channel", mirroring the resolver test double the
// outputs package's own test suite uses internally (that one is
// unexported to its package, so the REST handler tests need their own
// minimal copy over the exported outputs.DeviceResolver surface).
type fakeAlarmDeviceResolver struct {
	sirens    map[string]outputs.SirenDevice
	actuators map[string]outputs.ActuatorDevice
}

func newFakeAlarmDeviceResolver() *fakeAlarmDeviceResolver {
	return &fakeAlarmDeviceResolver{
		sirens:    map[string]outputs.SirenDevice{},
		actuators: map[string]outputs.ActuatorDevice{},
	}
}

func alarmResolverKey(central, channel string) string { return central + "|" + channel }

func (r *fakeAlarmDeviceResolver) addSiren(central, channel string, dev outputs.SirenDevice) {
	r.sirens[alarmResolverKey(central, channel)] = dev
}

func (r *fakeAlarmDeviceResolver) addActuator(central, channel string, dev outputs.ActuatorDevice) {
	r.actuators[alarmResolverKey(central, channel)] = dev
}

func (r *fakeAlarmDeviceResolver) Siren(central, channel string) (outputs.SirenDevice, error) {
	dev, ok := r.sirens[alarmResolverKey(central, channel)]
	if !ok {
		return nil, fmt.Errorf("fakeAlarmDeviceResolver: no siren for %s/%s", central, channel)
	}
	return dev, nil
}

func (r *fakeAlarmDeviceResolver) SmokeSounder(central, channel string) (outputs.SmokeSounderDevice, error) {
	return nil, fmt.Errorf("fakeAlarmDeviceResolver: no smoke sounder for %s/%s", central, channel)
}

func (r *fakeAlarmDeviceResolver) Actuator(central, channel string) (outputs.ActuatorDevice, error) {
	dev, ok := r.actuators[alarmResolverKey(central, channel)]
	if !ok {
		return nil, fmt.Errorf("fakeAlarmDeviceResolver: no actuator for %s/%s", central, channel)
	}
	return dev, nil
}

func (r *fakeAlarmDeviceResolver) Sound(central, channel string) (outputs.SoundDevice, error) {
	return nil, fmt.Errorf("fakeAlarmDeviceResolver: no sound device for %s/%s", central, channel)
}

// fakeAlarmSiren is a minimal no-op SirenDevice: the REST handler
// tests only assert on the HTTP response, never on device wire calls
// (those are the output-driver package's own responsibility).
type fakeAlarmSiren struct{}

func (*fakeAlarmSiren) TurnOn(context.Context, sirencdp.OnConfig, hmenum.CommandPriority) error {
	return nil
}

func (*fakeAlarmSiren) TurnOff(context.Context, hmenum.CommandPriority) error { return nil }

func (*fakeAlarmSiren) AcousticState() (active bool, selection string, observed bool) {
	return false, "", false
}

func (*fakeAlarmSiren) OpticalState() (active bool, selection string, observed bool) {
	return false, "", false
}

func (*fakeAlarmSiren) AvailableTones() []string  { return nil }
func (*fakeAlarmSiren) AvailableLights() []string { return nil }

// fakeAlarmActuator is a minimal no-op ActuatorDevice.
type fakeAlarmActuator struct{}

func (*fakeAlarmActuator) TurnOnBounded(context.Context, time.Duration, *float64, hmenum.CommandPriority) error {
	return nil
}

func (*fakeAlarmActuator) TurnOnSteady(context.Context, *float64, hmenum.CommandPriority) error {
	return nil
}

func (*fakeAlarmActuator) TurnOff(context.Context, hmenum.CommandPriority) error { return nil }

func (*fakeAlarmActuator) IsOn() (on, observed bool) { return false, false }
