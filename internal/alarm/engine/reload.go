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

// Reload re-reads zones and sensors from the stores while preserving
// the runtime state of surviving zones — the management surface calls
// it after every configuration write. Surviving zones keep their
// machine state, bypass list, open incident, and running countdowns;
// new zones start disarmed; removed zones are disarmed (silence
// implied) before they vanish, never dropped mid-alarm.
func (e *Engine) Reload(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return errors.New("engine: not started")
	}

	zoneRows, err := e.zonesStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: reload zones: %w", err)
	}
	sensorRows, err := e.sensorsStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: reload sensors: %w", err)
	}

	type desiredZone struct {
		name string
		cfg  ZoneConfig
	}
	desired := map[string]*desiredZone{}
	for i := range zoneRows {
		row := &zoneRows[i]
		cfg, err := ParseZoneConfig(row.ConfigJSON)
		if err != nil {
			// Keep the previously loaded configuration of this zone
			// rather than dropping it mid-flight; skip loudly (S7).
			e.log.Error("alarm zone config unparseable — keeping previous config", "zone", row.ID, "error", err)
			if _, jerr := e.journal.Append(ctx, JournalEntry{
				ZoneID: row.ID, Class: hmenum.AlarmJournalClassFault,
				Event: "zone_config_unparseable", Details: map[string]any{"error": err.Error()},
			}); jerr != nil {
				e.log.Error("alarm journal append failed", "error", jerr)
			}
			if prev, ok := e.zones[row.ID]; ok {
				desired[row.ID] = &desiredZone{name: prev.name, cfg: prev.cfg}
			}
			continue
		}
		desired[row.ID] = &desiredZone{name: row.Name, cfg: cfg}
	}

	// Drop removed zones. The management surface refuses to delete a
	// non-disarmed zone, but a direct store write must still never
	// leave an orphaned alarm running: disarm first, loudly.
	for id, a := range e.zones {
		if _, keep := desired[id]; keep {
			continue
		}
		if a.state != hmenum.AlarmZoneStateDisarmed {
			e.journalEntry(ctx, a, JournalEntry{
				Class: hmenum.AlarmJournalClassFault, Event: "zone_removed_while_armed",
			})
			e.disarmLocked(ctx, a, "engine:reload", "engine")
		}
		a.cancelTimers()
		// A post-trigger-disarmed zone may still hold a pending
		// auto-rearm; cancelTimers deliberately leaves it alone, so an
		// zone drop must cancel it explicitly or the scheduler goroutine
		// outlives the zone until its deadline.
		a.cancelAutoRearm()
		if err := e.stateStore.Delete(ctx, id); err != nil {
			e.log.Error("alarm state delete failed", "zone", id, "error", err)
		}
		delete(e.zones, id)
	}

	// Update surviving zones, create new ones.
	for id, d := range desired {
		a, exists := e.zones[id]
		if !exists {
			a = &zone{
				id:        id,
				name:      d.name,
				cfg:       d.cfg,
				sensors:   map[string]*sensorState{},
				state:     hmenum.AlarmZoneStateDisarmed,
				mode:      hmenum.AlarmModeDisarmed,
				bypassed:  map[string]bool{},
				openAtArm: map[string]bool{},
			}
			e.zones[id] = a
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

	e.reloadSensorsLocked(ctx, sensorRows)
	return nil
}

// reloadSensorsLocked rebuilds the sensor sets from fresh rows,
// preserving runtime sensor state (activation, availability, health)
// of surviving sensors and pruning references to removed ones. The
// caller holds the lock.
func (e *Engine) reloadSensorsLocked(ctx context.Context, sensorRows []sqlitestore.AlarmSensorRow) {
	newSensorsByZone := map[string]map[string]*sensorState{}
	newIndex := map[string]string{}
	for i := range sensorRows {
		row := &sensorRows[i]
		a, ok := e.zones[row.ZoneID]
		if !ok {
			e.log.Warn("alarm sensor references unknown zone", "sensor", row.ID, "zone", row.ZoneID)
			continue
		}
		cfg, err := ParseSensorConfig(row.ConfigJSON)
		if err != nil {
			e.log.Error("alarm sensor config unparseable — sensor skipped", "sensor", row.ID, "error", err)
			if _, jerr := e.journal.Append(ctx, JournalEntry{
				ZoneID: row.ZoneID, Class: hmenum.AlarmJournalClassFault,
				Event: "sensor_config_unparseable", Details: map[string]any{"sensor_id": row.ID, "error": err.Error()},
			}); jerr != nil {
				e.log.Error("alarm journal append failed", "error", jerr)
			}
			continue
		}
		set := newSensorsByZone[row.ZoneID]
		if set == nil {
			set = map[string]*sensorState{}
			newSensorsByZone[row.ZoneID] = set
		}
		if old, ok := a.sensors[row.ID]; ok {
			old.row = *row
			old.cfg = cfg
			set[row.ID] = old
		} else {
			set[row.ID] = &sensorState{row: *row, cfg: cfg, available: true}
		}
		newIndex[row.ID] = row.ZoneID
	}
	for id, a := range e.zones {
		set := newSensorsByZone[id]
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
		// A sensor enrolled by this write has never seen an event, so
		// its activation state is unknown — and the blocker policy only
		// classifies a *known* active sensor. Without the seed a contact
		// enrolled while it stands open leaves the zone reporting ready
		// to arm on every surface until it happens to push. Reads come
		// from the same cached channel model the live event path feeds.
		e.refreshSensorValues(ctx, a)
		e.persist(ctx, a)
		e.refreshReadiness(a)
	}
	e.sensorIndex = newIndex
}

// disarmLocked is the internal disarm transition for engine-initiated
// paths that already hold the lock.
func (e *Engine) disarmLocked(ctx context.Context, a *zone, by, source string) {
	from := a.state
	a.cancelTimers()
	a.cancelAutoRearm()
	if a.incident != nil {
		e.silenceIncident(ctx, a, by, source)
		e.closeIncident(ctx, a, closeReasonDisarm)
	}
	a.state = hmenum.AlarmZoneStateDisarmed
	a.mode = hmenum.AlarmModeDisarmed
	a.bypassed = map[string]bool{}
	a.openAtArm = map[string]bool{}
	a.pendingCause = ""
	a.preTriggerState = ""
	a.preTriggerMode = ""
	a.preAlarm = false
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassDisarm, Event: "disarmed", Actor: by, Source: source,
	})
	e.publishState(a, from, by, source)
}
