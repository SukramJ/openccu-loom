// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import "strings"

// PathData describes a data point's location in the daemon's logical
// namespace. It drives MQTT topic generation, REST URL routing, and
// UI breadcrumbs.
//
// The canonical form is:
//
//	central/<central>/device/<model>/<channel_address>/<parameter>
type PathData struct {
	CentralName    string
	DeviceModel    string
	ChannelAddress string
	Parameter      string
}

// Segments returns the path segments in canonical order. Empty fields
// are omitted so callers can safely join with "/".
func (p PathData) Segments() []string {
	out := make([]string, 0, 6)
	if p.CentralName != "" {
		out = append(out, "central", p.CentralName)
	}
	if p.DeviceModel != "" {
		out = append(out, "device", p.DeviceModel)
	}
	if p.ChannelAddress != "" {
		out = append(out, p.ChannelAddress)
	}
	if p.Parameter != "" {
		out = append(out, p.Parameter)
	}
	return out
}

// String returns the "/"-joined canonical path.
func (p PathData) String() string {
	return strings.Join(p.Segments(), "/")
}
