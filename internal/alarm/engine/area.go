// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
}

// area is the in-memory runtime state of one alarm area: its parsed
// configuration, its member sensors, and the state-machine position
// including the single active state timer.
type area struct {
	id      string
	name    string
	cfg     AreaConfig
	sensors map[string]*sensorState

	state    hmenum.AlarmAreaState
	mode     hmenum.AlarmMode
	bypassed map[string]bool
	incident *sqlitestore.AlarmIncident

	// openAtArm records the member sensors that were active when the
	// arm completed. A restore uses it to tell "was already open when
	// armed" from "opened while the daemon was down"; live closings
	// remove entries.
	openAtArm map[string]bool
	// pendingCause is the sensor that routed the area into pending.
	pendingCause string

	// The single active state timer (exit delay, entry delay, or
	// trigger time). seq guards against stale fires after cancel.
	timerKind      string
	timerDeadline  time.Time
	timerRemaining time.Duration
	timerCancel    func()
	timerSeq       uint64

	// arm-after-closing debounce timer.
	debounceCancel func()
	debounceSeq    uint64

	// readiness is the last published per-mode verdict.
	readiness map[hmenum.AlarmMode]hmevent.AlarmModeReadiness
}

// areaContext is the persisted runtime-context document stored in
// alarm_state.context_json. Keep the field names stable.
type areaContext struct {
	OpenAtArm    []string `json:"open_at_arm,omitempty"`
	PendingCause string   `json:"pending_cause,omitempty"`
}

// cancelTimers stops the state timer and the debounce timer.
func (a *area) cancelTimers() {
	if a.timerCancel != nil {
		a.timerCancel()
		a.timerCancel = nil
	}
	a.timerKind = ""
	a.timerSeq++
	a.cancelDebounce()
}

// cancelDebounce stops only the arm-after-closing debounce timer.
func (a *area) cancelDebounce() {
	if a.debounceCancel != nil {
		a.debounceCancel()
		a.debounceCancel = nil
	}
	a.debounceSeq++
}

// encodeBypass serializes the bypass set for alarm_state.bypass_json.
func (a *area) encodeBypass() string {
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
func (a *area) encodeContext() string {
	doc := areaContext{PendingCause: a.pendingCause}
	for id := range a.openAtArm {
		doc.OpenAtArm = append(doc.OpenAtArm, id)
	}
	sort.Strings(doc.OpenAtArm)
	b, err := json.Marshal(doc)
	if err != nil {
		// invariant: areaContext always marshals.
		return "{}"
	}
	return string(b)
}

// decodeContext parses alarm_state.context_json; corrupt content
// degrades to an empty context.
func decodeContext(raw string) areaContext {
	var doc areaContext
	if raw == "" {
		return doc
	}
	_ = json.Unmarshal([]byte(raw), &doc)
	return doc
}

// AreaSnapshot is a read-only view of one area's runtime state for
// surfaces and tests.
type AreaSnapshot struct {
	ID               string
	Name             string
	State            hmenum.AlarmAreaState
	Mode             hmenum.AlarmMode
	Bypassed         []string
	IncidentID       int64
	IncidentSilenced bool
	Readiness        map[hmenum.AlarmMode]hmevent.AlarmModeReadiness
	// TimerKind and TimerRemaining describe the active countdown
	// ("" / 0 when none). Remaining is relative to the snapshot time.
	TimerKind      string
	TimerRemaining time.Duration
}

// snapshot builds an AreaSnapshot; the caller holds the engine lock.
func (a *area) snapshot(now time.Time) AreaSnapshot {
	snap := AreaSnapshot{
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
