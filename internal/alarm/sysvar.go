// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

	// Export queue. Every export runs on one worker goroutine instead
	// of on the caller, because the caller is the engine's event sink
	// and that runs with the engine lock held: an export is a SQLite
	// read plus a CCU JSON-RPC round trip bounded only by the 30 s HTTP
	// timeout, so exporting inline blocks every alarm verb — Disarm and
	// Silence included — behind a CCU that is slow, rebooting or gone.
	// That is exactly when the state machine has to stay responsive.
	// One worker, FIFO, so the variable still ends up carrying the
	// transitions in the order the engine produced them.
	queueMu sync.Mutex
	queue   []hmevent.AlarmStateChangedEvent
	cancel  context.CancelFunc // non-nil while the worker runs
	done    chan struct{}
	wake    chan struct{}
}

// sysvarExportQueueDepth bounds the pending export queue. A queue in
// front of an unreachable CCU must not grow without limit; the oldest
// pending transition is dropped first because the mirror carries state,
// not history — the newest value is the one the variable has to end up
// with.
const sysvarExportQueueDepth = 64

func newSysvarMirror(svc *Service) *sysvarMirror {
	return &sysvarMirror{
		svc:         svc,
		ensured:     map[string]bool{},
		lastWritten: map[string]int{},
		wake:        make(chan struct{}, 1),
	}
}

// start launches the export worker. ctx bounds its lifetime the way
// the engine's lifeCtx bounds timer-driven work — the service start
// context, never a request. Idempotent; paired with stop.
func (m *sysvarMirror) start(ctx context.Context) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	go m.run(ctx, done)
}

// stop ends the export worker and discards whatever it has not written
// yet. Cancelling before waiting is what bounds the wait: an export
// already on the wire aborts instead of running out the CCU's HTTP
// timeout. The wait itself is not optional — the service closes its
// database handle right behind this call, and a worker still resolving
// mirror targets would be reading a store that is going away.
func (m *sysvarMirror) stop() {
	m.queueMu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.queue = nil
	m.queueMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// run drains the export queue until ctx is cancelled.
func (m *sysvarMirror) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
		for {
			if ctx.Err() != nil {
				return
			}
			e, ok := m.dequeue()
			if !ok {
				break
			}
			m.exportState(ctx, e)
		}
	}
}

// enqueue appends one transition to the export queue and wakes the
// worker. A mirror that was never started (or is already stopped)
// drops the transition: nothing would ever write it out.
func (m *sysvarMirror) enqueue(e hmevent.AlarmStateChangedEvent) {
	m.queueMu.Lock()
	if m.cancel == nil {
		m.queueMu.Unlock()
		return
	}
	if len(m.queue) >= sysvarExportQueueDepth {
		m.queue = m.queue[1:]
	}
	m.queue = append(m.queue, e)
	m.queueMu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// dequeue pops the oldest pending transition.
func (m *sysvarMirror) dequeue() (hmevent.AlarmStateChangedEvent, bool) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if len(m.queue) == 0 {
		return hmevent.AlarmStateChangedEvent{}, false
	}
	e := m.queue[0]
	m.queue = m.queue[1:]
	return e, true
}

// mirrorTargets returns the sysvar-mirror outputs of an zone.
func (m *sysvarMirror) mirrorTargets(ctx context.Context, zoneID string) []mirrorTarget {
	rows, err := m.svc.stores.Outputs.ListByZone(ctx, zoneID)
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

// onStateChanged queues the zone state for export to every mirror
// sysvar. It runs from the event sink, which holds the engine lock, so
// it must not touch the database or the CCU itself — the worker does
// both.
func (m *sysvarMirror) onStateChanged(e hmevent.AlarmStateChangedEvent) {
	m.enqueue(e)
}

// exportState resolves a queued transition to its mirror targets and
// writes it out. ctx is the worker's own lifetime, not a caller's — the
// sink it originates from has none.
func (m *sysvarMirror) exportState(ctx context.Context, e hmevent.AlarmStateChangedEvent) {
	idx := sysvarIndexByMode[e.Mode]
	if e.To == hmenum.AlarmZoneStateTriggered {
		idx = sysvarAlarmIndex
	}
	for _, t := range m.mirrorTargets(ctx, e.ZoneID) {
		m.export(ctx, t, idx)
	}
}

// export ensures the enum exists once, then writes the state index.
func (m *sysvarMirror) export(ctx context.Context, t mirrorTarget, idx int) {
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
		// Latch only on success. The first export after boot regularly
		// races the south-bound bring-up, and the creator answers
		// "sysvar creator not wired" until the primary client is
		// registered. Latching on that failure disabled enum creation
		// for the whole process lifetime, so every later write went to
		// a variable that had never been created and the mirror stayed
		// dead until the daemon restarted. Retrying is cheap: the
		// create script is a no-op when the variable already exists.
		if _, err := u.Hub.CreateSysvarEnum(ctx, t.name, sysvarStates); err != nil {
			m.svc.log.Warn("alarm sysvar mirror: ensure enum", "sysvar", t.name, "error", err)
		} else {
			m.mu.Lock()
			m.ensured[key] = true
			m.mu.Unlock()
		}
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
//
//nolint:contextcheck // bus dispatch has no caller ctx; the match runs on the service lifetime
func (m *sysvarMirror) onInbound(centralName string, e hmevent.SysvarChangedEvent) {
	idx, ok := sysvarValueIndex(e.NewValue)
	if !ok {
		return
	}
	ctx := context.Background()
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
		for _, t := range m.mirrorTargets(ctx, snap.ID) {
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
