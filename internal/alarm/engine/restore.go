// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Start loads the configured areas and sensors, bumps the boot
// counter, and restores every persisted area state per the restart
// table of docs/alarm-concept.md §10.2. It is not idempotent — call
// once per engine lifetime.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return fmt.Errorf("engine: already started")
	}

	nowMS := unixMS(e.clk.Now())
	boot, err := e.runtime.IncrementBootCount(ctx, nowMS)
	if err != nil {
		// A missing boot count degrades loop-breaker precision, not
		// safety: restore re-fires still count via the incident row.
		e.log.Error("alarm boot counter unavailable", "error", err)
	}
	e.bootCount = boot

	if err := e.loadConfig(ctx); err != nil {
		return err
	}
	for _, id := range e.sortedAreaIDs() {
		a := e.areas[id]
		row, ok, err := e.stateStore.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("engine: load persisted state for %q: %w", id, err)
		}
		if ok {
			e.restoreArea(ctx, a, row)
		} else {
			e.persist(ctx, a)
		}
		e.refreshReadiness(a)
	}
	e.started = true
	return nil
}

// loadConfig builds the runtime areas from the stores. The caller
// holds the lock.
func (e *Engine) loadConfig(ctx context.Context) error {
	areaRows, err := e.areasStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: load areas: %w", err)
	}
	sensorRows, err := e.sensorsStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: load sensors: %w", err)
	}
	e.areas = map[string]*area{}
	e.sensorIndex = map[string]string{}
	for _, row := range areaRows {
		cfg, err := ParseAreaConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("engine: area %q: %w", row.ID, err)
		}
		e.areas[row.ID] = &area{
			id:        row.ID,
			name:      row.Name,
			cfg:       cfg,
			sensors:   map[string]*sensorState{},
			state:     hmenum.AlarmAreaStateDisarmed,
			mode:      hmenum.AlarmModeDisarmed,
			bypassed:  map[string]bool{},
			openAtArm: map[string]bool{},
		}
	}
	for _, row := range sensorRows {
		a, ok := e.areas[row.AreaID]
		if !ok {
			// A sensor of a deleted area is a configuration leftover;
			// visible in logs, not fatal.
			e.log.Warn("alarm sensor references unknown area", "sensor", row.ID, "area", row.AreaID)
			continue
		}
		cfg, err := ParseSensorConfig(row.ConfigJSON)
		if err != nil {
			return fmt.Errorf("engine: sensor %q: %w", row.ID, err)
		}
		a.sensors[row.ID] = &sensorState{row: row, cfg: cfg, available: true}
		e.sensorIndex[row.ID] = row.AreaID
	}
	return nil
}

// restoreArea applies the §10.2 restore table to one persisted row.
// The caller holds the lock.
func (e *Engine) restoreArea(ctx context.Context, a *area, row sqlitestore.AlarmStateRow) {
	now := e.clk.Now()
	nowMS := unixMS(now)
	plausible := clockPlausible(nowMS, row.UpdatedAtMS)
	if !plausible {
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "implausible_clock_on_restore",
			Details: map[string]any{"persisted_at_ms": row.UpdatedAtMS, "now_ms": nowMS},
		})
	}

	a.mode = row.Mode
	a.bypassed = decodeBypass(row.BypassJSON)
	restoredCtx := decodeContext(row.ContextJSON)
	a.openAtArm = map[string]bool{}
	for _, id := range restoredCtx.OpenAtArm {
		a.openAtArm[id] = true
	}
	a.pendingCause = restoredCtx.PendingCause
	if row.IncidentID != 0 {
		if inc, ok, err := e.incidents.Get(ctx, row.IncidentID); err == nil && ok && inc.ClosedAtMS == 0 {
			a.incident = &inc
		} else if err != nil {
			e.journalFault(ctx, a, "incident_load_failed", err, row.IncidentID)
		}
	}
	timers := decodeTimers(row.TimersJSON)

	switch row.State {
	case hmenum.AlarmAreaStateDisarmed, "":
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		e.persist(ctx, a)

	case hmenum.AlarmAreaStateArmed:
		a.state = hmenum.AlarmAreaStateArmed
		e.persist(ctx, a)
		e.reEvaluateAfterRestore(ctx, a)

	case hmenum.AlarmAreaStateArming:
		e.restoreArming(ctx, a, timers, plausible, now)

	case hmenum.AlarmAreaStatePending:
		e.restorePending(ctx, a, timers, plausible, now)

	case hmenum.AlarmAreaStateTriggered:
		e.restoreTriggered(ctx, a, timers, plausible, now)

	default:
		// Unknown persisted state (downgrade?): fail safe to armed
		// visibility rather than silently disarming.
		e.journalFault(ctx, a, "unknown_persisted_state", nil, 0)
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		e.persist(ctx, a)
	}
}

// restoreArming resumes or completes an interrupted exit delay. Under
// an implausible clock the arm is never auto-completed off wall math;
// the countdown resumes with the persisted remaining duration, which
// is relative and therefore trustworthy.
func (e *Engine) restoreArming(ctx context.Context, a *area, timers []persistedTimer, plausible bool, now time.Time) {
	a.state = hmenum.AlarmAreaStateArming
	t := findTimer(timers, timerKindExit)
	mcfg := a.cfg.Modes[a.mode]
	fullExit := time.Duration(mcfg.ExitDelaySeconds) * time.Second

	remaining := fullExit
	deadlinePassed := false
	switch {
	case t == nil:
		// Corrupt/missing tuple: restart the full exit delay.
	case plausible:
		if d := time.UnixMilli(t.DeadlineMS).Sub(now); d > 0 {
			remaining = d
		} else {
			deadlinePassed = true
		}
	default:
		remaining = time.Duration(t.RemainingMS) * time.Millisecond
	}

	if deadlinePassed {
		// Complete the arm, but re-check readiness first (§10.2): a
		// blocked completion falls back to disarmed with a loud
		// journal entry — the failed-arm edge of the state machine.
		// Fresh values are pulled first; without them the re-check
		// would run against unknown sensor states and pass vacuously.
		e.refreshSensorValues(ctx, a)
		rd := a.computeReadiness(a.mode)
		var blocking []string
		for _, id := range rd.Blockers {
			if !a.bypassed[id] {
				blocking = append(blocking, id)
			}
		}
		if len(blocking) == 0 {
			e.completeArm(ctx, a, hmenum.AlarmAreaStateArming, "engine:restore", "engine")
			e.reEvaluateAfterRestore(ctx, a)
			return
		}
		from := a.state
		a.cancelTimers()
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.bypassed = map[string]bool{}
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "arm_failed_on_restore",
			Details: map[string]any{"blockers": blocking},
		})
		e.publishState(a, from, "engine:restore", "engine")
		return
	}
	if remaining <= 0 {
		remaining = time.Second
	}
	e.scheduleStateTimer(a, timerKindExit, remaining)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "arming_resumed",
		Details: map[string]any{"remaining_ms": remaining.Milliseconds(), "clock_plausible": plausible},
	})
}

// restorePending resumes or escalates an interrupted entry delay. An
// elapsed deadline escalates to triggered — better a late alarm than
// a silently swallowed one. Under an implausible clock the engine
// never auto-escalates: the area restores to armed with a journal
// warning (the conservative pre-timer state).
func (e *Engine) restorePending(ctx context.Context, a *area, timers []persistedTimer, plausible bool, now time.Time) {
	if !plausible {
		a.state = hmenum.AlarmAreaStateArmed
		a.pendingCause = ""
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "pending_demoted_implausible_clock",
		})
		e.reEvaluateAfterRestore(ctx, a)
		return
	}
	t := findTimer(timers, timerKindEntry)
	if t == nil || time.UnixMilli(t.DeadlineMS).Sub(now) <= 0 {
		cause := incidentCause{Kind: causeKindPendingElapsed, SensorID: a.pendingCause}
		if s, ok := a.sensors[a.pendingCause]; ok {
			cause.SensorName = s.row.Name
		}
		a.state = hmenum.AlarmAreaStateArmed // trigger() records the transition from an armed-side state
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "pending_elapsed_while_down",
		})
		e.trigger(ctx, a, cause, FireOptions{Restored: true})
		return
	}
	a.state = hmenum.AlarmAreaStatePending
	e.scheduleStateTimer(a, timerKindEntry, time.UnixMilli(t.DeadlineMS).Sub(now))
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "pending_resumed",
	})
}

// restoreTriggered restores an interrupted triggered phase per §10.2:
// resume outputs inside the window (counting the re-fire against the
// loop breaker), execute the post-trigger policy when the window
// elapsed while down, and keep a silenced incident silent (S3
// persistence). Under an implausible clock the engine stays triggered
// but never re-fires off untrusted wall math.
func (e *Engine) restoreTriggered(ctx context.Context, a *area, timers []persistedTimer, plausible bool, now time.Time) {
	a.state = hmenum.AlarmAreaStateTriggered
	inc := a.incident
	if inc == nil {
		// The incident row is gone (corruption): the triggered state
		// has no ledger to bound re-fires, so never re-fire; leave
		// triggered via the post-trigger policy and say so loudly.
		e.journalFault(ctx, a, "incident_lost_on_restore", nil, 0)
		e.finishTriggeredOnRestore(ctx, a, closeReasonLost)
		return
	}

	mcfg := a.cfg.Modes[a.mode]
	deadline := time.UnixMilli(inc.TriggerDeadlineMS)
	if t := findTimer(timers, timerKindTrigger); t != nil && inc.TriggerDeadlineMS == 0 {
		deadline = time.UnixMilli(t.DeadlineMS)
	}

	if !plausible {
		t := findTimer(timers, timerKindTrigger)
		remaining := mcfg.triggerDuration()
		if t != nil && t.RemainingMS > 0 {
			remaining = time.Duration(t.RemainingMS) * time.Millisecond
		}
		e.scheduleStateTimer(a, timerKindTrigger, remaining)
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "triggered_restored_implausible_clock",
			IncidentID: inc.ID,
		})
		return
	}

	if deadline.Sub(now) <= 0 {
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "trigger_window_elapsed_while_down",
			IncidentID: inc.ID,
		})
		e.finishTriggeredOnRestore(ctx, a, closeReasonPostTrigger)
		return
	}

	remaining := deadline.Sub(now)
	e.scheduleStateTimer(a, timerKindTrigger, remaining)
	e.persist(ctx, a)

	if inc.Silenced {
		// S3 persistence: a silenced incident never sounds again.
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassSilence, Event: "silenced_incident_restored",
			IncidentID: inc.ID,
		})
		return
	}

	// Restore-driven re-fire: account first; if accounting fails, do
	// not fire (the safe direction). After K re-fires the loop
	// breaker degrades the cycle to optical + notifications only.
	degraded := inc.RestoreRefires >= e.loopBreakerK
	if inc.ID != 0 {
		if err := e.incidents.IncrementRestoreRefires(ctx, inc.ID); err != nil {
			e.journalFault(ctx, a, "refire_account_failed", err, inc.ID)
			return
		}
	}
	inc.RestoreRefires++
	if degraded {
		e.journalFault(ctx, a, "restart_loop_breaker_degraded", nil, inc.ID)
	}
	if err := e.outputs.FireCycle(ctx, a.id, *inc, FireOptions{
		Cycle: inc.RetriggerCycles, Degraded: degraded, Restored: true,
	}); err != nil {
		e.journalFault(ctx, a, "output_fire_failed", err, inc.ID)
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "triggered_restored",
		IncidentID: inc.ID,
		Details:    map[string]any{"refire": inc.RestoreRefires, "degraded": degraded},
	})
}

// finishTriggeredOnRestore executes the post-trigger policy during a
// restore without firing outputs.
func (e *Engine) finishTriggeredOnRestore(ctx context.Context, a *area, closeReason string) {
	from := a.state
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	if err := e.outputs.StopAll(ctx, a.id, incID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, incID)
	}
	e.closeIncident(ctx, a, closeReason)
	if a.cfg.PostTrigger == hmenum.AlarmPostTriggerDisarm {
		a.state = hmenum.AlarmAreaStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.bypassed = map[string]bool{}
		a.openAtArm = map[string]bool{}
		e.persist(ctx, a)
		e.publishState(a, from, "engine:restore", "engine")
		return
	}
	e.completeArm(ctx, a, from, "engine:restore", "engine")
	e.reEvaluateAfterRestore(ctx, a)
}

// refreshSensorValues pulls fresh activation values into the sensor
// states without routing activations. Restore paths that make
// decisions off sensor state (readiness re-checks) call this first.
func (e *Engine) refreshSensorValues(ctx context.Context, a *area) {
	if e.reader == nil {
		return
	}
	for _, s := range a.sensors {
		if active, known := e.reader.CurrentActive(ctx, s.row); known {
			s.active = active
			s.activeKnown = true
		}
	}
}

// reEvaluateAfterRestore compares fresh sensor values against the
// open-at-arm baseline: an activation that happened while the daemon
// was down becomes a trigger or a pending, per the sensor's flags
// (§10.2). Without a SensorReader the engine keeps the persisted view
// and relies on live events.
func (e *Engine) reEvaluateAfterRestore(ctx context.Context, a *area) {
	if e.reader == nil || a.state != hmenum.AlarmAreaStateArmed {
		return
	}
	for _, id := range sortedSensorIDs(a) {
		s := a.sensors[id]
		active, known := e.reader.CurrentActive(ctx, s.row)
		if !known {
			continue
		}
		s.active = active
		s.activeKnown = true
		if !active {
			if a.openAtArm[id] {
				delete(a.openAtArm, id)
				e.persist(ctx, a)
			}
			continue
		}
		if !s.cfg.InMode(a.mode) || a.bypassed[id] || a.openAtArm[id] {
			continue
		}
		if a.state != hmenum.AlarmAreaStateArmed {
			// A previous iteration already escalated; further open
			// sensors are journaled by the live path semantics.
			continue
		}
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "activation_during_downtime",
			Details: map[string]any{"sensor_id": id},
		})
		e.routeActivation(ctx, a, s, incidentCause{
			Kind: causeKindDowntime, SensorID: id, SensorName: s.row.Name,
		})
	}
}

// findTimer returns the tuple of kind, or nil.
func findTimer(timers []persistedTimer, kind string) *persistedTimer {
	for i := range timers {
		if timers[i].Kind == kind {
			return &timers[i]
		}
	}
	return nil
}

// sortedSensorIDs returns the area's sensor IDs in stable order.
func sortedSensorIDs(a *area) []string {
	ids := make([]string, 0, len(a.sensors))
	for id := range a.sensors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
