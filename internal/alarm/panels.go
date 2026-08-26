// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// panelRegistry maintains the alarm-control-panel entity projections.
// It is fed exclusively from events and store reads outside the event
// path — never by calling back into the engine from the sink (the
// sink runs under the engine lock).
type panelRegistry struct {
	mu     sync.Mutex
	byZone map[string]*alarmpanel.Panel
	// health is the fleet-wide baseline: it only moves on a genuinely
	// global signal (the alarm service itself failing to start), never
	// on one output's failure — see zoneUnhealthy.
	health bool
	// zoneUnhealthy holds the zone IDs currently degraded by their own
	// enrolled output(s) failing (present == degraded; absent ==
	// healthy). A zone panel's Available is health && !zoneUnhealthy;
	// the master aggregate is health alone, so one zone's broken siren
	// never removes Home Assistant's disarm control from the other
	// zones or the whole-house panel during an active alarm (K1).
	zoneUnhealthy map[string]bool
	masterName    string
}

func newPanelRegistry(masterName string) *panelRegistry {
	if masterName == "" {
		masterName = "Alarm system"
	}
	return &panelRegistry{
		byZone: map[string]*alarmpanel.Panel{}, health: true,
		zoneUnhealthy: map[string]bool{}, masterName: masterName,
	}
}

// Panels returns the entity snapshots, zones sorted by ID, the master
// aggregate (present with two or more zones) last.
func (s *Service) Panels() []alarmpanel.Panel {
	r := s.panels
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.byZone))
	for id := range r.byZone {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]alarmpanel.Panel, 0, len(ids)+1)
	for _, id := range ids {
		out = append(out, *r.byZone[id])
	}
	if master, ok := r.masterLocked(); ok {
		out = append(out, master)
	}
	return out
}

// seedPanels (re)builds the registry from the configured zones and the
// engine snapshots. Called from Start and Reload — never from the
// event path.
func (s *Service) seedPanels(ctx context.Context) {
	snaps := s.engine.Zones()
	r := s.panels
	r.mu.Lock()
	known := map[string]bool{}
	for i := range snaps {
		snap := &snaps[i]
		known[snap.ID] = true
		modes := s.modesForZone(ctx, snap.ID)
		armReq, disarmReq := s.EffectiveCodePolicy(ctx, snap.ID)
		r.byZone[snap.ID] = &alarmpanel.Panel{
			UniqueID:           alarmpanel.PanelUniqueID(snap.ID),
			ZoneID:             snap.ID,
			Name:               snap.Name,
			Modes:              modes,
			State:              alarmpanel.StateToken(snap.State, snap.Mode),
			Available:          r.health && !r.zoneUnhealthy[snap.ID],
			CodeArmRequired:    armReq,
			CodeDisarmRequired: disarmReq,
		}
	}
	var removed []alarmpanel.Panel
	for id, p := range r.byZone {
		if !known[id] {
			removed = append(removed, *p)
			delete(r.byZone, id)
		}
	}
	panels := make([]alarmpanel.Panel, 0, len(r.byZone))
	for _, p := range r.byZone {
		panels = append(panels, *p)
	}
	master, hasMaster := r.masterLocked()
	r.mu.Unlock()

	for i := range removed {
		s.publishPanel(removed[i], true)
	}
	for _, p := range panels {
		s.publishPanel(p, false)
	}
	if hasMaster {
		s.publishPanel(master, false)
	}
}

// modesForZone reads the configured modes of an zone from the store
// (outside the event path).
func (s *Service) modesForZone(ctx context.Context, zoneID string) []hmenum.AlarmMode {
	row, ok, err := s.stores.Zones.Get(ctx, zoneID)
	if err != nil || !ok {
		return nil
	}
	cfg, err := engine.ParseZoneConfig(row.ConfigJSON)
	if err != nil {
		return nil
	}
	modes := make([]hmenum.AlarmMode, 0, len(cfg.Modes))
	for m := range cfg.Modes {
		modes = append(modes, m)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })
	return modes
}

// onPanelStateEvent updates the projection from a state transition.
// Runs on the sink path — event data only, no engine calls.
func (s *Service) onPanelStateEvent(e hmevent.AlarmStateChangedEvent) {
	r := s.panels
	r.mu.Lock()
	p, ok := r.byZone[e.ZoneID]
	if !ok {
		p = &alarmpanel.Panel{
			UniqueID: alarmpanel.PanelUniqueID(e.ZoneID),
			ZoneID:   e.ZoneID,
		}
		r.byZone[e.ZoneID] = p
	}
	if e.ZoneName != "" {
		p.Name = e.ZoneName
	}
	p.State = alarmpanel.StateToken(e.To, e.Mode)
	p.Available = r.health && !r.zoneUnhealthy[e.ZoneID]
	snapshot := *p
	master, hasMaster := r.masterLocked()
	r.mu.Unlock()

	s.publishPanel(snapshot, false)
	if hasMaster {
		s.publishPanel(master, false)
	}
}

// onPanelHealthEvent flips the fleet-wide baseline on every panel. It
// only ever fires for a genuinely global condition — the alarm service
// itself failing to start — never for one output's failure: those are
// scoped through onOutputZoneHealth instead, so a siren stuck in one
// zone does not remove Home Assistant's disarm control from the
// others while an alarm is active (K1).
func (s *Service) onPanelHealthEvent(e hmevent.AlarmHealthChangedEvent) {
	r := s.panels
	r.mu.Lock()
	if r.health == e.Healthy {
		r.mu.Unlock()
		return
	}
	r.health = e.Healthy
	panels := make([]alarmpanel.Panel, 0, len(r.byZone))
	for id, p := range r.byZone {
		p.Available = r.health && !r.zoneUnhealthy[id]
		panels = append(panels, *p)
	}
	master, hasMaster := r.masterLocked()
	r.mu.Unlock()

	for _, p := range panels {
		s.publishPanel(p, false)
	}
	if hasMaster {
		s.publishPanel(master, false)
	}
}

// onOutputZoneHealth applies the output manager's zone-scoped health
// signal to exactly the zone panel the failing (or recovered) output
// belongs to. It never touches another zone's panel or the master
// aggregate: an output enrolled in one zone must not take away the
// ability to disarm the other zones, or the whole house, at the exact
// moment an alarm needs that control (K1).
func (s *Service) onOutputZoneHealth(zoneID string, healthy bool) {
	r := s.panels
	if r == nil {
		return
	}
	r.mu.Lock()
	wasHealthy := !r.zoneUnhealthy[zoneID]
	if healthy {
		delete(r.zoneUnhealthy, zoneID)
	} else {
		r.zoneUnhealthy[zoneID] = true
	}
	if wasHealthy == healthy {
		r.mu.Unlock()
		return
	}
	p, ok := r.byZone[zoneID]
	if !ok {
		r.mu.Unlock()
		return
	}
	p.Available = r.health && healthy
	snapshot := *p
	r.mu.Unlock()

	s.publishPanel(snapshot, false)
}

// masterLocked aggregates the master panel; present with ≥2 zones.
// The caller holds the registry lock. The code flags are the any-zone
// union: a client driving the aggregate prompts upfront when any
// member zone will demand a code and fans the entered code out to the
// per-zone verbs. (The MQTT master *command topic* stays code-free by
// design — its aggregate verbs cannot carry per-zone codes — so its
// discovery config diverges deliberately from this projection.)
func (r *panelRegistry) masterLocked() (alarmpanel.Panel, bool) {
	if len(r.byZone) < 2 {
		return alarmpanel.Panel{}, false
	}
	tokens := make([]string, 0, len(r.byZone))
	var armReq, disarmReq bool
	for _, p := range r.byZone {
		tokens = append(tokens, p.State)
		armReq = armReq || p.CodeArmRequired
		disarmReq = disarmReq || p.CodeDisarmRequired
	}
	return alarmpanel.Panel{
		UniqueID:           alarmpanel.PanelUniqueID(alarmpanel.MasterZoneID),
		ZoneID:             alarmpanel.MasterZoneID,
		Name:               r.masterName,
		State:              alarmpanel.MasterStateToken(tokens),
		Available:          r.health,
		Master:             true,
		CodeArmRequired:    armReq,
		CodeDisarmRequired: disarmReq,
	}, true
}

// publishPanel emits the entity change onto the alarm bus.
func (s *Service) publishPanel(p alarmpanel.Panel, removed bool) {
	s.publish(hmevent.AlarmPanelChangedEvent{
		Base:               hmevent.NewBaseAt(s.clk.Now()),
		UniqueID:           p.UniqueID,
		ZoneID:             p.ZoneID,
		Name:               p.Name,
		State:              p.State,
		Available:          p.Available,
		CodeArmRequired:    p.CodeArmRequired,
		CodeDisarmRequired: p.CodeDisarmRequired,
		Removed:            removed,
	})
}

// refreshPanelCodePolicies re-derives every panel's effective code
// policy after a code-set change and republishes exactly the panels
// whose flags flipped (plus the master aggregate, whose union may flip
// with them). Store reads happen outside the registry lock; the caller
// is a management write, never the engine sink.
func (s *Service) refreshPanelCodePolicies(ctx context.Context) {
	r := s.panels
	if r == nil {
		return
	}
	r.mu.Lock()
	ids := make([]string, 0, len(r.byZone))
	for id := range r.byZone {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	type codeFlags struct{ arm, disarm bool }
	derived := make(map[string]codeFlags, len(ids))
	for _, id := range ids {
		armReq, disarmReq := s.EffectiveCodePolicy(ctx, id)
		derived[id] = codeFlags{arm: armReq, disarm: disarmReq}
	}

	r.mu.Lock()
	var changed []alarmpanel.Panel
	for id, f := range derived {
		p, ok := r.byZone[id]
		if !ok || (p.CodeArmRequired == f.arm && p.CodeDisarmRequired == f.disarm) {
			continue
		}
		p.CodeArmRequired = f.arm
		p.CodeDisarmRequired = f.disarm
		changed = append(changed, *p)
	}
	master, hasMaster := r.masterLocked()
	r.mu.Unlock()

	if len(changed) == 0 {
		return
	}
	for i := range changed {
		s.publishPanel(changed[i], false)
	}
	if hasMaster {
		s.publishPanel(master, false)
	}
}
