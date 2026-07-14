// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"reflect"
	"sort"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// computeReadiness evaluates the ready-to-arm verdict of one mode
// (docs/alarm-concept.md §6.3). Sensors that would auto-bypass and
// sensors that are already bypassed appear as warnings, not blockers —
// they cannot fail an arm. The caller holds the engine lock.
func (a *area) computeReadiness(mode hmenum.AlarmMode) hmevent.AlarmModeReadiness {
	rd, _ := a.readinessDetail(mode)
	return rd
}

// readinessDetail additionally returns the sensors that would block
// the arm but are flagged bypass_auto: an arm excludes them until the
// next disarm instead of failing (§6.2) — the exclusion is recorded,
// never silent. The caller holds the engine lock.
func (a *area) readinessDetail(mode hmenum.AlarmMode) (verdict hmevent.AlarmModeReadiness, autoBypassed []string) {
	pol := a.cfg.Blockers
	var blockers, warnings, autoBypass []string
	classify := func(id string, p hmenum.AlarmBlockerPolicy, auto bool) {
		switch {
		case p == hmenum.AlarmBlockerPolicyIgnore:
		case p == hmenum.AlarmBlockerPolicyWarn:
			warnings = append(warnings, id)
		case auto:
			// Would block, but the flag converts the failure into a
			// recorded exclusion.
			warnings = append(warnings, id)
			autoBypass = append(autoBypass, id)
		default:
			blockers = append(blockers, id)
		}
	}
	for id, s := range a.sensors {
		if s.cfg.AlwaysOn || !s.cfg.InMode(mode) {
			continue
		}
		if a.bypassed[id] {
			warnings = append(warnings, id)
			continue
		}
		auto := s.cfg.BypassAuto
		if s.activeKnown && s.active && !s.cfg.AllowOpenAfterArming {
			classify(id, pol.Open, auto)
		}
		if !s.available {
			classify(id, pol.Unreachable, auto)
		}
		if s.sabotage {
			classify(id, pol.Sabotage, auto)
		}
		if s.lowBattery {
			classify(id, pol.LowBattery, auto)
		}
	}
	sort.Strings(blockers)
	sort.Strings(warnings)
	sort.Strings(autoBypass)
	return hmevent.AlarmModeReadiness{
		Ready:    len(blockers) == 0,
		Blockers: dedupe(blockers),
		Warnings: dedupe(warnings),
	}, dedupe(autoBypass)
}

// refreshReadiness recomputes all configured modes of a and publishes
// a readiness event when the verdict changed. The caller holds the
// engine lock.
func (e *Engine) refreshReadiness(a *area) {
	next := make(map[hmenum.AlarmMode]hmevent.AlarmModeReadiness, len(a.cfg.Modes))
	for mode := range a.cfg.Modes {
		next[mode] = a.computeReadiness(mode)
	}
	if reflect.DeepEqual(a.readiness, next) {
		return
	}
	a.readiness = next
	e.sink.Publish(hmevent.AlarmReadinessChangedEvent{
		Base:      hmevent.NewBaseAt(e.clk.Now()),
		AreaID:    a.id,
		Readiness: next,
	})
}

// dedupe removes adjacent duplicates from a sorted slice (a sensor can
// hit several health classes at once).
func dedupe(sorted []string) []string {
	if len(sorted) < 2 {
		return sorted
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
