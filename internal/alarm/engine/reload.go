// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"context"
	"errors"
	"fmt"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Reload re-reads areas and sensors from the stores while preserving
// the runtime state of surviving areas — the management surface calls
// it after every configuration write. Surviving areas keep their
// machine state, bypass list, open incident, and running countdowns;
// new areas start disarmed; removed areas are disarmed (silence
// implied) before they vanish, never dropped mid-alarm.
func (e *Engine) Reload(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return errors.New("engine: not started")
	}

	areaRows, err := e.areasStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: reload areas: %w", err)
	}
	sensorRows, err := e.sensorsStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: reload sensors: %w", err)
	}

	type desiredArea struct {
		name string
		cfg  AreaConfig
	}
	desired := map[string]desiredArea{}
	for i := range areaRows {
		row := &areaRows[i]
		cfg, err := ParseAreaConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("engine: area %q: %w", row.ID, err)
		}
		desired[row.ID] = desiredArea{name: row.Name, cfg: cfg}
	}

	// Drop removed areas. The management surface refuses to delete a
	// non-disarmed area, but a direct store write must still never
	// leave an orphaned alarm running: disarm first, loudly.
	for id, a := range e.areas {
		if _, keep := desired[id]; keep {
			continue
		}
		if a.state != hmenum.AlarmAreaStateDisarmed {
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassFault, Event: "area_removed_while_armed",
			})
			e.disarmLocked(ctx, a, "engine:reload", "engine")
		}
		a.cancelTimers()
		if err := e.stateStore.Delete(ctx, id); err != nil {
			e.log.Error("alarm state delete failed", "area", id, "error", err)
		}
		delete(e.areas, id)
	}

	// Update surviving areas, create new ones.
	for id, d := range desired {
		a, exists := e.areas[id]
		if !exists {
			a = &area{
				id:        id,
				name:      d.name,
				cfg:       d.cfg,
				sensors:   map[string]*sensorState{},
				state:     hmenum.AlarmAreaStateDisarmed,
				mode:      hmenum.AlarmModeDisarmed,
				bypassed:  map[string]bool{},
				openAtArm: map[string]bool{},
			}
			e.areas[id] = a
			e.persist(ctx, a)
			continue
		}
		a.name = d.name
		a.cfg = d.cfg
		// The active mode vanished from the configuration: an armed
		// machine without its mode config has no delays, no trigger
		// bound, no policy — disarm loudly instead of guessing.
		if a.mode.Armed() {
			if _, ok := a.cfg.Modes[a.mode]; !ok {
				e.journalEntry(ctx, a, JournalEntry{
					Class: hmenum.AlarmJournalClassFault, Event: "mode_removed_while_armed",
					Details: map[string]any{"mode": string(a.mode)},
				})
				e.disarmLocked(ctx, a, "engine:reload", "engine")
			}
		}
	}

	return e.reloadSensorsLocked(ctx, sensorRows)
}

// reloadSensorsLocked rebuilds the sensor sets from fresh rows,
// preserving runtime sensor state (activation, availability, health)
// of surviving sensors and pruning references to removed ones. The
// caller holds the lock.
func (e *Engine) reloadSensorsLocked(ctx context.Context, sensorRows []sqlitestore.AlarmSensorRow) error {
	newSensorsByArea := map[string]map[string]*sensorState{}
	newIndex := map[string]string{}
	for i := range sensorRows {
		row := &sensorRows[i]
		a, ok := e.areas[row.AreaID]
		if !ok {
			e.log.Warn("alarm sensor references unknown area", "sensor", row.ID, "area", row.AreaID)
			continue
		}
		cfg, err := ParseSensorConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("engine: sensor %q: %w", row.ID, err)
		}
		set := newSensorsByArea[row.AreaID]
		if set == nil {
			set = map[string]*sensorState{}
			newSensorsByArea[row.AreaID] = set
		}
		if old, ok := a.sensors[row.ID]; ok {
			old.row = *row
			old.cfg = cfg
			set[row.ID] = old
		} else {
			set[row.ID] = &sensorState{row: *row, cfg: cfg, available: true}
		}
		newIndex[row.ID] = row.AreaID
	}
	for id, a := range e.areas {
		set := newSensorsByArea[id]
		if set == nil {
			set = map[string]*sensorState{}
		}
		a.sensors = set
		// Prune runtime references to removed sensors.
		for sid := range a.bypassed {
			if _, ok := set[sid]; !ok {
				delete(a.bypassed, sid)
			}
		}
		for sid := range a.openAtArm {
			if _, ok := set[sid]; !ok {
				delete(a.openAtArm, sid)
			}
		}
		if a.pendingCause != "" {
			if _, ok := set[a.pendingCause]; !ok {
				a.pendingCause = ""
			}
		}
		e.persist(ctx, a)
		e.refreshReadiness(a)
	}
	e.sensorIndex = newIndex
	return nil
}

// disarmLocked is the internal disarm transition for engine-initiated
// paths that already hold the lock.
func (e *Engine) disarmLocked(ctx context.Context, a *area, by, source string) {
	from := a.state
	a.cancelTimers()
	if a.incident != nil {
		e.silenceIncident(ctx, a, by, source)
		e.closeIncident(ctx, a, closeReasonDisarm)
	}
	a.state = hmenum.AlarmAreaStateDisarmed
	a.mode = hmenum.AlarmModeDisarmed
	a.bypassed = map[string]bool{}
	a.openAtArm = map[string]bool{}
	a.pendingCause = ""
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassDisarm, Event: "disarmed", Actor: by, Source: source,
	})
	e.publishState(a, from, by, source)
}
