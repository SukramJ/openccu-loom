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
