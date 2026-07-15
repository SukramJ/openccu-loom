// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	mu         sync.Mutex
	byArea     map[string]*alarmpanel.Panel
	health     bool
	masterName string
}

func newPanelRegistry(masterName string) *panelRegistry {
	if masterName == "" {
		masterName = "Alarm system"
	}
	return &panelRegistry{byArea: map[string]*alarmpanel.Panel{}, health: true, masterName: masterName}
}

// Panels returns the entity snapshots, areas sorted by ID, the master
// aggregate (present with two or more areas) last.
func (s *Service) Panels() []alarmpanel.Panel {
	r := s.panels
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.byArea))
	for id := range r.byArea {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]alarmpanel.Panel, 0, len(ids)+1)
	for _, id := range ids {
		out = append(out, *r.byArea[id])
	}
	if master, ok := r.masterLocked(); ok {
		out = append(out, master)
	}
	return out
}

// seedPanels (re)builds the registry from the configured areas and the
// engine snapshots. Called from Start and Reload — never from the
// event path.
func (s *Service) seedPanels(ctx context.Context) {
	snaps := s.engine.Areas()
	r := s.panels
	r.mu.Lock()
	known := map[string]bool{}
	for i := range snaps {
		snap := &snaps[i]
		known[snap.ID] = true
		modes := s.modesForArea(ctx, snap.ID)
		r.byArea[snap.ID] = &alarmpanel.Panel{
			UniqueID:  alarmpanel.PanelUniqueID(snap.ID),
			AreaID:    snap.ID,
			Name:      snap.Name,
			Modes:     modes,
			State:     alarmpanel.StateToken(snap.State, snap.Mode),
			Available: r.health,
		}
	}
	var removed []alarmpanel.Panel
	for id, p := range r.byArea {
		if !known[id] {
			removed = append(removed, *p)
			delete(r.byArea, id)
		}
	}
	panels := make([]alarmpanel.Panel, 0, len(r.byArea))
	for _, p := range r.byArea {
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

// modesForArea reads the configured modes of an area from the store
// (outside the event path).
func (s *Service) modesForArea(ctx context.Context, areaID string) []hmenum.AlarmMode {
	row, ok, err := s.stores.Areas.Get(ctx, areaID)
	if err != nil || !ok {
		return nil
	}
	cfg, err := engine.ParseAreaConfig(row.ConfigJSON)
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
	p, ok := r.byArea[e.AreaID]
	if !ok {
		p = &alarmpanel.Panel{
			UniqueID: alarmpanel.PanelUniqueID(e.AreaID),
			AreaID:   e.AreaID,
		}
		r.byArea[e.AreaID] = p
	}
	if e.AreaName != "" {
		p.Name = e.AreaName
	}
	p.State = alarmpanel.StateToken(e.To, e.Mode)
	p.Available = r.health
	snapshot := *p
	master, hasMaster := r.masterLocked()
	r.mu.Unlock()

	s.publishPanel(snapshot, false)
	if hasMaster {
		s.publishPanel(master, false)
	}
}

// onPanelHealthEvent flips availability on every panel.
func (s *Service) onPanelHealthEvent(e hmevent.AlarmHealthChangedEvent) {
	r := s.panels
	r.mu.Lock()
	if r.health == e.Healthy {
		r.mu.Unlock()
		return
	}
	r.health = e.Healthy
	panels := make([]alarmpanel.Panel, 0, len(r.byArea))
	for _, p := range r.byArea {
		p.Available = e.Healthy
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

// masterLocked aggregates the master panel; present with ≥2 areas.
// The caller holds the registry lock.
func (r *panelRegistry) masterLocked() (alarmpanel.Panel, bool) {
	if len(r.byArea) < 2 {
		return alarmpanel.Panel{}, false
	}
	tokens := make([]string, 0, len(r.byArea))
	for _, p := range r.byArea {
		tokens = append(tokens, p.State)
	}
	return alarmpanel.Panel{
		UniqueID:  alarmpanel.PanelUniqueID(alarmpanel.MasterAreaID),
		AreaID:    alarmpanel.MasterAreaID,
		Name:      r.masterName,
		State:     alarmpanel.MasterStateToken(tokens),
		Available: r.health,
		Master:    true,
	}, true
}

// publishPanel emits the entity change onto the alarm bus.
func (s *Service) publishPanel(p alarmpanel.Panel, removed bool) {
	s.publish(hmevent.AlarmPanelChangedEvent{
		Base:      hmevent.NewBaseAt(s.clk.Now()),
		UniqueID:  p.UniqueID,
		AreaID:    p.AreaID,
		Name:      p.Name,
		State:     p.State,
		Available: p.Available,
		Removed:   removed,
	})
}
