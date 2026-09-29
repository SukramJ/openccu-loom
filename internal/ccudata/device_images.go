// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"regexp"
	"strings"

	openccudata "github.com/SukramJ/go-openccu-data"
)

// deviceImageDir is the snapshot directory holding the 250px device
// artwork, a byte-identical copy of the CCU WebUI's
// config/img/devices/250/ tree.
const deviceImageDir = "device_images/250/"

// safeDeviceImageName admits the filename shapes the device_icons table
// actually carries: flat PNG names (mixed case, digits, '_', '-', '.')
// and names below a subdirectory such as "coupling/hm-coupling-dim.png".
// Together with the explicit ".." and leading-slash checks it keeps a
// name from escaping the image directory.
var safeDeviceImageName = regexp.MustCompile(`^[A-Za-z0-9._/-]+\.png$`)

// DeviceImage returns the embedded device image for a device_icons
// filename (the value [Translations.DeviceModelIcon] resolves, e.g.
// "PushButton-2ch-wm.png" or "coupling/hm-coupling-dim.png"). ok is
// false for an unsafe name or one the snapshot does not carry — the
// caller then falls back to another source.
func DeviceImage(name string) (data []byte, ok bool) {
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") ||
		!safeDeviceImageName.MatchString(name) {
		return nil, false
	}
	data, err := openccudata.ReadFile(deviceImageDir + name)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}
