// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"
)

// modelDeviceFromInfo lifts the device descriptor this daemon harvests into
// the shared model's [hamodel.Device], so the render pipeline emits the
// device block instead of a builder stamping one on afterwards.
//
// It exists because a daemon migrating one plane at a time still harvests
// its device blocks the old way, through [deviceDescriptor], and needs a
// [hamodel.Device] to render the new way. Three planes each wrote this
// conversion out in full, under three names, before the shared module
// carried it.
//
// The identifiers keep an EMPTY namespace, which the shared model renders
// verbatim. That is what lets the published `openccu-loom_<serial>`,
// `openccu-loom_<serial>-<group>` and `openccu-loom_central_<central>`
// spellings survive: Home Assistant keys its device registry on those
// strings and has no migration path for them.
func modelDeviceFromInfo(info *hadiscovery.DeviceInfo) *hamodel.Device {
	if info == nil {
		return nil
	}
	return hadiscovery.DeviceFromInfo(*info)
}
