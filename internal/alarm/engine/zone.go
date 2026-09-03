// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"encoding/json"
	"sort"
	"time"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// sensorState is the in-memory runtime view of one enrolled sensor.
type sensorState struct {
	row sqlitestore.AlarmSensorRow
	cfg SensorConfig

	// active is the last observed activation value; activeKnown
	// reports whether any value has been observed yet.
	active      bool
	activeKnown bool
	// available mirrors reachability; sensors start available until
	// told otherwise.
	available bool
	// sabotage / lowBattery mirror the device health flags.
	sabotage   bool
	lowBattery bool

	// hold-time debounce: a fresh activation waits this timer out
	// before it reaches the state machine; clearing cancels it. seq
	// guards against stale fires. Deliberately not restart-persisted —
	// the window is seconds-short.
	holdCancel func()
	holdSeq    uint64
}

// cancelHold stops a running hold-time debounce timer.
func (s *sensorState) cancelHold() {
	if s.holdCancel != nil {
		s.holdCancel()
		s.holdCancel = nil
	}
	s.holdSeq++
}

// zone is the in-memory runtime state of one alarm zone: its parsed
// configuration, its member sensors, and the state-machine position
// including the single active state timer.
type zone struct {
	id      string
	name    string
	cfg     ZoneConfig
	sensors map[string]*sensorState

	state    hmenum.AlarmZoneState
	mode     hmenum.AlarmMode
	bypassed map[string]bool
	incident *sqlitestore.AlarmIncident

	// sources accumulates every data point that has contributed to the
	// running incident, oldest first. It mirrors the persisted ledger
	// so the trigger path can publish the full list without reading the
	// database back. Reset when the incident closes.
	sources []hmevent.SecuritySourceRef
	// sourceSeen deduplicates sources within the running incident: a
	// detector that re-activates must not appear twice.
	sourceSeen map[string]bool

	// openAtArm records the member sensors that were active when the
	// arm completed. A restore uses it to tell "was already open when
	// armed" from "opened while the daemon was down"; live closings
	// remove entries.
	openAtArm map[string]bool
	// pendingCause is the sensor that routed the zone into pending.
	pendingCause string
	// silencedIncidentID mirrors the silenced flag of the open
	// incident into the state row (context_json): a second,
	// independent persistence path so a failed incident write cannot
	// cost the silence across a restart (S3 durability).
	silencedIncidentID int64

	// The single active state timer (exit delay, entry delay, or
	// trigger time). seq guards against stale fires after cancel.
	timerKind      string
	timerDeadline  time.Time
	timerRemaining time.Duration
	timerCancel    func()
	timerSeq       uint64

	// preTriggerState / preTriggerMode record the state an always-on
	// (hazard/panic) incident interrupted. Always-on incidents drive
	// the panel to triggered (visible everywhere) but return to the
	// prior state on post-trigger, not into armed. An empty
	// preTriggerState marks a normal intrusion incident.
	preTriggerState hmenum.AlarmZoneState
	preTriggerMode  hmenum.AlarmMode
	// preAlarm reports that the open incident is still in its pre-alarm
	// phase (only pre-alarm output classes have fired). It survives a
	// restart via context_json; a restore treats it as a full trigger.
	preAlarm bool

	// arm-after-closing debounce timer.
	debounceCancel func()
	debounceSeq    uint64

	// auto-rearm timer: runs while disarmed after a post-trigger
	// disarm, re-arming to autoRearmMode after a quiet period. It is a
	// separate timer from the state timer (the zone is disarmed, which
	// has no state countdown) and survives a restart via timers_json +
	// context_json.
	autoRearmCancel   func()
	autoRearmSeq      uint64
	autoRearmDeadline time.Time
	autoRearmMode     hmenum.AlarmMode

	// countdown tick chain (1 Hz while arming/pending).
	tickCancel func()
	tickSeq    uint64

	// groupHits records recent activations per cross-zoning group:
	// group name → sensor ID → activation time. Entries beyond the
	// cross-zone window are pruned lazily on the next hit.
	// Deliberately not restart-persisted — the window is
	// seconds-short.
	groupHits map[string]map[string]time.Time

	// readiness is the last published per-mode verdict.
	readiness map[hmenum.AlarmMode]hmevent.AlarmModeReadiness

	// walk is the running walk-test session, nil when none.
	walk *walkSession
}

// sourcesCopy returns a defensive copy of the incident's source list.
// Callers publish it onto the event bus, where a shared backing array
// would let a later append mutate an already-delivered event.
func (a *zone) sourcesCopy() []hmevent.SecuritySourceRef {
	if len(a.sources) == 0 {
		return nil
	}
	out := make([]hmevent.SecuritySourceRef, len(a.sources))
	copy(out, a.sources)
	return out
}

// resetSources clears the accumulator when an incident closes, so the
// next incident starts from an empty list.
func (a *zone) resetSources() {
	a.sources = nil
	a.sourceSeen = nil
}

// zoneContext is the persisted runtime-context document stored in
// alarm_state.context_json. Keep the field names stable.
type zoneContext struct {
	OpenAtArm    []string `json:"open_at_arm,omitempty"`
	PendingCause string   `json:"pending_cause,omitempty"`
	// SilencedIncidentID is the redundant silence marker (S3): the
	// open incident this zone has silenced, 0 when none.
	SilencedIncidentID int64 `json:"silenced_incident_id,omitempty"`
	// PreTriggerState / PreTriggerMode persist the state an always-on
	// incident interrupted, so a restore returns to the prior state.
	PreTriggerState hmenum.AlarmZoneState `json:"pre_trigger_state,omitempty"`
	PreTriggerMode  hmenum.AlarmMode      `json:"pre_trigger_mode,omitempty"`
	// PreAlarm persists that the open incident is in its pre-alarm
	// phase.
	PreAlarm bool `json:"pre_alarm,omitempty"`
	// AutoRearmMode persists the mode a pending auto-rearm will arm.
	AutoRearmMode hmenum.AlarmMode `json:"auto_rearm_mode,omitempty"`
}

// resetToDisarmed puts the zone into the disarmed shape: disarmed
// state and mode plus the full residue set an armed or triggered
// episode can leave behind. It is the single definition of what
// "returned to disarmed" means, because the residue fields are
// persisted (encodeContext writes pendingCause, preTriggerState,
// preTriggerMode and preAlarm into context_json), and a leftover
// preTriggerState re-routes the next ordinary trigger through the
// always-on exit. Timers are the caller's business — a post-trigger
// disarm cancels the state timers and then schedules the auto-rearm.
func (a *zone) resetToDisarmed() {
	a.state = hmenum.AlarmZoneStateDisarmed
	a.mode = hmenum.AlarmModeDisarmed
	a.bypassed = map[string]bool{}
	a.openAtArm = map[string]bool{}
	a.pendingCause = ""
	a.preTriggerState = ""
	a.preTriggerMode = ""
	a.preAlarm = false
}

// cancelTimers stops the state timer, the debounce timer, and the
// countdown tick chain.
func (a *zone) cancelTimers() {
	if a.timerCancel != nil {
		a.timerCancel()
		a.timerCancel = nil
	}
	a.timerKind = ""
	a.timerSeq++
	a.cancelDebounce()
	a.cancelTicks()
}

// cancelTicks stops only the countdown tick chain.
func (a *zone) cancelTicks() {
	if a.tickCancel != nil {
		a.tickCancel()
		a.tickCancel = nil
	}
	a.tickSeq++
}

// cancelDebounce stops only the arm-after-closing debounce timer.
func (a *zone) cancelDebounce() {
	if a.debounceCancel != nil {
		a.debounceCancel()
		a.debounceCancel = nil
	}
	a.debounceSeq++
}

// cancelAutoRearm stops any pending auto-rearm timer and forgets its
// target mode. It is kept separate from cancelTimers so a post-trigger
// disarm can schedule the auto-rearm right after cancelling the state
// timers.
func (a *zone) cancelAutoRearm() {
	if a.autoRearmCancel != nil {
		a.autoRearmCancel()
		a.autoRearmCancel = nil
	}
	a.autoRearmSeq++
	a.autoRearmMode = ""
}

// encodeBypass serializes the bypass set for alarm_state.bypass_json.
func (a *zone) encodeBypass() string {
	ids := make([]string, 0, len(a.bypassed))
	for id := range a.bypassed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, err := json.Marshal(ids)
	if err != nil {
		// invariant: a []string always marshals.
		return "[]"
	}
	return string(b)
}

// decodeBypass parses alarm_state.bypass_json; corrupt content
// degrades to an empty set.
func decodeBypass(raw string) map[string]bool {
	out := map[string]bool{}
	if raw == "" {
		return out
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return out
	}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// encodeContext serializes the runtime context for
// alarm_state.context_json.
func (a *zone) encodeContext() string {
	doc := zoneContext{
		PendingCause:       a.pendingCause,
		SilencedIncidentID: a.silencedIncidentID,
		PreTriggerState:    a.preTriggerState,
		PreTriggerMode:     a.preTriggerMode,
		PreAlarm:           a.preAlarm,
		AutoRearmMode:      a.autoRearmMode,
	}
	for id := range a.openAtArm {
		doc.OpenAtArm = append(doc.OpenAtArm, id)
	}
	sort.Strings(doc.OpenAtArm)
	b, err := json.Marshal(doc)
	if err != nil {
		// invariant: zoneContext always marshals.
		return "{}"
	}
	return string(b)
}

// decodeContext parses alarm_state.context_json; corrupt content
// degrades to an empty context.
func decodeContext(raw string) zoneContext {
	var doc zoneContext
	if raw == "" {
		return doc
	}
	_ = json.Unmarshal([]byte(raw), &doc)
	return doc
}

// ZoneSnapshot is a read-only view of one zone's runtime state for
// surfaces and tests.
type ZoneSnapshot struct {
	ID               string
	Name             string
	State            hmenum.AlarmZoneState
	Mode             hmenum.AlarmMode
	Bypassed         []string
	IncidentID       int64
	IncidentSilenced bool
	Readiness        map[hmenum.AlarmMode]hmevent.AlarmModeReadiness
	// TimerKind and TimerRemaining describe the active countdown
	// ("" / 0 when none). Remaining is relative to the snapshot time.
	TimerKind      string
	TimerRemaining time.Duration
	// TimerTotal is the countdown's full length as the engine armed it —
	// what a progress display divides TimerRemaining by.
	//
	// It is carried here because it is not derivable from the zone config: an
	// entry delay honours a per-sensor override (ModeConfig.entryDelay), so a
	// consumer reading the zone's EntryDelaySeconds computes a total the
	// engine never used, and the bar it draws runs at the wrong rate or jumps.
	TimerTotal time.Duration
}

// snapshot builds an ZoneSnapshot; the caller holds the engine lock.
func (a *zone) snapshot(now time.Time) ZoneSnapshot {
	snap := ZoneSnapshot{
		ID:    a.id,
		Name:  a.name,
		State: a.state,
		Mode:  a.mode,
	}
	for id := range a.bypassed {
		snap.Bypassed = append(snap.Bypassed, id)
	}
	sort.Strings(snap.Bypassed)
	if a.incident != nil {
		snap.IncidentID = a.incident.ID
		snap.IncidentSilenced = a.incident.Silenced
	}
	if a.timerCancel != nil {
		snap.TimerKind = a.timerKind
		snap.TimerTotal = a.timerRemaining
		if r := a.timerDeadline.Sub(now); r > 0 {
			snap.TimerRemaining = r
		}
	}
	if len(a.readiness) > 0 {
		snap.Readiness = make(map[hmenum.AlarmMode]hmevent.AlarmModeReadiness, len(a.readiness))
		for m, r := range a.readiness {
			snap.Readiness[m] = r
		}
	}
	return snap
}
