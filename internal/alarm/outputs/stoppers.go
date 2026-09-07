// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sirenStopper stops an ASIR-class siren by writing the disable
// defaults (the only stop mechanism the hardware offers) and verifies
// via the ACTIVE feedback of the acoustic or optical channel.
func (m *Manager) sirenStopper(inst *instance, acoustic bool) stopper {
	return stopper{
		stop: func(ctx context.Context) error {
			dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return err
			}
			return dev.TurnOff(ctx, hmenum.CommandPriorityCritical)
		},
		verify: func() bool {
			dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return false
			}
			if acoustic {
				active, _, observed := dev.AcousticState()
				return observed && !active
			}
			active, _, observed := dev.OpticalState()
			return observed && !active
		},
	}
}

// actuatorStopper stops a switch/dimmer-backed output and verifies
// via the switch state read-back.
func (m *Manager) actuatorStopper(inst *instance) stopper {
	return stopper{
		stop: func(ctx context.Context) error {
			dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return err
			}
			return dev.TurnOff(ctx, hmenum.CommandPriorityCritical)
		},
		verify: func() bool {
			dev, err := m.resolver.Actuator(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return false
			}
			on, observed := dev.IsOn()
			return observed && !on
		},
	}
}

// smokeStopper stops a smoke-detector sounder via
// INTRUSION_ALARM_OFF and verifies via the alarm-status read-back.
// There is no device-side duration — this watchdog is the only bound
// the class has.
func (m *Manager) smokeStopper(inst *instance) stopper {
	return stopper{
		stop: func(ctx context.Context) error {
			dev, err := m.resolver.SmokeSounder(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return err
			}
			return dev.TurnOff(ctx, hmenum.CommandPriorityCritical)
		},
		verify: func() bool {
			dev, err := m.resolver.SmokeSounder(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return false
			}
			active, observed := dev.IsActive()
			return observed && !active
		},
	}
}

// soundStopper stops an MP3 sound player through its own port. The
// port offers no read-back, so the verify half reports stopped: a
// chirp is a bounded one-shot emission, and re-writing a stop every
// verify interval would only burn radio budget.
func (m *Manager) soundStopper(inst *instance) stopper {
	return stopper{
		stop: func(ctx context.Context) error {
			dev, err := m.resolver.Sound(inst.row.CentralName, inst.row.ChannelAddress)
			if err != nil {
				return err
			}
			return dev.Stop(ctx, hmenum.CommandPriorityCritical)
		},
		verify: func() bool { return true },
	}
}

// chirpStopper stops a chirp output with the mechanism its target
// actually offers. The class covers both an ASIR confirmation tone and
// an MP3 sound player, and a sound player is not a siren: stopping it
// through the siren port made every silence, disarm and incident end a
// phantom output failure that degraded alarm health permanently (S7).
// The target is re-resolved per call — a device may appear or change
// class while the row stays enrolled.
func (m *Manager) chirpStopper(inst *instance) stopper {
	pick := func() stopper {
		if _, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress); err == nil {
			return m.sirenStopper(inst, true)
		}
		return m.soundStopper(inst)
	}
	return stopper{
		stop:   func(ctx context.Context) error { return pick().stop(ctx) },
		verify: func() bool { return pick().verify() },
	}
}
