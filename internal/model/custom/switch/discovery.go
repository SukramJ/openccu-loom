// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// AttachPowerEnergySources walks every Switch custom DP on dev and
// binds sibling POWER / ENERGY_COUNTER generic.Sensor data points as
// Matter measurement sources. Used by the device-pipeline post-discovery
// pass after [custom.CreateCustomDataPoints] has materialised the
// per-channel custom DPs — by that point every channel's generic DPs
// are also live, so the cross-channel sibling lookup succeeds for
// HmIP-PSM-style devices where POWER / ENERGY_COUNTER live on a
// different channel than the SWITCH_VIRTUAL_RECEIVER.
//
// Idempotent — re-running the pass overwrites the prior attachment
// with the (now identical) source. nil dev is a no-op.
//
// Selection rules:
//   - First channel (by deterministic iteration order) carrying a
//     [hmenum.ParameterPower] DP that satisfies
//     [interfaces.MatterFloatMeasurementSource] wins.
//   - Same for [hmenum.ParameterEnergyCounter].
//   - Switches whose host device exposes neither parameter are left
//     un-attached and project as a plain OnOff endpoint, unchanged.
func AttachPowerEnergySources(dev *device.Device) {
	if dev == nil {
		return
	}
	channels := dev.Channels()
	powerSrc, energySrc := findSiblingMeasurementSources(channels)
	if powerSrc == nil && energySrc == nil {
		return
	}
	for _, ch := range channels {
		sw, ok := ch.CustomDataPoint().(*Switch)
		if !ok {
			continue
		}
		if powerSrc != nil {
			sw.AttachPowerSource(powerSrc)
		}
		if energySrc != nil {
			sw.AttachEnergySource(energySrc)
		}
	}
}

// findSiblingMeasurementSources scans channels for a POWER and an
// ENERGY_COUNTER generic.Sensor that satisfies the
// MatterFloatMeasurementSource contract. Returns (nil, nil) when no
// channel exposes either.
func findSiblingMeasurementSources(channels []*device.Channel) (powerSrc, energySrc interfaces.MatterFloatMeasurementSource) {
	for _, ch := range channels {
		if powerSrc == nil {
			if dp := ch.Parameter(hmenum.ParameterPower); dp != nil {
				if src, ok := dp.(interfaces.MatterFloatMeasurementSource); ok {
					powerSrc = src
				}
			}
		}
		if energySrc == nil {
			if dp := ch.Parameter(hmenum.ParameterEnergyCounter); dp != nil {
				if src, ok := dp.(interfaces.MatterFloatMeasurementSource); ok {
					energySrc = src
				}
			}
		}
		if powerSrc != nil && energySrc != nil {
			break
		}
	}
	return powerSrc, energySrc
}
