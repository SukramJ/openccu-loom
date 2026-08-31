// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The alarm engine and the Security & Safety domain watch the same
// physical contacts through two independent subscriptions to the same
// event bus. Each one used to carry its own copy of "does this wire
// value count as an activation", and the copies disagreed: for an
// integer index the declared value list does not cover, the alarm
// engine read the sensor inactive while the security plane fell back to
// "not zero, so active" and lit the hazard class.
//
// A sensor that is active in one plane and idle in the other is a
// contradiction an operator can neither see nor resolve — the SPA shows
// both, and neither is wrong on its own terms. This file pins the two
// planes' *observable* verdicts against each other on one enrolled
// smoke detector, driven through both services' real constructors and
// the real event path. It never calls the shared rule directly: a test
// that evaluates the rule itself would pass while the two domains still
// disagreed in the field.
const (
	safetyActivationCentral   = "ccu-activation"
	safetyActivationIface     = "ccu-activation-HmIP-RF"
	safetyActivationDevice    = "SWSD0001"
	safetyActivationChannel   = "SWSD0001:1"
	safetyActivationZone      = "zone-activation"
	safetyActivationSensor    = "sensor-smoke"
	safetyActivationParameter = "SMOKE_DETECTOR_ALARM_STATUS"
)

// safetyActivationValueList is HmIP-SWSD's declared vocabulary for
// SMOKE_DETECTOR_ALARM_STATUS.
var safetyActivationValueList = []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

// safetyActivationFixture holds one central carrying an enrolled smoke
// detector plus both live domain services attached to it.
type safetyActivationFixture struct {
	unit     *central.Unit
	alarmSvc *alarm.Service
	secSvc   *security.Service
}

// TestAlarmAndSecurityAgreeOnEnumActivation drives one enrolled ENUM
// sensor through both domains and requires their observable verdicts to
// match, value for value.
//
// The three interesting rows are index 5 (outside the declared list —
// the divergence this pin exists for), index 3 (SECONDARY_ALARM: inside
// the list and inside the classifier's default label set, but outside
// the operator's own selection, so it proves both planes read the same
// label source) and index 1 (a real detection, which proves the pin can
// still tell active from inactive at all).
func TestAlarmAndSecurityAgreeOnEnumActivation(t *testing.T) {
	t.Parallel()

	f := newSafetyActivationFixture(t)

	cases := []struct {
		name  string
		index int
		want  bool
	}{
		{"PRIMARY_ALARM is the operator's selected value", 1, true},
		{"IDLE_OFF is idle", 0, false},
		{"INTRUSION_ALARM is the daemon's own siren command", 2, false},
		{"SECONDARY_ALARM is outside the operator's selection", 3, false},
		{"an index outside the declared value list", 5, false},
		{"a negative index", -1, false},
	}
	for _, tc := range cases {
		// Each row starts from a real activation, so the row's own value
		// is always a transition for whichever plane tracks changes.
		f.publish(1)
		if !f.alarmActive(t) || !f.securityActive() {
			t.Fatalf("%s: priming with PRIMARY_ALARM left alarm=%v security=%v, want both active",
				tc.name, f.alarmActive(t), f.securityActive())
		}
		f.publish(tc.index)

		gotAlarm, gotSecurity := f.alarmActive(t), f.securityActive()
		if gotAlarm != tc.want || gotSecurity != tc.want {
			t.Errorf("%s (index %d): alarm active = %v, security active = %v, want both %v",
				tc.name, tc.index, gotAlarm, gotSecurity, tc.want)
		}
	}
}

// newSafetyActivationFixture builds the central, the enrolment and both
// services through their production constructors.
func newSafetyActivationFixture(t *testing.T) *safetyActivationFixture {
	t.Helper()
	ctx := context.Background()

	dev := device.New(device.Config{
		InterfaceID: safetyActivationIface,
		Address:     safetyActivationDevice,
		Model:       "HmIP-SWSD",
		Name:        "Smoke detector",
	})
	ch := dev.AddChannel(safetyActivationChannel, 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    safetyActivationIface,
			ChannelAddress: safetyActivationChannel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      safetyActivationParameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  safetyActivationValueList,
		},
	}))

	unit, err := central.New(central.Config{Name: safetyActivationCentral})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.ModelRegistry.Put(dev)
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "activation.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	zoneCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	sensorCfg, err := json.Marshal(engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		// The operator's own narrowing: only a primary alarm counts.
		// SECONDARY_ALARM is deliberately left out although the
		// classifier table lists it, so the two planes disagree unless
		// both honour the enrolment.
		ActiveValues: []string{"PRIMARY_ALARM"},
	})
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}

	alarmStores := alarm.NewStores(db)
	if err := alarmStores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: safetyActivationZone, Name: "Activation", Slug: safetyActivationZone,
		ConfigJSON: string(zoneCfg),
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := alarmStores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID: safetyActivationSensor, ZoneID: safetyActivationZone,
		CentralName: safetyActivationCentral, InterfaceID: safetyActivationIface,
		ChannelAddress: safetyActivationChannel, Parameter: safetyActivationParameter,
		SensorType: hmenum.AlarmSensorTypeHazard, Name: "Smoke detector",
		ConfigJSON: string(sensorCfg),
	}); err != nil {
		t.Fatalf("seed sensor: %v", err)
	}

	alarmSvc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{Enabled: true},
		Registry: reg,
		Stores:   alarmStores,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("alarm.NewService: %v", err)
	}
	if err := alarmSvc.Start(ctx); err != nil {
		t.Fatalf("alarm service start: %v", err)
	}
	t.Cleanup(func() { _ = alarmSvc.Stop(context.Background()) })

	// The renderer calls into the catalogs on every class transition, so
	// a real one is required rather than merely convenient.
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	secSvc, err := security.New(security.Deps{
		Registry: reg,
		Stores: &security.Stores{
			Faults:  sqlitestore.NewSecurityFaultStore(db),
			Sources: sqlitestore.NewSecuritySourceStore(db),
			Sensors: sqlitestore.NewAlarmSensorStore(db),
			Zones:   sqlitestore.NewAlarmZoneStore(db),
		},
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	})
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	if err := secSvc.Start(ctx); err != nil {
		t.Fatalf("security service start: %v", err)
	}
	t.Cleanup(func() { _ = secSvc.Stop(context.Background()) })

	return &safetyActivationFixture{unit: unit, alarmSvc: alarmSvc, secSvc: secSvc}
}

// publish delivers one wire value change on the central's bus, the way
// the callback server does. Dispatch is synchronous, so both domains
// have folded the value in by the time it returns.
func (f *safetyActivationFixture) publish(index int) {
	events.Publish(f.unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    safetyActivationIface,
			ChannelAddress: safetyActivationChannel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      safetyActivationParameter,
		},
		NewValue: hmtypes.IntValue(index),
	})
}

// alarmActive reads the alarm domain's verdict off the zone's readiness:
// an active sensor blocks the arm with reason "open", which is the
// engine's own observable statement that this sensor is not idle.
func (f *safetyActivationFixture) alarmActive(t *testing.T) bool {
	t.Helper()
	snap, ok := f.alarmSvc.Engine().Zone(safetyActivationZone)
	if !ok {
		t.Fatalf("alarm zone %q not in the engine", safetyActivationZone)
	}
	details := snap.Readiness[hmenum.AlarmModeFull].Details
	for i := range details {
		d := &details[i]
		if d.SensorID == safetyActivationSensor && d.Reason == hmevent.AlarmBlockerReasonOpen {
			return true
		}
	}
	return false
}

// securityActive reads the security domain's verdict off the aggregate
// snapshot rather than off a published event: the plane publishes only
// on a change, so "no event" would read as "inactive" and pass the very
// divergence this pin exists to catch.
func (f *safetyActivationFixture) securityActive() bool {
	return f.secSvc.Snapshot().Classes[hmenum.SecurityClassSmoke].Active
}
