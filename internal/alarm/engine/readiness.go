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
// (notes/concepts/alarm-concept.md §6.3). Sensors that would auto-bypass and
// sensors that are already bypassed appear as warnings, not blockers —
// they cannot fail an arm. The caller holds the engine lock.
func (a *zone) computeReadiness(mode hmenum.AlarmMode) hmevent.AlarmModeReadiness {
	rd, _ := a.readinessDetail(mode)
	return rd
}

// readinessDetail additionally returns the sensors that would block
// the arm but are flagged bypass_auto: an arm excludes them until the
// next disarm instead of failing (§6.2) — the exclusion is recorded,
// never silent. The caller holds the engine lock.
func (a *zone) readinessDetail(mode hmenum.AlarmMode) (verdict hmevent.AlarmModeReadiness, autoBypassed []string) {
	pol := a.cfg.Blockers
	var blockers, warnings, autoBypass []string
	var details []hmevent.AlarmBlockerDetail
	// detail records the reason alongside the sensor ID. The flat ID
	// lists deduplicate, so a sensor that is both unreachable and
	// low on battery collapses to one entry there and the reason is
	// lost entirely — which is why "why can I not arm?" needs this
	// second, un-deduplicated channel.
	detail := func(s *sensorState, id string, reason hmevent.AlarmBlockerReason, blocking bool) {
		ref := hmevent.NewSecuritySourceRef(s.row.CentralName, s.row.InterfaceID,
			s.row.ChannelAddress, s.row.Parameter)
		ref.SensorID = id
		ref.Name = s.row.Name
		ref.SensorType = s.row.SensorType
		details = append(details, hmevent.AlarmBlockerDetail{
			SensorID: id, Name: s.row.Name, Source: ref,
			Reason: reason, Blocking: blocking,
		})
	}
	classify := func(s *sensorState, id string, p hmenum.AlarmBlockerPolicy, auto bool, reason hmevent.AlarmBlockerReason) {
		switch {
		case p == hmenum.AlarmBlockerPolicyIgnore:
		case p == hmenum.AlarmBlockerPolicyWarn:
			warnings = append(warnings, id)
			detail(s, id, reason, false)
		case auto:
			// Would block, but the flag converts the failure into a
			// recorded exclusion.
			warnings = append(warnings, id)
			autoBypass = append(autoBypass, id)
			detail(s, id, reason, false)
		default:
			blockers = append(blockers, id)
			detail(s, id, reason, true)
		}
	}
	for id, s := range a.sensors {
		if s.cfg.AlwaysOn || !s.cfg.InMode(mode) {
			continue
		}
		if a.bypassed[id] {
			warnings = append(warnings, id)
			detail(s, id, hmevent.AlarmBlockerReasonBypassed, false)
			continue
		}
		auto := s.cfg.BypassAuto
		if s.activeKnown && s.active && !s.cfg.AllowOpenAfterArming {
			classify(s, id, pol.Open, auto, hmevent.AlarmBlockerReasonOpen)
		}
		if !s.available {
			classify(s, id, pol.Unreachable, auto, hmevent.AlarmBlockerReasonUnreachable)
		}
		if s.sabotage {
			classify(s, id, pol.Sabotage, auto, hmevent.AlarmBlockerReasonSabotage)
		}
		if s.lowBattery {
			classify(s, id, pol.LowBattery, auto, hmevent.AlarmBlockerReasonLowBattery)
		}
	}
	sort.Strings(blockers)
	sort.Strings(warnings)
	sort.Strings(autoBypass)
	sort.Slice(details, func(i, j int) bool {
		if details[i].SensorID != details[j].SensorID {
			return details[i].SensorID < details[j].SensorID
		}
		return details[i].Reason < details[j].Reason
	})
	return hmevent.AlarmModeReadiness{
		Ready:    len(blockers) == 0,
		Blockers: dedupe(blockers),
		Warnings: dedupe(warnings),
		Details:  details,
	}, dedupe(autoBypass)
}

// refreshReadiness recomputes all configured modes of a and publishes
// a readiness event when the verdict changed. The caller holds the
// engine lock.
func (e *Engine) refreshReadiness(a *zone) {
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
		ZoneID:    a.id,
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
