// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Cross-zoning group rule (notes/concepts/alarm-concept.md §6.2): a grouped
// sensor only triggers when a second distinct member of the same
// group activates within the window. Kills single-PIR false alarms.
const (
	// crossZoneMinSensors is the number of distinct group members that
	// must activate within the window before the group fires.
	crossZoneMinSensors = 2
	// crossZoneWindow bounds how far apart the member activations may
	// lie.
	crossZoneWindow = 60 * time.Second
)

// gateSensorActivation applies the per-sensor noise filters — hold
// time, then the cross-zoning group — before a fresh member-sensor
// activation reaches the state machine. The caller holds the lock and
// has already filtered walk tests, always-on sensors, bypasses, and
// mode membership (always-on hazard/panic sensors never pass through
// here, so neither filter can delay a hazard or panic alarm). Both
// windows are seconds-short and deliberately not restart-persisted.
func (e *Engine) gateSensorActivation(ctx context.Context, a *zone, s *sensorState, sensorID string) {
	if d := time.Duration(s.cfg.HoldTimeSeconds) * time.Second; d > 0 {
		e.scheduleHold(a, s, sensorID, d)
		return
	}
	e.gateCrossZone(ctx, a, s, sensorID)
}

// scheduleHold arms the hold-time debounce: the activation only counts
// when the sensor is still active after the hold window. Clearing the
// sensor cancels the timer (HandleSensorEvent's inactive path). The
// caller holds the lock.
//
//nolint:contextcheck // timer fires deliberately detach from the scheduling caller's ctx (see lifeCtx)
func (e *Engine) scheduleHold(a *zone, s *sensorState, sensorID string, d time.Duration) {
	s.cancelHold()
	seq := s.holdSeq
	zoneID := a.id
	s.holdCancel = e.sched.Schedule(d, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		aa, ok := e.zones[zoneID]
		if !ok {
			return
		}
		ss, ok := aa.sensors[sensorID]
		if !ok || ss.holdSeq != seq {
			return
		}
		ss.holdCancel = nil
		// The activation must still be standing, and the dispatch
		// preconditions may have changed while holding.
		if !ss.active || !ss.cfg.InMode(aa.mode) || aa.bypassed[sensorID] {
			return
		}
		e.gateCrossZone(e.lifeCtx, aa, ss, sensorID)
	})
}

// gateCrossZone applies the cross-zoning group rule and dispatches the
// activation when it passes. A suppressed first hit is journaled — a
// single-sensor activation of an armed zone must stay visible (S7),
// it just does not sound the sirens on its own. The caller holds the
// lock.
func (e *Engine) gateCrossZone(ctx context.Context, a *zone, s *sensorState, sensorID string) {
	group := s.cfg.Group
	if group == "" {
		e.dispatchSensorActivation(ctx, a, s, sensorID)
		return
	}
	now := e.clk.Now()
	if a.groupHits == nil {
		a.groupHits = map[string]map[string]time.Time{}
	}
	hits := a.groupHits[group]
	if hits == nil {
		hits = map[string]time.Time{}
		a.groupHits[group] = hits
	}
	for id, at := range hits {
		if now.Sub(at) > crossZoneWindow {
			delete(hits, id)
		}
	}
	hits[sensorID] = now
	if len(hits) >= crossZoneMinSensors {
		delete(a.groupHits, group)
		e.dispatchSensorActivation(ctx, a, s, sensorID)
		return
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "cross_zone_first_hit",
		Details: map[string]any{"sensor_id": sensorID, "group": group},
	})
}
