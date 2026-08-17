// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ErrWalkTestActive reports a walk-test start while one is running.
var ErrWalkTestActive = errors.New("engine: walk test already active")

// walkSession is one arm-less walk-test session (notes/concepts/alarm-concept.md
// §12.4). Sessions are in-memory only: a daemon restart ends them.
type walkSession struct {
	startedAt time.Time
	seen      map[string]time.Time
}

// WalkTestSensor is one sensor row of a walk-test status.
type WalkTestSensor struct {
	SensorID string
	Name     string
	SeenAt   time.Time // zero when not yet tripped
}

// WalkTestStatus reports a session's progress.
type WalkTestStatus struct {
	Active    bool
	StartedAt time.Time
	Seen      int
	Total     int
	Sensors   []WalkTestSensor
}

// WalkTestStart begins a walk test on a disarmed zone: every sensor
// activation is recorded and journaled instead of evaluated — the
// checklist view ticks live without arming anything.
func (e *Engine) WalkTestStart(ctx context.Context, zoneID, by, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return ErrInvalidState
	}
	a, ok := e.zones[zoneID]
	if !ok {
		return ErrUnknownZone
	}
	if a.state != hmenum.AlarmZoneStateDisarmed {
		return ErrInvalidState
	}
	if a.walk != nil {
		return ErrWalkTestActive
	}
	a.walk = &walkSession{startedAt: e.clk.Now(), seen: map[string]time.Time{}}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTest, Event: "walktest_started", Actor: by, Source: source,
	})
	return nil
}

// WalkTestStop ends the session and journals the report (verified and
// missing sensors).
func (e *Engine) WalkTestStop(ctx context.Context, zoneID, by, source string) (WalkTestStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return WalkTestStatus{}, ErrUnknownZone
	}
	if a.walk == nil {
		return WalkTestStatus{}, ErrInvalidState
	}
	status := e.walkStatusLocked(a)
	var missing []string
	for _, s := range status.Sensors {
		if s.SeenAt.IsZero() {
			missing = append(missing, s.SensorID)
		}
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTest, Event: "walktest_finished", Actor: by, Source: source,
		Details: map[string]any{"seen": status.Seen, "total": status.Total, "missing": missing},
	})
	a.walk = nil
	status.Active = false
	return status, nil
}

// WalkTestStatus reports the running (or absent) session of an zone.
func (e *Engine) WalkTestStatus(zoneID string) (WalkTestStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.zones[zoneID]
	if !ok {
		return WalkTestStatus{}, ErrUnknownZone
	}
	if a.walk == nil {
		return WalkTestStatus{Active: false}, nil
	}
	return e.walkStatusLocked(a), nil
}

// walkStatusLocked builds the progress snapshot. The caller holds the
// lock and has checked a.walk != nil.
func (e *Engine) walkStatusLocked(a *zone) WalkTestStatus {
	st := WalkTestStatus{Active: true, StartedAt: a.walk.startedAt}
	ids := sortedSensorIDs(a)
	for _, id := range ids {
		s := a.sensors[id]
		if s.cfg.AlwaysOn {
			continue
		}
		row := WalkTestSensor{SensorID: id, Name: s.row.Name}
		if t, ok := a.walk.seen[id]; ok {
			row.SeenAt = t
			st.Seen++
		}
		st.Total++
		st.Sensors = append(st.Sensors, row)
	}
	sort.Slice(st.Sensors, func(i, j int) bool { return st.Sensors[i].SensorID < st.Sensors[j].SensorID })
	return st
}

// abortWalkTestForArm ends a running walk-test session before an arm
// takes effect. Without this, a session left open across an arm stays
// open through the whole armed period — walkTestObserve already
// refuses to consume events while the zone is not disarmed, so an
// intrusion during that period alarms normally — but the session
// itself survives, and once the zone disarms again it silently
// resumes swallowing every sensor activation as walk-test progress
// instead of real disarmed-state handling (door chime, open-at-arm
// bookkeeping). The caller holds the lock.
func (e *Engine) abortWalkTestForArm(ctx context.Context, a *zone, by, source string) {
	if a.walk == nil {
		return
	}
	status := e.walkStatusLocked(a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTest, Event: "walktest_aborted_by_arm", Actor: by, Source: source,
		Details: map[string]any{"seen": status.Seen, "total": status.Total},
	})
	a.walk = nil
}

// walkTestObserve records a sensor activation during a running
// session. The caller holds the lock; returns true when consumed.
func (e *Engine) walkTestObserve(ctx context.Context, a *zone, sensorID string, active bool) bool {
	if a.walk == nil || a.state != hmenum.AlarmZoneStateDisarmed {
		return false
	}
	if !active {
		return true // closings are part of the walk, nothing to record
	}
	s, ok := a.sensors[sensorID]
	if !ok {
		return false
	}
	if _, seen := a.walk.seen[sensorID]; !seen {
		now := e.clk.Now()
		a.walk.seen[sensorID] = now
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTest, Event: "walktest_sensor_seen",
			Details: map[string]any{"sensor_id": sensorID},
		})
		st := e.walkStatusLocked(a)
		e.sink.Publish(hmevent.AlarmWalkTestEvent{
			Base:   hmevent.NewBaseAt(now),
			ZoneID: a.id, SensorID: sensorID, SensorName: s.row.Name,
			Seen: st.Seen, Total: st.Total,
		})
	}
	return true
}
