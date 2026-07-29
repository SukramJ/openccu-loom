// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SoundingOutput describes one output found sounding during
// reconciliation.
type SoundingOutput struct {
	OutputID      string
	SharedWithCCU bool
}

// Sounding reads the live active-state of every siren-capable output
// of the zone (S4 reconciliation input). Only observed-active outputs
// are reported; unobserved states count as silent — reconciliation
// must not stop or adopt on stale guesses.
func (m *Manager) Sounding(ctx context.Context, zoneID string) []SoundingOutput {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	m.mu.Unlock()

	var out []SoundingOutput
	for _, inst := range instances {
		if m.isSounding(inst) {
			out = append(out, SoundingOutput{OutputID: inst.row.ID, SharedWithCCU: inst.cfg.SharedWithCCU})
		}
	}
	_ = ctx
	return out
}

// isSounding reads one instance's live activity.
func (m *Manager) isSounding(inst *instance) bool {
	switch inst.row.Class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return false
		}
		aActive, _, aObs := dev.AcousticState()
		oActive, _, oObs := dev.OpticalState()
		return (aObs && aActive) || (oObs && oActive)
	case hmenum.AlarmOutputClassSwitchedSiren:
		dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return false
		}
		on, observed := dev.IsOn()
		return observed && on
	case hmenum.AlarmOutputClassSmokeSounder:
		dev, err := m.resolver.SmokeSounder(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return false
		}
		active, observed := dev.IsActive()
		return observed && active && dev.IsIntrusion()
	default:
		return false
	}
}

// AdoptBounded arms the stop watchdog for outputs adopted as an
// incident (S4 adopt-before-stop): the elapsed sounding time is
// unknown, so the full bounded duration is accounted on the ledger
// (over-counting is the safe direction) and the stop lands at
// now + bound at the latest.
func (m *Manager) AdoptBounded(ctx context.Context, zoneID string, incidentID int64, outputIDs []string) {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	m.mu.Unlock()
	wanted := map[string]bool{}
	for _, id := range outputIDs {
		wanted[id] = true
	}
	for _, inst := range instances {
		if !wanted[inst.row.ID] {
			continue
		}
		s, ok := m.stopperFor(inst)
		if !ok {
			continue
		}
		d := inst.cfg.acousticDuration(m.defaultSiren)
		// Only acoustic classes consume the acoustic ledger; adopted
		// optical/light activations are bounded but not budgeted.
		if incidentID != 0 && inst.row.Class.Acoustic() {
			if err := m.ledger.AddAcousticMS(ctx, incidentID, d.Milliseconds()); err != nil {
				m.journalFault(ctx, zoneID, "acoustic_ledger_failed", inst.row.ID, incidentID, err)
			}
		}
		m.armStopWatchdog(inst, incidentID, d, s)
	}
}

// StopUnowned stops every sounding output of a disarmed zone that has
// no declared third-party owner (S4: only a siren whose owning zone
// is disarmed, with no always-hot link and no shared-with-CCU flag,
// is stopped immediately).
func (m *Manager) StopUnowned(ctx context.Context, zoneID string) {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	m.mu.Unlock()
	for _, inst := range instances {
		if inst.cfg.SharedWithCCU || !m.isSounding(inst) {
			continue
		}
		if err := m.stopAndVerify(ctx, inst, 0); err != nil {
			m.journalFault(ctx, zoneID, "output_stop_failed", inst.row.ID, 0, err)
			continue
		}
		m.journalFault(ctx, zoneID, "reconcile_stopped_unowned_siren", inst.row.ID, 0, nil)
	}
}
