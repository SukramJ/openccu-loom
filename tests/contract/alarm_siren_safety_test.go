// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// This file pins the siren-safety invariants of docs/alarm-concept.md
// §2 at the contract level. It grows with the output-driver layer;
// every S-invariant behaviour lands here with the code that carries it.

// TestAlarmS5CriticalCommandProbesOpenCircuit pins the S5 exception in
// the reliability layer: a CommandPriorityCritical call (the alarm
// engine's stop/silence path) is attempted as a single probe even
// while the interface circuit breaker is OPEN, while non-critical
// traffic keeps being shed. If this carve-out disappears, a siren
// stop issued during a wire outage is rejected unsent — the exact
// failure S5 exists to prevent.
func TestAlarmS5CriticalCommandProbesOpenCircuit(t *testing.T) {
	t.Parallel()

	c := reliability.NewCircuit(reliability.CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
	c.RecordFailure()
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state = %v, want OPEN", c.State())
	}

	attempted := 0
	if err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { attempted++; return nil }); err != nil {
		t.Fatalf("critical stop while OPEN: err = %v, want attempted probe", err)
	}
	if attempted != 1 {
		t.Fatalf("critical stop attempted %d times, want exactly 1", attempted)
	}

	if err := c.DoWithPriority(context.Background(), "setValue", hmenum.CommandPriorityHigh,
		func(context.Context) error { t.Fatal("non-critical must be shed"); return nil }); !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("non-critical while OPEN: err = %v, want ErrCircuitBreakerOpen", err)
	}
}

// TestAlarmS6SilenceAndDisarmNeverStateGated pins the S3/S6 rule at
// the engine surface: silence and disarm succeed from every
// state-machine position — they are role-gated by surfaces, never
// state-gated by the engine, and no confirmation step exists between
// the verb and its effect.
func TestAlarmS6SilenceAndDisarmNeverStateGated(t *testing.T) {
	t.Parallel()

	states := []struct {
		name  string
		drive func(t *testing.T, h *alarmEngineFixture)
	}{
		{"disarmed", func(*testing.T, *alarmEngineFixture) {}},
		{"arming", func(t *testing.T, h *alarmEngineFixture) {
			h.arm(t, engine.ArmRequest{Mode: hmenum.AlarmModeFull})
		}},
		{"armed", func(t *testing.T, h *alarmEngineFixture) {
			h.arm(t, engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true})
		}},
		{"pending", func(t *testing.T, h *alarmEngineFixture) {
			h.arm(t, engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true})
			h.eng.HandleSensorEvent(context.Background(), "door", true)
		}},
		{"triggered", func(t *testing.T, h *alarmEngineFixture) {
			h.arm(t, engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true})
			h.eng.HandleSensorEvent(context.Background(), "window", true)
		}},
	}
	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newAlarmEngineFixture(t)
			tc.drive(t, h)
			if err := h.eng.Silence(context.Background(), "area", "contract", "test"); err != nil {
				t.Fatalf("silence in state %s: %v", tc.name, err)
			}
			if err := h.eng.Disarm(context.Background(), "area", "contract", "test"); err != nil {
				t.Fatalf("disarm in state %s: %v", tc.name, err)
			}
		})
	}
}

// alarmEngineFixture is a minimal real-engine setup on a migrated
// temporary database (runtime component-instantiation pattern).
type alarmEngineFixture struct {
	eng *engine.Engine
}

func newAlarmEngineFixture(t *testing.T) *alarmEngineFixture {
	t.Helper()
	db, err := sqlitestore.Open(context.Background(), sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	areas := sqlitestore.NewAlarmAreaStore(db)
	sensors := sqlitestore.NewAlarmSensorStore(db)
	areaCfg, _ := json.Marshal(engine.AreaConfig{Modes: map[hmenum.AlarmMode]engine.ModeConfig{
		hmenum.AlarmModeFull: {ExitDelaySeconds: 300, EntryDelaySeconds: 300, TriggerSeconds: 300},
	}})
	if err := areas.Upsert(ctx, sqlitestore.AlarmAreaRow{ID: "area", Name: "Area", ConfigJSON: string(areaCfg)}); err != nil {
		t.Fatalf("seed area: %v", err)
	}
	doorCfg, _ := json.Marshal(engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}, UseEntryDelay: true})
	windowCfg, _ := json.Marshal(engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	for id, cfg := range map[string][]byte{"door": doorCfg, "window": windowCfg} {
		if err := sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
			ID: id, AreaID: "area", CentralName: "c", InterfaceID: "HmIP-RF",
			ChannelAddress: id + ":1", Parameter: "STATE",
			SensorType: hmenum.AlarmSensorTypeWindow, ConfigJSON: string(cfg),
		}); err != nil {
			t.Fatalf("seed sensor: %v", err)
		}
	}
	eng, err := engine.New(engine.Deps{
		Areas:     areas,
		Sensors:   sensors,
		State:     sqlitestore.NewAlarmStateStore(db),
		Incidents: sqlitestore.NewAlarmIncidentStore(db),
		Runtime:   sqlitestore.NewAlarmRuntimeStore(db),
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	return &alarmEngineFixture{eng: eng}
}

func (h *alarmEngineFixture) arm(t *testing.T, req engine.ArmRequest) {
	t.Helper()
	if _, err := h.eng.Arm(context.Background(), "area", req); err != nil {
		t.Fatalf("arm: %v", err)
	}
}
