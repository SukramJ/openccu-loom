// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"context"
	"sort"
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
	zoneID     string
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
	act := &activation{outputID: inst.row.ID, zoneID: inst.row.ZoneID, incidentID: incidentID}
	m.active[inst.row.ID] = act
	act.cancel = m.sched.Schedule(d, func() {
		m.runStop(inst, act, s, m.clk.Now().Add(m.stopVerifyWindow))
	})
	m.mu.Unlock()
}

// shutdownStopBudget bounds the stop writes [Manager.Shutdown] issues, so
// a CCU that has itself gone away cannot hold the daemon's shutdown open.
// It composes with the caller's deadline — whichever is shorter wins.
const shutdownStopBudget = 10 * time.Second

// Shutdown writes the stop command of every activation the device does
// not bound itself, then drops the watchdogs.
//
// Cancelling a watchdog is not stopping the output. A smoke-detector
// sounder latches: SMOKE_DETECTOR_COMMAND=INTRUSION_ALARM stays set on
// the device — and on every peer detector of its group — until
// INTRUSION_ALARM_OFF is written, which is why fireSmokeSounder arms the
// watchdog before the activation write and calls it the only bound the
// class has. Dropping that bound without writing the stop leaves the
// detectors sounding with nothing left in the system able to end it: the
// operator's instinctive reaction to a false alarm, stopping the daemon,
// is then the very action that makes the noise permanent. The alarm light
// is unbounded in the same way — steady-on until a stop write — and is
// stopped here for the same reason, one class less to leave running.
//
// Classes the device bounds itself (the ASIR duration in the activation
// paramset, a bounded actuator switch-on) need no write here: they end on
// their own whether the daemon runs or not.
func (m *Manager) Shutdown(ctx context.Context) {
	pending := m.unboundedActivations()
	if len(pending) > 0 {
		// The cancellation is dropped on purpose: a shutdown context that
		// is already done is not a reason to leave a sounder latched, and
		// the budget below is the bound that matters here.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownStopBudget)
		for _, act := range pending {
			s, ok := m.stopperFor(act.inst)
			if !ok {
				continue
			}
			if err := s.stop(stopCtx); err != nil {
				m.journalFault(stopCtx, act.inst.row.ZoneID, "output_stop_failed", act.inst.row.ID, act.incidentID, err)
				m.log.Error("alarm output stop on shutdown failed — the device stays active",
					"output", act.inst.row.ID, "zone", act.inst.row.ZoneID,
					"channel", act.inst.row.ChannelAddress, "error", err)
			}
		}
		cancel()
	}
	m.StopWatchdogs()
}

// pendingStop pairs an output instance with the incident its activation
// belongs to.
type pendingStop struct {
	inst       *instance
	incidentID int64
}

// unboundedActivations collects the enrolled instances of unbounded
// classes that are currently active. Both bookkeeping maps are consulted:
// a watchdogged activation lives in active, a sustained fire in demands,
// and a stop already in flight has released its demand while its
// activation record still stands.
//
// The shared-channel arbitration is deliberately not consulted: after
// this process exits no demand survives, so every stop proceeds — the
// same safe direction a restart takes.
func (m *Manager) unboundedActivations() []pendingStop {
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := map[string]*instance{}
	for _, list := range m.byZone {
		for _, inst := range list {
			byID[inst.row.ID] = inst
		}
	}
	incidents := make(map[string]int64, len(m.active))
	ids := make([]string, 0, len(m.active)+len(m.demands))
	for id, act := range m.active {
		incidents[id] = act.incidentID
		ids = append(ids, id)
	}
	for id := range m.demands {
		if _, dup := incidents[id]; dup {
			continue
		}
		incidents[id] = 0
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]pendingStop, 0, len(ids))
	for _, id := range ids {
		inst, ok := byID[id]
		if !ok || !unboundedClass(inst.row.Class) {
			continue
		}
		out = append(out, pendingStop{inst: inst, incidentID: incidents[id]})
	}
	return out
}

// unboundedClass reports whether an activation of the class stays on
// until the daemon writes the stop, with no device-side duration to end
// it.
func unboundedClass(class hmenum.AlarmOutputClass) bool {
	switch class {
	case hmenum.AlarmOutputClassSmokeSounder, hmenum.AlarmOutputClassAlarmLight:
		return true
	default:
		return false
	}
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
	// Drop all shared-channel demands with the watchdogs: after a
	// service stop every later stop must proceed (safe direction).
	m.demands = map[string]demandRec{}
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
	// Shared-channel arbitration: another zone still demands this
	// channel — leave the device on for it and drop this activation
	// (the verify chain would read the intentionally-active device as
	// a stop failure and escalate). See arbitration.go.
	if m.releaseDemandForeignRemains(inst) {
		m.logSharedStopDeferred(inst)
		m.clearActivation(act)
		return
	}
	if err := s.stop(ctx); err != nil {
		m.outputFailed(ctx, act.zoneID, "output_stop_failed", act.outputID, act.incidentID, err)
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
		// A verified stop resolves this output's own outstanding failure
		// (if any) and reports healthy only when nothing else is still
		// failed — never unconditionally, or it would erase an unrelated
		// output's failed-fire degradation (S7).
		m.resolveFailure(act.outputID)
		return
	}
	if m.clk.Now().After(verifyUntil) {
		m.clearActivation(act)
		m.journalFault(ctx, act.zoneID, "output_stop_unverified", act.outputID, act.incidentID, nil)
		m.noteFailure(act.outputID, "alarm output "+act.outputID+" stop unverified")
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
	// Shared-channel arbitration: another zone still demands this
	// channel — keep the device on for it, cancel any pending fire
	// watchdog of this row, and skip the write + verify chain. See
	// arbitration.go.
	if m.releaseDemandForeignRemains(inst) {
		m.logSharedStopDeferred(inst)
		m.cancelWatchdog(inst.row.ID)
		return nil
	}
	m.mu.Lock()
	if prev, exists := m.active[inst.row.ID]; exists && prev.cancel != nil {
		prev.cancel()
	}
	act := &activation{outputID: inst.row.ID, zoneID: inst.row.ZoneID, incidentID: incidentID}
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
