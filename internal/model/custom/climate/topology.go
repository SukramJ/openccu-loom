// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time assertions that the ADR-0011 declarative source surface
// is satisfied. payload.Slotted is consumed by the bridge; HAEntity and
// DiscoveryDynamic are declarative only and have no consumption site
// yet.
var (
	_ payload.HAEntity         = (*Climate)(nil)
	_ payload.Slotted          = (*Climate)(nil)
	_ payload.DiscoveryDynamic = (*Climate)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Climate
// custom-DPs always surface as `climate` regardless of variant
// (KindIP / KindRF / KindSimpleRF) — the platform handles the
// underlying differences via `modes` / `preset_modes`.
func (c *Climate) HAComponent() string { return "climate" }

// TopicSlot returns the slot under the device's channel where the
// custom-DP aggregate state lives — `channels/<ch>/custom/climate/`.
// Climate.Address carries the channel-address (`<deviceAddr>:<chNo>`)
// the materialiser captured from the channel; we split it back into
// device+channel for the slot.
func (c *Climate) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(c.Address)
	if !ok {
		// No channel suffix — the source was attached to a bare
		// device address. Slot still valid; channel defaults to 0
		// which is the maintenance/device channel by HM convention.
		deviceAddr = c.Address
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "climate",
	}
}

// DiscoveryTriggers lists the wire parameters whose value change can
// flip the discovery shape:
//
//   - CONTROL_MODE — mode=AUTO/MANU/AWAY/BOOST changes the
//     `preset_modes` set (week-program slots only valid in AUTO,
//   - HEATING_COOLING — flips the `modes` list between
//     `[auto,heat,off]` and `[auto,cool,off]`.
//   - ACTIVE_PROFILE — when the device exposes ACTIVE_PROFILE the
//     selected slot may move within `preset_modes`.
func (c *Climate) DiscoveryTriggers() []hmenum.Parameter {
	return []hmenum.Parameter{
		hmenum.ParameterControlMode,
		hmenum.ParameterHeatingCooling,
		hmenum.ParameterActiveProfile,
	}
}
