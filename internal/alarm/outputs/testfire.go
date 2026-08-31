// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"context"
	"errors"
	"time"

	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// testFireDuration bounds one test activation (short and reduced per
// notes/concepts/alarm-concept.md §7 — the HmIP "Test-Alarm" parity).
const testFireDuration = 3 * time.Second

// Test-fire refusal errors surfaced to the management API.
var (
	// ErrUnknownOutput reports an output ID the manager does not know.
	ErrUnknownOutput = errors.New("outputs: unknown output")
	// ErrTestFireUnsupported reports a class without a safe test path.
	// Smoke-detector sounders are deliberately excluded: each commanded
	// activation costs irreplaceable battery life and likely fans out
	// to the whole smoke-detector group.
	ErrTestFireUnsupported = errors.New("outputs: test fire not supported for this output class")
)

// TestFire runs one short, bounded test activation of a single
// output. Acoustic tests carry the full watchdog treatment; the
// optical-only variant spares the neighbours.
func (m *Manager) TestFire(ctx context.Context, outputID string, opticalOnly bool) error {
	m.mu.Lock()
	var inst *instance
	for _, list := range m.byZone {
		for _, cand := range list {
			if cand.row.ID == outputID {
				inst = cand
				break
			}
		}
	}
	m.mu.Unlock()
	if inst == nil {
		return ErrUnknownOutput
	}

	// A test that worked is not a fault. Filing it as one put a fault
	// row in the journal per successful siren test, which buries the
	// failures the fault filter exists to surface.
	journalTest := func() {
		m.journalEntry(ctx, hmenum.AlarmJournalClassTest, inst.row.ZoneID,
			"output_test_fired", inst.row.ID, 0, nil)
	}
	// Every return below used to hand the error to the caller and stop
	// there. The operator who pressed the button saw it; the journal and
	// the health signal did not, so a siren sweep recorded only the
	// outputs that worked (S7).
	testFailed := func(err error) error {
		m.outputFailed(ctx, inst.row.ZoneID, "output_test_failed", inst.row.ID, 0, err)
		return err
	}
	switch inst.row.Class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return testFailed(err)
		}
		on := sirencdp.OnConfig{Duration: testFireDuration}
		if opticalOnly || inst.row.Class == hmenum.AlarmOutputClassOpticalSiren {
			// A silent test fire is still one atomic write: the
			// acoustic half has to name the device's disable
			// selection, or the write re-sends the tone selected last.
			if off, ok := disableAcousticSelection(dev); ok {
				on.AcousticSelection = &off
			}
			p := inst.cfg.OpticalPattern
			if p == "" {
				if lights := dev.AvailableLights(); len(lights) > 1 {
					p = lights[len(lights)-1]
				}
			}
			if p != "" {
				on.OpticalSelection = &p
			}
		} else if inst.cfg.AcousticTone != "" {
			tone := inst.cfg.AcousticTone
			on.AcousticSelection = &tone
			on.AcousticTone = tone
		}
		if err := dev.TurnOn(ctx, on, hmenum.CommandPriorityLow); err != nil {
			return testFailed(err)
		}
		m.armStopWatchdog(inst, 0, testFireDuration, m.sirenStopper(inst, !opticalOnly && inst.row.Class != hmenum.AlarmOutputClassOpticalSiren))
		journalTest()
		return nil
	case hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight:
		dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return testFailed(err)
		}
		if err := dev.TurnOnBounded(ctx, testFireDuration, inst.cfg.Level, hmenum.CommandPriorityLow); err != nil {
			return testFailed(err)
		}
		m.armStopWatchdog(inst, 0, testFireDuration, m.actuatorStopper(inst))
		journalTest()
		return nil
	default:
		// A class without a safe test path is a refusal by design, not
		// a device that failed. Reporting it as a degradation would put
		// a false fault on the one signal an operator trusts.
		return ErrTestFireUnsupported
	}
}
