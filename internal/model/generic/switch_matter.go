// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write carries: a tap in a controller app is a foreground path and must
// not queue behind a background refresh. Spelled out because the zero
// value of [hmenum.CommandPriority] is Critical.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// Compile-time assertions: a generic STATE switch participates in the
// Matter source surface (ADR 0012) as an OnOffPlugInUnit device whose
// OnOff cluster is a declaration for go-fabric's device kit (ADR 0084).
var (
	_ interfaces.MatterDeviceSource   = (*Switch)(nil)
	_ interfaces.MatterChangeNotifier = (*Switch)(nil)
)

// matterGenericSwitchDeviceType is OnOffPlugInUnit (0x010A): the nearest
// Device Library type for a bare writable STATE channel with no profile.
// It mandates the LT feature on OnOff, which the server advertises.
const matterGenericSwitchDeviceType = uint16(fabricdevice.OnOffPlugInUnitID)

// matterGenericSwitchEligible reports whether a generic Switch projects to
// Matter at all. Only STATE does: every other writable bool parameter a
// generic Switch may wrap (INHIBIT, ON_TIME-style flags, config toggles)
// is an operational setting with no Matter cluster, and exposing it as an
// OnOffPlugInUnit would present a controller with a "switch" that is not
// one.
func matterGenericSwitchEligible(p hmenum.Parameter) bool {
	return p == hmenum.ParameterState
}

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (s *Switch) MatterDeviceType() uint16 { return matterGenericSwitchDeviceType }

// MatterDevice implements [interfaces.MatterDeviceSource]: an
// OnOffPlugInUnit with the OnOff cluster declared, built once per
// switch. A switch whose parameter is not STATE projects nothing, so the
// assembler skips the channel. The Groups and ScenesManagement clusters
// the device type mandates are stack state the endpoint assembler
// mounts.
//
// The Sink is the STATE write at [matterDispatchPriority] — On through
// [Switch.TurnOn] so a configured ON_TIME companion applies, Off through
// [Switch.Set] — and the data point's confirmed-update hook feeds the
// CCU's state back through [SetMatterOnOff].
func (s *Switch) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	if s == nil || !matterGenericSwitchEligible(s.Parameter()) {
		return nil, nil //nolint:nilnil // a switch that is not STATE projects no device
	}
	return s.matterDevice.Device(func() (*fabricdevice.Device, error) {
		on, _ := s.Value()
		d, err := NewMatterDevice(s, matterGenericSwitchDeviceType, opts,
			MatterOnOffDeclaration(true, on, func(ctx context.Context, on bool) error {
				if on {
					return s.TurnOn(ctx, matterDispatchPriority)
				}
				return s.Set(ctx, false, matterDispatchPriority)
			}))
		if err != nil {
			return nil, err
		}
		_ = s.OnConfirmedUpdate(func(_, on bool) { SetMatterOnOff(d, on) })
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if on, observed := s.Value(); observed {
			SetMatterOnOff(d, on)
		}
		return d, nil
	})
}
