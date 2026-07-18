// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stopper bundles the class-specific stop write and its read-back
// verification for one output instance.
type stopper struct {
	// stop issues the class's stop write at critical priority.
	stop func(ctx context.Context) error
	// verify reads the device state back: stopped=true only on an
	// observed inactive state; unobserved counts as not verified.
	verify func() (stopped bool)
}

// activation tracks one bounded output activation and its watchdog.
type activation struct {
	outputID   string
	areaID     string
	incidentID int64
	cancel     func()
}

// armStopWatchdog schedules the engine-side stop for one activation
// (S2): at deadline the stop is written at critical priority and
// verified by read-back; failures retry every stopVerifyInterval
// until StopVerifyWindow has elapsed, then convert into a health
// signal + journal fault instead of retrying forever.
func (m *Manager) armStopWatchdog(inst *instance, incidentID int64, d time.Duration, s stopper) {
	m.mu.Lock()
	if prev, ok := m.active[inst.row.ID]; ok && prev.cancel != nil {
		prev.cancel()
	}
	act := &activation{outputID: inst.row.ID, areaID: inst.row.AreaID, incidentID: incidentID}
	m.active[inst.row.ID] = act
	act.cancel = m.sched.Schedule(d, func() {
		m.runStop(inst, act, s, m.clk.Now().Add(m.stopVerifyWindow))
	})
	m.mu.Unlock()
}

// StopWatchdogs cancels every pending watchdog timer (bridge-level
// service stop without process exit — running stops-in-flight finish,
// scheduled ones are dropped).
func (m *Manager) StopWatchdogs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, act := range m.active {
		if act.cancel != nil {
			act.cancel()
		}
		delete(m.active, id)
	}
}

// cancelWatchdog drops the pending watchdog of an output (activation
// write failed after scheduling).
func (m *Manager) cancelWatchdog(outputID string) {
	m.mu.Lock()
	if act, ok := m.active[outputID]; ok {
		if act.cancel != nil {
			act.cancel()
		}
		delete(m.active, outputID)
	}
	m.mu.Unlock()
}

// runStop is one stop + verify pass; it reschedules itself until
// verified or the verify window closes. Runs on scheduler callbacks —
// never on the engine lock and deliberately detached from the
// activation's caller context (a stop must not die with a request).
//
//nolint:contextcheck // watchdog stops deliberately detach from the scheduling caller's ctx
func (m *Manager) runStop(inst *instance, act *activation, s stopper, verifyUntil time.Time) {
	ctx := context.Background()
	if err := s.stop(ctx); err != nil {
		m.journalFault(ctx, act.areaID, "output_stop_failed", act.outputID, act.incidentID, err)
	}
	m.mu.Lock()
	current, ok := m.active[act.outputID]
	if !ok || current != act {
		m.mu.Unlock()
		return
	}
	act.cancel = m.sched.Schedule(stopVerifyInterval, func() {
		m.verifyStop(inst, act, s, verifyUntil)
	})
	m.mu.Unlock()
}

// verifyStop reads the device back; still-active outputs retry the
// stop until the window closes, then the failure escalates (S2: a
// siren smashed off the wall must not burn radio budget forever).
//
//nolint:contextcheck // watchdog verification deliberately detaches from the scheduling caller's ctx
func (m *Manager) verifyStop(inst *instance, act *activation, s stopper, verifyUntil time.Time) {
	ctx := context.Background()
	if s.verify() {
		m.clearActivation(act)
		if m.health != nil {
			m.health(true, "alarm output stop verified")
		}
		return
	}
	if m.clk.Now().After(verifyUntil) {
		m.clearActivation(act)
		m.journalFault(ctx, act.areaID, "output_stop_unverified", act.outputID, act.incidentID, nil)
		if m.health != nil {
			m.health(false, "alarm output "+act.outputID+" stop unverified")
		}
		return
	}
	m.runStop(inst, act, s, verifyUntil)
}

// clearActivation removes the activation record if it is still
// current.
func (m *Manager) clearActivation(act *activation) {
	m.mu.Lock()
	if current, ok := m.active[act.outputID]; ok && current == act {
		delete(m.active, act.outputID)
	}
	m.mu.Unlock()
}

// stopAndVerify performs an immediate stop with verification for
// StopAll: any pending fire-watchdog is replaced by the immediate
// stop pass.
//
//nolint:contextcheck // the deferred verify chain detaches from the stop caller's ctx by design
func (m *Manager) stopAndVerify(ctx context.Context, inst *instance, incidentID int64) error {
	s, ok := m.stopperFor(inst)
	if !ok {
		return nil
	}
	m.mu.Lock()
	if prev, exists := m.active[inst.row.ID]; exists && prev.cancel != nil {
		prev.cancel()
	}
	act := &activation{outputID: inst.row.ID, areaID: inst.row.AreaID, incidentID: incidentID}
	m.active[inst.row.ID] = act
	m.mu.Unlock()

	err := s.stop(ctx)
	verifyUntil := m.clk.Now().Add(m.stopVerifyWindow)
	m.mu.Lock()
	if current, ok := m.active[act.outputID]; ok && current == act {
		act.cancel = m.sched.Schedule(stopVerifyInterval, func() {
			m.verifyStop(inst, act, s, verifyUntil)
		})
	}
	m.mu.Unlock()
	return err
}

// stopperFor builds the class-specific stopper of an instance. The
// boolean is false for classes without a device stop path.
func (m *Manager) stopperFor(inst *instance) (stopper, bool) {
	switch inst.row.Class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		return m.sirenStopper(inst, inst.row.Class == hmenum.AlarmOutputClassAcousticSiren), true
	case hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight:
		return m.actuatorStopper(inst), true
	case hmenum.AlarmOutputClassSmokeSounder:
		return m.smokeStopper(inst), true
	default:
		return stopper{}, false
	}
}
