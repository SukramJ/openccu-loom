// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// alarmSourceSchedule tags journal entries and Arm calls the schedule
// chain issues (docs/alarm-concept.md §15 row 19).
const alarmSourceSchedule = "schedule"

// scheduleEngine is the narrow slice of the engine the schedule
// runner needs: the current area snapshot (to decide "already in
// mode") and the Arm verb for the AutoArm path. *engine.Engine
// satisfies it directly.
type scheduleEngine interface {
	Area(id string) (engine.AreaSnapshot, bool)
	Arm(ctx context.Context, areaID string, req engine.ArmRequest) (engine.ArmResult, error)
}

// scheduleEntry is one resolved area+schedule pair, loaded fresh from
// the area store on every (re)build.
type scheduleEntry struct {
	areaID   string
	areaName string
	sched    engine.AlarmSchedule
}

// scheduleRunnerDeps wires a scheduleRunner.
type scheduleRunnerDeps struct {
	// Areas loads every area's persisted config, which carries the
	// per-area Schedules list (docs/alarm-concept.md §15 row 19).
	Areas engine.AreaStore
	// Engine resolves the current area state and drives the AutoArm
	// verb.
	Engine scheduleEngine
	// Journal receives "arm_reminder" and "failed_to_arm" entries via
	// the engine's Journal port. A nil Journal disables journaling.
	Journal engine.Journal
	// Publish fans hmevent.AlarmReminderEvent onto the alarm bus. A
	// nil Publish disables the event (journaling still happens).
	Publish func(hmevent.Event)
	// Scheduler backs the daily-time chains. Defaults to a
	// clock-backed TimerScheduler when nil.
	Scheduler engine.TimerScheduler
	Clock     clock.Clock
	Logger    *slog.Logger
	// ArmFailure is the FAILED_TO_ARM notification hook the AutoArm
	// path calls in addition to the journal fault, when a *NotReadyError
	// blocks the arm. It mirrors the MQTT alarm publisher's
	// PublishFailedToArm signature (cmd/openccu-loom/daemon_north.go) so
	// the daemon composition root can wire it in one line; nil (the
	// default until that wiring lands) keeps the failure journal-only.
	ArmFailure ArmFailureHook
}

// scheduleRunner drives the per-area daily-time chains that back
// arm schedules and reminders (docs/alarm-concept.md §15 row 19). One
// chain per configured schedule entry: it fires at the next matching
// HH:MM/weekday, dispatches, and re-chains itself for the following
// occurrence — the same self-rechaining TimerScheduler pattern as the
// service's journal-retention chain, generalized to N independent
// chains instead of one fixed 24 h chain.
type scheduleRunner struct {
	deps scheduleRunnerDeps

	mu      sync.Mutex
	started bool
	cancels []func() // index-aligned with the entries built by the last start()
}

// newScheduleRunner constructs a runner over deps. Construction is
// cheap and side-effect free; start() loads the area configs and
// begins chaining.
func newScheduleRunner(deps scheduleRunnerDeps) *scheduleRunner {
	if deps.Clock == nil {
		deps.Clock = clock.New()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Scheduler == nil {
		deps.Scheduler = engine.NewClockScheduler(deps.Clock)
	}
	return &scheduleRunner{deps: deps}
}

// start (re)builds every configured schedule's daily-time chain from
// the current area configs. Safe to call again — e.g. from Reload
// after a config write — it cancels the previous generation of chains
// first so a stale schedule never keeps firing after its area or
// schedule entry was edited away.
func (r *scheduleRunner) start(ctx context.Context) {
	r.stop()
	entries, err := r.loadEntries(ctx)
	if err != nil {
		r.deps.Logger.Error("alarm schedules: load area config failed", "error", err)
		return
	}
	r.mu.Lock()
	r.started = true
	r.cancels = make([]func(), len(entries))
	r.mu.Unlock()
	for i, e := range entries {
		r.chainAt(i, e)
	}
}

// stop cancels every live chain. Idempotent.
func (r *scheduleRunner) stop() {
	r.mu.Lock()
	r.started = false
	cancels := r.cancels
	r.cancels = nil
	r.mu.Unlock()
	for _, c := range cancels {
		if c != nil {
			c()
		}
	}
}

// loadEntries reads every area's persisted config and flattens its
// schedules into (area, schedule) pairs. A malformed area config is
// logged and skipped rather than failing the whole load (S7).
func (r *scheduleRunner) loadEntries(ctx context.Context) ([]scheduleEntry, error) {
	rows, err := r.deps.Areas.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("alarm schedules: load areas: %w", err)
	}
	var out []scheduleEntry
	for i := range rows {
		row := &rows[i]
		cfg, err := engine.ParseAreaConfig(row.ConfigJSON)
		if err != nil {
			r.deps.Logger.Error("alarm schedules: area config malformed, skipping", "area", row.ID, "error", err)
			continue
		}
		for _, sch := range cfg.Schedules {
			out = append(out, scheduleEntry{areaID: row.ID, areaName: row.Name, sched: sch})
		}
	}
	return out, nil
}

// chainAt schedules the next fire of entry e and re-chains itself
// after every firing, storing the current cancel func at cancels[idx]
// so stop() never accumulates more than one live timer per entry.
//
//nolint:contextcheck // schedule fires run on the runner lifetime, detached from any caller ctx — mirrors the journal-retention chain in service.go
func (r *scheduleRunner) chainAt(idx int, e scheduleEntry) {
	hour, minute, err := parseHHMM(e.sched.Time)
	if err != nil {
		r.deps.Logger.Error("alarm schedules: invalid schedule time, skipping", "area", e.areaID, "time", e.sched.Time, "error", err)
		return
	}
	var arm func()
	arm = func() {
		now := r.deps.Clock.Now()
		d := nextFire(now, hour, minute, e.sched.Days).Sub(now)
		cancel := r.deps.Scheduler.Schedule(d, func() {
			r.fire(context.Background(), e)
			r.mu.Lock()
			started := r.started
			r.mu.Unlock()
			if started {
				arm()
			}
		})
		r.mu.Lock()
		if idx < len(r.cancels) {
			r.cancels[idx] = cancel
		}
		r.mu.Unlock()
	}
	arm()
}

// fire runs one schedule occurrence: an area already in the scheduled
// mode is left alone; otherwise a reminder-only schedule journals and
// publishes the reminder event, and an AutoArm schedule attempts to
// arm without force, journaling (and, when reachable, notifying) a
// failed-to-arm fault on remaining blockers.
func (r *scheduleRunner) fire(ctx context.Context, e scheduleEntry) {
	snap, ok := r.deps.Engine.Area(e.areaID)
	if !ok {
		return // area removed since the chain was built; Reload rebuilds
	}
	if snap.Mode == e.sched.Mode {
		return // already in the scheduled mode
	}
	if !e.sched.AutoArm {
		r.journal(ctx, e.areaID, hmenum.AlarmJournalClassArm, "arm_reminder", map[string]any{
			"mode": string(e.sched.Mode),
		})
		r.publish(hmevent.AlarmReminderEvent{
			Base: hmevent.NewBaseAt(r.deps.Clock.Now()), AreaID: e.areaID, AreaName: e.areaName, Mode: e.sched.Mode,
		})
		return
	}
	_, err := r.deps.Engine.Arm(ctx, e.areaID, engine.ArmRequest{
		Mode: e.sched.Mode, Source: alarmSourceSchedule,
	})
	if err == nil {
		return
	}
	var nre *engine.NotReadyError
	if errors.As(err, &nre) {
		r.journal(ctx, e.areaID, hmenum.AlarmJournalClassFault, "failed_to_arm", map[string]any{
			"mode": string(e.sched.Mode), "blockers": nre.Blockers,
		})
		if r.deps.ArmFailure != nil {
			r.deps.ArmFailure(e.areaID, e.areaName, e.sched.Mode, nre.Blockers)
		}
		return
	}
	// Any other refusal (unknown mode, invalid state, a code policy the
	// "schedule" source cannot satisfy, ...) is not the bounded
	// NotReadyError, so there are no blockers to report and no
	// FAILED_TO_ARM notification — the fault is journal-only.
	r.journal(ctx, e.areaID, hmenum.AlarmJournalClassFault, "schedule_arm_failed", map[string]any{
		"mode": string(e.sched.Mode), "error": err.Error(),
	})
}

// journal appends one entry, logging (never blocking on) a journal
// failure. A nil Journal is a silent no-op.
func (r *scheduleRunner) journal(ctx context.Context, areaID string, class hmenum.AlarmJournalClass, event string, details map[string]any) {
	if r.deps.Journal == nil {
		return
	}
	if _, err := r.deps.Journal.Append(ctx, engine.JournalEntry{
		AreaID: areaID, Class: class, Event: event, Actor: "engine", Source: alarmSourceSchedule, Details: details,
	}); err != nil {
		r.deps.Logger.Error("alarm schedules: journal append failed", "event", event, "error", err)
	}
}

// publish fans an event through Publish; a nil Publish is a silent
// no-op.
func (r *scheduleRunner) publish(e hmevent.Event) {
	if r.deps.Publish != nil {
		r.deps.Publish(e)
	}
}

// parseHHMM parses a 24h "HH:MM" time-of-day string.
func parseHHMM(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("alarm: invalid schedule time %q", s)
	}
	hour, err = strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("alarm: invalid schedule time %q", s)
	}
	minute, err = strconv.Atoi(m)
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("alarm: invalid schedule time %q", s)
	}
	return hour, minute, nil
}

// nextFire returns the next time strictly after now that matches the
// given time-of-day and weekday filter (an empty days list matches
// every day, using Go's time.Weekday numbering — 0=Sunday..6=Saturday,
// mirroring engine.AlarmSchedule.Days). It walks forward day by day,
// bounded to a week: since days is a subset of {0..6}, a matching
// weekday always recurs within 7 days.
func nextFire(now time.Time, hour, minute int, days []int) time.Time {
	base := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	for i := range 8 {
		c := base.AddDate(0, 0, i)
		if !c.After(now) {
			continue
		}
		if len(days) == 0 || slices.Contains(days, int(c.Weekday())) {
			return c
		}
	}
	// Unreachable in practice: every weekday recurs within 7 days, so
	// the loop above always returns. Kept as a safe fallback rather
	// than a panic — a malformed Days list (e.g. every value out of
	// 0-6) degrades to "one week out" instead of wedging the chain.
	return base.AddDate(0, 0, 7)
}
