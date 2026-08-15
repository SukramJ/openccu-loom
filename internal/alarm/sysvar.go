// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// sysvarStates is the fixed value list of the mirror sysvar
// (notes/concepts/alarm-concept.md §13.5). Order is part of the persisted enum;
// keep it stable. The German labels follow the CCU convention so
// existing programs read naturally.
var sysvarStates = []string{
	"Unscharf",          // 0 — disarmed
	"Hüllschutz",        // 1 — perimeter
	"Vollschutz",        // 2 — full
	"Nachtschutz",       // 3 — night
	"Urlaub",            // 4 — vacation
	"Benutzerdefiniert", // 5 — custom
	"Alarm",             // 6 — triggered
}

// sysvarIndexByMode maps engine modes onto the value-list positions.
var sysvarIndexByMode = map[hmenum.AlarmMode]int{
	hmenum.AlarmModeDisarmed:  0,
	hmenum.AlarmModePerimeter: 1,
	hmenum.AlarmModeFull:      2,
	hmenum.AlarmModeNight:     3,
	hmenum.AlarmModeVacation:  4,
	hmenum.AlarmModeCustom:    5,
}

const sysvarAlarmIndex = 6

// sysvarMirror maintains the optional CCU sysvar mirror per zone:
// outbound state export on every transition, and inbound intents that
// are arm-only by default — a sysvar write can never disarm or
// silence a code-protected zone (§13.5; the CCU auth model is far
// weaker than loom's).
type sysvarMirror struct {
	svc *Service

	mu          sync.Mutex
	ensured     map[string]bool // central|name → enum ensured
	lastWritten map[string]int  // central|name → last exported index
}

func newSysvarMirror(svc *Service) *sysvarMirror {
	return &sysvarMirror{svc: svc, ensured: map[string]bool{}, lastWritten: map[string]int{}}
}

// mirrorTargets returns the sysvar-mirror outputs of an zone.
func (m *sysvarMirror) mirrorTargets(zoneID string) []mirrorTarget {
	rows, err := m.svc.stores.Outputs.ListByZone(context.Background(), zoneID)
	if err != nil {
		m.svc.log.Error("alarm sysvar mirror: load outputs", "zone", zoneID, "error", err)
		return nil
	}
	var out []mirrorTarget
	for i := range rows {
		row := &rows[i]
		if row.Class != hmenum.AlarmOutputClassSysvarMirror {
			continue
		}
		cfg, err := parseMirrorConfig(row.ConfigJSON)
		if err != nil || cfg.SysvarName == "" {
			continue
		}
		out = append(out, mirrorTarget{
			central:     row.CentralName,
			name:        cfg.SysvarName,
			zoneID:      zoneID,
			allowDisarm: cfg.SysvarAllowDisarm,
			existing:    cfg.SysvarExisting,
		})
	}
	return out
}

type mirrorTarget struct {
	central     string
	name        string
	zoneID      string
	allowDisarm bool
	// existing marks a pre-existing ALARM-type (bool) variable owned
	// by the operator: the mirror writes true while triggered and
	// false otherwise, never creates or retypes the variable, and
	// accepts no inbound intents through it (a bool carries no mode).
	existing bool
}

// mirrorConfig is the sysvar-relevant slice of the output config.
type mirrorConfig struct {
	SysvarName        string `json:"sysvar_name"`
	SysvarAllowDisarm bool   `json:"sysvar_allow_disarm"`
	SysvarExisting    bool   `json:"sysvar_existing"`
}

// onStateChanged exports the zone state to every mirror sysvar. It
// runs from the event sink — detached from any caller context by
// design.
//
//nolint:contextcheck // sink callbacks have no caller ctx; exports run on the service lifetime
func (m *sysvarMirror) onStateChanged(e hmevent.AlarmStateChangedEvent) {
	idx := sysvarIndexByMode[e.Mode]
	if e.To == hmenum.AlarmZoneStateTriggered {
		idx = sysvarAlarmIndex
	}
	for _, t := range m.mirrorTargets(e.ZoneID) {
		m.export(t, idx)
	}
}

// export ensures the enum exists once, then writes the state index.
func (m *sysvarMirror) export(t mirrorTarget, idx int) {
	ctx := context.Background()
	u, ok := m.svc.reg.Get(t.central)
	if !ok || u.Hub == nil {
		return
	}
	key := t.central + "|" + t.name
	if t.existing {
		// Operator-owned ALARM (bool) variable: plain triggered flag,
		// no ensure — creating or retyping it is not ours to do.
		m.mu.Lock()
		m.lastWritten[key] = idx
		m.mu.Unlock()
		if err := u.Hub.SetSystemVariable(ctx, t.name, idx == sysvarAlarmIndex); err != nil {
			m.svc.log.Warn("alarm sysvar mirror: export failed", "sysvar", t.name, "error", err)
		}
		return
	}
	m.mu.Lock()
	ensured := m.ensured[key]
	m.mu.Unlock()
	if !ensured {
		if _, err := u.Hub.CreateSysvarEnum(ctx, t.name, sysvarStates); err != nil {
			m.svc.log.Warn("alarm sysvar mirror: ensure enum", "sysvar", t.name, "error", err)
		}
		m.mu.Lock()
		m.ensured[key] = true
		m.mu.Unlock()
	}
	m.mu.Lock()
	m.lastWritten[key] = idx
	m.mu.Unlock()
	if err := u.Hub.SetSystemVariable(ctx, t.name, idx); err != nil {
		m.svc.log.Warn("alarm sysvar mirror: export failed", "sysvar", t.name, "error", err)
	}
}

// onInbound turns third-party sysvar writes into intents. Arm intents
// (a higher protection level) are honored; disarm intents are refused
// unless the zone's mirror explicitly opts in — the refusal is
// journaled, never silent.
func (m *sysvarMirror) onInbound(centralName string, e hmevent.SysvarChangedEvent) {
	idx, ok := sysvarValueIndex(e.NewValue)
	if !ok {
		return
	}
	// Match the sysvar to a mirror target across zones. Two zones
	// mirroring the same sysvar name would make an inbound intent
	// ambiguous (and stomp each other's echo guard) — refuse loudly
	// instead of guessing.
	type match struct {
		target mirrorTarget
		snap   engine.ZoneSnapshot
	}
	var matches []match
	zones := m.svc.engine.Zones()
	for i := range zones {
		snap := zones[i]
		for _, t := range m.mirrorTargets(snap.ID) {
			if t.existing {
				// A bool triggered flag carries no mode — never an
				// inbound intent channel.
				continue
			}
			if t.central == centralName && t.name == e.Name {
				matches = append(matches, match{target: t, snap: snap})
			}
		}
	}
	if len(matches) == 0 {
		return
	}
	if len(matches) > 1 {
		m.journalIntentFault(context.Background(), "", "sysvar_intent_ambiguous",
			map[string]any{"sysvar": e.Name, "zones": len(matches)})
		return
	}
	t := matches[0].target
	key := t.central + "|" + t.name
	m.mu.Lock()
	last, hasLast := m.lastWritten[key]
	m.mu.Unlock()
	if hasLast && last == idx {
		// Echo of our own export.
		return
	}
	m.applyIntent(t, matches[0].snap, idx)
}

// applyIntent executes one inbound sysvar intent against the engine.
//
// Every exit that leaves the zone where it was re-exports the real
// state: the value the third party wrote already sits in the CCU
// variable, so a policy refusal, a rejected arm and a value that is no
// intent at all would each leave it asserting a protection level the
// engine never entered. Nothing else converges it — the export runs off
// real transitions only.
func (m *sysvarMirror) applyIntent(t mirrorTarget, snap engine.ZoneSnapshot, idx int) {
	ctx := context.Background()
	if idx == 0 {
		if !t.allowDisarm {
			// Pinned by contract test: a sysvar write cannot disarm a
			// protected zone by default.
			m.journalIntentFault(ctx, t.zoneID, "sysvar_disarm_refused", nil)
			m.reexport(t, snap)
			return
		}
		if err := m.svc.engine.Disarm(ctx, t.zoneID, "", "sysvar"); err != nil {
			m.svc.log.Warn("alarm sysvar disarm failed", "zone", t.zoneID, "error", err)
			m.journalIntentFault(ctx, t.zoneID, "sysvar_disarm_failed",
				map[string]any{"error": err.Error()})
			m.reexport(t, snap)
		}
		return
	}
	mode := modeForSysvarIndex(idx)
	if idx == sysvarAlarmIndex || mode == "" {
		// "Alarm" is an export-only state, and an index outside the
		// value list carries no mode — neither is an intent.
		m.reexport(t, snap)
		return
	}
	if _, err := m.svc.engine.Arm(ctx, t.zoneID, engine.ArmRequest{Mode: mode, Source: "sysvar"}); err != nil {
		m.journalIntentFault(ctx, t.zoneID, "sysvar_arm_failed",
			map[string]any{"mode": string(mode), "error": err.Error()})
		m.reexport(t, snap)
	}
}

// reexport pushes the zone's current state back onto its mirrors. The
// engine is asked again rather than trusting the pre-intent snapshot,
// so the write — and the echo guard it refreshes — carry the state as
// it stands after the attempt.
func (m *sysvarMirror) reexport(t mirrorTarget, snap engine.ZoneSnapshot) {
	cur := snap
	zones := m.svc.engine.Zones()
	for i := range zones {
		if zones[i].ID == t.zoneID {
			cur = zones[i]
			break
		}
	}
	m.onStateChanged(hmevent.AlarmStateChangedEvent{ZoneID: t.zoneID, To: cur.State, Mode: cur.Mode})
}

// journalIntentFault records a refused or failed inbound sysvar intent.
// An empty zoneID files the entry system-wide (no zone could be
// resolved).
func (m *sysvarMirror) journalIntentFault(ctx context.Context, zoneID, event string, details map[string]any) {
	if _, err := m.svc.journal.Append(ctx, engine.JournalEntry{
		ZoneID: zoneID, Class: hmenum.AlarmJournalClassFault,
		Event: event, Source: "sysvar", Details: details,
	}); err != nil {
		m.svc.log.Error("alarm sysvar journal append failed", "error", err)
	}
}

// modeForSysvarIndex is the inverse of sysvarIndexByMode.
func modeForSysvarIndex(idx int) hmenum.AlarmMode {
	for mode, i := range sysvarIndexByMode {
		if i == idx {
			return mode
		}
	}
	return ""
}

// sysvarValueIndex extracts the enum index of a sysvar value change.
// The CCU delivers ENUM sysvar values as integers (occasionally as
// floats through the JSON path); labels are matched as a fallback.
func sysvarValueIndex(v hmtypes.ParamValue) (int, bool) {
	switch v.Kind {
	case hmtypes.ValueKindInt:
		return v.Int, true
	case hmtypes.ValueKindFloat:
		return int(v.Float), true
	case hmtypes.ValueKindString:
		for i, label := range sysvarStates {
			if label == v.String {
				return i, true
			}
		}
	default:
	}
	return 0, false
}

// parseMirrorConfig decodes the sysvar-relevant slice of an output
// config document.
func parseMirrorConfig(raw string) (mirrorConfig, error) {
	var cfg mirrorConfig
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return mirrorConfig{}, err
	}
	return cfg, nil
}
