// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"context"
	"log/slog"
	"sort"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TriggeredMotionSensor identifies one enrolled sensor that is latched
// and can be cleared.
type TriggeredMotionSensor struct {
	SensorID string
	ZoneID   string
	Name     string
	// ChannelAddress and Parameter identify the sensor's own data
	// point, not the RESET_MOTION one — they are what the operator sees
	// in the enrolment list.
	ChannelAddress string
	Parameter      string
}

// MotionResetResult reports what one reset pass did. The counts are
// disjoint: Reset + Failed equals the number of sensors attempted.
type MotionResetResult struct {
	// Sensors are the sensors the pass acted on, sorted by ID.
	Sensors []TriggeredMotionSensor
	// Reset counts the sensors whose RESET_MOTION write succeeded.
	Reset int
	// Failed counts the sensors whose write returned an error. The pass
	// continues past a failure: a detector that stopped answering must
	// not be able to block arming.
	Failed int
}

// TriggeredMotionSensors lists the latched, resettable sensors of one
// zone, or of every zone when zoneID is empty.
//
// A sensor qualifies when it is currently active and its channel
// exposes a writable RESET_MOTION data point. Deriving the list from
// the same predicate [ResetTriggeredMotion] uses is deliberate: a count
// that named sensors the reset would skip would send an operator
// looking for a button that cannot help them.
func (e *Engine) TriggeredMotionSensors(zoneID string) []TriggeredMotionSensor {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.triggeredMotionLocked(zoneID)
}

// triggeredMotionLocked is the lock-held body of
// [Engine.TriggeredMotionSensors].
func (e *Engine) triggeredMotionLocked(zoneID string) []TriggeredMotionSensor {
	if e.motionReset == nil {
		return nil
	}
	out := make([]TriggeredMotionSensor, 0, 4)
	for id, a := range e.zones {
		if zoneID != "" && id != zoneID {
			continue
		}
		for sensorID, s := range a.sensors {
			if !s.activeKnown || !s.active {
				continue
			}
			if !e.motionReset.Supports(s.row) {
				continue
			}
			out = append(out, TriggeredMotionSensor{
				SensorID:       sensorID,
				ZoneID:         id,
				Name:           s.row.Name,
				ChannelAddress: s.row.ChannelAddress,
				Parameter:      s.row.Parameter,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SensorID < out[j].SensorID })
	return out
}

// ResetTriggeredMotion clears every latched motion sensor of one zone,
// or of every zone when zoneID is empty. Returns what it acted on.
//
// The device writes run without the engine lock: each write goes to the
// radio and the resulting MOTION=false events come back through the
// normal sensor path, which needs the lock to make progress. Holding it
// across the writes would deadlock the very events the reset exists to
// produce.
//
// Errors from individual devices are counted and logged, never
// returned: this is called on the arming path, where one unreachable
// detector must not fail the whole verb.
func (e *Engine) ResetTriggeredMotion(ctx context.Context, zoneID, by, source string) MotionResetResult {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return MotionResetResult{}
	}
	if zoneID != "" {
		if _, ok := e.zones[zoneID]; !ok {
			e.mu.Unlock()
			return MotionResetResult{}
		}
	}
	targets := e.triggeredMotionLocked(zoneID)
	rows := make([]sqlitestore.AlarmSensorRow, 0, len(targets))
	for _, t := range targets {
		if a, ok := e.zones[t.ZoneID]; ok {
			if s, ok := a.sensors[t.SensorID]; ok {
				rows = append(rows, s.row)
			}
		}
	}
	e.mu.Unlock()

	res := MotionResetResult{Sensors: targets}
	// perZone accumulates the outcome for the journal: one entry per
	// zone, so a fleet-wide reset shows up in each affected zone's
	// timeline rather than only in the first.
	type tally struct{ reset, failed int }
	perZone := map[string]*tally{}
	for i := range rows {
		row := &rows[i]
		t, ok := perZone[targets[i].ZoneID]
		if !ok {
			t = &tally{}
			perZone[targets[i].ZoneID] = t
		}
		if err := e.motionReset.Reset(ctx, *row); err != nil {
			res.Failed++
			t.failed++
			e.log.Warn("alarm: motion reset failed",
				slog.String("sensor", targets[i].SensorID),
				slog.String("zone", targets[i].ZoneID),
				slog.String("channel", row.ChannelAddress),
				slog.String("err", err.Error()))
			continue
		}
		res.Reset++
		t.reset++
	}

	e.mu.Lock()
	for id, t := range perZone {
		a, ok := e.zones[id]
		if !ok {
			continue
		}
		ids := make([]string, 0, len(targets))
		for i := range targets {
			if targets[i].ZoneID == id {
				ids = append(ids, targets[i].SensorID)
			}
		}
		e.journalEntry(ctx, a, JournalEntry{
			Class:  hmenum.AlarmJournalClassMaintenance,
			Event:  "motion_reset",
			Actor:  by,
			Source: source,
			Details: map[string]any{
				"sensors": ids,
				"reset":   t.reset,
				"failed":  t.failed,
			},
		})
	}
	e.mu.Unlock()
	return res
}

// resetTriggeredMotionForArm runs the pre-arm reset pass. It is a
// separate seam from [Engine.ResetTriggeredMotion] so the arming path
// can hand off asynchronously: the caller holds the engine lock, and
// the writes must not run under it.
//
// Fire-and-forget is correct here rather than sloppy. The reset's
// effect arrives as MOTION=false events, which the normal sensor path
// folds into readiness while the exit delay runs; waiting for the
// writes inline would stall the verb behind radio round-trips for no
// gain. Arming proceeds under the existing blocker and auto-bypass
// rules either way — the reset improves the odds, it does not gate the
// decision.
func (e *Engine) resetTriggeredMotionForArm(ctx context.Context, a *zone, by, source string) {
	if e.motionReset == nil {
		return
	}
	if len(e.triggeredMotionLocked(a.id)) == 0 {
		return
	}
	zoneID := a.id
	// The writes outlive the verb that scheduled them, so they detach
	// onto the engine lifetime rather than the caller's context — the
	// same seam the timer callbacks use. The caller's ctx bounds only
	// the decision to start, not the radio traffic.
	//nolint:contextcheck // the writes deliberately outlive the verb: they
	// run on the engine lifetime like the timer callbacks, so a caller
	// whose request ends does not abort a half-finished reset sweep.
	runCtx := context.WithoutCancel(ctx)
	if e.lifeCtx != nil {
		runCtx = e.lifeCtx
	}
	go e.ResetTriggeredMotion(runCtx, zoneID, by, source)
}
