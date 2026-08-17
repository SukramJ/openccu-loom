// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// deviceLifecycleStart keeps the fake clock past the engine's
// clock-plausibility epoch, as the other alarm harnesses do.
var deviceLifecycleStart = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

const (
	lifecycleCentral   = "ccu"
	lifecycleZone      = "eg"
	lifecycleSensor    = "window"
	lifecycleDevice    = "0001D3C99ABCDE"
	lifecycleChannel   = "0001D3C99ABCDE:1"
	lifecycleInterface = "ccu-HmIP-RF"
)

// TestRemovedDeviceMarksItsSensorsUnavailable pins what happens to an
// enrolled sensor whose device leaves the CCU.
//
// A deleted, unpaired or re-addressed device emits no UNREACH — it emits
// nothing ever again — and the alarm domain subscribed to no device
// lifecycle event at all, so the sensor kept available=true and its last
// value for as long as the daemon ran. The zone reported ready-to-arm,
// armed, and the window behind that contact was silently unmonitored:
// the default unreachable=block policy defeated by the one case it
// exists for, with no blocker, no journal entry and no health signal.
//
// The removal runs through Unit.RemoveDevice, the production path that
// publishes the event, so the subscription is part of what is tested.
func TestRemovedDeviceMarksItsSensorsUnavailable(t *testing.T) {
	t.Parallel()

	svc, unit := deviceLifecycleService(t)
	ctx := context.Background()

	if _, err := svc.Engine().Arm(ctx, lifecycleZone,
		engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm before the device left: %v", err)
	}

	if !unit.RemoveDevice(lifecycleDevice) {
		t.Fatal("the device was not in the model registry")
	}

	waitForJournalEvent(t, svc, "sensor_unavailable_while_armed",
		"a sensor whose device left the CCU must degrade while the zone is armed")
	waitForBlockers(t, svc, lifecycleZone, hmenum.AlarmModeFull, []string{lifecycleSensor},
		"a sensor whose device no longer exists must block the arm")

	// The blocker has to survive the disarm: a zone that arms again with
	// a member sensor that can never fire is the whole defect.
	if err := svc.Engine().Disarm(ctx, lifecycleZone, "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	_, err := svc.Engine().Arm(ctx, lifecycleZone, engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var notReady *engine.NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("second arm error = %v, want a NotReadyError: the default unreachable=block policy must "+
			"refuse a zone whose sensor no longer exists", err)
	}
	if !slices.Contains(notReady.Blockers, lifecycleSensor) {
		t.Fatalf("arm blockers = %v, want %q among them", notReady.Blockers, lifecycleSensor)
	}
}

// TestBootAgainstAModelWithoutTheEnrolledDeviceBlocksTheArm covers the
// half no runtime event can reach: the device was deleted while the
// daemon was down, so the removal event fired into a process that no
// longer existed and the restore marks every sensor available again.
//
// The order is production's: the service starts against a central whose
// southbound bring-up has not delivered any devices yet — where "absent
// from the model" means "not loaded" and must not degrade anything — and
// the model only arrives afterwards. Seeding the model first would
// invert it and pass however broken the wiring is.
func TestBootAgainstAModelWithoutTheEnrolledDeviceBlocksTheArm(t *testing.T) {
	t.Parallel()

	svc, unit := deviceLifecycleService(t)
	ctx := context.Background()

	// The enrolled device is gone; the rest of the model is there.
	if !unit.RemoveDevice(lifecycleDevice) {
		t.Fatal("the device was not in the model registry")
	}
	// RemoveDevice already published a removal event. Restart the service
	// so the engine restores from the store exactly as a fresh daemon
	// would: every sensor available, nothing left of the removal.
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Southbound bring-up completes: the model is complete and does not
	// contain the enrolled device.
	unit.MarkSouthboundReady()
	events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{CentralName: lifecycleCentral})

	waitForBlockers(t, svc, lifecycleZone, hmenum.AlarmModeFull, []string{lifecycleSensor},
		"a daemon that boots against a model without the enrolled device must not report the zone ready")
	if _, err := svc.Engine().Arm(ctx, lifecycleZone,
		engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err == nil {
		t.Fatal("the zone armed with a sensor that is not in the device model")
	}
}

// deviceLifecycleService brings up an alarm service with one enrolled
// window contact on a central whose model holds the matching device, and
// returns the service plus that central.
func deviceLifecycleService(t *testing.T) (*Service, *central.Unit) {
	t.Helper()

	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-device-lifecycle.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	unit, err := central.New(central.Config{Name: lifecycleCentral})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: lifecycleInterface,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     lifecycleDevice,
		Model:       "HmIP-SWDO",
	})
	dev.AddChannel(lifecycleChannel, 1, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(dev)

	ctx := context.Background()
	stores := NewStores(db)
	zoneCfg, err := json.Marshal(engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := deviceLifecycleStart.UnixMilli()
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: lifecycleZone, Name: "Erdgeschoss", Slug: lifecycleZone, ConfigJSON: string(zoneCfg),
		CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	sensorCfg, err := json.Marshal(engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID: lifecycleSensor, ZoneID: lifecycleZone, CentralName: lifecycleCentral,
		InterfaceID: lifecycleInterface, ChannelAddress: lifecycleChannel,
		Parameter: string(hmenum.ParameterState), SensorType: hmenum.AlarmSensorTypeWindow,
		Name: "Fenster", ConfigJSON: string(sensorCfg), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed sensor: %v", err)
	}

	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: reg,
		Stores:   stores,
		Clock:    clock.NewFake(deviceLifecycleStart),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("service start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	return svc, unit
}
