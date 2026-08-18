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
// chain issues (notes/concepts/alarm-concept.md §15 row 19).
const alarmSourceSchedule = "schedule"

// scheduleEngine is the narrow slice of the engine the schedule
// runner needs: the current zone snapshot (to decide "already in
// mode") and the Arm verb for the AutoArm path. *engine.Engine
// satisfies it directly.
type scheduleEngine interface {
	Zone(id string) (engine.ZoneSnapshot, bool)
	Arm(ctx context.Context, zoneID string, req engine.ArmRequest) (engine.ArmResult, error)
}

// scheduleEntry is one resolved zone+schedule pair, loaded fresh from
// the zone store on every (re)build.
type scheduleEntry struct {
	zoneID   string
	zoneName string
	sched    engine.AlarmSchedule
}

// scheduleRunnerDeps wires a scheduleRunner.
type scheduleRunnerDeps struct {
	// Zones loads every zone's persisted config, which carries the
	// per-zone Schedules list (notes/concepts/alarm-concept.md §15 row 19).
	Zones engine.ZoneStore
	// Engine resolves the current zone state and drives the AutoArm
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
	// PublishFailedToArm signature so the daemon composition root can
	// wire it in one line; nil — the state of a daemon configured
	// without MQTT — keeps the failure journal-only.
	ArmFailure ArmFailureHook
}

// scheduleRunner drives the per-zone daily-time chains that back
// arm schedules and reminders (notes/concepts/alarm-concept.md §15 row 19). One
// chain per configured schedule entry: it fires at the next matching
// HH:MM/weekday, dispatches, and re-chains itself for the following
// occurrence — the same self-rechaining TimerScheduler pattern as the
// service's journal-retention chain, generalized to N independent
// chains instead of one fixed 24 h chain.
type scheduleRunner struct {
	deps scheduleRunnerDeps

	mu      sync.Mutex
	started bool
	// gen numbers the generations start() builds. A chain re-arms
	// itself after every fire, and a fire that is still running when
	// start() rebuilds the runner would otherwise re-arm with an index
	// into an entry list that no longer exists: the deleted schedule
	// keeps firing and its cancel overwrites the live entry's slot, so
	// stop() can never reach the entry that replaced it.
	gen     uint64
	cancels []func() // index-aligned with the entries built by the last start()
}

// newScheduleRunner constructs a runner over deps. Construction is
// cheap and side-effect free; start() loads the zone configs and
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
// the current zone configs. Safe to call again — e.g. from Reload
// after a config write — it cancels the previous generation of chains
// first so a stale schedule never keeps firing after its zone or
// schedule entry was edited away.
func (r *scheduleRunner) start(ctx context.Context) {
	r.stop()
	entries, err := r.loadEntries(ctx)
	if err != nil {
		r.deps.Logger.Error("alarm schedules: load zone config failed", "error", err)
		return
	}
	r.mu.Lock()
	r.gen++
	gen := r.gen
	r.started = true
	r.cancels = make([]func(), len(entries))
	r.mu.Unlock()
	for i, e := range entries {
		r.chainAt(i, gen, e)
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

// loadEntries reads every zone's persisted config and flattens its
// schedules into (zone, schedule) pairs. A malformed zone config is
// logged and skipped rather than failing the whole load (S7).
func (r *scheduleRunner) loadEntries(ctx context.Context) ([]scheduleEntry, error) {
	rows, err := r.deps.Zones.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("alarm schedules: load zones: %w", err)
	}
	var out []scheduleEntry
	for i := range rows {
		row := &rows[i]
		cfg, err := engine.ParseZoneConfig(row.ConfigJSON)
		if err != nil {
			r.deps.Logger.Error("alarm schedules: zone config malformed, skipping", "zone", row.ID, "error", err)
			continue
		}
		for _, sch := range cfg.Schedules {
			out = append(out, scheduleEntry{zoneID: row.ID, zoneName: row.Name, sched: sch})
		}
	}
	return out, nil
}

// chainAt schedules the next fire of entry e and re-chains itself
// after every firing, storing the current cancel func at cancels[idx]
// so stop() never accumulates more than one live timer per entry.
//
// gen is the start() generation the entry belongs to. stop() cannot
// reach a fire that is already running, so a chain whose fire outlives
// its generation would re-arm a schedule the operator just edited away
// and write its cancel into the slot the new generation's entry owns.
// Both halves are refused when the generation moved on.
//
//nolint:contextcheck // schedule fires run on the runner lifetime, detached from any caller ctx — mirrors the journal-retention chain in service.go
func (r *scheduleRunner) chainAt(idx int, gen uint64, e scheduleEntry) {
	hour, minute, err := parseHHMM(e.sched.Time)
	if err != nil {
		r.deps.Logger.Error("alarm schedules: invalid schedule time, skipping", "zone", e.zoneID, "time", e.sched.Time, "error", err)
		return
	}
	var arm func()
	arm = func() {
		now := r.deps.Clock.Now()
		d := nextFire(now, hour, minute, e.sched.Days).Sub(now)
		cancel := r.deps.Scheduler.Schedule(d, func() {
			r.fire(context.Background(), e)
			r.mu.Lock()
			live := r.started && r.gen == gen
			r.mu.Unlock()
			if live {
				arm()
			}
		})
		r.mu.Lock()
		live := r.started && r.gen == gen && idx < len(r.cancels)
		if live {
			r.cancels[idx] = cancel
		}
		r.mu.Unlock()
		if !live {
			// The generation moved on between scheduling and storing:
			// nothing owns this cancel any more, so cancel it here or
			// the timer outlives every handle to it.
			cancel()
		}
	}
	arm()
}

// fire runs one schedule occurrence: an zone already in the scheduled
// mode is left alone; otherwise a reminder-only schedule journals and
// publishes the reminder event, and an AutoArm schedule attempts to
// arm without force, journaling (and, when reachable, notifying) a
// failed-to-arm fault on remaining blockers.
func (r *scheduleRunner) fire(ctx context.Context, e scheduleEntry) {
	snap, ok := r.deps.Engine.Zone(e.zoneID)
	if !ok {
		return // zone removed since the chain was built; Reload rebuilds
	}
	if snap.Mode == e.sched.Mode {
		return // already in the scheduled mode
	}
	if !e.sched.AutoArm {
		r.journal(ctx, e.zoneID, hmenum.AlarmJournalClassArm, "arm_reminder", map[string]any{
			"mode": string(e.sched.Mode),
		})
		r.publish(hmevent.AlarmReminderEvent{
			Base: hmevent.NewBaseAt(r.deps.Clock.Now()), ZoneID: e.zoneID, ZoneName: e.zoneName, Mode: e.sched.Mode,
		})
		return
	}
	_, err := r.deps.Engine.Arm(ctx, e.zoneID, engine.ArmRequest{
		Mode: e.sched.Mode, Source: alarmSourceSchedule,
	})
	if err == nil {
		return
	}
	var nre *engine.NotReadyError
	if errors.As(err, &nre) {
		r.journal(ctx, e.zoneID, hmenum.AlarmJournalClassFault, "failed_to_arm", map[string]any{
			"mode": string(e.sched.Mode), "blockers": nre.Blockers,
		})
		if r.deps.ArmFailure != nil {
			r.deps.ArmFailure(e.zoneID, e.zoneName, e.sched.Mode, nre.Details)
		}
		return
	}
	// Any other refusal (unknown mode, invalid state, a code policy the
	// "schedule" source cannot satisfy, ...) is not the bounded
	// NotReadyError, so there are no blockers to report and no
	// FAILED_TO_ARM notification — the fault is journal-only.
	r.journal(ctx, e.zoneID, hmenum.AlarmJournalClassFault, "schedule_arm_failed", map[string]any{
		"mode": string(e.sched.Mode), "error": err.Error(),
	})
}

// journal appends one entry, logging (never blocking on) a journal
// failure. A nil Journal is a silent no-op.
func (r *scheduleRunner) journal(ctx context.Context, zoneID string, class hmenum.AlarmJournalClass, event string, details map[string]any) {
	if r.deps.Journal == nil {
		return
	}
	if _, err := r.deps.Journal.Append(ctx, engine.JournalEntry{
		ZoneID: zoneID, Class: class, Event: event, Actor: "engine", Source: alarmSourceSchedule, Details: details,
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
//
// Each candidate re-applies hour:minute to its own calendar date
// instead of reusing a previously normalized wall clock. now's own
// day may sit inside a spring-forward gap (the daemon's previous fire
// landed there because the requested time did not exist that day),
// and time.Date silently normalizes a gap instant forward by an hour;
// deriving every candidate from that normalized instant via AddDate
// would carry the +1h into every later day too, one calendar day
// later than the gap itself.
func nextFire(now time.Time, hour, minute int, days []int) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for i := range 8 {
		d := day.AddDate(0, 0, i)
		c := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, now.Location())
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
	d := day.AddDate(0, 0, 7)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, now.Location())
}
