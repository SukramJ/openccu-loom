// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// Wire identity for custom data points on the per-name REST/WS surface
// (`GET/POST …/cdps/{name}`, `cdp.get`/`cdp.invoke`).
//
// A profile channel group materialises the same parameter as a CDP on
// several channels (a dimmer's LEVEL on ch4/vch5/vch6, a switch's STATE
// on ch3/vch4/vch5). The bare parameter name then no longer identifies
// one CDP: clients keyed by name keep only the last list entry and a
// name-routed invoke always hits the first channel. [WireName]
// disambiguates colliding names as `PARAM@<channel>`; unique names stay
// bare so single-CDP devices (the common case) keep their stable wire
// identity. MQTT identity is channel-address-based and unaffected.

// WireName returns the wire identity for the custom DP attached at
// channelNo: the bare parameter name when unique on the device,
// `PARAM@<channel>` when the same parameter materialises CDPs on
// multiple channels.
func WireName(dev *device.Device, dp device.AttachableDataPoint, channelNo int) string {
	name := dp.DataPointKey().Parameter
	if dev == nil {
		return name
	}
	count := 0
	for _, ch := range dev.Channels() {
		other := ch.CustomDataPoint()
		if other == nil {
			continue
		}
		if other.DataPointKey().Parameter == name {
			count++
		}
	}
	if count > 1 {
		return name + "@" + strconv.Itoa(channelNo)
	}
	return name
}

// ParseWireName splits a wire identity into the parameter name and the
// optional channel selector. `"LEVEL@5"` → ("LEVEL", 5, true);
// `"LEVEL"` → ("LEVEL", 0, false). A suffix that is not a number is
// treated as part of the name (no valid channel selector).
func ParseWireName(name string) (param string, channelNo int, exact bool) {
	if at := strings.LastIndex(name, "@"); at >= 0 {
		if no, err := strconv.Atoi(name[at+1:]); err == nil {
			return name[:at], no, true
		}
	}
	return name, 0, false
}

// FindByWireName resolves a wire identity against the device's
// channels. Accepts both the bare parameter name (first match) and the
// channel-exact `PARAM@<channel>` form.
func FindByWireName(dev *device.Device, name string) (device.AttachableDataPoint, int, bool) {
	if dev == nil {
		return nil, 0, false
	}
	param, channelNo, exact := ParseWireName(name)
	for _, ch := range dev.Channels() {
		dp := ch.CustomDataPoint()
		if dp == nil {
			continue
		}
		if dp.DataPointKey().Parameter != param {
			continue
		}
		if exact && ch.Number != channelNo {
			continue
		}
		return dp, ch.Number, true
	}
	return nil, 0, false
}
