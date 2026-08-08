// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

	journalTest := func() {
		m.journalFault(ctx, inst.row.ZoneID, "output_test_fired", inst.row.ID, 0, nil)
	}
	switch inst.row.Class {
	case hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassOpticalSiren, hmenum.AlarmOutputClassChirp:
		dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return err
		}
		on := sirencdp.OnConfig{Duration: testFireDuration}
		if opticalOnly || inst.row.Class == hmenum.AlarmOutputClassOpticalSiren {
			if tones := dev.AvailableTones(); len(tones) > 0 {
				off := tones[0]
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
			return err
		}
		m.armStopWatchdog(inst, 0, testFireDuration, m.sirenStopper(inst, !opticalOnly && inst.row.Class != hmenum.AlarmOutputClassOpticalSiren))
		journalTest()
		return nil
	case hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight:
		dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return err
		}
		if err := dev.TurnOnBounded(ctx, testFireDuration, inst.cfg.Level, hmenum.CommandPriorityLow); err != nil {
			return err
		}
		m.armStopWatchdog(inst, 0, testFireDuration, m.actuatorStopper(inst))
		journalTest()
		return nil
	default:
		return ErrTestFireUnsupported
	}
}
