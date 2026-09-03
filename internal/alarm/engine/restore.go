// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// resumedTimerFloor is the shortest delay a countdown resumed at boot
// is rescheduled with: an already-elapsed deadline still fires, but one
// tick later, so the restore path never schedules a zero-length timer.
const resumedTimerFloor = time.Second

// flooredRemaining applies resumedTimerFloor. The restore paths that
// resume a countdown share it; the paths that escalate on an elapsed
// deadline (restorePending, restoreTriggered) deliberately do not floor.
func flooredRemaining(d time.Duration) time.Duration {
	if d <= 0 {
		return resumedTimerFloor
	}
	return d
}

// Start loads the configured zones and sensors, bumps the boot
// counter, and restores every persisted zone state per the restart
// table of notes/concepts/alarm-concept.md §10.2. It is not idempotent — call
// once per engine lifetime.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return errors.New("engine: already started")
	}
	// ctx bounds the engine lifetime: timer-driven work (countdown
	// expiries, debounce completions) derives from it, never from the
	// request that scheduled the countdown.
	e.lifeCtx = ctx

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
	for _, id := range e.sortedZoneIDs() {
		a := e.zones[id]
		// Seed every zone's sensor values before its state is restored.
		// A sensor whose value is unknown is invisible to the blocker
		// policy — it only classifies a *known* active sensor — so a
		// zone that boots without the seed reports ready to arm while a
		// contact stands open, and stays wrong until that contact
		// happens to push. The armed-side restore paths re-read the
		// same values; this pass is what covers the disarmed ones.
		e.refreshSensorValues(ctx, a)
		row, ok, err := e.stateStore.Get(ctx, id)
		if err != nil {
			// A partial start must not leave live countdowns behind:
			// earlier zones may already have scheduled timers that
			// would fire on an engine nobody owns.
			e.teardownLocked()
			return fmt.Errorf("engine: load persisted state for %q: %w", id, err)
		}
		if ok {
			e.restoreZone(ctx, a, row)
		} else {
			e.persist(ctx, a)
		}
		e.refreshReadiness(a)
	}
	e.started = true
	return nil
}

// teardownLocked cancels every scheduled timer and drops the runtime
// zones after a failed Start. The caller holds the lock.
func (e *Engine) teardownLocked() {
	for _, a := range e.zones {
		a.cancelTimers()
	}
	e.zones = map[string]*zone{}
	e.sensorIndex = map[string]string{}
}

// loadConfig builds the runtime zones from the stores. The caller
// holds the lock.
func (e *Engine) loadConfig(ctx context.Context) error {
	zoneRows, err := e.zonesStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: load zones: %w", err)
	}
	sensorRows, err := e.sensorsStore.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("engine: load sensors: %w", err)
	}
	e.zones = map[string]*zone{}
	e.sensorIndex = map[string]string{}
	for i := range zoneRows {
		row := &zoneRows[i]
		cfg, err := ParseZoneConfig(row.ConfigJSON)
		if err != nil {
			// One poisoned row must not brick every other zone: skip
			// it loudly (S7) — the zone simply does not exist until
			// its configuration is repaired.
			e.log.Error("alarm zone config unparseable — zone skipped", "zone", row.ID, "error", err)
			if _, jerr := e.journal.Append(ctx, JournalEntry{
				ZoneID: row.ID, Class: hmenum.AlarmJournalClassFault,
				Event: "zone_config_unparseable", Details: map[string]any{"error": err.Error()},
			}); jerr != nil {
				e.log.Error("alarm journal append failed", "error", jerr)
			}
			continue
		}
		e.zones[row.ID] = &zone{
			id:        row.ID,
			name:      row.Name,
			cfg:       cfg,
			sensors:   map[string]*sensorState{},
			state:     hmenum.AlarmZoneStateDisarmed,
			mode:      hmenum.AlarmModeDisarmed,
			bypassed:  map[string]bool{},
			openAtArm: map[string]bool{},
		}
	}
	for i := range sensorRows {
		row := &sensorRows[i]
		a, ok := e.zones[row.ZoneID]
		if !ok {
			// A sensor of a deleted zone is a configuration leftover;
			// visible in logs, not fatal.
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
		a.sensors[row.ID] = &sensorState{row: *row, cfg: cfg, available: true}
		e.sensorIndex[row.ID] = row.ZoneID
	}
	return nil
}

// restoreZone applies the §10.2 restore table to one persisted row.
// The caller holds the lock.
func (e *Engine) restoreZone(ctx context.Context, a *zone, row sqlitestore.AlarmStateRow) {
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
	a.silencedIncidentID = restoredCtx.SilencedIncidentID
	a.preTriggerState = restoredCtx.PreTriggerState
	a.preTriggerMode = restoredCtx.PreTriggerMode
	a.preAlarm = restoredCtx.PreAlarm
	a.autoRearmMode = restoredCtx.AutoRearmMode
	if row.IncidentID != 0 {
		if inc, ok, err := e.incidents.Get(ctx, row.IncidentID); err == nil && ok && inc.ClosedAtMS == 0 {
			a.incident = &inc
		} else if err != nil {
			e.journalFault(ctx, a, "incident_load_failed", err, row.IncidentID)
		}
	}
	// An open incident the state row does not reference is a crash
	// leftover (created before the state persist landed): adopt it
	// when the row says triggered, close it otherwise — orphans must
	// not accumulate.
	if a.incident == nil {
		if orphan, ok, err := e.incidents.GetOpenByZone(ctx, a.id); err != nil {
			e.journalFault(ctx, a, "incident_load_failed", err, 0)
		} else if ok {
			if row.State == hmenum.AlarmZoneStateTriggered {
				a.incident = &orphan
				e.journalEntry(ctx, a, JournalEntry{
					Class: hmenum.AlarmJournalClassFault, Event: "orphan_incident_adopted",
					IncidentID: orphan.ID,
				})
			} else {
				if err := e.incidents.Close(ctx, orphan.ID, unixMS(now), closeReasonLost); err != nil {
					e.journalFault(ctx, a, "incident_persist_failed", err, orphan.ID)
				}
				e.journalEntry(ctx, a, JournalEntry{
					Class: hmenum.AlarmJournalClassFault, Event: "orphan_incident_closed",
					IncidentID: orphan.ID,
				})
			}
		}
	}
	// S3 durability: the state-row marker and the incident flag are
	// two independent records of a silence — honor either, and heal
	// the incident row when only the marker survived.
	if a.incident != nil && !a.incident.Silenced && a.silencedIncidentID == a.incident.ID {
		a.incident.Silenced = true
		if err := e.incidents.MarkSilenced(ctx, a.incident.ID, unixMS(now), "engine:restore"); err != nil {
			e.journalFault(ctx, a, "silence_persist_failed", err, a.incident.ID)
		}
	}
	if a.incident == nil || a.silencedIncidentID != a.incident.ID {
		a.silencedIncidentID = 0
	}
	// The accumulator is per-process; the ledger is what carries the
	// incident's sources across the restart.
	e.restoreSources(ctx, a)
	timers := decodeTimers(row.TimersJSON)

	switch row.State {
	case hmenum.AlarmZoneStateDisarmed, "":
		a.state = hmenum.AlarmZoneStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		a.preTriggerState = ""
		a.preTriggerMode = ""
		a.preAlarm = false
		e.restoreAutoRearm(ctx, a, timers, plausible, now)
		e.persist(ctx, a)

	case hmenum.AlarmZoneStateArmed:
		a.state = hmenum.AlarmZoneStateArmed
		e.persist(ctx, a)
		e.reEvaluateAfterRestore(ctx, a)

	case hmenum.AlarmZoneStateArming:
		e.restoreArming(ctx, a, timers, plausible, now)

	case hmenum.AlarmZoneStatePending:
		e.restorePending(ctx, a, timers, plausible, now)

	case hmenum.AlarmZoneStateTriggered:
		e.restoreTriggered(ctx, a, timers, plausible, now)

	default:
		// Unknown persisted state — a downgrade wrote a token this build
		// cannot interpret. Come up disarmed rather than guess an
		// armed-side position whose mode, delays, and output policy this
		// build cannot reconstruct: the same "disarm loudly instead of
		// guessing" direction Reload takes when a zone's active mode
		// disappears from the configuration. The fault entry is what
		// makes the downgrade visible to the operator — the zone is
		// unprotected until it is armed again.
		e.journalFault(ctx, a, "unknown_persisted_state", nil, 0)
		a.state = hmenum.AlarmZoneStateDisarmed
		a.mode = hmenum.AlarmModeDisarmed
		e.persist(ctx, a)
	}
}

// restoreArming resumes or completes an interrupted exit delay. Under
// an implausible clock the arm is never auto-completed off wall math;
// the countdown resumes with the persisted remaining duration, which
// is relative and therefore trustworthy.
func (e *Engine) restoreArming(ctx context.Context, a *zone, timers []persistedTimer, plausible bool, now time.Time) {
	a.state = hmenum.AlarmZoneStateArming
	t := findTimer(timers, TimerKindExit)
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
		// bypass_auto sensors convert their blocking condition into a
		// recorded exclusion here too.
		e.refreshSensorValues(ctx, a)
		rd, autoBypass := a.readinessDetail(a.mode)
		for _, id := range autoBypass {
			if !a.bypassed[id] {
				a.bypassed[id] = true
				e.journalEntry(ctx, a, JournalEntry{
					Class: hmenum.AlarmJournalClassBypass, Event: "sensor_bypassed",
					Actor: "engine:restore", Details: map[string]any{"sensor_id": id},
				})
			}
		}
		var blocking []string
		for _, id := range rd.Blockers {
			if !a.bypassed[id] {
				blocking = append(blocking, id)
			}
		}
		if len(blocking) == 0 {
			e.completeArm(ctx, a, hmenum.AlarmZoneStateArming, "engine:restore", "engine")
			e.reEvaluateAfterRestore(ctx, a)
			return
		}
		from := a.state
		a.cancelTimers()
		a.state = hmenum.AlarmZoneStateDisarmed
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
	remaining = flooredRemaining(remaining)
	e.scheduleStateTimer(a, TimerKindExit, remaining)
	// The tick chain is a separate timer from the state timer, and it
	// is the only producer of AlarmCountdownEvent and of the countdown
	// chirps. Resuming the state timer alone left a restored countdown
	// silent for the rest of the delay: no live countdown on the WS
	// plane and, with Outputs.CountdownTicks on, no exit chirps.
	e.startTicks(a, TimerKindExit)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "arming_resumed",
		Details: map[string]any{"remaining_ms": remaining.Milliseconds(), "clock_plausible": plausible},
	})
}

// restorePending resumes or escalates an interrupted entry delay. An
// elapsed deadline escalates to triggered — better a late alarm than
// a silently swallowed one. Under an implausible clock the engine
// never auto-escalates: the zone restores to armed with a journal
// warning (the conservative pre-timer state).
func (e *Engine) restorePending(ctx context.Context, a *zone, timers []persistedTimer, plausible bool, now time.Time) {
	if !plausible {
		a.state = hmenum.AlarmZoneStateArmed
		a.pendingCause = ""
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "pending_demoted_implausible_clock",
		})
		e.reEvaluateAfterRestore(ctx, a)
		return
	}
	t := findTimer(timers, TimerKindEntry)
	if t == nil || time.UnixMilli(t.DeadlineMS).Sub(now) <= 0 {
		cause := pendingElapsedCause(a)
		a.state = hmenum.AlarmZoneStateArmed // trigger() records the transition from an armed-side state
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "pending_elapsed_while_down",
		})
		e.trigger(ctx, a, cause, FireOptions{Restored: true})
		return
	}
	a.state = hmenum.AlarmZoneStatePending
	e.scheduleStateTimer(a, TimerKindEntry, time.UnixMilli(t.DeadlineMS).Sub(now))
	// Same as the exit case, with more at stake: the entry-warning
	// chirps are what tell a returning resident to enter their code
	// before the zone triggers.
	e.startTicks(a, TimerKindEntry)
	e.persist(ctx, a)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "pending_resumed",
	})
}

// stopSilencedIncidentOnRestore issues the S3 counter-stop for a
// silenced incident on restore: a silenced incident never sounds
// again, on either the plausible- or implausible-clock path. The
// counter-stop covers a crash that landed between the silence persist
// and the output stop — stopping again is free.
func (e *Engine) stopSilencedIncidentOnRestore(ctx context.Context, a *zone, inc *sqlitestore.AlarmIncident) {
	if err := e.outputs.StopAll(ctx, a.id, inc.ID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, inc.ID)
	}
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassSilence, Event: "silenced_incident_restored",
		IncidentID: inc.ID,
	})
}

// restoreTriggered restores an interrupted triggered phase per §10.2:
// resume outputs inside the window (counting the re-fire against the
// loop breaker), execute the post-trigger policy when the window
// elapsed while down, and keep a silenced incident silent (S3
// persistence). Under an implausible clock the engine stays triggered
// but never re-fires off untrusted wall math.
func (e *Engine) restoreTriggered(ctx context.Context, a *zone, timers []persistedTimer, plausible bool, now time.Time) {
	a.state = hmenum.AlarmZoneStateTriggered
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
	// An always-on (hazard/panic) incident re-fires with its class
	// policy and returns to the state it interrupted, not into armed.
	alwaysOn := a.preTriggerState != ""
	policy := mcfg.Outputs
	if alwaysOn {
		policy = alwaysOnPolicyForIncident(a, inc)
	}
	// A persisted pre-alarm phase is restored conservatively as a full
	// trigger: a fresh full window with full outputs, never re-entering
	// the pre-alarm phase.
	wasPreAlarm := a.preAlarm
	if wasPreAlarm {
		a.preAlarm = false
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "pre_alarm_restored_as_full",
			IncidentID: inc.ID,
		})
	}

	deadline := time.UnixMilli(inc.TriggerDeadlineMS)
	if t := findTimer(timers, TimerKindTrigger); t != nil && inc.TriggerDeadlineMS == 0 {
		deadline = time.UnixMilli(t.DeadlineMS)
	}
	if wasPreAlarm {
		deadline = now.Add(mcfg.triggerDuration())
	}

	if !plausible {
		t := findTimer(timers, TimerKindTrigger)
		remaining := mcfg.triggerDuration()
		if t != nil && t.RemainingMS > 0 {
			remaining = time.Duration(t.RemainingMS) * time.Millisecond
		}
		e.scheduleStateTimer(a, TimerKindTrigger, remaining)
		e.persist(ctx, a)
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassFault, Event: "triggered_restored_implausible_clock",
			IncidentID: inc.ID,
		})
		if inc.Silenced {
			e.stopSilencedIncidentOnRestore(ctx, a, inc)
		}
		return
	}

	if deadline.Sub(now) <= 0 {
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "trigger_window_elapsed_while_down",
			IncidentID: inc.ID,
		})
		if alwaysOn {
			e.finishAlwaysOn(ctx, a, "always_on_elapsed", "engine:restore")
			return
		}
		e.finishTriggeredOnRestore(ctx, a, closeReasonPostTrigger)
		return
	}

	remaining := deadline.Sub(now)
	e.scheduleStateTimer(a, TimerKindTrigger, remaining)
	e.persist(ctx, a)

	if inc.Silenced {
		e.stopSilencedIncidentOnRestore(ctx, a, inc)
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
	e.fireCycle(ctx, a, *inc, FireOptions{
		Cycle: inc.RetriggerCycles, Degraded: degraded, Restored: true,
		Policy: policy,
	})
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassTrigger, Event: "triggered_restored",
		IncidentID: inc.ID,
		Details:    map[string]any{"refire": inc.RestoreRefires, "degraded": degraded},
	})
}

// finishTriggeredOnRestore executes the post-trigger policy during a
// restore without firing outputs.
func (e *Engine) finishTriggeredOnRestore(ctx context.Context, a *zone, closeReason string) {
	from := a.state
	incID := int64(0)
	if a.incident != nil {
		incID = a.incident.ID
	}
	if err := e.outputs.StopAll(ctx, a.id, incID); err != nil {
		e.journalFault(ctx, a, "output_stop_failed", err, incID)
	}
	e.closeIncident(ctx, a, closeReason)
	// The incident that recorded the always-on interruption is closed,
	// so its pre-trigger tuple is spent. It has to go on both branches
	// below: a persisted Triggered zone whose incident row is
	// unrecoverable reaches this function with the tuple still set
	// (restoreTriggered's inc == nil arm), and a residual
	// preTriggerState re-routes the next ordinary trigger through
	// finishAlwaysOn.
	a.preTriggerState = ""
	a.preTriggerMode = ""
	if a.cfg.PostTrigger == hmenum.AlarmPostTriggerDisarm {
		e.disarmAfterTrigger(ctx, a, from, "engine:restore")
		return
	}
	e.completeArm(ctx, a, from, "engine:restore", "engine")
	e.reEvaluateAfterRestore(ctx, a)
}

// restoreAutoRearm resumes an interrupted auto-rearm quiet period on a
// disarmed zone: reschedule the remaining wait, or attempt the rearm
// when the quiet period elapsed while the daemon was down. Under an
// implausible clock it never fires off wall math and resumes the
// persisted remaining duration. The caller holds the lock.
func (e *Engine) restoreAutoRearm(ctx context.Context, a *zone, timers []persistedTimer, plausible bool, now time.Time) {
	if !a.autoRearmMode.Armed() {
		a.autoRearmMode = ""
		return
	}
	if _, ok := a.cfg.Modes[a.autoRearmMode]; !ok {
		a.autoRearmMode = ""
		return
	}
	t := findTimer(timers, TimerKindAutoRearm)
	var remaining time.Duration
	switch {
	case t == nil:
		remaining = time.Duration(a.cfg.AutoRearmSeconds) * time.Second
	case plausible:
		if d := time.UnixMilli(t.DeadlineMS).Sub(now); d > 0 {
			remaining = d
		} else {
			// The quiet period elapsed while down: attempt the rearm now.
			e.onAutoRearmElapsed(ctx, a)
			return
		}
	default:
		remaining = time.Duration(t.RemainingMS) * time.Millisecond
	}
	remaining = flooredRemaining(remaining)
	e.scheduleAutoRearm(a, a.autoRearmMode, remaining)
	e.journalEntry(ctx, a, JournalEntry{
		Class: hmenum.AlarmJournalClassArm, Event: "auto_rearm_resumed",
		Details: map[string]any{"remaining_ms": remaining.Milliseconds(), "clock_plausible": plausible},
	})
}

// refreshSensorValues pulls fresh activation values into the sensor
// states without routing activations. Restore paths that make
// decisions off sensor state (readiness re-checks) call this first.
func (e *Engine) refreshSensorValues(ctx context.Context, a *zone) {
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
func (e *Engine) reEvaluateAfterRestore(ctx context.Context, a *zone) {
	if e.reader == nil || a.state != hmenum.AlarmZoneStateArmed {
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
		if a.state != hmenum.AlarmZoneStateArmed {
			// A previous iteration already escalated; further open
			// sensors are journaled by the live path semantics.
			continue
		}
		e.journalEntry(ctx, a, JournalEntry{
			Class: hmenum.AlarmJournalClassTrigger, Event: "activation_during_downtime",
			Details: map[string]any{"sensor_id": id},
		})
		e.routeActivation(ctx, a, s, causeFromSensor(causeKindDowntime, s.row))
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

// sortedSensorIDs returns the zone's sensor IDs in stable order.
func sortedSensorIDs(a *zone) []string {
	ids := make([]string, 0, len(a.sensors))
	for id := range a.sensors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
